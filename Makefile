CIRCUITPYTHON_VERSION := 10.2.1
CIRCUITPYTHON_BOARD   := pimoroni_pico_plus2
CIRCUITPYTHON_LANG    := en_US

CIRCUITPYTHON_UF2     := adafruit-circuitpython-$(CIRCUITPYTHON_BOARD)-$(CIRCUITPYTHON_LANG)-$(CIRCUITPYTHON_VERSION).uf2
CIRCUITPYTHON_URL     := https://downloads.circuitpython.org/bin/$(CIRCUITPYTHON_BOARD)/$(CIRCUITPYTHON_LANG)/$(CIRCUITPYTHON_UF2)
CIRCUITPYTHON_SHA256  := 1079dcaa14617613507993bb186b427da5d4e18af23cf61df672b41ca7553b7a

CIRCUITPY_VOLUME      := /Volumes/CIRCUITPY

.PHONY: firmware-uf2
firmware-uf2: firmware/modules/$(CIRCUITPYTHON_UF2)

firmware/modules/$(CIRCUITPYTHON_UF2):
	mkdir -p firmware/modules
	curl -fL -o $@ $(CIRCUITPYTHON_URL)
	echo "$(CIRCUITPYTHON_SHA256)  $@" | shasum -a 256 -c - || (rm -f $@; exit 1)

.PHONY: flash
flash:
	diskutil info $(CIRCUITPY_VOLUME) 2>/dev/null | grep -q '^ *Volume Name: *CIRCUITPY$$' || \
		(echo "error: CIRCUITPY volume not found at $(CIRCUITPY_VOLUME)" >&2; exit 1)
	rsync -rc --delete \
		--exclude=modules/ --exclude=__pycache__/ --exclude=README.md \
		--exclude=.Trashes --exclude=.Spotlight-V100 --exclude=.fseventsd --exclude=.DS_Store \
		firmware/ $(CIRCUITPY_VOLUME)/
