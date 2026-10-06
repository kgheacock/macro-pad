"""Fake `microcontroller` module. See ../README.md for how these stubs are used.

`nvm` is the board's 4096 bytes of non-volatile memory, as a plain
`bytearray` that starts out erased.
"""

nvm = bytearray(b"\xff" * 4096)
