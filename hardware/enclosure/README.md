# Enclosure

3D-printed case for the 2×3 ScreenKey pad. Regenerate with
`python hardware/enclosure/build_case.py` (needs `numpy trimesh manifold3d`).
All dimensions and tolerances are constants at the top of that script.

| File | What |
|---|---|
| `macropad_case_print.stl` | All three parts laid out flat on one 209 × 192 mm bed. Your slicer splits it into objects. |
| `faceplate.stl`, `mounting_plate.stl`, `body.stl` | The same parts, one per file |
| `macropad_case_assembled.stl` | Preview only. Do not print. |
| `test_coupon.stl` | One plate cell, about 15 minutes. **Print this first** to check the M2 hole pitch and keycap fit. |

## Stack, front to back

1. **Faceplate**, 4 mm. Six keycap windows. It covers the M2 screw heads. Print front face down.
2. **Mounting plate**, 3 mm. Six keycap windows and 4× M2 holes per module with a pan-head recess.
   Each module's PCB screws to the plate's back face. Print front face up.
3. **Body**. An open tub 99.7 × 91.1 × 78.6 mm with 63.5 mm (2.5 in) of free space below the
   module standoff tips. A 14 × 9 mm USB-C slot sits in the left short end wall, 30 mm above the floor.

## Hardware

- 24× M2 × 8 screws for the modules. Reuse the factory ones after unscrewing the front heads.
- 4× M3 × 16 flat-head screws through the faceplate and plate into the body.
- 4× M3 heat-set inserts (4.0 mm pockets in the body's corner bosses).

## Check before printing

- **M2 hole pitch.** The Waveshare drawing gives no number. 20.6 × 30.2 mm was measured off the
  drawing, so measure a real module and edit `HOLE_DX` / `HOLE_DY`. The 2.6 mm holes absorb ±0.3 mm.
- **Keycap depth.** `PCB_FRONT_DEPTH = 11.9` mm (keycap face to PCB front) is read off the side view.
  The keycaps stand about 4.9 mm proud of the faceplate at rest.
- **Slot position.** The Pico has no mount posts yet, so set `USB_ABOVE_FLOOR` to where the board will sit.

## Print settings

- PLA or PETG, 0.2 mm layers, 4 walls, 15% infill. A brim on the body helps keep its large floor flat.
- No supports. Edges on the bed have 0.8 mm 45° chamfers, the corner bosses end in 45° cones, and the USB slot is an arch.
- The corner screws pull along the layer direction. Do not over-tighten them.

## Body for the one-piece 110 × 100 mm panel

`screenkey_panel_3x2.step` is a single 3 mm panel that replaces both the faceplate and the
mounting plate. The module PCBs screw to its back face. `panel_body.stl` is the body for it.
Regenerate it with `python hardware/enclosure/build_panel_body.py`.

- The tub is 110 × 100 × 78.6 mm. It keeps the same wall, floor, 63.5 mm of space below the modules
  and USB-C slot as `body.stl`. The slot is in the 100 mm end wall.
- Its 4 corner bosses sit under the panel's countersunk holes at (±50, ±45) mm from the centre.
  They are full-height pillars from the floor up, so they print without supports. Each boss has
  a 4.0 mm M3 heat-set insert pocket, 12 mm deep.
- Use 4× **M3 × 10** flat-head screws. With the 3 mm panel, the old M3 × 16 screws would hit
  the bottom of the pocket.
- The script checks that every module PCB clears the walls and bosses.
