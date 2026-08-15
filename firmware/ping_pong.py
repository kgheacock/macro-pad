"""Minimal firmware image that answers docs/wire-protocol.md's Ping.

`make ping-pong` copies this file to the CIRCUITPY drive as `code.py`, in
place of `app.py`'s full loop, so it proves the HID and CDC channels carry
data with no displays, mic, or switches attached. `firmware/code.py` and
`firmware/boot.py` stay on disk unchanged; `make flash` puts the real
`code.py` back on the board afterward.

See tasks/ongoing/0027-make-ping-pong-connectivity-check.md for the design
decision.
"""

import usb_cdc
import usb_hid

import wire


def _strip_report_id(report):
    """Drop the leading report ID byte when CircuitPython includes it.

    Mirrors `app.MacroPad._strip_report_id`.
    """
    if len(report) == wire.KEY_STATE_SIZE + 1:
        return report[1:]
    return report


def answer_ping(hid_device, serial):
    """Answer one ping if the last HID report carries one.

    Returns True when a pong was written, so a caller can tell a no-op
    iteration from one that answered.
    """
    report = hid_device.get_last_received_report()
    if not report:
        return False

    try:
        message = wire.decode_key_state(_strip_report_id(report))
    except ValueError:
        return False

    if message.key_index != wire.PING_KEY_INDEX:
        return False

    wire.write_frame(serial, wire.MESSAGE_TYPE_PONG, wire.encode_pong(message.emoji_id))
    return True


def run(hid_device, serial):
    """Loop forever, answering every ping."""
    while True:
        answer_ping(hid_device, serial)


run(usb_hid.devices[0], usb_cdc.data)
