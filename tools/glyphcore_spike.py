#!/usr/bin/env python3
"""Measure the glyphcore module on the board (task 0059): the inflate on core 1,
what it costs core 0, the garbage collection with 128 slots in module memory,
and a 32 KB copy into a slot.

`make glyphcore-spike` runs `install`, then `read`. See
tasks/ongoing/0059-offload-glyph-inflate-to-the-second-core.md, DoD-1, DoD-2,
and DoD-8.

    glyphcore_spike.py install CIRCUITPY_VOLUME
    glyphcore_spike.py read

The board must run the custom build of CircuitPython (`tools/build_circuitpython.sh`)
and have the firmware (`make flash`). `install` writes a probe as `code.py` and
an icon's zlib stream as `spike_glyph.z`, on the board only; `make flash` puts
the real `code.py` back. The board sends the figures over the CDC data port,
and repeats them every 2 s. `read` prints the figures and exits 0 when every
inflate on core 1 equals `zlib.decompress` on core 0 and the median collection
with 128 slots takes at most 10 ms.
"""

import glob
import os
import re
import sys
import termios
import time
import zlib
from pathlib import Path

# DoD-8: the longest median collection with 128 slots in module memory.
MAX_GC_US = 10_000
ASSETS = Path(__file__).parent.parent / "assets"

_PROBE = '''\
import gc
import time
import zlib

import displayio
import usb_cdc

import display_render
import glyphcore

with open("spike_glyph.z", "rb") as f:
    stream = f.read()
raw = zlib.decompress(stream)
lines = []


def us(t0):
    return (time.monotonic_ns() - t0) // 1000


# 1. One inflate on core 0 and on core 1, with the pixels compared.
glyphcore.alloc(128)
for trial in range(5):
    t0 = time.monotonic_ns()
    zlib.decompress(stream)
    core0_us = us(t0)
    t0 = time.monotonic_ns()
    glyphcore.submit(stream, 0)
    submit_us = us(t0)
    polls = 0
    while glyphcore.poll() is None:
        polls += 1
    wall_us = us(t0)
    equal = int(bytes(glyphcore.glyph_view(0)) == raw)
    lines.append(
        "inflate stream={} core0_us={} submit_us={} wall_us={} core1_us={} polls={} equal={}".format(
            len(stream), core0_us, submit_us, wall_us, glyphcore.last_us(), polls, equal
        )
    )

# 2. A compose on core 0, alone and while core 1 inflates.
frame = displayio.Bitmap(128, 128, 65536)
scratch = displayio.Bitmap(128, 128, 65536)
pixels = glyphcore.glyph_view(0)
for trial in range(5):
    t0 = time.monotonic_ns()
    display_render.compose_into(frame, scratch, 0x001F, pixels)
    alone_us = us(t0)
    glyphcore.submit(stream, 1)
    t0 = time.monotonic_ns()
    display_render.compose_into(frame, scratch, 0x001F, pixels)
    during_us = us(t0)
    while glyphcore.poll() is None:
        pass
    lines.append("compose alone_us={} during_us={}".format(alone_us, during_us))

# 3. A 32 KB copy of a composed frame into a slot.
view = memoryview(frame).cast("B")
slot = glyphcore.slot_view(5)
for trial in range(5):
    t0 = time.monotonic_ns()
    slot[:] = view
    lines.append("slotcopy bytes={} copy_us={}".format(len(view), us(t0)))

# 4. A garbage collection with 128 slots in module memory (allocated above).
for trial in range(5):
    t0 = time.monotonic_ns()
    gc.collect()
    lines.append("gc slots=128 collect_us={}".format(us(t0)))
lines.append("heap free={}".format(gc.mem_free()))

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


def _stream_of(path):
    from PIL import Image

    image = Image.open(path).convert("RGBA").resize((128, 128))
    rgba = image.tobytes()
    out = bytearray()
    for r, g, b, a in zip(rgba[0::4], rgba[1::4], rgba[2::4], rgba[3::4]):
        value = 0 if a < 128 else ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3)
        if a >= 128 and value == 0:
            value = 1
        out += value.to_bytes(2, "big")
    return zlib.compress(bytes(out), 9)


def icon_stream(assets=ASSETS):
    """The zlib stream of the icon that compresses worst, as the driver sends
    it: 128x128 big-endian RGB565, 0x0000 for a transparent pixel, 0x0001 for
    opaque black, deflate level 9. The longest stream is the slowest inflate."""
    return max((_stream_of(p) for p in sorted(Path(assets).glob("*.png"))), key=len)


def parse_figures(text):
    """Return the lines as `{kind: [ {name: int}, ... ]}`."""
    figures = {}
    for line in text.splitlines():
        kind = line.split(" ", 1)[0]
        values = {k: int(v) for k, v in re.findall(r"(\w+)=(-?\d+)", line)}
        if values:
            figures.setdefault(kind, []).append(values)
    return figures


def _median(values):
    values = sorted(values)
    return values[len(values) // 2]


def report(text, out=sys.stdout):
    """Print the figures. Return True when every inflate on core 1 equals the
    one on core 0, and the median collection meets DoD-8."""
    figures = parse_figures(text)
    needed = ("inflate", "compose", "slotcopy", "gc")
    if any(kind not in figures for kind in needed):
        print("no figures from the board", file=out)
        return False
    inflate = figures["inflate"]
    equal = all(trial["equal"] == 1 for trial in inflate)
    print("stream {} bytes".format(inflate[0]["stream"]), file=out)
    print("inflate on core 0 {} us (median)".format(_median(t["core0_us"] for t in inflate)), file=out)
    print("inflate on core 1 {} us (median)".format(_median(t["core1_us"] for t in inflate)), file=out)
    print("submit to done {} us (median)".format(_median(t["wall_us"] for t in inflate)), file=out)
    print("submit call {} us (median)".format(_median(t["submit_us"] for t in inflate)), file=out)
    print("pixels equal to zlib on core 0: {}".format("yes" if equal else "NO"), file=out)
    compose = figures["compose"]
    alone = _median(t["alone_us"] for t in compose)
    during = _median(t["during_us"] for t in compose)
    print("compose on core 0 {} us alone, {} us while core 1 inflates".format(alone, during), file=out)
    print("32 KB copy into a slot {} us (median)".format(_median(t["copy_us"] for t in figures["slotcopy"])), file=out)
    collect = _median(t["collect_us"] for t in figures["gc"])
    print("gc.collect() with 128 slots in module memory {} us (median; limit {})".format(collect, MAX_GC_US), file=out)
    return equal and collect <= MAX_GC_US


def read_board(timeout_s=90):
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
            if buf.count(b"DONE\n") >= 2:
                break
        else:
            time.sleep(0.05)
    blocks = buf.decode(errors="replace").split("DONE\n")
    return max(blocks, key=len)


def main(argv, out=sys.stdout):
    if len(argv) == 2 and argv[0] == "install":
        volume = Path(argv[1])
        (volume / "spike_glyph.z").write_bytes(icon_stream())
        (volume / "code.py").write_text(probe_code())
        print("glyphcore spike code.py written to {}".format(argv[1]), file=out)
        return 0
    if argv == ["read"]:
        return 0 if report(read_board(), out) else 1
    print(__doc__.split("\n")[0], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
