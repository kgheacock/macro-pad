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
            lines.append(_trace_line(blink_trace.PUSH_STARTED, key, t + key * 25_000))
        t += blink_period_us
    lines.append(_trace_line(blink_trace.HOST_MESSAGE_DECODED, 4, 1_000_000))
    lines.append(_trace_line(blink_trace.PUSH_STARTED, 4, 1_060_000))
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
        _trace_line(blink_trace.PUSH_STARTED, 0, 0),
        _trace_line(blink_trace.PUSH_STARTED, 0, 900_000),
        _trace_line(blink_trace.HOST_MESSAGE_DECODED, 4, 1_000_000),
        _trace_line(blink_trace.PUSH_STARTED, 0, 1_500_000),
    ]

    assert blink_trace.blink_gaps_ms(blink_trace.load_trace(lines)) == [600.0]


@pytest.mark.parametrize("decoded,passes", [(6, True), (2, False)])
def test_burst_counts_the_keys_whose_report_was_decoded(decoded, passes):
    lines = [
        _trace_line(blink_trace.HOST_MESSAGE_DECODED, key, 50_000 * key) for key in range(decoded)
    ]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(lines), "burst", out) is passes
    assert "decoded {}/6".format(decoded) in out.getvalue()


def test_burst_with_a_repeated_decode_still_passes():
    lines = [_trace_line(blink_trace.HOST_MESSAGE_DECODED, key, 50_000 * key) for key in range(6)]
    lines.append(_trace_line(blink_trace.HOST_MESSAGE_DECODED, 5, 350_000))
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(lines), "burst", out) is True
    assert "decoded 6/6" in out.getvalue()
    assert "7 records" in out.getvalue()


def _decoded(key, payload, device_us):
    return json.dumps(
        {
            "type": "trace",
            "code": blink_trace.HOST_MESSAGE_DECODED,
            "key_index": key,
            "payload": payload,
            "device_us": device_us,
        }
    )


def test_busy_burst_counts_only_the_tagged_reports():
    base = blink_trace.BUSY_BURST_EMOJI_BASE
    setup = [_decoded(key, 0, 1000 * key) for key in range(3)]
    burst = [_decoded(key, base + key, 1_000_000 + 50_000 * key) for key in range(6)]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(setup + burst), "busyburst", out) is True
    assert "decoded 6/6" in out.getvalue()


def test_busy_burst_names_the_keys_whose_report_was_lost():
    base = blink_trace.BUSY_BURST_EMOJI_BASE
    setup = [_decoded(key, 0, 1000 * key) for key in range(3)]
    burst = [_decoded(key, base + key, 1_000_000 + 50_000 * key) for key in (0, 1, 3, 4, 5)]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(setup + burst), "busyburst", out) is False
    assert "decoded 5/6" in out.getvalue()
    assert "lost the reports of keys [2]" in out.getvalue()


def test_traced_code_adds_a_tracer_to_the_real_code_py():
    code = (Path(blink_trace._REPO_ROOT) / "firmware" / "code.py").read_text()

    traced = blink_trace.traced_code(code)

    assert "import tracer as tracer_module" in traced
    assert "tracer=tracer_module.Tracer(capacity=256, enabled=True)," in traced
    compile(traced, "code.py", "exec")


def test_traced_code_refuses_a_code_py_it_does_not_recognise():
    with pytest.raises(ValueError):
        blink_trace.traced_code("print('hello')\n")


def _event(code, key, device_us):
    return _trace_line(code, key, device_us)


def _sync_run(starts_us, push_ms=26):
    """A `sync` run: the setup decodes all six keys, key 4 is updated at 1.25 s,
    then six blink slots. `starts_us` is how far after the slot each key's
    push starts. Each push ends `push_ms` after its own start.
    """
    lines = [_decoded(key, 0, 1000 * key) for key in range(6)]
    lines.append(_decoded(4, 0, 1_250_000))  # the first update, between two slots
    lines.append(_event(blink_trace.PUSH_STARTED, 4, 1_251_000))
    lines.append(_event(blink_trace.REFRESH_DONE, 4, 1_251_000 + push_ms * 1000))
    for slot in range(2, 8):
        for key, offset in zip(range(6), starts_us):
            start = slot * 500_000 + 2000 + offset
            lines.append(_event(blink_trace.PUSH_STARTED, key, start))
            lines.append(_event(blink_trace.REFRESH_DONE, key, start + push_ms * 1000))
    return lines


def test_sync_run_with_parallel_pushes_passes():
    records = blink_trace.load_trace(_sync_run([0, 100, 200, 300, 400, 500]))
    out = io.StringIO()

    assert blink_trace.report(records, "sync", out) is True
    text = out.getvalue()
    assert "slots 5" in text
    assert "max skew 0.5 ms (limit 35)" in text
    assert "max span 26.5 ms (limit 50)" in text
    assert "max gap 500.0 ms (limit 600)" in text


def test_sync_run_with_pushes_in_turn_fails_on_skew():
    # Six pushes of 18 ms, one after the other: 90 ms from the first start to
    # the last, 108 ms to the last end.
    records = blink_trace.load_trace(_sync_run([0, 18_000, 36_000, 54_000, 72_000, 90_000], 18))
    out = io.StringIO()

    assert blink_trace.report(records, "sync", out) is False
    assert "max skew 90.0 ms (limit 35)" in out.getvalue()


def test_sync_skew_ignores_the_updated_key():
    # Key 4 starts 40 ms after the others in every slot, like an update does.
    records = blink_trace.load_trace(_sync_run([0, 0, 0, 0, 40_000, 0]))
    out = io.StringIO()

    assert blink_trace.report(records, "sync", out) is True
    assert "max skew 0.0 ms" in out.getvalue()


def test_sync_run_with_a_slow_push_fails_on_span():
    records = blink_trace.load_trace(_sync_run([0, 0, 0, 0, 0, 0], push_ms=55))
    out = io.StringIO()

    assert blink_trace.report(records, "sync", out) is False
    assert "max span 55.0 ms (limit 50)" in out.getvalue()


def test_sync_run_with_a_missed_blink_fails_on_gap():
    lines = [line for line in _sync_run([0] * 6)]
    # Drop key 0's pushes of slot 4 and its done record.
    drop = 4 * 500_000 + 2000
    lines = [
        line
        for line in lines
        if not (json.loads(line).get("key_index") == 0 and json.loads(line).get("device_us", 0) in (drop, drop + 26_000))
    ]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(lines), "sync", out) is False
    assert "max gap 1000.0 ms (limit 600)" in out.getvalue()


def test_sync_run_without_an_update_fails():
    lines = [_decoded(key, 0, 1000 * key) for key in range(6)]
    out = io.StringIO()

    assert blink_trace.report(blink_trace.load_trace(lines), "sync", out) is False
    assert "no update to key 4" in out.getvalue()


def test_trace_codes_match_the_firmware_tracer():
    # The tool reads records by code. A wrong code finds no record, and a
    # figure built from it reads 0.0 ms and passes (found on 2026-10-10).
    sys.path.insert(0, str(Path(__file__).parent.parent / "firmware"))
    import tracer as tracer_module

    assert blink_trace.HOST_MESSAGE_DECODED == tracer_module.HOST_MESSAGE_DECODED
    assert blink_trace.PUSH_STARTED == tracer_module.PUSH_STARTED
    assert blink_trace.REFRESH_DONE == tracer_module.REFRESH_DONE
