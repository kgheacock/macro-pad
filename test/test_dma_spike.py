import io
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "tools"))

import dma_spike

_CODE = (Path(__file__).parent.parent / "firmware" / "code.py").read_text()


def test_spike_code_replaces_the_run_call_with_the_measurement():
    code = dma_spike.spike_code(_CODE)

    assert "macro_pad.run()" not in code
    assert "panels[0].start_push(frame)" in code
    assert code.startswith(_CODE.split("macro_pad.run()")[0])
    compile(code, "code.py", "exec")


def test_spike_code_refuses_a_code_py_with_no_run_call():
    try:
        dma_spike.spike_code("print('no loop')\n")
    except ValueError:
        return
    raise AssertionError("expected ValueError")


def _lines(*cpu_free, start_push_us=300):
    return "\n".join(
        "push start_push_us={} total_ms=18 cpu_free_ms={} loops=440".format(start_push_us, ms)
        for ms in cpu_free
    )


def test_report_passes_when_every_trial_leaves_15_ms_or_more():
    out = io.StringIO()

    assert dma_spike.report(_lines(17, 17, 16), out) is True
    assert "cpu_free_ms 16 (limit 15)" in out.getvalue()


def test_report_fails_when_one_trial_leaves_less_than_15_ms():
    assert dma_spike.report(_lines(17, 14, 17), io.StringIO()) is False


def test_report_fails_with_no_figures():
    out = io.StringIO()

    assert dma_spike.report("garbage", out) is False
    assert "no push figures" in out.getvalue()


def test_report_passes_when_every_start_push_is_500_us_or_less():
    out = io.StringIO()

    assert dma_spike.report(_lines(17, 17, start_push_us=500), out) is True
    assert "start_push_us 500 (limit 500)" in out.getvalue()


def test_report_fails_when_start_push_holds_the_cpu_over_500_us():
    out = io.StringIO()

    assert dma_spike.report(_lines(17, 17, start_push_us=1200), out) is False
    assert "start_push_us 1200 (limit 500)" in out.getvalue()
