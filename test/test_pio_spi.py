import pio_spi

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
