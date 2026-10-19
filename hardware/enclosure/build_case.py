#!/usr/bin/env python3
"""Parametric 3D-printed case for the 2x3 ScreenKey macro pad.

    pip install numpy trimesh manifold3d
    python hardware/enclosure/build_case.py

Writes STL files next to this script (units: mm):

    macropad_case_print.stl   all three parts laid out flat on one bed (the deliverable)
    faceplate.stl             printed front face down
    mounting_plate.stl        printed front face up
    body.stl                  printed floor down
    macropad_case_assembled.stl   parts in assembled position (preview only, do not print)
    test_coupon.stl           one plate cell: print first to check hole pitch and keycap fit

Stack, front to back (+Z is toward the user; Z=0 is the back face of the mounting plate):

    faceplate       keycap windows; covers the M2 screw heads
    mounting plate  keycap windows + 4x M2 holes per module; module PCBs screw to its BACK face
    body            open tub holding the Pico, perfboard and wires; USB-C slot in a short end

Module dimensions come from the Waveshare 0.85inch ScreenKey Module drawings
(https://docs.waveshare.com/0.85inch-ScreenKey-Module-W). The hole pitch is not
dimensioned there, so it was measured from the drawing: verify it with calipers.
"""
from pathlib import Path

import numpy as np
import trimesh
from manifold3d import CrossSection, Manifold

OUT = Path(__file__).resolve().parent

# ---- ScreenKey module (Waveshare drawing) ---------------------------------
PCB_W, PCB_H = 25.94, 35.29        # PCB outline, portrait
KEY_W, KEY_H = 22.00, 25.30        # keycap outline, centred on the PCB
KEY_TALL = 8.60                    # keycap height above its switch
PCB_FRONT_DEPTH = 11.9             # keycap front face to PCB front face, key at rest (read off the side view)
STANDOFF_TIP_DEPTH = 24.00         # keycap front face to the tips of the rear standoffs
PCB_THICK = 1.6
HOLE_DX, HOLE_DY = 20.6, 30.2      # M2 hole pitch, measured from the drawing (+-0.3)

# ---- layout ---------------------------------------------------------------
COLS, ROWS = 3, 2
PITCH_X = 27.5                     # PCB 25.94 wide -> 1.56 gap
PITCH_Y = 37.0                     # PCB 35.29 tall -> 1.71 gap
MARGIN = 9.4                       # module block to outer edge, leaves room for the M3 corner screws

# ---- tolerances and fasteners ----------------------------------------------
KEY_CLEAR = 0.5                    # per side, around the keycap
KEY_CUT_R = 3.0                    # corner radius of the keycap windows
M2_HOLE = 2.6                      # M2 clearance incl. the +-0.3 pitch uncertainty
M2_CBORE_D, M2_CBORE_DEPTH = 4.0, 1.4   # pan-head recess on the plate's front face
M3_CLEAR = 3.4
M3_CSK_D, M3_CSK_DEPTH = 6.4, 1.8       # countersink for M3 flat heads, in the faceplate
M3_INSERT_D, M3_INSERT_DEPTH = 4.0, 12.0  # M3 heat-set insert pocket in the body bosses
BOSS_R = 4.5
BOSS_LEN = 20.0                    # straight part of the corner boss; it ends in a 45-degree cone so it prints without supports
BED_CHAMFER = 0.8                  # on the edges that touch the bed: faceplate front, body bottom (also stops elephant's foot)
CORNER_INSET = 5.0                 # M3 screw centre from the outer edges

# ---- shell ------------------------------------------------------------------
FACE_T = 4.0
PLATE_T = 3.0
WALL = 2.4
FLOOR_T = 3.0
OUTER_R = 3.0
SPACE_BELOW_MODULES = 63.5         # 2.5 in of free space below the standoff tips
USB_W, USB_H = 14.0, 9.0           # slot for a standard USB-C plug overmold
USB_ABOVE_FLOOR = 30.0

# ---- derived ----------------------------------------------------------------
BLOCK_W = (COLS - 1) * PITCH_X + PCB_W
BLOCK_H = (ROWS - 1) * PITCH_Y + PCB_H
OUT_W = round(BLOCK_W + 2 * MARGIN, 1)
OUT_H = round(BLOCK_H + 2 * MARGIN, 1)
MODULE_BEHIND_PLATE = STANDOFF_TIP_DEPTH - PCB_FRONT_DEPTH
CAVITY_DEPTH = MODULE_BEHIND_PLATE + SPACE_BELOW_MODULES
BODY_H = CAVITY_DEPTH + FLOOR_T

KEY_CENTRES = [((c - (COLS - 1) / 2) * PITCH_X, ((ROWS - 1) / 2 - r) * PITCH_Y)
               for r in range(ROWS) for c in range(COLS)]
CORNERS = [(sx * (OUT_W / 2 - CORNER_INSET), sy * (OUT_H / 2 - CORNER_INSET))
           for sx in (-1, 1) for sy in (-1, 1)]


def rrect(w, h, r):
    """Rounded rectangle cross-section centred on the origin."""
    return CrossSection.square((w - 2 * r, h - 2 * r), center=True).offset(r, 2, 2.0, 48)


def slab(w, h, r, z0, z1, chamfer_bottom=0.0, chamfer_top=0.0):
    """Rounded-corner slab; the optional 45-degree chamfers are for the edge that sits on the print bed."""
    zb, zt = z0 + chamfer_bottom, z1 - chamfer_top
    out = Manifold.extrude(rrect(w, h, r), zt - zb).translate((0, 0, zb))
    if chamfer_bottom:
        c = chamfer_bottom
        out = out + Manifold.extrude(rrect(w - 2 * c, h - 2 * c, r - c), c, 0, 0.0,
                                     ((w) / (w - 2 * c), (h) / (h - 2 * c))).translate((0, 0, z0))
    if chamfer_top:
        c = chamfer_top
        out = out + Manifold.extrude(rrect(w, h, r), c, 0, 0.0,
                                     ((w - 2 * c) / w, (h - 2 * c) / h)).translate((0, 0, zt))
    return out


def cyl(d, z0, z1, x=0.0, y=0.0):
    return Manifold.cylinder(z1 - z0, d / 2, d / 2, 40).translate((x, y, z0))


def cone(d0, d1, z0, z1, x, y):
    return Manifold.cylinder(z1 - z0, d0 / 2, d1 / 2, 40).translate((x, y, z0))


def union(parts):
    out = parts[0]
    for p in parts[1:]:
        out = out + p
    return out


def key_windows(z0, z1):
    w = slab(KEY_W + 2 * KEY_CLEAR, KEY_H + 2 * KEY_CLEAR, KEY_CUT_R, z0 - 0.01, z1 + 0.01)
    return union([w.translate((x, y, 0)) for x, y in KEY_CENTRES])


def faceplate():
    z0, z1 = PLATE_T, PLATE_T + FACE_T
    f = slab(OUT_W, OUT_H, OUTER_R, z0, z1, chamfer_top=BED_CHAMFER) - key_windows(z0, z1)
    for x, y in CORNERS:   # flat-head M3 countersinks, wide end on the front face
        f = f - cyl(M3_CLEAR, z0 - 0.01, z1 + 0.01, x, y)
        f = f - cone(M3_CLEAR, M3_CSK_D, z1 - M3_CSK_DEPTH, z1 + 0.01, x, y)
    return f


def mounting_plate():
    z0, z1 = 0.0, PLATE_T
    p = slab(OUT_W, OUT_H, OUTER_R, z0, z1) - key_windows(z0, z1)
    for kx, ky in KEY_CENTRES:
        for sx in (-1, 1):
            for sy in (-1, 1):
                x, y = kx + sx * HOLE_DX / 2, ky + sy * HOLE_DY / 2
                p = p - cyl(M2_HOLE, z0 - 0.01, z1 + 0.01, x, y)
                p = p - cyl(M2_CBORE_D, z1 - M2_CBORE_DEPTH, z1 + 0.01, x, y)
    for x, y in CORNERS:
        p = p - cyl(M3_CLEAR, z0 - 0.01, z1 + 0.01, x, y)
    return p


def body():
    z_top, z_bot = 0.0, -BODY_H
    outer = slab(OUT_W, OUT_H, OUTER_R, z_bot, z_top, chamfer_bottom=BED_CHAMFER)
    cavity = slab(OUT_W - 2 * WALL, OUT_H - 2 * WALL, OUTER_R - WALL / 2, z_bot + FLOOR_T, z_top + 0.01)
    bosses = union([cyl(2 * BOSS_R, -BOSS_LEN, z_top + 0.02, x, y)
                    + cone(0.0, 2 * BOSS_R, -BOSS_LEN - BOSS_R, -BOSS_LEN, x, y) for x, y in CORNERS])
    b = outer - (cavity - bosses)
    for x, y in CORNERS:
        b = b - cyl(M3_INSERT_D, z_top - M3_INSERT_DEPTH, z_top + 0.01, x, y)
    # USB-C slot through the short end wall at -X
    zc = z_bot + FLOOR_T + USB_ABOVE_FLOOR
    # rotate((0, 90, 0)) sends the section's X to -Z, so give it (height, width) to land as (Y=width, Z=height)
    slot = Manifold.extrude(rrect(USB_H, USB_W, USB_H / 2 - 0.01), WALL + 2)
    slot = slot.rotate((0, 90, 0)).translate((-OUT_W / 2 - 1, 0, zc))
    return b - slot


def test_coupon():
    """One module cell of the mounting plate: print it first to check the M2 hole pitch and keycap fit."""
    w, h = PCB_W + 8, PCB_H + 8
    p = slab(w, h, OUTER_R, 0, PLATE_T) - slab(KEY_W + 2 * KEY_CLEAR, KEY_H + 2 * KEY_CLEAR, KEY_CUT_R, -0.01, PLATE_T + 0.01)
    for sx in (-1, 1):
        for sy in (-1, 1):
            x, y = sx * HOLE_DX / 2, sy * HOLE_DY / 2
            p = p - cyl(M2_HOLE, -0.01, PLATE_T + 0.01, x, y) - cyl(M2_CBORE_D, PLATE_T - M2_CBORE_DEPTH, PLATE_T + 0.01, x, y)
    return p


def to_mesh(m):
    mesh = m.to_mesh()
    return trimesh.Trimesh(np.array(mesh.vert_properties)[:, :3], np.array(mesh.tri_verts), process=True)


def placed(m, dx, dy, flip=False):
    """Move a part onto the print bed: lowest point at Z=0, footprint corner at (dx, dy)."""
    if flip:
        m = m.rotate((180, 0, 0))
    lo, hi = m.bounding_box()[:3], m.bounding_box()[3:]
    return m.translate((dx - lo[0], dy - lo[1], -lo[2]))


def main():
    parts = {"faceplate": faceplate(), "mounting_plate": mounting_plate(), "body": body()}
    gap = 10.0
    layout = [placed(parts["body"], 0, 0),
              placed(parts["faceplate"], OUT_W + gap, 0, flip=True),      # front face on the bed
              placed(parts["mounting_plate"], OUT_W + gap, OUT_H + gap)]

    meshes = {
        "faceplate": to_mesh(placed(parts["faceplate"], 0, 0, flip=True)),
        "mounting_plate": to_mesh(placed(parts["mounting_plate"], 0, 0)),
        "body": to_mesh(placed(parts["body"], 0, 0)),
        "macropad_case_print": to_mesh(union(layout)),
        "macropad_case_assembled": to_mesh(union(list(parts.values()))),
        "test_coupon": to_mesh(placed(test_coupon(), 0, 0)),
    }
    for name, mesh in meshes.items():
        mesh.export(OUT / f"{name}.stl")
        ext = mesh.bounds[1] - mesh.bounds[0]
        print(f"{name:26s} {ext[0]:6.1f} x {ext[1]:6.1f} x {ext[2]:6.1f} mm  "
              f"watertight={mesh.is_watertight}  volume={mesh.volume / 1000:.1f} cm3")


if __name__ == "__main__":
    main()
