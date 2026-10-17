import io
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "tools"))

import slot_probe

_FIGURES = (
    "pool slots=128 alloc_ms=410 free_before=7000000 free_after={free}\n"
    "copy bytes=32768 copy_us=900\n"
    "copy bytes=32768 copy_us=700\n"
    "copy bytes=32768 copy_us=800\n"
)


def test_probe_code_compiles_and_builds_the_real_cache():
    code = slot_probe.probe_code()

    compile(code, "code.py", "exec")
    assert "frame_cache.FrameCache()" in code
    assert "slot_count" in code


def test_probe_never_calls_mem_free_inside_a_timed_section():
    code = slot_probe.probe_code()

    assert code.count("gc.mem_free()") == 2
    before_alloc = code.index("t0 = time.monotonic_ns()")
    after_alloc = code.index("alloc_ns = ")
    assert "mem_free" not in code[before_alloc:after_alloc]
    copy_loop = code[code.index("for trial"): code.index("data = usb_cdc")]
    assert "mem_free" not in copy_loop


def test_report_prints_the_three_figures_and_passes_with_2_mb_free():
    out = io.StringIO()

    assert slot_probe.report(_FIGURES.format(free=2_500_000), out) is True
    text = out.getvalue()
    assert "allocation 410 ms" in text
    assert "free heap after 2500000 bytes" in text
    assert "32 KB copy 800 us" in text


def test_report_fails_and_says_to_stop_under_2_mb():
    out = io.StringIO()

    assert slot_probe.report(_FIGURES.format(free=2_097_151), out) is False
    assert "stop" in out.getvalue()


def test_report_passes_at_exactly_2_mb():
    assert slot_probe.report(_FIGURES.format(free=2_097_152), io.StringIO()) is True


def test_report_fails_with_no_figures():
    assert slot_probe.report("garbage", io.StringIO()) is False
