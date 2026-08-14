"""Fake `digitalio` module. See ../README.md for how these stubs are used."""


class Direction:
    INPUT = "INPUT"
    OUTPUT = "OUTPUT"


class Pull:
    UP = "UP"
    DOWN = "DOWN"


class DriveMode:
    PUSH_PULL = "PUSH_PULL"
    OPEN_DRAIN = "OPEN_DRAIN"


class DigitalInOut:
    def __init__(self, pin):
        self._pin = pin
        self.direction = Direction.INPUT
        self.pull = None
        self.drive_mode = DriveMode.PUSH_PULL
        self._value = False

    def switch_to_input(self, pull=None):
        self.direction = Direction.INPUT
        self.pull = pull
        self._value = pull == Pull.UP

    def switch_to_output(self, value=False, drive_mode=DriveMode.PUSH_PULL):
        self.direction = Direction.OUTPUT
        self.drive_mode = drive_mode
        self._value = value

    @property
    def value(self):
        return self._value

    @value.setter
    def value(self, new_value):
        # Writable in either direction, unlike the real module, so a test
        # can drive an input pin low to simulate a switch closing.
        self._value = bool(new_value)

    def deinit(self):
        pass

    def __enter__(self):
        return self

    def __exit__(self, *exc_info):
        self.deinit()
