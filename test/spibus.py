"""A fake shared SPI bus with one DC line, one RST line, and one CS line
per panel, for tests of `firmware/st7735.py`.

`FakeSPI.write` checks what real hardware needs: the bus must be locked,
and exactly one CS line must be low. It then files each write under the
panel that CS line selects, as a command (DC low) or as data for the last
command (DC high). `FakeBus.commands(key)` and `FakeBus.image(key)` read
back what each panel was sent.
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


class FakeSPI:
    def __init__(self, bus):
        self._bus = bus
        self._locked = False
        self.configures = []

    def try_lock(self):
        assert not self._locked, "the bus is already locked"
        self._locked = True
        return True

    def unlock(self):
        assert self._locked, "unlock without a lock"
        self._locked = False

    def configure(self, *, baudrate, polarity, phase):
        assert self._locked, "configure needs the lock"
        self.configures.append((baudrate, polarity, phase))

    def write(self, data):
        assert self._locked, "write needs the lock"
        selected = [i for i, cs in enumerate(self._bus.cs) if cs.value is False]
        assert len(selected) == 1, "exactly one CS line must be low, got {}".format(selected)
        key = selected[0]
        data = bytes(memoryview(data))
        if self._bus.dc.value is False:
            assert len(data) == 1, "a command is one byte"
            self._bus.records.append([key, data[0], b""])
        else:
            record = self._bus.records[-1]
            assert record[0] == key, "data written to another panel than its command"
            record[2] += data


class FakeBus:
    def __init__(self, key_count):
        self.dc = FakePin("dc", value=False)
        self.rst = FakePin("rst", value=True)
        self.cs = [FakePin("cs{}".format(i), value=True) for i in range(key_count)]
        self.spi = FakeSPI(self)
        # One [key, command, data] entry per command written.
        self.records = []

    def panel(self, key, **kwargs):
        kwargs.setdefault("baudrate", 4_000_000)
        kwargs.setdefault("sleep", lambda seconds: None)
        return st7735.Panel(self.spi, self.dc, self.cs[key], **kwargs)

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
        """The bytes of the last frame written to a panel, or `None`."""
        frames = [d for k, c, d in self.records if k == key and c == st7735.RAMWR]
        return frames[-1] if frames else None

    def frame_count(self, key):
        return len([1 for k, c, _ in self.records if k == key and c == st7735.RAMWR])
