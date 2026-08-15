CIRCUITPYTHON_VERSION := 10.2.1
CIRCUITPYTHON_BOARD   := pimoroni_pico_plus2
CIRCUITPYTHON_LANG    := en_US

CIRCUITPYTHON_UF2     := adafruit-circuitpython-$(CIRCUITPYTHON_BOARD)-$(CIRCUITPYTHON_LANG)-$(CIRCUITPYTHON_VERSION).uf2
CIRCUITPYTHON_URL     := https://downloads.circuitpython.org/bin/$(CIRCUITPYTHON_BOARD)/$(CIRCUITPYTHON_LANG)/$(CIRCUITPYTHON_UF2)
CIRCUITPYTHON_SHA256  := 1079dcaa14617613507993bb186b427da5d4e18af23cf61df672b41ca7553b7a

CIRCUITPY_VOLUME      := /Volumes/CIRCUITPY

# PINGPONG_VENDOR_ID and PINGPONG_PRODUCT_ID identify the macro pad's USB
# device descriptor for `make ping-pong`. Override them on the command
# line once the board's real values are known, from
# `system_profiler SPUSBDataType`; see driver/README.md.
PINGPONG_VENDOR_ID    := 0x0000
PINGPONG_PRODUCT_ID   := 0x0000

.PHONY: firmware-uf2
firmware-uf2: firmware/modules/$(CIRCUITPYTHON_UF2)

firmware/modules/$(CIRCUITPYTHON_UF2):
	mkdir -p firmware/modules
	curl -fL -o $@ $(CIRCUITPYTHON_URL)
	echo "$(CIRCUITPYTHON_SHA256)  $@" | shasum -a 256 -c - || (rm -f $@; exit 1)

.PHONY: check-circuitpy
check-circuitpy:
	diskutil info $(CIRCUITPY_VOLUME) 2>/dev/null | grep -q '^ *Volume Name: *CIRCUITPY$$' || \
		(echo "error: CIRCUITPY volume not found at $(CIRCUITPY_VOLUME)" >&2; exit 1)

.PHONY: flash
flash: check-circuitpy
	rsync -rc --delete \
		--exclude=modules/ --exclude=__pycache__/ --exclude=README.md \
		--exclude=.Trashes --exclude=.Spotlight-V100 --exclude=.fseventsd --exclude=.DS_Store \
		firmware/ $(CIRCUITPY_VOLUME)/

.PHONY: debug
debug: check-circuitpy
	sed 's/console=False/console=True/' firmware/boot.py > $(CIRCUITPY_VOLUME)/boot.py
	if diff -q firmware/boot.py $(CIRCUITPY_VOLUME)/boot.py >/dev/null; then \
		echo "error: console=False not found in firmware/boot.py; boot.py on device left unchanged" >&2; \
		exit 1; \
	fi
	@echo "boot.py written to $(CIRCUITPY_VOLUME) with console=True."
	@echo "Find the console port and run: screen \"\$$(ls /dev/cu.usbmodem*)\" 115200"
	@echo "Run 'make flash' afterward to restore console=False."

.PHONY: ping-pong
ping-pong: check-circuitpy
	cp firmware/boot.py $(CIRCUITPY_VOLUME)/boot.py
	cp firmware/ping_pong.py $(CIRCUITPY_VOLUME)/code.py
	cd driver && go run ./cmd/pingpong --vendor-id=$(PINGPONG_VENDOR_ID) --product-id=$(PINGPONG_PRODUCT_ID)
	@echo "Run 'make flash' to restore the real code.py."
