# Carrier board

A 90 × 70 mm, 2-layer board that replaces the perfboard. The Pimoroni Pico Plus 2 plugs into two
female headers, each ScreenKey plugs into its own 1.25 mm 9-pin socket, and the I2S mic gets a
6-pin header. All the shared lines (3V3, GND, CLK, DC, RST, BL) are copper, and GND is poured on both layers.

**To order:** upload `carrier_jlcpcb.zip` to JLCPCB. The defaults are fine: 2 layers, 1.6 mm, 1 oz, HASL.
`carrier_top.png` and `carrier_bottom.png` show the copper.

## Assembly by JLCPCB

To have JLCPCB solder the parts on, upload `carrier_bom.csv` and `carrier_cpl.csv` at the PCBA step:
- Choose **Parts Selection: By JLCPCB (Manual Match)**, since the BOM gives descriptions and an MPN but no LCSC numbers.
- Set **Confirm Parts Placement: Yes** so they check the through-hole positions and rotations.
- The mic header (J3) is left off. Solder it yourself once you've checked your mic's pin order.

## Parts

| Ref | Part | Qty |
|---|---|---|
| J1, J2 | 1×20 female header, 2.54 mm (low-profile saves about 3 mm) | 2 |
| K0–K5 | 1.25 mm 9-pin vertical through-hole socket: Molex PicoBlade 53047-0910, or a generic "MX1.25 9P vertical" | 6 |
| J3 | 1×6 male header, 2.54 mm | 1 |
| C1 | 10 µF 16 V electrolytic, radial, 5 mm body, 2 mm lead pitch. + goes to the square pad | 1 |
| — | 1.25 mm 9-pin cable with a connector at both ends, 10 cm, wired pin 1 to pin 1 | 6 |
| — | M3 screws for the 4 mounting holes (3.2 mm, 4 mm in from each corner) | 4 |

## Pinout

- **Key sockets.** Pins 1 to 9 are RST, BL, 3V3, GND, DIN, CLK, CS, DC, KEY. These match the module's
  own connector (H2 in `docs/0.85inch_ScreenKey_Module.pdf`), so each cable must carry pin 1 to pin 1.
  Check one with a multimeter before plugging in a key. Socket K*n* is key *n* in `firmware/pins.py`.
- **Pico.** It plugs in with its USB-C end toward the `<USB-C` mark. J1 is Pico pins 1 to 20, and J2 is
  pins 40 to 21.
- **Mic.** J3 is SEL, LRCL, DOUT, BCLK, GND, 3V, in the order of Adafruit's SPH0645 breakout.
  SEL is tied to GND (left channel). Check your breakout's order before soldering it on.

## Rebuild

`make -C hardware/pcb FREEROUTING=/path/to/freerouting-1.9.0.jar` needs KiCad 7, Java and `xvfb-run`.
`build_carrier.py` holds the placement and nets. Freerouting does the routing, and the run ends
with a DRC report in `drc.rpt`. The committed board has 0 unconnected pads and no clearance errors.
Its only warnings are footprint-library notices and 7 GND pads with thermal spokes on one layer only.
