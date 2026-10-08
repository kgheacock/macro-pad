CIRCUITPYTHON_VERSION := 10.2.1
CIRCUITPYTHON_BOARD   := pimoroni_pico_plus2
CIRCUITPYTHON_LANG    := en_US

CIRCUITPYTHON_UF2     := adafruit-circuitpython-$(CIRCUITPYTHON_BOARD)-$(CIRCUITPYTHON_LANG)-$(CIRCUITPYTHON_VERSION).uf2
CIRCUITPYTHON_URL     := https://downloads.circuitpython.org/bin/$(CIRCUITPYTHON_BOARD)/$(CIRCUITPYTHON_LANG)/$(CIRCUITPYTHON_UF2)
CIRCUITPYTHON_SHA256  := 1079dcaa14617613507993bb186b427da5d4e18af23cf61df672b41ca7553b7a

CIRCUITPY_VOLUME      := /Volumes/CIRCUITPY

# PINGPONG_VENDOR_ID and PINGPONG_PRODUCT_ID identify the macro pad's USB
# device descriptor for `make ping-pong`. Read from `ioreg -p IOUSB -l`
# with the Pimoroni Pico Plus 2 attached, running CircuitPython; override
# on the command line if a different board reports different values. See
# driver/README.md.
PINGPONG_VENDOR_ID    := 0x2E8A
PINGPONG_PRODUCT_ID   := 0x10A3

.PHONY: firmware-uf2
firmware-uf2: firmware/modules/$(CIRCUITPYTHON_UF2)

firmware/modules/$(CIRCUITPYTHON_UF2):
	mkdir -p firmware/modules
	curl -fL -o $@ $(CIRCUITPYTHON_URL)
	echo "$(CIRCUITPYTHON_SHA256)  $@" | shasum -a 256 -c - || (rm -f $@; exit 1)

.PHONY: check-circuitpy
check-circuitpy:
	@if diskutil info $(CIRCUITPY_VOLUME) 2>/dev/null | grep -q '^ *Volume Name: *CIRCUITPY$$'; then \
		echo "OK"; \
	else \
		echo "Error: CIRCUITPY volume not found at $(CIRCUITPY_VOLUME)" >&2; \
		exit 1; \
	fi

.PHONY: flash
flash: check-circuitpy
	rsync -rc --delete \
		--exclude=modules/ --exclude=__pycache__/ --exclude=README.md --exclude=lib/ \
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
	cp firmware/wire.py $(CIRCUITPY_VOLUME)/wire.py
	cp firmware/ping_pong.py $(CIRCUITPY_VOLUME)/code.py
	cd driver && go run ./cmd/pingpong --vendor-id=$(PINGPONG_VENDOR_ID) --product-id=$(PINGPONG_PRODUCT_ID)
	@echo "Run 'make flash' to restore the real code.py and wire.py."

# `make blink-trace` runs a scripted run of key-state updates against the
# board and prints its figures. SCENARIO is `single` (task 0044's DoD-5:
# 10 updates to key 4 while keys 0 to 2 blink) or `burst` (DoD-6: 6 updates
# back to back). It puts a tracing code.py on the board, so run `make flash`
# afterward to restore the real one. It unmounts CIRCUITPY for the run,
# because a mounted CIRCUITPY on macOS reloads the board and breaks CDC and
# HID, and it mounts the volume again when the run ends, even if it failed.
SCENARIO              ?= single
BLINK_TRACE_FILE      ?= /tmp/macropad-blink-trace-$(SCENARIO).jsonl

.PHONY: blink-trace
blink-trace: check-circuitpy
	python3 tools/blink_trace.py install $(CIRCUITPY_VOLUME)
	sync
	@dev=$$(diskutil info $(CIRCUITPY_VOLUME) | awk '/Device Identifier:/ {print $$3}'); \
	diskutil unmount $(CIRCUITPY_VOLUME) || exit 1; \
	echo "Waiting for the board to reload code.py: a report sent during the reload is dropped."; \
	sleep 10; \
	( cd driver && go run ./cmd/blinksend --vendor-id=$(PINGPONG_VENDOR_ID) --product-id=$(PINGPONG_PRODUCT_ID) \
		--scenario=warmup --trace-file=/dev/null ) && \
	( cd driver && go run ./cmd/blinksend --vendor-id=$(PINGPONG_VENDOR_ID) --product-id=$(PINGPONG_PRODUCT_ID) \
		--scenario=$(SCENARIO) --trace-file=$(BLINK_TRACE_FILE) ) \
	&& python3 tools/blink_trace.py report --scenario=$(SCENARIO) $(BLINK_TRACE_FILE); \
	status=$$?; \
	diskutil mount /dev/$$dev >/dev/null; \
	echo "Run 'make flash' to restore the real code.py."; \
	exit $$status

# `make dma-spike` measures how long the CPU is free while a cached-frame
# push is on the wire (task 0045's DoD-1). Run `make flash` first, so the board
# has the current firmware modules. It puts a measuring code.py on the board,
# so run `make flash` afterward to restore the real one. Like `blink-trace`, it
# unmounts CIRCUITPY for the run and mounts it again at the end.
.PHONY: dma-spike
dma-spike: check-circuitpy
	python3 tools/dma_spike.py install $(CIRCUITPY_VOLUME)
	sync
	@dev=$$(diskutil info $(CIRCUITPY_VOLUME) | awk '/Device Identifier:/ {print $$3}'); \
	diskutil unmount $(CIRCUITPY_VOLUME) || exit 1; \
	echo "Waiting for the board to reload code.py and measure."; \
	sleep 10; \
	python3 tools/dma_spike.py read; \
	status=$$?; \
	diskutil mount /dev/$$dev >/dev/null; \
	echo "Run 'make flash' to restore the real code.py."; \
	exit $$status

.PHONY: e2e
e2e: flash
	cd driver && MACROPAD_VENDOR_ID=$(PINGPONG_VENDOR_ID) MACROPAD_PRODUCT_ID=$(PINGPONG_PRODUCT_ID) \
		go test -tags hardware -count=1 ./e2e/...
