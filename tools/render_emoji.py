"""Renders one Unicode emoji character to a 128x128 PNG, using macOS's
built-in Apple Color Emoji font. `driver/cmd/macrodriver`'s `emoji`
subcommand runs this script and sends the PNG it writes through task
0030's setCustomGlyph wire path — see tasks/complete/0034-emoji-
character-to-custom-glyph-image.md.

Usage:

    python3 tools/render_emoji.py <emoji-char> <output-path.png>

macOS only: Apple Color Emoji lives at a fixed system path this script
assumes is present.
"""

import sys

from PIL import Image, ImageDraw, ImageFont

FONT_PATH = "/System/Library/Fonts/Apple Color Emoji.ttc"

# Apple Color Emoji only rasterizes at a handful of fixed strike sizes;
# most other point sizes fail with Pillow's "invalid pixel size". 96px is
# a working strike size, confirmed live against this font. See the task
# spec's Risks section.
STRIKE_SIZE = 96

CANVAS_SIZE = 128


def render(char, output_path):
    font = ImageFont.truetype(FONT_PATH, STRIKE_SIZE)

    # textbbox needs a real image to measure against; its own pixels are
    # discarded once the glyph's bounding box is known.
    probe = ImageDraw.Draw(Image.new("RGBA", (1, 1)))
    bbox = probe.textbbox((0, 0), char, font=font, embedded_color=True)
    width, height = bbox[2] - bbox[0], bbox[3] - bbox[1]

    glyph = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    ImageDraw.Draw(glyph).text(
        (-bbox[0], -bbox[1]), char, font=font, embedded_color=True
    )

    canvas = Image.new("RGB", (CANVAS_SIZE, CANVAS_SIZE), (0, 0, 0))
    offset = ((CANVAS_SIZE - width) // 2, (CANVAS_SIZE - height) // 2)
    canvas.paste(glyph, offset, glyph)
    canvas.save(output_path, "PNG")


def main(argv):
    if len(argv) != 3:
        print(f"usage: {argv[0]} <emoji-char> <output-path.png>", file=sys.stderr)
        return 2
    render(argv[1], argv[2])
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
