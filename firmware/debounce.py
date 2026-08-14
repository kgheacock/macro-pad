from typing import Optional


class Debouncer:
    """Time-window debounce for a single switch's raw GPIO transitions.

    See tasks/ongoing/0003-debounce-module.md for the design decision.
    """

    def __init__(self, window_ms: float = 7.5) -> None:
        self._window_us = window_ms * 1000
        self._state = False
        self._last_accepted_us: Optional[int] = None

    def feed(self, pin_state: bool, timestamp_us: int) -> Optional[str]:
        if pin_state == self._state:
            return None

        if (
            self._last_accepted_us is not None
            and timestamp_us - self._last_accepted_us < self._window_us
        ):
            return None

        self._state = pin_state
        self._last_accepted_us = timestamp_us
        return "press" if pin_state else "release"
