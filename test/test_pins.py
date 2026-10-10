from firmware.pins import BACKLIGHT, KEYS, SPI_SCK


def test_pins_count():
    assert len(KEYS) == 6


def test_pins_unique():
    switch_pins = [key.switch_pin for key in KEYS]
    display_cs_pins = [key.display_cs_pin for key in KEYS]
    din_pins = [key.din_pin for key in KEYS]

    for pins in (switch_pins, display_cs_pins, din_pins):
        assert all(pins)
        assert len(set(pins)) == len(pins)


def test_each_key_has_its_own_din_pin():
    """Task 0048: keys 0 to 3 and 5 have their DIN on their old backlight pin.
    Key 4 has it on GP12, because the GP0 row did not work on the board."""
    assert [key.din_pin for key in KEYS] == ["GP27", "GP22", "GP28", "GP26", "GP12", "GP1"]


def test_keys_are_numbered_in_installation_order():
    """The one-time renumbering: each key's switch and CS pin stay with its
    DIN pin, in the new order."""
    assert [(key.switch_pin, key.display_cs_pin) for key in KEYS] == [
        ("GP17", "GP8"), ("GP15", "GP6"), ("GP18", "GP9"),
        ("GP16", "GP3"), ("GP13", "GP4"), ("GP14", "GP5"),
    ]


def test_the_six_backlights_share_one_pwm_pin_on_gp7():
    assert BACKLIGHT == "GP7"


def test_no_pin_is_used_for_two_functions():
    used = [SPI_SCK, BACKLIGHT]
    for key in KEYS:
        used += [key.switch_pin, key.display_cs_pin, key.din_pin]

    assert len(set(used)) == len(used)
