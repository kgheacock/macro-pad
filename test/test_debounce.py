from firmware.debounce import Debouncer


def test_within_window_ignored():
    debouncer = Debouncer(window_ms=5)

    assert debouncer.feed(True, 0) == "press"
    assert debouncer.feed(False, 2000) is None  # bounce inside the window
    assert debouncer.feed(False, 6000) == "release"  # window has passed


def test_press_release_sequence():
    debouncer = Debouncer(window_ms=5)

    assert debouncer.feed(True, 0) == "press"
    assert debouncer.feed(False, 10000) == "release"
