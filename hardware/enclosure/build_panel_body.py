#!/usr/bin/env python3
"""Body for the one-piece 110 x 100 mm ScreenKey panel (screenkey_panel_3x2.step).

    pip install numpy trimesh manifold3d
    python hardware/enclosure/build_panel_body.py

Writes panel_body.stl next to this script (units: mm), printed floor down.

The panel replaces the faceplate and the mounting plate of build_case.py: one
3 mm plate, the module PCBs screw to its back face, and 4 countersunk M3 holes
sit 5 mm in from each outer edge. This body is the same open tub as
build_case.body(), sized to the panel, with an M3 heat-set insert under each
of the panel's corner holes.

Panel numbers are measured from the STEP file: outline 110 x 100 mm with
4 mm corner radii, M3 holes at (+-50, +-45), keys on a 30 x 40 mm pitch.
"""
from build_case import (
    BED_CHAMFER, BOSS_LEN, BOSS_R, FLOOR_T, M3_INSERT_D, M3_INSERT_DEPTH, OUT,
    PCB_FRONT_DEPTH, PCB_H, PCB_W, SPACE_BELOW_MODULES, STANDOFF_TIP_DEPTH,
    USB_ABOVE_FLOOR, USB_H, USB_W, WALL, Manifold, cone, cyl, placed, rrect, slab,
    to_mesh, union,
)

# ---- panel (screenkey_panel_3x2.step) ----------------------------------------
PANEL_W, PANEL_H = 110.0, 100.0
PANEL_R = 4.0
CORNERS = [(sx * 50.0, sy * 45.0) for sx in (-1, 1) for sy in (-1, 1)]
KEY_CENTRES = [(x, y) for y in (20.0, -20.0) for x in (-30.0, 0.0, 30.0)]

# ---- derived ----------------------------------------------------------------
MODULE_BEHIND_PANEL = STANDOFF_TIP_DEPTH - PCB_FRONT_DEPTH
CAVITY_DEPTH = MODULE_BEHIND_PANEL + SPACE_BELOW_MODULES
BODY_H = CAVITY_DEPTH + FLOOR_T


def check_fit():
    """Every module PCB must clear the walls and the corner bosses."""
    in_x, in_y = PANEL_W / 2 - WALL, PANEL_H / 2 - WALL
    for kx, ky in KEY_CENTRES:
        for sx in (-1, 1):
            for sy in (-1, 1):
                px, py = kx + sx * PCB_W / 2, ky + sy * PCB_H / 2
                assert abs(px) < in_x and abs(py) < in_y, f"PCB corner ({px}, {py}) hits a wall"
                for bx, by in CORNERS:
                    d = ((px - bx) ** 2 + (py - by) ** 2) ** 0.5
                    assert d > BOSS_R + 1.0, f"PCB corner ({px}, {py}) hits the boss at ({bx}, {by})"


def body():
    z_top, z_bot = 0.0, -BODY_H
    outer = slab(PANEL_W, PANEL_H, PANEL_R, z_bot, z_top, chamfer_bottom=BED_CHAMFER)
    cavity = slab(PANEL_W - 2 * WALL, PANEL_H - 2 * WALL, PANEL_R - WALL / 2, z_bot + FLOOR_T, z_top + 0.01)
    bosses = union([cyl(2 * BOSS_R, -BOSS_LEN, z_top + 0.02, x, y)
                    + cone(0.0, 2 * BOSS_R, -BOSS_LEN - BOSS_R, -BOSS_LEN, x, y) for x, y in CORNERS])
    b = outer - (cavity - bosses)
    for x, y in CORNERS:
        b = b - cyl(M3_INSERT_D, z_top - M3_INSERT_DEPTH, z_top + 0.01, x, y)
    # USB-C slot through the short end wall at -X, as in build_case.body()
    zc = z_bot + FLOOR_T + USB_ABOVE_FLOOR
    slot = Manifold.extrude(rrect(USB_H, USB_W, USB_H / 2 - 0.01), WALL + 2)
    slot = slot.rotate((0, 90, 0)).translate((-PANEL_W / 2 - 1, 0, zc))
    return b - slot


def main():
    check_fit()
    mesh = to_mesh(placed(body(), 0, 0))
    mesh.export(OUT / "panel_body.stl")
    ext = mesh.bounds[1] - mesh.bounds[0]
    print(f"panel_body {ext[0]:6.1f} x {ext[1]:6.1f} x {ext[2]:6.1f} mm  "
          f"watertight={mesh.is_watertight}  volume={mesh.volume / 1000:.1f} cm3")


if __name__ == "__main__":
    main()
