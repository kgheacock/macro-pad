"""Fake `board` module. See ../README.md for how these stubs are used."""


class Pin:
    def __init__(self, name):
        self.id = name

    def __repr__(self):
        return f"Pin({self.id})"


_GPIO_COUNT = 30

for _n in range(_GPIO_COUNT):
    globals()[f"GP{_n}"] = Pin(f"GP{_n}")

del _n
