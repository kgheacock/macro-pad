from firmware.idle_timer import IdleTimer


def test_bright_before_window():
    timer = IdleTimer(idle_window_us=5000)

    assert timer.duty_cycle(0) == 1.0
    assert timer.duty_cycle(4999) == 1.0


def test_dim_after_window():
    timer = IdleTimer(idle_window_us=5000)

    assert timer.duty_cycle(5000) == 0.1


def test_touch_resets():
    timer = IdleTimer(idle_window_us=5000)

    timer.touch(4000)
    assert timer.duty_cycle(8999) == 1.0
    assert timer.duty_cycle(9000) == 0.1
