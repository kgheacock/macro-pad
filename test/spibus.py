"""A fake shared PIO SPI bus with one DC line, one RST line, and one CS line
per panel, for tests of `firmware/st7735.py`.

`FakePioBus` has the shape of `pio_spi.PioBus`: `write` waits for a short
transfer, `start` begins a transfer by DMA, and `done` says it is on the
wire. It checks what real hardware needs: exactly one CS line must be low
for a transfer, and a new transfer must not start while one is on the wire.
It files each short write under the panel that CS line selects, as a command
(DC low) or as data for the last command (DC high). A frame started with
`start` is filed as a frame of that panel. Like the real panel, a fake panel
takes a frame only after `RAMWR`, and stays in write mode until it is sent
another command (task 0049). `FakeBus.commands(key)`, `FakeBus.image(key)`
and `FakeBus.frame_count(key)` read back what each panel was sent.

A frame started with `start` is read when the transfer ends, as the DMA reads
it while it runs. A write to the frame in between shows in `image`. CS must
still be low when the transfer ends. With `auto_finish=True`, the default, a
transfer ends the first time `done` is read. With `auto_finish=False` it ends
when a test calls `FakeBus.finish`.
"""

import st7735


class FakePin:
    """A `digitalio.DigitalInOut` output that logs every value it is set to."""

    def __init__(self, name, value=None):
        self.name = name
        self.log = []
        self._value = value

    @property
    def value(self):
        return self._value

    @value.setter
    def value(self, new_value):
        self._value = bool(new_value)
        self.log.append(self._value)


class FakePioBus:
    def __init__(self, bus, auto_finish=True):
        self._bus = bus
        self.auto_finish = auto_finish
        self._in_flight = None  # (key, data, frame) of the transfer on the wire
        self.starts = 0

    def _selected(self):
        selected = [i for i, cs in enumerate(self._bus.cs) if cs.value is False]
        assert len(selected) == 1, "exactly one CS line must be low, got {}".format(selected)
        return selected[0]

    def write(self, data):
        assert self._in_flight is None, "the bus is still sending a transfer"
        key = self._selected()
        data = bytes(memoryview(data))
        assert self._bus.dc.value is not None
        if self._bus.dc.value is False:
            assert len(data) == 1, "a command is one byte"
            self._bus.records.append([key, data[0], b""])
            if data[0] == st7735.RAMWR:
                self._bus.write_mode.add(key)
            else:
                self._bus.write_mode.discard(key)
        else:
            record = self._bus.records[-1]
            assert record[0] == key, "data written to another panel than its command"
            record[2] += data

    def start(self, data):
        assert self._in_flight is None, "the bus is still sending a transfer"
        key = self._selected()
        assert self._bus.dc.value is True, "a frame goes out as data"
        assert key in self._bus.write_mode, "a frame needs a RAMWR before it"
        frame = [key, b""]
        self._bus.frames.append(frame)
        self._in_flight = (key, data, frame)
        self.starts += 1

    def finish(self):
        """End the transfer on the wire, reading its buffer now."""
        if self._in_flight is None:
            return
        key, data, frame = self._in_flight
        assert self._bus.cs[key].value is False, "CS went high before the transfer ended"
        frame[1] = bytes(memoryview(data))
        self._in_flight = None

    @property
    def done(self):
        if self.auto_finish:
            self.finish()
        return self._in_flight is None


class FakeBus:
    def __init__(self, key_count, auto_finish=True):
        self.dc = FakePin("dc", value=False)
        self.rst = FakePin("rst", value=True)
        self.cs = [FakePin("cs{}".format(i), value=True) for i in range(key_count)]
        self.pio = FakePioBus(self, auto_finish=auto_finish)
        # One [key, command, data] entry per command written.
        self.records = []
        # One [key, bytes] entry per frame started, in order.
        self.frames = []
        # The panels that took a RAMWR and no other command since.
        self.write_mode = set()

    def finish(self):
        """End the transfer on the wire."""
        self.pio.finish()

    def panel(self, key, **kwargs):
        kwargs.setdefault("sleep", lambda seconds: None)
        return st7735.Panel(self.pio, self.dc, self.cs[key], **kwargs)

    def commands(self, key=None):
        """The command bytes written, in order, to one panel or to all."""
        return [c for k, c, _ in self.records if key is None or k == key]

    def window(self, key):
        """The last (column range, row range) set on a panel."""
        caset = [d for k, c, d in self.records if k == key and c == st7735.CASET][-1]
        raset = [d for k, c, d in self.records if k == key and c == st7735.RASET][-1]
        return (
            ((caset[0] << 8) | caset[1], (caset[2] << 8) | caset[3]),
            ((raset[0] << 8) | raset[1], (raset[2] << 8) | raset[3]),
        )

    def image(self, key):
        """The bytes of the last frame started to a panel, or `None`. It is
        empty until the frame is on the wire.
        """
        frames = [d for k, d in self.frames if k == key]
        return frames[-1] if frames else None

    def frame_count(self, key):
        return len([1 for k, _ in self.frames if k == key])
