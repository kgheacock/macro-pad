from firmware.pins import KEYS


def test_pins_count():
    assert len(KEYS) == 6


def test_pins_unique():
    switch_pins = [key.switch_pin for key in KEYS]
    display_cs_pins = [key.display_cs_pin for key in KEYS]
    backlight_pins = [key.backlight_pin for key in KEYS]

    for pins in (switch_pins, display_cs_pins, backlight_pins):
        assert all(pins)
        assert len(set(pins)) == len(pins)
