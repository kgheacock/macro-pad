"""Configures the RP2350 to enumerate as a USB composite device: HID for
the key state message, CDC serial for events and audio.

The `usb_hid.Device` report descriptor below defines a single output
report sized to the key state message in
docs/wire-protocol.md#key-state-hid-host--device. It carries no input
report — the host only sends key state, the device never sends HID
reports back.

See tasks/ongoing/0008-usb-composite-descriptor.md for the design
decision.
"""

import usb_cdc
import usb_hid

KEY_STATE_REPORT_ID = 1
KEY_STATE_REPORT_SIZE = 6  # docs/wire-protocol.md's Key state message

_KEY_STATE_REPORT_DESCRIPTOR = bytes(
    (
        0x06, 0x00, 0xFF,  # Usage Page (Vendor Defined 0xFF00)
        0x09, 0x01,  # Usage (Vendor Usage 1)
        0xA1, 0x01,  # Collection (Application)
        0x85, KEY_STATE_REPORT_ID,  #   Report ID
        0x09, 0x02,  #   Usage (Vendor Usage 2)
        0x15, 0x00,  #   Logical Minimum (0)
        0x26, 0xFF, 0x00,  #   Logical Maximum (255)
        0x75, 0x08,  #   Report Size (8 bits)
        0x95, KEY_STATE_REPORT_SIZE,  #   Report Count
        0x91, 0x02,  #   Output (Data, Var, Abs)
        0xC0,  # End Collection
    )
)

KEY_STATE_DEVICE = usb_hid.Device(
    report_descriptor=_KEY_STATE_REPORT_DESCRIPTOR,
    usage_page=0xFF00,
    usage=0x01,
    report_ids=(KEY_STATE_REPORT_ID,),
    in_report_lengths=(0,),
    out_report_lengths=(KEY_STATE_REPORT_SIZE,),
)

usb_hid.enable((KEY_STATE_DEVICE,))
usb_cdc.enable(console=False, data=True)
