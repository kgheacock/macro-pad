"""Tests the C inflater of the glyphcore module (task 0059) on the host.

The source in `tools/usermod/glyphcore/inflate.c` has no dependency on
CircuitPython, so it compiles with the host's `cc` and runs through ctypes.
Python's `zlib` is the reference: the inflater must give the same bytes for
any stream that `zlib.decompress` accepts, and refuse a stream it refuses.
"""

import ctypes
import os
import random
import shutil
import subprocess
import zlib
from pathlib import Path

import pytest

_SOURCE_DIR = Path(__file__).parent.parent / "tools" / "usermod" / "glyphcore"

OK = 0
ERR_HEADER = -1
ERR_DATA = -2
ERR_OVERFLOW = -3
ERR_CHECKSUM = -4
ERR_CANCELLED = -5

pytestmark = pytest.mark.skipif(shutil.which("cc") is None, reason="no C compiler")


@pytest.fixture(scope="module")
def inflate(tmp_path_factory):
    library = tmp_path_factory.mktemp("glyphcore") / "libinflate.so"
    subprocess.run(
        [
            "cc", "-O2", "-Wall", "-Werror", "-shared", "-fPIC",
            "-DGLYPHCORE_HOST", "-o", str(library), str(_SOURCE_DIR / "inflate.c"),
        ],
        check=True,
    )
    lib = ctypes.CDLL(str(library))
    lib.glyphcore_inflate_zlib.restype = ctypes.c_int
    lib.glyphcore_inflate_zlib.argtypes = [
        ctypes.c_char_p, ctypes.c_size_t, ctypes.c_void_p, ctypes.c_size_t,
        ctypes.POINTER(ctypes.c_size_t), ctypes.c_void_p,
    ]

    def run(stream, capacity=32768, cancel=None):
        out = ctypes.create_string_buffer(max(capacity, 1))
        out_len = ctypes.c_size_t(0)
        flag = None
        if cancel is not None:
            flag = ctypes.byref(ctypes.c_uint8(cancel))
        status = lib.glyphcore_inflate_zlib(
            stream, len(stream), out, capacity, ctypes.byref(out_len), flag
        )
        return status, out.raw[: out_len.value]

    return run


def _glyph_like(seed, size=32768):
    """A 128x128 RGB565 picture: runs of a few colors, some noise."""
    rng = random.Random(seed)
    palette = [bytes((rng.randrange(256), rng.randrange(256))) for _ in range(6)]
    data = bytearray()
    while len(data) < size:
        data += rng.choice(palette) * rng.randrange(1, 90)
        if rng.random() < 0.1:
            data += bytes(rng.randrange(256) for _ in range(rng.randrange(1, 40)))
    return bytes(data[:size])


@pytest.mark.parametrize("level", [0, 1, 6, 9])
@pytest.mark.parametrize("seed", range(8))
def test_equals_zlib_on_glyph_like_data(inflate, level, seed):
    raw = _glyph_like(seed)
    stream = zlib.compress(raw, level)

    status, out = inflate(stream)

    assert status == OK
    assert out == zlib.decompress(stream)
    assert out == raw


def test_equals_zlib_on_random_data(inflate):
    raw = os.urandom(32768)
    status, out = inflate(zlib.compress(raw))
    assert (status, out) == (OK, raw)


def test_equals_zlib_on_all_zero_data(inflate):
    raw = bytes(32768)
    status, out = inflate(zlib.compress(raw, 9))
    assert (status, out) == (OK, raw)


@pytest.mark.parametrize("strategy", [zlib.Z_FIXED, zlib.Z_HUFFMAN_ONLY, zlib.Z_RLE])
def test_equals_zlib_with_each_block_type(inflate, strategy):
    raw = _glyph_like(99)
    compressor = zlib.compressobj(6, zlib.DEFLATED, 15, 8, strategy)
    stream = compressor.compress(raw) + compressor.flush()

    status, out = inflate(stream)

    assert (status, out) == (OK, raw)


def test_empty_output_is_ok(inflate):
    status, out = inflate(zlib.compress(b""))
    assert (status, out) == (OK, b"")


def test_a_stream_that_overflows_the_buffer_is_refused(inflate):
    stream = zlib.compress(bytes(40000))
    status, _ = inflate(stream, capacity=32768)
    assert status == ERR_OVERFLOW


def test_a_bad_header_is_refused(inflate):
    stream = bytearray(zlib.compress(b"hello hello hello"))
    stream[0] = 0x00
    status, _ = inflate(bytes(stream))
    assert status == ERR_HEADER


def test_a_bad_checksum_is_refused(inflate):
    stream = bytearray(zlib.compress(_glyph_like(3)))
    stream[-1] ^= 0xFF
    status, _ = inflate(bytes(stream))
    assert status == ERR_CHECKSUM


def test_a_truncated_stream_is_refused(inflate):
    stream = zlib.compress(_glyph_like(4))
    for cut in (3, len(stream) // 2, len(stream) - 5):
        status, _ = inflate(stream[:cut])
        assert status != OK


def test_damaged_streams_never_crash_and_never_disagree_with_zlib(inflate):
    rng = random.Random(7)
    stream = zlib.compress(_glyph_like(5))
    for _ in range(300):
        damaged = bytearray(stream)
        for _ in range(rng.randrange(1, 4)):
            damaged[rng.randrange(len(damaged))] ^= 1 << rng.randrange(8)
        status, out = inflate(bytes(damaged))
        try:
            reference = zlib.decompress(bytes(damaged))
        except zlib.error:
            assert status != OK
        else:
            assert (status, out) == (OK, reference)


def test_cancel_stops_the_inflate(inflate):
    stream = zlib.compress(_glyph_like(6))
    status, _ = inflate(stream, cancel=1)
    assert status == ERR_CANCELLED
