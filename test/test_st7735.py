import pytest

import st7735
from spibus import FakeBus

SWRESET = 0x01
SLPOUT = 0x11
INVON = 0x21
DISPON = 0x29
INIT_ONLY_COMMANDS = {SWRESET, SLPOUT, INVON, DISPON}
FRAME_BYTES = 128 * 128 * 2


def test_init_runs_the_whole_sequence_on_one_panel_only():
    bus = FakeBus(key_count=3)

    bus.panel(1).init()

    commands = bus.commands(1)
    assert commands[0] == SWRESET
    assert commands[1] == SLPOUT
    assert commands[-1] == INVON
    assert INIT_ONLY_COMMANDS <= set(commands)
    assert bus.commands(0) == []
    assert bus.commands(2) == []


def test_init_sleeps_for_the_sequence_delays():
    bus = FakeBus(key_count=1)
    slept = []

    bus.panel(0, sleep=slept.append).init()

    # SWRESET 150 ms, SLPOUT 500 ms, NORON 10 ms, DISPON 100 ms.
    assert slept == [0.150, 0.500, 0.010, 0.100]


def test_init_sets_color_mode_madctl_and_invert():
    bus = FakeBus(key_count=1)

    bus.panel(0).init()

    by_command = {c: d for _, c, d in bus.records}
    assert by_command[0x3A] == b"\x05"  # COLMOD 16-bit
    assert [d for _, c, d in bus.records if c == 0x36][-1] == b"\xc8"  # RGB order
    assert INVON in by_command


def test_pulse_reset_leaves_rst_high():
    bus = FakeBus(key_count=1)

    st7735.pulse_reset(bus.rst, sleep=lambda seconds: None)

    assert bus.rst.log == [True, False, True]
    assert bus.rst.value is True


def test_init_panels_pulses_rst_once_and_inits_every_panel():
    bus = FakeBus(key_count=3)
    panels = [bus.panel(i) for i in range(3)]

    st7735.init_panels(bus.rst, panels, sleep=lambda seconds: None)

    assert bus.rst.log == [True, False, True]
    for key in range(3):
        assert bus.commands(key).count(SWRESET) == 1


def test_push_sets_window_with_offsets_then_writes_frame():
    bus = FakeBus(key_count=2)
    panel = bus.panel(1, colstart=2, rowstart=3)
    frame = bytes(range(256)) * (FRAME_BYTES // 256)

    panel.push(frame)

    assert bus.commands(1) == [st7735.CASET, st7735.RASET, st7735.RAMWR]
    assert bus.window(1) == ((2, 129), (3, 130))
    assert bus.image(1) == frame
    assert bus.commands(0) == []


def test_push_sends_no_init_command_and_leaves_rst_alone():
    bus = FakeBus(key_count=1)
    panel = bus.panel(0)

    panel.push(bytes(FRAME_BYTES))
    panel.push(bytes(FRAME_BYTES))

    assert not (INIT_ONLY_COMMANDS & set(bus.commands()))
    assert bus.rst.log == []


def test_push_takes_a_memoryview():
    bus = FakeBus(key_count=1)
    frame = bytearray(b"\xf8\x00") * (FRAME_BYTES // 2)

    bus.panel(0).push(memoryview(frame))

    assert bus.image(0) == bytes(frame)


def test_transaction_releases_cs_and_bus_after_push():
    bus = FakeBus(key_count=2)

    bus.panel(0).push(bytes(FRAME_BYTES))

    assert bus.cs[0].value is True
    assert bus.spi._locked is False


def test_transaction_releases_cs_and_bus_when_a_write_fails():
    bus = FakeBus(key_count=1)
    panel = bus.panel(0)

    class Broken:
        def __len__(self):
            raise RuntimeError("write failed")

    with pytest.raises(Exception):
        panel.push(Broken())

    assert bus.cs[0].value is True
    assert bus.spi._locked is False


def test_panel_uses_its_baudrate():
    bus = FakeBus(key_count=1)

    bus.panel(0, baudrate=16_000_000).push(bytes(FRAME_BYTES))

    assert bus.spi.configures == [(16_000_000, 0, 0)]


def test_cs_idles_high():
    bus = FakeBus(key_count=2)
    bus.cs[0]._value = None

    bus.panel(0)

    assert bus.cs[0].value is True
