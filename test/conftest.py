import sys
from pathlib import Path

_REPO_ROOT = Path(__file__).parent.parent
_STUBS_DIR = Path(__file__).parent / "stubs"

# pytest's default import mode only adds test/ to sys.path (no __init__.py
# stops it walking up further), so firmware/ needs adding explicitly for
# `import firmware.pins` to resolve.
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

if str(_STUBS_DIR) not in sys.path:
    sys.path.insert(0, str(_STUBS_DIR))
