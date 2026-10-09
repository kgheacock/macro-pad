import pytest
import rp2pio

import pio_spi
import st7735
from test_app import FakeBacklight, FakeHID, FakeSerial
from app import MacroPad, make_switch
from spibus import FakePin

FRAME_BYTES = 128 * 128 * 2


def _bus(baudrate=15_000_000):
    return pio_spi.PioBus(sck="SCK", mosi="MOSI", baudrate=baudrate)


def test_state_machine_runs_at_two_pio_clocks_per_bit():
    bus = _bus(15_000_000)

    assert bus._sm.frequency == 30_000_000


def test_state_machine_shifts_eight_bits_most_significant_first_on_the_pins():
    sm = _bus()._sm

    assert sm.kwargs["first_out_pin"] == "MOSI"
    assert sm.kwargs["first_sideset_pin"] == "SCK"
    assert sm.kwargs["auto_pull"] is True
    assert sm.kwargs["pull_threshold"] == 8
    assert sm.kwargs["out_shift_right"] is False


def test_program_is_the_assembled_spi_mode_0_loop():
    # `out pins, 1 side 0` and `nop side 1`, little-endian 16-bit words.
    assert _bus()._sm.program == bytes.fromhex("0160" "42b0")


def test_the_system_clock_divides_by_a_whole_number_at_15_mhz():
    divider = pio_spi.SYSTEM_CLOCK_HZ / (15_000_000 * 2)

    assert divider == 5


def test_start_passes_the_buffer_to_the_dma_without_copying_it():
    bus = _bus()
    frame = bytearray(FRAME_BYTES)

    bus.start(frame)

    assert bus._sm.writes == [frame]
    assert bus._sm.writes[0] is frame


def test_done_is_false_until_the_background_write_ends():
    bus = _bus()
    bus._sm.polls_to_finish = 2

    bus.start(b"\x00" * 8)

    assert bus.done is False
    assert bus.done is False
    assert bus.done is True


def test_done_waits_for_the_last_bytes_to_shift_out():
    bus = _bus()
    bus._sm.stall_polls = 3
    bus.start(b"\x00" * 8)
    bus._sm.finish()

    assert bus.done is False  # the DMA is done, the last bytes are not
    assert bus.done is False
    assert bus.done is True


def test_done_ignores_a_stall_flag_set_before_the_dma_ends():
    bus = _bus()
    bus._sm.polls_to_finish = 5
    bus._sm._stalled = True  # the state machine idled before the first byte

    bus.start(b"\x00" * 8)

    assert bus._sm.txstall is True
    assert bus.done is False


def test_done_is_true_at_once_for_an_idle_state_machine():
    assert _bus().done is True


def test_write_returns_when_the_bytes_are_on_the_wire():
    bus = _bus()
    bus._sm.polls_to_finish = 3

    bus.write(b"\x2c")

    assert bus.done is True
    assert bus._sm.writes == [b"\x2c"]


# --- ParallelBus (task 0048) ---

DINS = ["DIN0", "DIN1", "DIN2", "DIN3", "DIN4", "DIN5"]


def setup_function():
    rp2pio.reset()


def _parallel():
    return pio_spi.ParallelBus(sck="SCK", dins=DINS)


def _frames():
    return [bytearray([key]) * 8 for key in range(6)]


def _start_all(bus):
    frames = _frames()
    bus.start_group(tuple(enumerate(frames)))
    return frames


def _poll_until_done(bus, limit=10):
    for _ in range(limit):
        if bus.done:
            return True
    return False


def test_the_programs_are_the_assembled_leader_and_follower():
    # Assembled with adafruit_pioasm 1.3.8. Little-endian 16-bit words.
    assert bytes(memoryview(pio_spi._LEADER_PROGRAM)) == bytes.fromhex("6169" "42b4")
    assert bytes(memoryview(pio_spi._FOLLOWER_PROGRAM)) == bytes.fromhex("2020" "0160" "a020")


def test_the_leader_makes_a_10_mhz_clock_from_150_mhz():
    cycles_per_bit = 10 + 5  # `side 0 [9]`, then `side 1 [4]`

    assert pio_spi.SYSTEM_CLOCK_HZ / cycles_per_bit == pio_spi.PARALLEL_BAUDRATE == 10_000_000


def test_a_frame_takes_26_ms_at_10_mhz():
    seconds = FRAME_BYTES * 8 / pio_spi.PARALLEL_BAUDRATE

    assert round(seconds * 1000) == 26


def test_start_group_builds_the_six_followers_before_the_leader():
    bus = _parallel()

    _start_all(bus)

    followers, leader = rp2pio.created[:6], rp2pio.created[6]
    assert len(rp2pio.created) == 7
    assert [sm.kwargs["first_out_pin"] for sm in followers] == DINS
    for sm in followers:
        assert sm.kwargs["first_in_pin"] == "SCK"
        assert sm.kwargs["exclusive_pin_use"] is False
        assert sm.kwargs["auto_pull"] is True
        assert sm.kwargs["pull_threshold"] == 8
        assert sm.kwargs["out_shift_right"] is False
        assert sm.frequency == pio_spi.SYSTEM_CLOCK_HZ
    assert leader.kwargs["first_sideset_pin"] == "SCK"
    assert "first_out_pin" not in leader.kwargs
    assert leader.frequency == pio_spi.SYSTEM_CLOCK_HZ


def test_start_group_gives_each_follower_its_own_frame_without_copying_it():
    bus = _parallel()

    frames = _start_all(bus)

    for key, frame in enumerate(frames):
        assert rp2pio.created[key].writes[0] is frame
    assert rp2pio.created[6].writes[0] is frames[0]  # the leader's bytes pace it


def test_start_group_starts_only_the_followers_of_the_group():
    bus = _parallel()
    frame = bytearray(8)

    bus.start_group(((1, frame), (4, frame)))

    written = [i for i, sm in enumerate(rp2pio.created[:6]) if sm.writes]
    assert written == [1, 4]


def test_done_is_false_until_the_leader_ends_and_every_follower_has_stalled():
    bus = _parallel()
    _start_all(bus)
    rp2pio.created[6].polls_to_finish = 2

    assert bus.done is False  # the leader's DMA runs
    assert bus.done is False
    assert bus.done is False  # the stall flags are cleared now
    assert bus.done is True
    assert bus.desynced is False
    assert bus.done is True  # nothing is on the wire any more


def test_done_does_not_block_a_second_group_after_the_first_ends():
    bus = _parallel()
    _start_all(bus)
    assert _poll_until_done(bus)

    _start_all(bus)

    assert len(rp2pio.created) == 7  # the machines were not rebuilt
    assert _poll_until_done(bus)


def test_start_group_refuses_while_a_group_is_on_the_wire():
    bus = _parallel()
    _start_all(bus)

    with pytest.raises(RuntimeError):
        _start_all(bus)


def test_write_sends_a_command_through_one_panels_din_at_the_command_rate():
    bus = _parallel()

    bus.write(2, b"\x2c")

    assert len(rp2pio.created) == 1
    sm = rp2pio.created[0]
    assert sm.kwargs["first_out_pin"] == "DIN2"
    assert sm.kwargs["first_sideset_pin"] == "SCK"
    assert sm.frequency == pio_spi.COMMAND_BAUDRATE * 2
    assert sm.writes == [b"\x2c"]


def test_write_to_another_panel_frees_the_first_panels_machine():
    bus = _parallel()
    bus.write(2, b"\x2c")
    bus.write(2, b"\x2a")

    bus.write(3, b"\x2b")

    assert len(rp2pio.created) == 2  # panel 2 kept one machine for both writes
    assert rp2pio.created[0].deinitialized
    assert rp2pio.created[1].kwargs["first_out_pin"] == "DIN3"


def test_a_group_after_a_command_frees_the_command_machine_and_builds_seven():
    bus = _parallel()
    bus.write(2, b"\x2c")

    _start_all(bus)

    assert rp2pio.created[0].deinitialized
    assert len(rp2pio.created) == 8


def test_a_command_while_a_group_is_on_the_wire_is_refused():
    bus = _parallel()
    _start_all(bus)

    with pytest.raises(RuntimeError):
        bus.write(0, b"\x2c")


def test_desync_recovery_rebuilds_all_seven_machines():
    """DoD-4: a follower whose DMA still runs after the leader ended has lost
    sync. The bus stops and frees all seven machines, and says so.
    """
    bus = _parallel()
    _start_all(bus)
    rp2pio.created[3].polls_to_finish = 10**9  # follower 3: its DMA never ends

    assert _poll_until_done(bus)

    assert bus.desynced is True
    assert all(sm.deinitialized for sm in rp2pio.created[:7])
    assert all(sm.stops >= 1 for sm in rp2pio.created[:7])

    _start_all(bus)  # the next group builds seven new machines

    assert len(rp2pio.created) == 14
    assert bus.desynced is False
    assert _poll_until_done(bus)
    assert bus.desynced is False


def test_desync_recovery_finds_a_follower_that_waits_for_a_clock_edge():
    bus = _parallel()
    _start_all(bus)
    rp2pio.created[5].never_stalls = True  # its DMA is done, but it is not drained

    assert _poll_until_done(bus)

    assert bus.desynced is True


def test_desync_recovery_ignores_a_follower_outside_the_group():
    bus = _parallel()
    frame = bytearray(8)
    bus.start_group(((0, frame),))
    rp2pio.created[4].never_stalls = True  # key 4 is not in this group

    assert _poll_until_done(bus)

    assert bus.desynced is False


def test_desync_recovery_gives_up_on_a_push_that_never_ends(monkeypatch):
    monkeypatch.setattr(pio_spi, "PUSH_TIMEOUT_NS", 0)
    bus = _parallel()
    _start_all(bus)
    rp2pio.created[6].polls_to_finish = 10**9  # the leader's DMA never ends

    assert bus.done is True

    assert bus.desynced is True


class _Recorder:
    """A `FakePin`-free CS and DC set for real `st7735.Panel`s on a real
    `ParallelBus` over the `rp2pio` stub."""

    def __init__(self):
        self.dc = FakePin("dc", value=False)
        self.cs = [FakePin("cs{}".format(i), value=True) for i in range(6)]


def _real_pad():
    bus = _parallel()
    pins_ = _Recorder()
    panels = [
        st7735.Panel(bus.panel_bus(i), pins_.dc, pins_.cs[i], sleep=lambda s: None)
        for i in range(6)
    ]
    import board

    switches = [make_switch(getattr(board, name)) for name in ("GP13", "GP14", "GP15", "GP16", "GP17", "GP18")]
    pad = MacroPad(
        switches=switches,
        panels=panels,
        backlights=[FakeBacklight()],
        hid_device=FakeHID(),
        serial=FakeSerial(),
        push_bus=bus,
    )
    return pad, bus, pins_


def _commands(sm):
    return [bytes(w) for w in sm.writes]


def test_desync_recovery_resends_the_window_and_the_frame():
    """DoD-4: a follower that does not finish with the leader makes the code
    rebuild, resend the window, and resend the frame.
    """
    pad, bus, pins_ = _real_pad()
    pad.step(0)  # windows, then six frames on the wire
    first = list(rp2pio.created)
    assert len(first) == 6 + 7  # six command machines, then the seven parallel ones
    sent = [first[7 + key].writes[0] for key in range(6)]
    first[7 + 3].polls_to_finish = 10**9  # follower 3 loses sync

    for _ in range(6):
        pad.step(1000)
        if pad.resyncs:
            break

    assert pad.resyncs == 1
    assert all(sm.deinitialized for sm in first)
    after = rp2pio.created[len(first):]
    # The window goes out again through one panel's DIN at a time, then seven
    # new machines send the same six frames.
    command_machines = after[:6]
    assert [sm.kwargs["first_out_pin"] for sm in command_machines] == DINS
    for sm in command_machines:
        assert _commands(sm)[0] == bytes((st7735.CASET,))
        assert _commands(sm)[-1] == bytes((st7735.RAMWR,))
    followers = after[6:12]
    assert [sm.kwargs["first_out_pin"] for sm in followers] == DINS
    assert [sm.writes[0] for sm in followers] == sent
    assert all(cs.value is True for cs in pins_.cs)  # and the resent group has ended
