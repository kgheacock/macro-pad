import io
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parent.parent / "tools"))
import blink_trace  # noqa: E402


def _trace_line(code, key, device_us):
    return json.dumps(
        {"type": "trace", "code": code, "key_index": key, "payload": 0, "device_us": device_us}
    )


def _single_run(blink_period_us):
    """Keys 0 to 2 blink for 3 s. Key 4 is updated at 1 s."""
    lines = [json.dumps({"offset_us": 0, "samples": 1, "estimator": "x"})]
    t = 0
    while t <= 3_000_000:
        for key in (0, 1, 2):
            lines.append(_trace_line(blink_trace.REFRESH_DONE, key, t + key * 25_000))
        t += blink_period_us
    lines.append(_trace_line(blink_trace.HOST_MESSAGE_DECODED, 4, 1_000_000))
    lines.append(_trace_line(blink_trace.REFRESH_DONE, 4, 1_060_000))
    return lines


def test_single_run_within_the_limit_passes():
    records = blink_trace.load_trace(_single_run(500_000))
    out = io.StringIO()

    assert blink_trace.report(records, "single", out) is True
    assert "max gap 500.0 ms" in out.getvalue()


def test_single_run_with_a_frozen_blink_fails():
    records = blink_trace.load_trace(_single_run(950_000))

    assert blink_trace.report(records, "single", io.StringIO()) is False


def test_gaps_before_the_first_update_do_not_count():
    lines = [
        _trace_line(blink_trace.REFRESH_DONE, 0, 0),
        _trace_line(blink_trace.REFRESH_DONE, 0, 900_000),
        _trace_line(blink_trace.HOST_MESSAGE_DECODED, 4, 1_000_000),
        _trace_line(blink_trace.REFRESH_DONE, 0, 1_500_000),
    ]

    assert blink_trace.blink_gaps_ms(blink_trace.load_trace(lines)) == [600.0]


@pytest.mark.parametrize("decoded,passes", [(6, True), (2, False)])
def test_burst_counts_decoded_messages(decoded, passes):
    lines = [
        _trace_line(blink_trace.HOST_MESSAGE_DECODED, key, 50_000 * key) for key in range(decoded)
    ]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(lines), "burst", out) is passes
    assert "decoded {}/6".format(decoded) in out.getvalue()


def test_traced_code_adds_a_tracer_to_the_real_code_py():
    code = (Path(blink_trace._REPO_ROOT) / "firmware" / "code.py").read_text()

    traced = blink_trace.traced_code(code)

    assert "import tracer as tracer_module" in traced
    assert "tracer=tracer_module.Tracer(capacity=256, enabled=True)," in traced
    compile(traced, "code.py", "exec")


def test_traced_code_refuses_a_code_py_it_does_not_recognise():
    with pytest.raises(ValueError):
        blink_trace.traced_code("print('hello')\n")
