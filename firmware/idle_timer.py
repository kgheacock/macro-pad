class IdleTimer:
    """Tracks idle time and the resulting backlight duty cycle.

    See tasks/ongoing/0004-idle-timeout-backlight-dimming.md for the design
    decision.
    """

    def __init__(
        self,
        idle_window_us: int = 300_000_000,
        dim_duty_cycle: float = 0.1,
    ) -> None:
        self._idle_window_us = idle_window_us
        self._dim_duty_cycle = dim_duty_cycle
        self._last_activity_us = 0

    def touch(self, timestamp_us: int) -> None:
        self._last_activity_us = timestamp_us

    def duty_cycle(self, timestamp_us: int) -> float:
        if timestamp_us - self._last_activity_us >= self._idle_window_us:
            return self._dim_duty_cycle
        return 1.0
