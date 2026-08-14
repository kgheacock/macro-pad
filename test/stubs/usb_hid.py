"""Fake `usb_hid` module. See ../README.md for how these stubs are used."""


class Device:
    """Fake HID device.

    Records the last report sent by code under test, and hands back
    reports a test has queued with `feed`. `get_last_received_report`
    returns each queued report once and `None` after that, matching the
    core's behavior of reporting nothing new between host writes.
    """

    def __init__(self, *, usage_page=0x01, usage=0x06):
        self.usage_page = usage_page
        self.usage = usage
        self.last_report_data = None
        self._received = []

    def send_report(self, report):
        self.last_report_data = bytes(report)

    def get_last_received_report(self, report_id=None):
        if not self._received:
            return None
        return self._received.pop(0)

    def feed(self, report):
        """Test helper: queue a report for the next
        get_last_received_report() call.
        """
        self._received.append(bytes(report))


devices = (Device(),)
