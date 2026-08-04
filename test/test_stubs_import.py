import board
import digitalio
import usb_hid
import usb_cdc
import audiobusio
import displayio


def test_stub_modules_import():
    assert board
    assert digitalio
    assert usb_hid
    assert usb_cdc
    assert audiobusio
    assert displayio


def test_digital_in_out_switch_to_input_reads_pull():
    switch = digitalio.DigitalInOut(board.GP0)
    switch.switch_to_input(pull=digitalio.Pull.UP)

    assert switch.direction == digitalio.Direction.INPUT
    assert switch.value is True

    switch.value = False
    assert switch.value is False
