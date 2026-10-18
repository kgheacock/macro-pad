import io
import sys
import zlib
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "tools"))

import glyphcore_spike

_FIGURES = (
    "inflate stream=9000 core0_us=40000 submit_us=900 wall_us=21000 core1_us=20500 polls=3000 equal=1\n"
    "inflate stream=9000 core0_us=42000 submit_us=800 wall_us=22000 core1_us=21500 polls=3100 equal=1\n"
    "inflate stream=9000 core0_us=41000 submit_us=850 wall_us=21500 core1_us=21000 polls=3050 equal={equal}\n"
    "compose alone_us=75000 during_us=82000\n"
    "compose alone_us=76000 during_us=83000\n"
    "slotcopy bytes=32768 copy_us=6000\n"
    "slotcopy bytes=32768 copy_us=6200\n"
    "gc slots=128 collect_us={gc}\n"
    "gc slots=128 collect_us={gc}\n"
    "heap free=2500000\n"
)


def test_probe_code_compiles_and_uses_the_module_calls():
    code = glyphcore_spike.probe_code()

    compile(code, "code.py", "exec")
    for call in ("glyphcore.alloc(128)", "glyphcore.submit(", "glyphcore.poll()", "glyphcore.last_us()", "gc.collect()"):
        assert call in code


def test_the_probe_never_calls_mem_free_inside_a_timed_section():
    code = glyphcore_spike.probe_code()

    assert code.count("gc.mem_free()") == 1
    assert code.index("gc.mem_free()") > code.rindex("collect_us")


def test_icon_stream_is_a_zlib_stream_of_a_whole_glyph():
    stream = glyphcore_spike.icon_stream()

    assert len(zlib.decompress(stream)) == 32768


def test_report_passes_when_pixels_are_equal_and_the_collection_is_fast():
    out = io.StringIO()

    assert glyphcore_spike.report(_FIGURES.format(equal=1, gc=900), out) is True
    text = out.getvalue()
    assert "inflate on core 1 21000 us" in text
    assert "compose on core 0 76000 us alone, 83000 us while core 1 inflates" in text
    assert "32 KB copy into a slot 6200 us" in text
    assert "equal to zlib on core 0: yes" in text


def test_report_fails_when_one_inflate_differs():
    out = io.StringIO()

    assert glyphcore_spike.report(_FIGURES.format(equal=0, gc=900), out) is False
    assert "NO" in out.getvalue()


def test_report_fails_over_10_ms_and_passes_at_exactly_10_ms():
    assert glyphcore_spike.report(_FIGURES.format(equal=1, gc=10001), io.StringIO()) is False
    assert glyphcore_spike.report(_FIGURES.format(equal=1, gc=10000), io.StringIO()) is True


def test_report_fails_with_no_figures():
    assert glyphcore_spike.report("garbage", io.StringIO()) is False
