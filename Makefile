CIRCUITPYTHON_VERSION := 10.2.1
CIRCUITPYTHON_BOARD   := pimoroni_pico_plus2
CIRCUITPYTHON_LANG    := en_US

CIRCUITPYTHON_UF2     := adafruit-circuitpython-$(CIRCUITPYTHON_BOARD)-$(CIRCUITPYTHON_LANG)-$(CIRCUITPYTHON_VERSION).uf2
CIRCUITPYTHON_URL     := https://downloads.circuitpython.org/bin/$(CIRCUITPYTHON_BOARD)/$(CIRCUITPYTHON_LANG)/$(CIRCUITPYTHON_UF2)
CIRCUITPYTHON_SHA256  := 1079dcaa14617613507993bb186b427da5d4e18af23cf61df672b41ca7553b7a

.PHONY: firmware-uf2
firmware-uf2: firmware/modules/$(CIRCUITPYTHON_UF2)

firmware/modules/$(CIRCUITPYTHON_UF2):
	mkdir -p firmware/modules
	curl -fL -o $@ $(CIRCUITPYTHON_URL)
	echo "$(CIRCUITPYTHON_SHA256)  $@" | shasum -a 256 -c - || (rm -f $@; exit 1)
