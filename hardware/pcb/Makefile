# Builds the carrier board and the zip to upload to JLCPCB.
# Needs KiCad 7 (pcbnew, kicad-cli), Java, xvfb-run, and FREEROUTING pointing at freerouting-1.9.0.jar.
FREEROUTING ?= freerouting-1.9.0.jar

carrier_jlcpcb.zip: carrier.kicad_pcb
	rm -rf gerbers && mkdir gerbers
	kicad-cli pcb export gerbers -o gerbers/ -l F.Cu,B.Cu,F.Silkscreen,B.Silkscreen,F.Mask,B.Mask,Edge.Cuts --subtract-soldermask $<
	kicad-cli pcb export drill -o gerbers/ --format excellon --excellon-separate-th $<
	rm -f $@ && cd gerbers && zip -q ../$@ *

carrier.kicad_pcb: build_carrier.py ../../firmware/pins.py
	python3 build_carrier.py place
	xvfb-run -a java -jar $(FREEROUTING) -de carrier.dsn -do carrier.ses -mp 40
	python3 build_carrier.py finish
	python3 -c "import pcbnew; pcbnew.WriteDRCReport(pcbnew.LoadBoard('$@'), 'drc.rpt', pcbnew.EDA_UNITS_MILLIMETRES, True)"
	grep '^\*\*' drc.rpt
