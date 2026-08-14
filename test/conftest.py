import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).parent.parent
_FIRMWARE_DIR = _REPO_ROOT / "firmware"
_STUBS_DIR = Path(__file__).parent / "stubs"

# pytest's default import mode only adds test/ to sys.path (no __init__.py
# stops it walking up further), so firmware/ needs adding explicitly for
# `import firmware.pins` to resolve.
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

# firmware/'s contents are copied to the root of the CIRCUITPY drive, where
# there is no `firmware` package, so modules that import each other do it
# flat (`import wire`). Putting firmware/ on sys.path lets a test import
# those modules under the same names the board uses. Import one module by
# one name per test file: `wire` and `firmware.wire` are two module objects,
# so classes from one fail `isinstance` against the other.
#
# Appended, not inserted: firmware/code.py would otherwise shadow the
# standard library's `code` module, which pdb imports and pytest loads at
# startup. Last place on the path keeps the standard library winning every
# name it already owns.
if str(_FIRMWARE_DIR) not in sys.path:
    sys.path.append(str(_FIRMWARE_DIR))

if str(_STUBS_DIR) not in sys.path:
    sys.path.insert(0, str(_STUBS_DIR))
