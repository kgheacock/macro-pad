import sys
from pathlib import Path

_STUBS_DIR = Path(__file__).parent / "stubs"

if str(_STUBS_DIR) not in sys.path:
    sys.path.insert(0, str(_STUBS_DIR))
