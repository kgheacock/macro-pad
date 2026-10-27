#!/usr/bin/env python3
"""Carrier PCB: the Pico Plus 2 in female headers, six ScreenKey sockets, the mic header.

Needs KiCad 7 (its `pcbnew` Python module and `kicad-cli`) and Java for Freerouting:

    python3 hardware/pcb/build_carrier.py place      # writes carrier_unrouted.kicad_pcb + carrier.dsn
    java -jar freerouting.jar -de carrier.dsn -do carrier.ses -mp 30
    python3 hardware/pcb/build_carrier.py finish     # imports carrier.ses, pours GND, writes carrier.kicad_pcb

`make -C hardware/pcb` runs all three steps, DRC, and the Gerber/drill zip for JLCPCB.

The nets come from firmware/pins.py and the ScreenKey Module schematic
(docs/0.85inch_ScreenKey_Module.pdf, connector H2):

    1 RST  2 BL  3 3V3  4 GND  5 DIN  6 CLK  7 CS  8 DC  9 KEY

Socket pin N carries module pin N, so each key needs a same-direction cable
(pin 1 to pin 1). Units are mm, origin at the board's top-left corner.
"""
import re
import sys
from pathlib import Path

import pcbnew

HERE = Path(__file__).resolve().parent
FP = Path("/usr/share/kicad/footprints")
sys.path.insert(0, str(HERE.parents[1] / "firmware"))
import pins  # noqa: E402

# ---- board ------------------------------------------------------------------
BOARD_W, BOARD_H = 90.0, 70.0
CORNER_R = 3.0
HOLE_INSET = 4.0                                  # M3 mounting holes, from each edge
TRACK_W = 0.3                                     # every net; GND is also poured on both layers
CLEARANCE = 0.2

# ---- Pico Plus 2: two 1x20 female headers, USB end toward x = 0 ----------------
PICO_X0 = 12.0                                    # pins 1 and 40, next to the USB-C end
PICO_ROW_LOW = 62.0                               # pins 1..20, left to right
PICO_ROW_HIGH = PICO_ROW_LOW - 17.78              # pins 40..21, left to right

# Pico pin -> function, standard Pico pinout (docs/ppico_plus_2_pinout_diagram.pdf)
PICO_GP = {1: 0, 2: 1, 4: 2, 5: 3, 6: 4, 7: 5, 9: 6, 10: 7, 11: 8, 12: 9, 14: 10, 15: 11,
           16: 12, 17: 13, 19: 14, 20: 15, 21: 16, 22: 17, 24: 18, 25: 19, 26: 20, 27: 21,
           29: 22, 31: 26, 32: 27, 34: 28}
PICO_GND = (3, 8, 13, 18, 23, 28, 33, 38)
PICO_3V3 = 36

# ---- ScreenKey sockets (Molex PicoBlade 53047-0910, vertical, through-hole) -----
SOCKET_PINS = ["RST", "BL", "3V3", "GND", "DIN", "CLK", "CS", "DC", "KEY"]
SOCKET_ORIGINS = [(8.0, 10.0), (38.0, 10.0), (68.0, 10.0),   # keys 0, 1, 2 (pin 1 position)
                  (8.0, 26.0), (38.0, 26.0), (68.0, 26.0)]   # keys 3, 4, 5

# ---- I2S mic: 1x6 male header in the Adafruit SPH0645 breakout's order ---------
MIC_PINS = ["GND", "MIC_WS", "MIC_DATA", "MIC_BCLK", "GND", "3V3"]   # SEL LRCL DOUT BCLK GND 3V
MIC_LABELS = ["SEL", "LRCL", "DOUT", "BCLK", "GND", "3V"]
MIC_ORIGIN = (68.0, 50.0)

CAP_POS = (22.0, 37.0)                            # 10 uF bulk cap by the Pico's 3V3 pin


def gp(name):
    return int(name.removeprefix("GP"))


def shared_nets():
    """GPn -> net name for the lines every key shares, plus the mic."""
    return {gp(pins.SPI_SCK): "CLK", gp(pins.BACKLIGHT): "BL", gp(pins.DISPLAY_DC): "DC",
            gp(pins.DISPLAY_RST): "RST", gp(pins.MIC_BCLK): "MIC_BCLK",
            gp(pins.MIC_WS): "MIC_WS", gp(pins.MIC_DATA): "MIC_DATA"}


def key_nets():
    """GPn -> net name for each key's own lines."""
    out = {}
    for k, key in enumerate(pins.KEYS):
        out[gp(key.din_pin)] = f"K{k}_DIN"
        out[gp(key.display_cs_pin)] = f"K{k}_CS"
        out[gp(key.switch_pin)] = f"K{k}_KEY"
    return out


def socket_net(k, name):
    return f"K{k}_{name}" if name in ("DIN", "CS", "KEY") else name


class Builder:
    def __init__(self):
        self.board = pcbnew.BOARD()
        self.nets = {}

    def net(self, name):
        if name not in self.nets:
            n = pcbnew.NETINFO_ITEM(self.board, name)
            self.board.Add(n)
            self.nets[name] = n
        return self.nets[name]

    def footprint(self, lib, name, ref, x, y, deg=0.0, value=""):
        fp = pcbnew.FootprintLoad(str(FP / f"{lib}.pretty"), name)
        fp.SetReference(ref)
        fp.SetValue(value or name)
        fp.Value().SetVisible(False)
        fp.SetOrientationDegrees(deg)
        fp.SetPosition(pcbnew.VECTOR2I_MM(x, y))
        self.board.Add(fp)
        return fp

    def text(self, s, x, y, size=1.0, deg=0.0, layer=pcbnew.F_SilkS):
        t = pcbnew.PCB_TEXT(self.board)
        t.SetText(s)
        t.SetTextAngleDegrees(deg)
        if deg:   # vertical pin labels read upward from just below the pad
            t.SetHorizJustify(pcbnew.GR_TEXT_H_ALIGN_RIGHT)
        t.SetPosition(pcbnew.VECTOR2I_MM(x, y))
        t.SetLayer(layer)
        t.SetTextSize(pcbnew.VECTOR2I_MM(size, size))
        t.SetTextThickness(pcbnew.FromMM(0.15))
        self.board.Add(t)

    def line(self, x0, y0, x1, y1, layer, width=0.12):
        s = pcbnew.PCB_SHAPE(self.board)
        s.SetShape(pcbnew.SHAPE_T_SEGMENT)
        s.SetStart(pcbnew.VECTOR2I_MM(x0, y0))
        s.SetEnd(pcbnew.VECTOR2I_MM(x1, y1))
        s.SetLayer(layer)
        s.SetWidth(pcbnew.FromMM(width))
        self.board.Add(s)

    def arc(self, cx, cy, sx, sy, deg, layer):
        s = pcbnew.PCB_SHAPE(self.board)
        s.SetShape(pcbnew.SHAPE_T_ARC)
        s.SetCenter(pcbnew.VECTOR2I_MM(cx, cy))
        s.SetStart(pcbnew.VECTOR2I_MM(sx, sy))
        s.SetArcAngleAndEnd(pcbnew.EDA_ANGLE(deg, pcbnew.DEGREES_T), True)
        s.SetLayer(layer)
        s.SetWidth(pcbnew.FromMM(0.1))
        self.board.Add(s)

    def outline(self):
        w, h, r, L = BOARD_W, BOARD_H, CORNER_R, pcbnew.Edge_Cuts
        self.line(r, 0, w - r, 0, L, 0.1)
        self.line(w, r, w, h - r, L, 0.1)
        self.line(w - r, h, r, h, L, 0.1)
        self.line(0, h - r, 0, r, L, 0.1)
        for cx, cy, sx, sy in ((r, r, 0, r), (w - r, r, w - r, 0), (w - r, h - r, w, h - r), (r, h - r, r, h)):
            self.arc(cx, cy, sx, sy, 90, L)

    def design_rules(self):
        ds = self.board.GetDesignSettings()
        ds.m_MinClearance = pcbnew.FromMM(CLEARANCE)
        ds.m_TrackMinWidth = pcbnew.FromMM(0.2)
        default = ds.m_NetSettings.m_DefaultNetClass
        default.SetClearance(pcbnew.FromMM(CLEARANCE))
        default.SetTrackWidth(pcbnew.FromMM(TRACK_W))
        default.SetViaDiameter(pcbnew.FromMM(0.6))
        default.SetViaDrill(pcbnew.FromMM(0.3))

    def place(self):
        self.design_rules()
        self.outline()
        gpnet = {**shared_nets(), **key_nets()}

        # Pico: row of pins 1..20 at PICO_ROW_LOW, pins 40..21 at PICO_ROW_HIGH
        low = self.footprint("Connector_PinSocket_2.54mm", "PinSocket_1x20_P2.54mm_Vertical",
                             "J1", PICO_X0, PICO_ROW_LOW, 90, "Pico pins 1-20")
        high = self.footprint("Connector_PinSocket_2.54mm", "PinSocket_1x20_P2.54mm_Vertical",
                              "J2", PICO_X0, PICO_ROW_HIGH, 90, "Pico pins 40-21")
        for fp, pico_pin in ((low, lambda n: n), (high, lambda n: 41 - n)):
            for pad in fp.Pads():
                p = pico_pin(int(pad.GetNumber()))
                name = None
                if p in PICO_GND:
                    name = "GND"
                elif p == PICO_3V3:
                    name = "3V3"
                elif p in PICO_GP and PICO_GP[p] in gpnet:
                    name = gpnet[PICO_GP[p]]
                if name:
                    pad.SetNet(self.net(name))
        # Pico label and USB-end marker on the silkscreen
        x0, x1 = PICO_X0 - 1.37, PICO_X0 + 19 * 2.54 + 1.37
        y0, y1 = PICO_ROW_HIGH - 1.61, PICO_ROW_LOW + 1.61
        self.text("PICO PLUS 2", (x0 + x1) / 2, (y0 + y1) / 2 - 3, 1.5)
        self.text("USB-C", x0 + 4.5, (y0 + y1) / 2 + 1, 1.0)
        self.text("<", x0 + 1.2, (y0 + y1) / 2 + 1, 1.2)

        # ScreenKey sockets
        for k, (x, y) in enumerate(SOCKET_ORIGINS):
            fp = self.footprint("Connector_Molex", "Molex_PicoBlade_53047-0910_1x09_P1.25mm_Vertical",
                                f"K{k}", x, y, 0, f"ScreenKey {k}")
            fp.Reference().SetVisible(False)
            for pad in fp.Pads():
                pad.SetNet(self.net(socket_net(k, SOCKET_PINS[int(pad.GetNumber()) - 1])))
            self.text(f"KEY {k}", x + 5, y - 4.2, 1.2)
            for i, name in enumerate(SOCKET_PINS):
                self.text(name, x + i * 1.25, y + 2.1, 0.8, 90)

        # mic header
        mic = self.footprint("Connector_PinHeader_2.54mm", "PinHeader_1x06_P2.54mm_Vertical",
                             "J3", *MIC_ORIGIN, 90, "I2S mic")
        mic.Reference().SetVisible(False)
        for pad in mic.Pads():
            pad.SetNet(self.net(MIC_PINS[int(pad.GetNumber()) - 1]))
        self.text("I2S MIC", MIC_ORIGIN[0] + 6.35, MIC_ORIGIN[1] - 3.2, 1.0)
        for i, name in enumerate(MIC_LABELS):
            self.text(name, MIC_ORIGIN[0] + i * 2.54, MIC_ORIGIN[1] + 1.8, 0.8, 90)

        # bulk cap, + to 3V3
        cap = self.footprint("Capacitor_THT", "CP_Radial_D5.0mm_P2.00mm", "C1", *CAP_POS, 0, "10uF 16V")
        for pad in cap.Pads():
            pad.SetNet(self.net("3V3" if pad.GetNumber() == "1" else "GND"))

        # M3 mounting holes
        for i, (x, y) in enumerate(((HOLE_INSET, HOLE_INSET), (BOARD_W - HOLE_INSET, HOLE_INSET),
                                    (HOLE_INSET, BOARD_H - HOLE_INSET), (BOARD_W - HOLE_INSET, BOARD_H - HOLE_INSET))):
            h = self.footprint("MountingHole", "MountingHole_3.2mm_M3", f"H{i + 1}", x, y)
            h.Reference().SetVisible(False)

        self.text("macro-pad carrier", 45, 66.5, 1.0)

    def gnd_pours(self):
        for layer in (pcbnew.F_Cu, pcbnew.B_Cu):
            z = pcbnew.ZONE(self.board)
            z.SetLayer(layer)
            z.SetNet(self.net("GND"))
            z.SetLocalClearance(pcbnew.FromMM(0.3))
            z.SetMinThickness(pcbnew.FromMM(0.25))
            z.SetPadConnection(pcbnew.ZONE_CONNECTION_THERMAL)
            o = z.Outline()
            o.NewOutline()
            for x, y in ((0, 0), (BOARD_W, 0), (BOARD_W, BOARD_H), (0, BOARD_H)):
                o.Append(pcbnew.FromMM(x), pcbnew.FromMM(y))
            self.board.Add(z)
        pcbnew.ZONE_FILLER(self.board).Fill(self.board.Zones())


def place():
    b = Builder()
    b.place()
    pcbnew.SaveBoard(str(HERE / "carrier_unrouted.kicad_pcb"), b.board)
    assert pcbnew.ExportSpecctraDSN(b.board, str(HERE / "carrier.dsn"))


def sexpr(text):
    """Parse an S-expression into nested lists of strings."""
    tokens = re.findall(r'\(|\)|"[^"]*"|[^\s()]+', text)
    stack = [[]]
    for t in tokens:
        if t == "(":
            stack.append([])
        elif t == ")":
            done = stack.pop()
            stack[-1].append(done)
        else:
            stack[-1].append(t.strip('"'))
    return stack[0][0]


def import_ses(b, path):
    """Add Freerouting's wires and vias to the board.

    KiCad 7's ImportSpecctraSES only works inside the GUI, so this reads the
    session file itself. Its units are 0.1 um with Y pointing up.
    """
    def find(node, key):
        return [c for c in node if isinstance(c, list) and c and c[0] == key]

    ses = sexpr(Path(path).read_text())
    routes = find(ses, "routes")[0]
    scale = 1e-4                                          # (resolution um 10) -> mm
    layers = {"F.Cu": pcbnew.F_Cu, "B.Cu": pcbnew.B_Cu}
    for net in find(find(routes, "network_out")[0], "net"):
        ni = b.net(net[1])
        for wire in find(net, "wire"):
            path_ = find(wire, "path")[0]
            layer, width, xy = path_[1], float(path_[2]) * scale, [float(v) * scale for v in path_[3:]]
            pts = [(xy[i], -xy[i + 1]) for i in range(0, len(xy), 2)]
            for (x0, y0), (x1, y1) in zip(pts, pts[1:]):
                t = pcbnew.PCB_TRACK(b.board)
                t.SetStart(pcbnew.VECTOR2I_MM(x0, y0))
                t.SetEnd(pcbnew.VECTOR2I_MM(x1, y1))
                t.SetWidth(pcbnew.FromMM(width))
                t.SetLayer(layers[layer])
                t.SetNet(ni)
                b.board.Add(t)
        for via in find(net, "via"):
            v = pcbnew.PCB_VIA(b.board)
            v.SetPosition(pcbnew.VECTOR2I_MM(float(via[2]) * scale, -float(via[3]) * scale))
            v.SetWidth(pcbnew.FromMM(0.6))
            v.SetDrill(pcbnew.FromMM(0.3))
            v.SetNet(ni)
            b.board.Add(v)


def finish():
    board = pcbnew.LoadBoard(str(HERE / "carrier_unrouted.kicad_pcb"))
    b = Builder()
    b.board = board
    b.nets = {str(n.GetNetname()): n for n in board.GetNetInfo().NetsByName().values()}
    import_ses(b, HERE / "carrier.ses")
    b.gnd_pours()
    pcbnew.SaveBoard(str(HERE / "carrier.kicad_pcb"), board)


# JLCPCB assembly: (comment, footprint, manufacturer part, LCSC part). An empty LCSC part is left
# for JLCPCB's "Manual Match" to fill. J3, the mic header, is left off so the mic's pin order
# can be checked first.
JLC_PARTS = {
    "J1": ("Female header 1x20 2.54mm straight through-hole", "PinSocket_1x20_P2.54mm_Vertical", "PM254-1-20-Z-8.5", "C2897383"),
    "J2": ("Female header 1x20 2.54mm straight through-hole", "PinSocket_1x20_P2.54mm_Vertical", "PM254-1-20-Z-8.5", "C2897383"),
    "C1": ("10uF 16V aluminium electrolytic radial D5mm P2.0mm", "CP_Radial_D5.0mm_P2.00mm", "", ""),
    **{f"K{k}": ("1.25mm 9-pin vertical through-hole wafer header (PicoBlade compatible)",
                 "Molex_PicoBlade_53047-0910_1x09_P1.25mm_Vertical", "530470910", "C564027")
       for k in range(6)},
}


def jlc():
    """Write the BOM and placement (CPL) files JLCPCB's assembly order asks for."""
    import csv
    board = pcbnew.LoadBoard(str(HERE / "carrier.kicad_pcb"))
    groups = {}
    with open(HERE / "carrier_cpl.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["Designator", "Mid X", "Mid Y", "Layer", "Rotation"])
        for fp in sorted(board.GetFootprints(), key=lambda fp: fp.GetReference()):
            ref = fp.GetReference()
            if ref not in JLC_PARTS:
                continue
            pads = [pcbnew.ToMM(p.GetPosition()) for p in fp.Pads()]
            mx = sum(p[0] for p in pads) / len(pads)
            my = sum(p[1] for p in pads) / len(pads)
            w.writerow([ref, f"{mx:.3f}mm", f"{-my:.3f}mm", "Top", f"{fp.GetOrientationDegrees():.0f}"])
            groups.setdefault(JLC_PARTS[ref], []).append(ref)
    with open(HERE / "carrier_bom.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["Comment", "Designator", "Footprint", "Manufacturer Part", "LCSC Part #"])
        for (comment, footprint, mpn, lcsc), refs in groups.items():
            w.writerow([comment, ",".join(refs), footprint, mpn, lcsc])


if __name__ == "__main__":
    {"place": place, "finish": finish, "jlc": jlc}[sys.argv[1]]()
