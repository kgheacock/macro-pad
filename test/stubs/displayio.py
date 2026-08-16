"""Fake `displayio` module. See ../README.md for how these stubs are used."""


class Bitmap:
    def __init__(self, width, height, value_count):
        self.width = width
        self.height = height
        self._pixels = [0] * (width * height)

    def __getitem__(self, index):
        return self._pixels[index]

    def __setitem__(self, index, value):
        self._pixels[index] = value


class Palette:
    def __init__(self, color_count):
        self._colors = [0] * color_count

    def __len__(self):
        return len(self._colors)

    def __getitem__(self, index):
        return self._colors[index]

    def __setitem__(self, index, color):
        self._colors[index] = color


class Colorspace:
    RGB565 = "RGB565"


class ColorConverter:
    def __init__(self, *, input_colorspace=None, **kwargs):
        self.input_colorspace = input_colorspace


class TileGrid:
    def __init__(self, bitmap, *, pixel_shader, **kwargs):
        self.bitmap = bitmap
        self.pixel_shader = pixel_shader
        self.x = kwargs.get("x", 0)
        self.y = kwargs.get("y", 0)


class Group:
    def __init__(self, *, scale=1, **kwargs):
        self.scale = scale
        self._layers = []

    def append(self, layer):
        self._layers.append(layer)

    def remove(self, layer):
        self._layers.remove(layer)

    def __len__(self):
        return len(self._layers)

    def __iter__(self):
        return iter(self._layers)


class FourWire:
    def __init__(self, spi_bus, *, command, chip_select, reset=None, **kwargs):
        self.spi_bus = spi_bus
        self.command = command
        self.chip_select = chip_select
        self.reset = reset


class Display:
    def __init__(self, display_bus, *, width, height, **kwargs):
        self.display_bus = display_bus
        self.width = width
        self.height = height
        self.root_group = None

    def show(self, group):
        self.root_group = group

    def refresh(self, **kwargs):
        return True


def release_displays():
    pass
