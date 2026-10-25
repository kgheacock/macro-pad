#!/usr/bin/env python3
"""Body for the one-piece 110 x 100 mm ScreenKey panel (screenkey_panel_3x2.step).

    pip install numpy trimesh manifold3d
    python hardware/enclosure/build_panel_body.py

Writes panel_body.stl next to this script (units: mm), printed floor down.

The panel replaces the faceplate and the mounting plate of build_case.py: one
3 mm plate, the module PCBs screw to its back face, and 4 countersunk M3 holes
sit 5 mm in from each outer edge. This body is the same open tub as
build_case.body(), sized to the panel, with an M3 heat-set insert under each
of the panel's corner holes. The inserts are JLC3DP's threaded insert service, Type 5.

Panel numbers are measured from the STEP file: outline 110 x 100 mm with
4 mm corner radii, M3 holes at (+-50, +-45), keys on a 30 x 40 mm pitch.
"""
from build_case import (
    BED_CHAMFER, FLOOR_T, OUT,
    PCB_FRONT_DEPTH, PCB_H, PCB_W, SPACE_BELOW_MODULES, STANDOFF_TIP_DEPTH,
    USB_ABOVE_FLOOR, WALL, Manifold, cyl, placed, slab,
    to_mesh, union,
)

# ---- panel (screenkey_panel_3x2.step) ----------------------------------------
PANEL_W, PANEL_H = 110.0, 100.0
PANEL_R = 4.0
CORNERS = [(sx * 50.0, sy * 45.0) for sx in (-1, 1) for sy in (-1, 1)]
KEY_CENTRES = [(x, y) for y in (20.0, -20.0) for x in (-30.0, 0.0, 30.0)]

# ---- USB-C: Adafruit 4218 round panel-mount extension ------------------------
# Adafruit asks for a 21.5 to 27 mm hole; its flange and nut are about 30 mm across,
# so USB_ABOVE_FLOOR (30 mm to the hole centre) leaves 15 mm below the nut.
USB_HOLE_D = 22.0

# ---- corner inserts: JLC3DP threaded insert service, Type 5 "M3*4*5" -----------
# Insert OD 4.0, length 5. JLC3DP asks for a 3.6 mm hole 7 mm deep with at least
# 3 mm of wall around it, so each corner is a solid block reaching 3.2 mm past the hole.
INSERT_HOLE_D, INSERT_HOLE_DEPTH = 3.6, 7.0
INSERT_WALL = 3.2
BLOCK_HALF = INSERT_HOLE_D / 2 + INSERT_WALL          # hole centre to each block face

# ---- derived ----------------------------------------------------------------
MODULE_BEHIND_PANEL = STANDOFF_TIP_DEPTH - PCB_FRONT_DEPTH
CAVITY_DEPTH = MODULE_BEHIND_PANEL + SPACE_BELOW_MODULES
BODY_H = CAVITY_DEPTH + FLOOR_T


def check_fit():
    """Every module PCB must clear the walls and the corner blocks."""
    in_x, in_y = PANEL_W / 2 - WALL, PANEL_H / 2 - WALL
    for kx, ky in KEY_CENTRES:
        for sx in (-1, 1):
            for sy in (-1, 1):
                px, py = kx + sx * PCB_W / 2, ky + sy * PCB_H / 2
                assert abs(px) < in_x and abs(py) < in_y, f"PCB corner ({px}, {py}) hits a wall"
                for bx, by in CORNERS:
                    clear = max(abs(px - bx), abs(py - by)) - BLOCK_HALF
                    assert clear > 1.0, f"PCB corner ({px}, {py}) hits the block at ({bx}, {by})"


def body():
    z_top, z_bot = 0.0, -BODY_H
    outer = slab(PANEL_W, PANEL_H, PANEL_R, z_bot, z_top, chamfer_bottom=BED_CHAMFER)
    cavity = slab(PANEL_W - 2 * WALL, PANEL_H - 2 * WALL, PANEL_R - WALL / 2, z_bot + FLOOR_T, z_top + 0.01)
    # Full-height corner blocks from the floor up: nothing overhangs, and the corners are stiffer.
    block = Manifold.cube((2 * BLOCK_HALF, 2 * BLOCK_HALF, BODY_H - FLOOR_T + 0.03), center=True)
    blocks = union([block.translate((x, y, z_bot + FLOOR_T + (BODY_H - FLOOR_T) / 2)) for x, y in CORNERS])
    b = outer - (cavity - blocks)
    for x, y in CORNERS:
        b = b - cyl(INSERT_HOLE_D, z_top - INSERT_HOLE_DEPTH, z_top + 0.01, x, y)
    # Round hole for the Adafruit 4218 panel-mount USB-C jack, through the short end wall at -X
    zc = z_bot + FLOOR_T + USB_ABOVE_FLOOR
    hole = Manifold.cylinder(WALL + 2, USB_HOLE_D / 2, USB_HOLE_D / 2, 64)
    hole = hole.rotate((0, 90, 0)).translate((-PANEL_W / 2 - 1, 0, zc))
    return b - hole


def main():
    check_fit()
    mesh = to_mesh(placed(body(), 0, 0))
    mesh.export(OUT / "panel_body.stl")
    ext = mesh.bounds[1] - mesh.bounds[0]
    print(f"panel_body {ext[0]:6.1f} x {ext[1]:6.1f} x {ext[2]:6.1f} mm  "
          f"watertight={mesh.is_watertight}  volume={mesh.volume / 1000:.1f} cm3")


if __name__ == "__main__":
    main()
