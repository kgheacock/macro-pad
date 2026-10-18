# glyphcore: a user C module for CircuitPython (task 0059). Build with
# `make USER_C_MODULES=<the folder that holds this one>`; tools/build_circuitpython.sh does.

GLYPHCORE_DIR := $(USERMOD_DIR)

SRC_USERMOD_C += $(GLYPHCORE_DIR)/glyphcore.c
SRC_USERMOD_C += $(GLYPHCORE_DIR)/inflate.c

CFLAGS_USERMOD += -I$(GLYPHCORE_DIR)

# A flash write must not start while core 1 runs. These two options (only the
# linker reads them) route the flash calls of the port through the checks in
# glyphcore.c. The raspberrypi port links with $(CFLAGS), not $(LDFLAGS).
CFLAGS_USERMOD += -Wl,--wrap=flash_range_erase -Wl,--wrap=flash_range_program
