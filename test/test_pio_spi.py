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


def _frames(count=3):
    return [bytearray([key]) * 8 for key in range(count)]


def _start_group(bus, keys=(0, 1, 2)):
    frames = _frames(len(keys))
    bus.start_group(tuple(zip(keys, frames)))
    return frames


def _poll_until_done(bus, limit=10):
    for _ in range(limit):
        if bus.done:
            return True
    return False


def _leader():
    """The newest state machine that makes SCK."""
    return [sm for sm in rp2pio.created if "first_sideset_pin" in sm.kwargs][-1]


def _live_followers():
    return [sm for sm in rp2pio.created if "first_out_pin" in sm.kwargs and "first_in_pin" in sm.kwargs and not sm.deinitialized]


def test_the_programs_are_the_assembled_leaders_and_followers():
    # Assembled with adafruit_pioasm 1.3.8. Little-endian 16-bit words.
    leaders = [
        None if program is None else bytes(memoryview(program)).hex()
        for program in pio_spi._LEADER_PROGRAMS
    ]
    assert leaders == [
        None,
        "c020" "27e0" "42a6" "4214",
        "c020" "c120" "27e0" "42a6" "4314",
        "c020" "c120" "c220" "27e0" "42a6" "4414",
    ]
    followers = [bytes(memoryview(program)).hex() for program in pio_spi._FOLLOWER_PROGRAMS]
    assert followers == [
        "a080" "00c0" "27e0" "2020" "0160" "a020" "4300",
        "a080" "01c0" "27e0" "2020" "0160" "a020" "4300",
        "a080" "02c0" "27e0" "2020" "0160" "a020" "4300",
    ]


def test_the_leader_makes_a_12_5_mhz_clock_from_150_mhz():
    cycles_per_bit = 7 + 5  # `side 0 [6]`, then `side 1 [4]` after the jump's own cycle

    assert pio_spi.SYSTEM_CLOCK_HZ / cycles_per_bit == pio_spi.PARALLEL_BAUDRATE == 12_500_000


def test_sck_stays_high_for_at_least_five_cycles_so_a_late_follower_sees_it():
    jmp_word = pio_spi._LEADER_PROGRAMS[3][-1]
    delay = (jmp_word >> 8) & 0x0F  # 4 bits of delay, 1 bit of side-set

    assert 1 + delay >= 5


def test_a_frame_takes_21_ms_at_12_5_mhz():
    seconds = FRAME_BYTES * 8 / pio_spi.PARALLEL_BAUDRATE

    assert round(seconds * 1000) == 21


def test_a_group_has_at_most_three_panels_because_a_pio_block_has_four_machines():
    assert pio_spi.MAX_GROUP == 3
    assert _parallel().max_group == 3


def test_start_group_builds_the_followers_before_the_leader():
    bus = _parallel()

    _start_group(bus, keys=(1, 3, 5))

    followers, leader = rp2pio.created[:3], rp2pio.created[3]
    assert len(rp2pio.created) == 4
    assert leader.kwargs["first_sideset_pin"] == "SCK"
    assert leader.kwargs["exclusive_pin_use"] is False
    assert "first_out_pin" not in leader.kwargs
    assert leader.frequency == pio_spi.SYSTEM_CLOCK_HZ
    assert leader.program == bytes(memoryview(pio_spi._LEADER_PROGRAMS[3]))
    assert [sm.kwargs["first_out_pin"] for sm in followers] == ["DIN1", "DIN3", "DIN5"]
    for slot, sm in enumerate(followers):
        assert sm.program == bytes(memoryview(pio_spi._FOLLOWER_PROGRAMS[slot]))
        assert sm.kwargs["first_in_pin"] == "SCK"
        assert sm.kwargs["exclusive_pin_use"] is False
        assert sm.kwargs["out_shift_right"] is False
        assert not sm.kwargs.get("auto_pull")  # the program pulls a byte itself
        assert sm.frequency == pio_spi.SYSTEM_CLOCK_HZ


def test_the_leader_waits_for_exactly_the_flags_of_the_followers_in_the_group():
    for count in (1, 2, 3):
        rp2pio.reset()
        bus = _parallel()

        _start_group(bus, keys=tuple(range(count)))

        assert _leader().program == bytes(memoryview(pio_spi._LEADER_PROGRAMS[count]))



def test_start_group_gives_each_follower_its_own_frame_without_copying_it():
    bus = _parallel()

    frames = _start_group(bus)

    for index, frame in enumerate(frames):
        assert rp2pio.created[index].writes[0] is frame
    assert _leader().writes == []  # the leader needs no data: the followers pace it


def test_start_group_builds_only_the_followers_of_the_group():
    bus = _parallel()
    frame = bytearray(8)

    bus.start_group(((1, frame), (4, frame)))

    assert len(rp2pio.created) == 3
    assert [sm.kwargs["first_out_pin"] for sm in rp2pio.created[:2]] == ["DIN1", "DIN4"]


def test_start_group_refuses_a_group_of_four():
    bus = _parallel()
    frame = bytearray(8)

    with pytest.raises(ValueError):
        bus.start_group(tuple((key, frame) for key in range(4)))


def test_a_group_of_the_same_panels_reuses_the_machines():
    bus = _parallel()
    _start_group(bus)
    assert _poll_until_done(bus)

    _start_group(bus)

    assert len(rp2pio.created) == 4
    assert _poll_until_done(bus)
    assert bus.desynced is False


def test_a_group_of_other_panels_rebuilds_the_followers_and_the_leader():
    bus = _parallel()
    _start_group(bus, keys=(0, 1, 2))
    assert _poll_until_done(bus)

    _start_group(bus, keys=(3, 4, 5))

    assert all(sm.deinitialized for sm in rp2pio.created[:4])
    assert len(rp2pio.created) == 8
    assert rp2pio.created[7] is _leader() and not _leader().deinitialized
    assert [sm.kwargs["first_out_pin"] for sm in _live_followers()] == ["DIN3", "DIN4", "DIN5"]
    assert _poll_until_done(bus)


def test_done_is_false_until_every_follower_dma_ends_and_every_follower_has_stalled():
    bus = _parallel()
    _start_group(bus)
    rp2pio.created[1].polls_to_finish = 2  # the DMA of one follower is slow

    assert bus.done is False  # a follower's DMA runs
    assert bus.done is False
    assert bus.done is False  # the DMAs are done, and the stall flags are cleared now
    assert bus.done is True
    assert bus.desynced is False
    assert bus.done is True  # nothing is on the wire any more


def test_done_waits_for_the_last_bytes_to_shift_out_of_every_follower():
    bus = _parallel()
    _start_group(bus)
    rp2pio.created[2].stall_polls = 3  # its last bytes are still shifting out

    polls = 0
    while not bus.done:
        polls += 1
        assert polls < 10

    assert polls >= 3
    assert bus.desynced is False


def test_start_group_refuses_while_a_group_is_on_the_wire():
    bus = _parallel()
    _start_group(bus)

    with pytest.raises(RuntimeError):
        _start_group(bus)


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


def test_a_group_after_a_command_frees_the_command_machine_and_builds_four():
    bus = _parallel()
    bus.write(2, b"\x2c")

    _start_group(bus)

    assert rp2pio.created[0].deinitialized
    assert len(rp2pio.created) == 5


def test_a_command_after_a_group_frees_the_leader_and_the_followers():
    bus = _parallel()
    _start_group(bus)
    assert _poll_until_done(bus)

    bus.write(0, b"\x2c")

    assert all(sm.deinitialized for sm in rp2pio.created[:4])


def test_a_command_while_a_group_is_on_the_wire_is_refused():
    bus = _parallel()
    _start_group(bus)

    with pytest.raises(RuntimeError):
        bus.write(0, b"\x2c")


def test_desync_recovery_rebuilds_the_leader_and_the_followers(monkeypatch):
    """DoD-4: a follower whose DMA never ends has lost sync, because it is
    waiting for a clock edge it missed. The bus stops and frees all the
    machines, and says so.
    """
    monkeypatch.setattr(pio_spi, "PUSH_TIMEOUT_NS", 0)
    bus = _parallel()
    _start_group(bus)
    rp2pio.created[1].polls_to_finish = 10**9  # follower 1: its DMA never ends

    assert _poll_until_done(bus)

    assert bus.desynced is True
    assert all(sm.deinitialized for sm in rp2pio.created[:4])
    assert all(sm.stops >= 1 for sm in rp2pio.created[:4])

    _start_group(bus)  # the next group builds four new machines

    assert len(rp2pio.created) == 8
    assert bus.desynced is False
    assert _poll_until_done(bus)
    assert bus.desynced is False


def test_desync_recovery_finds_a_follower_that_waits_for_a_clock_edge(monkeypatch):
    monkeypatch.setattr(pio_spi, "DRAIN_TIMEOUT_NS", 0)
    bus = _parallel()
    _start_group(bus)
    rp2pio.created[2].never_stalls = True  # its DMA is done, but it is not drained

    assert _poll_until_done(bus)

    assert bus.desynced is True


def test_a_follower_that_has_not_stalled_yet_is_given_time_to_drain():
    bus = _parallel()
    _start_group(bus)
    rp2pio.created[0].never_stalls = True

    assert bus.done is False  # the DMAs are done: the flags are cleared
    assert bus.done is False  # follower 0 has not stalled, but has time left
    assert bus.desynced is False


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


def test_desync_recovery_resends_the_window_and_the_frame(monkeypatch):
    """DoD-4: a follower that does not finish with the leader makes the code
    rebuild, resend the window, and resend the frame.
    """
    clock = [10**12]
    monkeypatch.setattr(pio_spi.time, "monotonic_ns", lambda: clock[0])
    pad, bus, pins_ = _real_pad()
    pad.step(0)  # the windows of keys 0 to 2, then their frames on the wire
    first = list(rp2pio.created)
    # Three one-panel command machines, then three followers and the leader.
    assert len(first) == 3 + 1 + 3
    sent = [first[3 + key].writes[0] for key in range(3)]
    first[4].never_stalls = True  # the follower of key 1 waits for a clock edge
    clock[0] += 2 * pio_spi.DRAIN_TIMEOUT_NS  # and the time to drain runs out

    pad.step(1000)

    assert pad.resyncs == 1
    assert all(sm.deinitialized for sm in first)
    after = rp2pio.created[len(first):]
    # The window goes out again through one panel's DIN at a time, then three
    # new followers and a new leader send the same three frames.
    command_machines = after[:3]
    assert [sm.kwargs["first_out_pin"] for sm in command_machines] == DINS[:3]
    for sm in command_machines:
        assert _commands(sm)[0] == bytes((st7735.CASET,))
        assert _commands(sm)[-1] == bytes((st7735.RAMWR,))
    followers, leader = after[3:6], after[6]
    assert leader.kwargs["first_sideset_pin"] == "SCK"
    assert [sm.kwargs["first_out_pin"] for sm in followers] == DINS[:3]
    assert [sm.writes[0] for sm in followers] == sent
    assert [cs.value for cs in pins_.cs[:3]] == [True] * 3  # the resent group has ended
