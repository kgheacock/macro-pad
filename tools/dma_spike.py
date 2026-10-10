#!/usr/bin/env python3
"""Measure how much CPU time a cached-frame push leaves free, on the board.

`make dma-spike` runs `install`, then `read`. See
tasks/complete/0045-double-buffered-dma-panel-push.md, DoD-1, and
tasks/ongoing/0049-frame-only-panel-push.md, DoD-4.

    dma_spike.py install CIRCUITPY_VOLUME
    dma_spike.py read

`install` edits `firmware/code.py` in memory: it keeps everything up to the
`macro_pad.run()` call, and puts a measurement in place of that call. It writes
the result to the board only. The repo's `code.py` is not touched. `make flash`
puts the real one back.

The board times `Panel.start_push` and a loop that counts while the DMA runs,
five times. It sends the lines over the CDC data port and repeats them every 2 s,
because a line sent before the host opens the port is lost. `read` prints the
lines and exits 0 when `cpu_free_ms` is at least 15, the limit of DoD-1, and
`start_push_us` is at most 500 in every trial, the limit of task 0049's DoD-4.
"""

import glob
import os
import re
import sys
import termios
import time
from pathlib import Path

# DoD-1: the CPU must run a loop for at least this long while a cached-frame
# push is on the wire. The push takes 17.5 ms at 15 MHz.
MIN_CPU_FREE_MS = 15

# Task 0049's DoD-4: `Panel.start_push` sends the frame and no command, so it
# must hold the CPU no longer than this in any trial.
MAX_START_PUSH_US = 500

_REPO_ROOT = Path(__file__).resolve().parent.parent

_RUN_CALL = "macro_pad.run()"

# Replaces `macro_pad.run()` in `code.py` on the board. `panels`, `macro_pad`,
# and `usb_cdc` come from `code.py`. The first step paints every key, so the
# blink-free key 0 has a cached "on" frame.
_MEASUREMENT = '''
import time

macro_pad.step(time.monotonic_ns() // 1000)
while macro_pad._active_keys or macro_pad._push_queue:
    macro_pad.step(time.monotonic_ns() // 1000)

frame = macro_pad.key_states[0]._on_frame
lines = []
for trial in range(5):
    t0 = time.monotonic_ns()
    panels[0].start_push(frame)
    start_ns = time.monotonic_ns() - t0
    loops = 0
    while not panels[0].poll():
        loops += 1
    total_ns = time.monotonic_ns() - t0
    lines.append(
        "push start_push_us={} total_ms={} cpu_free_ms={} loops={}".format(
            start_ns // 1000,
            total_ns // 1000000,
            (total_ns - start_ns) // 1000000,
            loops,
        )
    )
    time.sleep(0.05)

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


def spike_code(code):
    """Return `code`, the text of `firmware/code.py`, with the measurement
    in place of its `macro_pad.run()` call.
    """
    if code.count(_RUN_CALL) != 1:
        raise ValueError(
            "firmware/code.py has no single {!r} to replace".format(_RUN_CALL)
        )
    # `run()` sits inside a `try`, so the measurement takes the call's indent.
    line_start = code.rfind("\n", 0, code.index(_RUN_CALL)) + 1
    indent = code[line_start : code.index(_RUN_CALL)]
    measurement = "\n".join(
        indent + line if line and number else line
        for number, line in enumerate(_MEASUREMENT.strip("\n").split("\n"))
    )
    return code.replace(_RUN_CALL, measurement)


def parse_figures(text):
    """Return a list of `{name: int}` dicts, one for each `push` line."""
    figures = []
    for line in text.splitlines():
        if not line.startswith("push "):
            continue
        figures.append({k: int(v) for k, v in re.findall(r"(\w+)=(\d+)", line)})
    return figures


def report(text, out=sys.stdout):
    """Print the figures. Return True when every trial meets both limits."""
    figures = parse_figures(text)
    if not figures:
        print("no push figures from the board", file=out)
        return False
    for trial in figures:
        print(
            "start_push {} us, total {} ms, cpu_free_ms {}".format(
                trial["start_push_us"], trial["total_ms"], trial["cpu_free_ms"]
            ),
            file=out,
        )
    worst = min(trial["cpu_free_ms"] for trial in figures)
    print("cpu_free_ms {} (limit {})".format(worst, MIN_CPU_FREE_MS), file=out)
    slowest = max(trial["start_push_us"] for trial in figures)
    print("start_push_us {} (limit {})".format(slowest, MAX_START_PUSH_US), file=out)
    return worst >= MIN_CPU_FREE_MS and slowest <= MAX_START_PUSH_US


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
        code = (_REPO_ROOT / "firmware" / "code.py").read_text()
        (Path(argv[1]) / "code.py").write_text(spike_code(code))
        print("dma spike code.py written to {}".format(argv[1]), file=out)
        return 0
    if argv == ["read"]:
        return 0 if report(read_board(), out) else 1
    print(__doc__.split("\n")[0], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
