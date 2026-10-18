#!/bin/sh
# Build CircuitPython 10.2.1 for the Pimoroni Pico Plus 2 with the glyphcore
# module (task 0059). The result is build/.../firmware.uf2.
#
# Usage: tools/build_circuitpython.sh [WORKDIR]
#        tools/build_circuitpython.sh --uf2 [WORKDIR]   (print the UF2 path only)
#
# WORKDIR (default ../circuitpython-build, beside this repo) holds the clone of
# CircuitPython. It needs about 3 GB. The script needs git, python3, make, and
# an Arm GNU toolchain 14.x (`arm-none-eabi-gcc`) with newlib on the PATH.
# Homebrew's arm-none-eabi-gcc has no newlib: download the "AArch32 bare-metal
# target (arm-none-eabi)" tarball from developer.arm.com instead.
set -eu

CIRCUITPYTHON_TAG=10.2.1
BOARD=pimoroni_pico_plus2

repo=$(cd "$(dirname "$0")/.." && pwd)
print_only=
if [ "${1:-}" = "--uf2" ]; then
    print_only=1
    shift
fi
workdir=${1:-"$repo/../circuitpython-build"}
mkdir -p "$workdir"
workdir=$(cd "$workdir" && pwd)
source_dir=$workdir/circuitpython
uf2=$source_dir/ports/raspberrypi/build-$BOARD/firmware.uf2

if [ -n "$print_only" ]; then
    echo "$uf2"
    exit 0
fi

if ! command -v arm-none-eabi-gcc >/dev/null; then
    echo "arm-none-eabi-gcc is not on the PATH" >&2
    exit 1
fi
if ! echo 'int main(void){return 0;}' | arm-none-eabi-gcc -x c --specs=nosys.specs -o /dev/null - 2>/dev/null; then
    echo "this arm-none-eabi-gcc has no newlib (no nosys.specs); use the Arm GNU toolchain" >&2
    exit 1
fi

if [ ! -d "$source_dir/.git" ]; then
    git clone --depth 1 --branch "$CIRCUITPYTHON_TAG" https://github.com/adafruit/circuitpython.git "$source_dir"
fi
cd "$source_dir"
python3 tools/ci_fetch_deps.py raspberrypi
python3 -m pip install --quiet -r requirements-dev.txt
make -j8 -C mpy-cross

# USER_C_MODULES names the folder whose subfolders hold a micropython.mk.
make -j8 -C ports/raspberrypi BOARD="$BOARD" USER_C_MODULES="$repo/tools/usermod"

echo "built: $uf2"
