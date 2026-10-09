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

`FakeParallelBus` has the shape of `pio_spi.ParallelBus`: `start_group` begins
the frames of several panels at once, and `done` and `desynced` say how the
group ended. It checks that the panels whose CS line is low are exactly the
panels of the group. `FakeBus.groups` lists the keys of each group started.
With `fail_groups = n`, the next `n` groups desync: `done` ends them with
`desynced` true and the panels get no frame.

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


class _FakePanelBus:
    def __init__(self, parallel, key):
        self._parallel = parallel
        self._key = key

    def write(self, data):
        assert self._parallel._group is None, "a command while a group is on the wire"
        assert self._parallel._bus.cs[self._key].value is False
        self._parallel.pio.write(data)

    def start(self, data):
        self._parallel.start_group(((self._key, data),))

    @property
    def done(self):
        return self._parallel.done


class FakeParallelBus:
    # `pio_spi.ParallelBus.max_group`: the leader and three followers fill one
    # PIO block.
    max_group = 3

    def __init__(self, bus, auto_finish=True):
        self._bus = bus
        self.pio = bus.pio
        self.auto_finish = auto_finish
        self.fail_groups = 0
        self.desynced = False
        self._group = None  # [(key, data, frame)] of the group on the wire

    def panel_bus(self, key):
        return _FakePanelBus(self, key)

    def start_group(self, items):
        assert self._group is None, "a group is already on the wire"
        assert self.pio._in_flight is None, "the bus is still sending a transfer"
        assert len(items) <= self.max_group, "a group has at most {} panels".format(self.max_group)
        keys = [key for key, _ in items]
        low = [i for i, cs in enumerate(self._bus.cs) if cs.value is False]
        assert sorted(low) == sorted(keys), "CS is low on {}, group is {}".format(low, keys)
        assert self._bus.dc.value is True, "a frame goes out as data"
        group = []
        for key, data in items:
            assert key in self._bus.write_mode, "a frame needs a RAMWR before it"
            frame = [key, b""]
            group.append((key, data, frame))
        self._bus.groups.append(keys)
        self.desynced = False
        self._group = group

    def finish(self):
        if self._group is None:
            return
        group, self._group = self._group, None
        if self.fail_groups > 0:
            self.fail_groups -= 1
            self.desynced = True
            return
        for key, data, frame in group:
            assert self._bus.cs[key].value is False, "CS went high before the group ended"
            frame[1] = bytes(memoryview(data))
            self._bus.frames.append(frame)

    @property
    def done(self):
        if self.auto_finish:
            self.finish()
        return self._group is None


class FakeBus:
    def __init__(self, key_count, auto_finish=True):
        self.dc = FakePin("dc", value=False)
        self.rst = FakePin("rst", value=True)
        self.cs = [FakePin("cs{}".format(i), value=True) for i in range(key_count)]
        self.pio = FakePioBus(self, auto_finish=auto_finish)
        self.parallel = FakeParallelBus(self, auto_finish=auto_finish)
        # The keys of each group started on `parallel`, in order.
        self.groups = []
        # One [key, command, data] entry per command written.
        self.records = []
        # One [key, bytes] entry per frame started, in order.
        self.frames = []
        # The panels that took a RAMWR and no other command since.
        self.write_mode = set()

    def finish(self):
        """End the transfer on the wire."""
        self.pio.finish()
        self.parallel.finish()

    def panel(self, key, parallel=False, **kwargs):
        """A `Panel` for one key, on the single shared bus, or with
        `parallel=True` on its own DIN line of `FakeBus.parallel`.
        """
        kwargs.setdefault("sleep", lambda seconds: None)
        bus = self.parallel.panel_bus(key) if parallel else self.pio
        return st7735.Panel(bus, self.dc, self.cs[key], **kwargs)

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
