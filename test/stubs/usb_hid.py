"""Fake `usb_hid` module. See ../README.md for how these stubs are used."""


class Device:
    """Fake HID device. Records the last report sent by code under test."""

    def __init__(self, *, usage_page=0x01, usage=0x06):
        self.usage_page = usage_page
        self.usage = usage
        self.last_report_data = None

    def send_report(self, report):
        self.last_report_data = bytes(report)


devices = (Device(),)
