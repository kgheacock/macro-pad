#!/usr/bin/env python3
"""Measure the frame cache's pool on the board: the time to allocate 128 slots,
the free heap after, and the time of a 32 KB copy.

`make slot-probe` runs `install`, then `read`. See
tasks/ongoing/0061-cache-composed-glyph-frames-on-the-board.md, DoD-1.

    slot_probe.py install CIRCUITPY_VOLUME
    slot_probe.py read

`install` writes a probe as `code.py` on the board only. The repo's `code.py`
is not touched, and `make flash` puts the real one back. Run `make flash`
first, so the board has `frame_cache.py` and `display_render.py`. The probe
builds a `FrameCache`, as `MacroPad` does at start-up, and times it. It calls
`gc.mem_free()` once before and once after, never inside a timed section,
because it walks the heap and costs about 20 ms.

The board sends the lines over the CDC data port and repeats them every 2 s,
because a line sent before the host opens the port is lost. `read` prints the
figures. It exits 0 when at least 2 MB of heap is free after the pool is
allocated, the limit of DoD-1. Under that limit the task stops: the firmware
does not shrink the pool by itself.
"""

import glob
import os
import re
import sys
import termios
import time
from pathlib import Path

# DoD-1: free heap after the pool of 128 slots is allocated, in bytes.
MIN_FREE_HEAP_BYTES = 2 * 1024 * 1024

# What the probe sends: one `pool` line, then one `copy` line for each trial.
_PROBE = '''\
import gc
import time

import usb_cdc

import frame_cache

gc.collect()
free_before = gc.mem_free()
t0 = time.monotonic_ns()
cache = frame_cache.FrameCache()
alloc_ns = time.monotonic_ns() - t0
free_after = gc.mem_free()

lines = [
    "pool slots={} alloc_ms={} free_before={} free_after={}".format(
        cache.slot_count, alloc_ns // 1000000, free_before, free_after
    )
]
source = cache.view(0)
target = cache.view(1)
for trial in range(5):
    t0 = time.monotonic_ns()
    target[:] = source
    copy_ns = time.monotonic_ns() - t0
    lines.append("copy bytes={} copy_us={}".format(len(source), copy_ns // 1000))

data = usb_cdc.data
while True:
    while not data.connected:
        time.sleep(0.1)
    text = ("\\n".join(lines) + "\\nDONE\\n").encode()
    for i in range(0, len(text), 48):
        data.write(text[i : i + 48])
        time.sleep(0.02)
    time.sleep(2)
'''


def probe_code():
    """Return the text of the probe's `code.py`."""
    return _PROBE


def parse_figures(text):
    """Return `(pool, copies)`: the `pool` line as a `{name: int}` dict, or
    `None`, and the `copy` lines as a list of dicts."""
    pool = None
    copies = []
    for line in text.splitlines():
        figures = {k: int(v) for k, v in re.findall(r"(\w+)=(\d+)", line)}
        if line.startswith("pool "):
            pool = figures
        elif line.startswith("copy "):
            copies.append(figures)
    return pool, copies


def report(text, out=sys.stdout):
    """Print the figures. Return True when the free heap after the pool meets
    the limit."""
    pool, copies = parse_figures(text)
    if pool is None or not copies:
        print("no pool figures from the board", file=out)
        return False
    print("slots {}".format(pool["slots"]), file=out)
    print("allocation {} ms".format(pool["alloc_ms"]), file=out)
    print("free heap before {} bytes".format(pool["free_before"]), file=out)
    print("free heap after {} bytes".format(pool["free_after"]), file=out)
    times = sorted(trial["copy_us"] for trial in copies)
    print(
        "32 KB copy {} us (best {}, worst {}, {} trials)".format(
            times[len(times) // 2], times[0], times[-1], len(times)
        ),
        file=out,
    )
    ok = pool["free_after"] >= MIN_FREE_HEAP_BYTES
    print(
        "free heap after the pool {} bytes (limit {})".format(
            pool["free_after"], MIN_FREE_HEAP_BYTES
        ),
        file=out,
    )
    if not ok:
        print("under the limit: stop, and ask the owner; do not shrink the pool", file=out)
    return ok


def read_board(timeout_s=60):
    """Read the board's CDC data port until it has sent one full block."""
    deadline = time.time() + timeout_s
    fd = None
    buf = b""
    while time.time() < deadline:
        if fd is None:
            ports = glob.glob("/dev/cu.usbmodem*")
            if not ports:
                time.sleep(0.2)
                continue
            try:
                fd = os.open(ports[0], os.O_RDWR | os.O_NOCTTY | os.O_NONBLOCK)
                attrs = termios.tcgetattr(fd)
                attrs[0] = attrs[1] = attrs[3] = 0
                termios.tcsetattr(fd, termios.TCSANOW, attrs)
            except OSError:
                fd = None
                time.sleep(0.2)
                continue
        try:
            chunk = os.read(fd, 4096)
        except BlockingIOError:
            chunk = b""
        except OSError:
            os.close(fd)
            fd = None
            continue
        if chunk:
            buf += chunk
            # The first block can start in the middle of a line. Wait for
            # the end of a second one, then use the longest block.
            if buf.count(b"DONE\n") >= 2:
                break
        else:
            time.sleep(0.05)
    blocks = buf.decode(errors="replace").split("DONE\n")
    return max(blocks, key=len)


def main(argv, out=sys.stdout):
    if len(argv) == 2 and argv[0] == "install":
        (Path(argv[1]) / "code.py").write_text(probe_code())
        print("slot probe code.py written to {}".format(argv[1]), file=out)
        return 0
    if argv == ["read"]:
        return 0 if report(read_board(), out) else 1
    print(__doc__.split("\n")[0], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
