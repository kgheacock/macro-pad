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


def _booted_bus(key_count, **panel_kwargs):
    bus = FakeBus(key_count=key_count)
    panels = [bus.panel(i, **panel_kwargs) for i in range(key_count)]
    st7735.init_panels(bus.rst, panels, sleep=lambda seconds: None)
    return bus, panels


def test_window_once_per_panel_after_init_panels():
    bus, panels = _booted_bus(3, colstart=2, rowstart=3)

    for key in range(3):
        commands = bus.commands(key)
        assert commands.count(st7735.CASET) == 1
        assert commands.count(st7735.RASET) == 1
        assert commands.count(st7735.RAMWR) == 1
        assert commands[-3:] == [st7735.CASET, st7735.RASET, st7735.RAMWR]
        assert bus.window(key) == ((2, 129), (3, 130))
        assert bus.cs[key].value is True
    assert bus.frames == []


def test_frame_only_push_sends_the_frame_and_no_command():
    bus, panels = _booted_bus(2)
    commands_before = list(bus.records)
    frame = bytes(range(256)) * (FRAME_BYTES // 256)

    panels[1].push(frame)
    panels[1].push(frame)

    assert bus.records == commands_before
    assert bus.frame_count(1) == 2
    assert bus.image(1) == frame
    assert bus.frame_count(0) == 0
    assert bus.dc.value is True


def test_window_after_failed_push():
    bus, panels = _booted_bus(1, colstart=2, rowstart=3)
    panel = panels[0]
    real_start = bus.pio.start

    def broken(data):
        raise RuntimeError("write failed")

    bus.pio.start = broken
    with pytest.raises(RuntimeError):
        panel.start_push(bytes(FRAME_BYTES))
    bus.pio.start = real_start
    commands_before = len(bus.records)

    panel.push(bytes(FRAME_BYTES))
    panel.push(bytes(FRAME_BYTES))

    # The window and RAMWR go out once, before the first good frame only.
    assert [c for _, c, _ in bus.records[commands_before:]] == [
        st7735.CASET,
        st7735.RASET,
        st7735.RAMWR,
    ]
    assert bus.window(0) == ((2, 129), (3, 130))
    assert bus.frame_count(0) == 2


def test_first_push_without_init_panels_sends_the_window():
    bus = FakeBus(key_count=1)

    bus.panel(0).push(bytes(FRAME_BYTES))

    assert bus.commands(0) == [st7735.CASET, st7735.RASET, st7735.RAMWR]


def test_a_command_takes_the_fake_panel_out_of_write_mode():
    bus, panels = _booted_bus(1)
    panels[0]._begin()
    panels[0]._command(0x13)  # NORON, no arguments
    bus.dc.value = True

    with pytest.raises(AssertionError, match="RAMWR"):
        bus.pio.start(bytes(FRAME_BYTES))


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


def test_transaction_releases_cs_after_push():
    bus = FakeBus(key_count=2)

    bus.panel(0).push(bytes(FRAME_BYTES))

    assert bus.cs[0].value is True


def test_transaction_releases_cs_when_a_write_fails():
    bus = FakeBus(key_count=1)
    panel = bus.panel(0)

    def broken(data):
        raise RuntimeError("write failed")

    bus.pio.start = broken

    with pytest.raises(RuntimeError):
        panel.start_push(bytes(FRAME_BYTES))

    assert bus.cs[0].value is True
    assert panel.busy is False


def test_cs_idles_high():
    bus = FakeBus(key_count=2)
    bus.cs[0]._value = None

    bus.panel(0)

    assert bus.cs[0].value is True


def test_start_push_sends_the_window_and_the_frame_byte_identical():
    bus = FakeBus(key_count=2)
    panel = bus.panel(1, colstart=2, rowstart=3)
    frame = bytes(range(256)) * (FRAME_BYTES // 256)

    panel.start_push(frame)
    assert panel.poll() is True

    assert bus.commands(1) == [st7735.CASET, st7735.RASET, st7735.RAMWR]
    assert bus.window(1) == ((2, 129), (3, 130))
    assert bus.image(1) == frame
    assert bus.commands(0) == []


def test_start_push_returns_with_cs_low_and_the_panel_busy():
    bus = FakeBus(key_count=2, auto_finish=False)
    panel = bus.panel(0)

    panel.start_push(bytes(FRAME_BYTES))

    assert panel.busy is True
    assert bus.cs[0].value is False
    assert bus.cs[1].value is True
    assert bus.pio.starts == 1


def test_start_push_poll_keeps_cs_low_until_the_state_machine_is_done():
    bus = FakeBus(key_count=1, auto_finish=False)
    panel = bus.panel(0)
    panel.start_push(bytes(FRAME_BYTES))

    assert panel.poll() is False
    assert panel.poll() is False
    assert bus.cs[0].value is False
    assert panel.busy is True

    bus.finish()

    assert panel.poll() is True
    assert bus.cs[0].value is True
    assert panel.busy is False
    assert bus.image(0) == bytes(FRAME_BYTES)


def test_start_push_takes_a_memoryview():
    bus = FakeBus(key_count=1)
    frame = bytearray(b"\xf8\x00") * (FRAME_BYTES // 2)

    panel = bus.panel(0)
    panel.start_push(memoryview(frame))
    panel.poll()

    assert bus.image(0) == bytes(frame)


def test_start_push_refuses_a_second_push_while_busy():
    bus = FakeBus(key_count=1, auto_finish=False)
    panel = bus.panel(0)
    panel.start_push(bytes(FRAME_BYTES))

    with pytest.raises(RuntimeError):
        panel.start_push(bytes(FRAME_BYTES))

    assert bus.frame_count(0) == 1  # one RAMWR, not two
    assert bus.image(0) == b""  # its frame is not on the wire yet
    bus.finish()
    panel.poll()
    assert bus.image(0) == bytes(FRAME_BYTES)


def test_start_push_holds_the_frame_until_poll_sees_it_sent():
    bus = FakeBus(key_count=1, auto_finish=False)
    panel = bus.panel(0)
    frame = bytearray(FRAME_BYTES)

    panel.start_push(frame)

    assert panel._frame is frame
    bus.finish()
    panel.poll()
    assert panel._frame is None


def test_poll_on_an_idle_panel_is_true_and_leaves_cs_alone():
    bus = FakeBus(key_count=1)
    panel = bus.panel(0)

    assert panel.poll() is True
    assert bus.cs[0].log == [True]  # only the constructor's write


def test_panel_can_push_again_after_poll_sees_the_last_push_sent():
    bus = FakeBus(key_count=1, auto_finish=False)
    panel = bus.panel(0)
    panel.start_push(bytes(FRAME_BYTES))
    bus.finish()
    panel.poll()

    panel.start_push(b"\x01\x02" * (FRAME_BYTES // 2))
    bus.finish()
    panel.poll()

    assert bus.frame_count(0) == 2
    assert bus.image(0) == b"\x01\x02" * (FRAME_BYTES // 2)
