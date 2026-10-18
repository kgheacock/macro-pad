import io
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent / "tools"))
import blink_trace  # noqa: E402


def _record(code, key, payload, device_us):
    return json.dumps(
        {"type": "trace", "code": code, "key_index": key, "payload": payload, "device_us": device_us}
    )


def _cached_lines(show_times_us, group_gap_us=30_000, push_us=26_000):
    """The records of one Show frames of six keys per entry: FRAMES_SHOWN, then
    two groups of three pushes, `group_gap_us` apart, each ending `push_us`
    after it starts."""
    lines = []
    for t in show_times_us:
        lines.append(_record(blink_trace.FRAMES_SHOWN, 255, 0b111111, t))
        for key in range(6):
            start = t + 100 + (key // 3) * group_gap_us
            lines.append(_record(blink_trace.PUSH_STARTED, key, 0, start + key))
            lines.append(_record(blink_trace.REFRESH_DONE, key, 0, start + push_us))
    return lines


def test_the_frames_shown_code_is_11():
    assert blink_trace.FRAMES_SHOWN == 11


def test_cached_run_within_the_limits_passes_and_prints_both_lines():
    records = blink_trace.load_trace(_cached_lines([1_000_000, 2_000_000, 3_000_000]))
    out = io.StringIO()

    assert blink_trace.report(records, "cached", out) is True
    text = out.getvalue()
    assert "shows 3" in text
    assert "six keys in 56.1 ms (limit 150)" in text
    assert "push span 30.0 ms (limit 40)" in text


def test_cached_run_with_a_slow_show_fails():
    records = blink_trace.load_trace(_cached_lines([1_000_000], group_gap_us=120_000))

    assert blink_trace.report(records, "cached", io.StringIO()) is False


def test_cached_run_with_pushes_too_far_apart_fails_on_the_span():
    records = blink_trace.load_trace(_cached_lines([1_000_000], group_gap_us=45_000))
    out = io.StringIO()

    assert blink_trace.report(records, "cached", out) is False
    assert "push span 45.0 ms (limit 40)" in out.getvalue()


def test_cached_run_with_a_show_missing_a_push_fails():
    lines = [
        line
        for line in _cached_lines([1_000_000, 2_000_000])
        if not (
            json.loads(line)["code"] == blink_trace.PUSH_STARTED
            and json.loads(line)["key_index"] == 5
            and json.loads(line)["device_us"] > 1_500_000
        )
    ]

    assert blink_trace.report(blink_trace.load_trace(lines), "cached", io.StringIO()) is False


def test_cached_run_with_no_show_fails():
    out = io.StringIO()

    assert blink_trace.report([], "cached", out) is False
    assert "no Show frames" in out.getvalue()


def _pips_lines(show_times_us, stamp_us=40_000, group_gap_us=30_000, push_us=26_000):
    """One Show frames with pips of six keys per entry: SHOW_WITH_PIPS_STARTED,
    the stamps (`stamp_us`), FRAMES_SHOWN, then two groups of three pushes."""
    lines = []
    for t in show_times_us:
        lines.append(_record(blink_trace.SHOW_WITH_PIPS_STARTED, 255, 0b111111, t))
        shown = t + stamp_us
        lines.append(_record(blink_trace.FRAMES_SHOWN, 255, 0b111111, shown))
        for key in range(6):
            start = shown + 100 + (key // 3) * group_gap_us
            lines.append(_record(blink_trace.PUSH_STARTED, key, 0, start + key))
            lines.append(_record(blink_trace.REFRESH_DONE, key, 0, start + push_us))
    return lines


def test_the_show_with_pips_started_code_is_13():
    assert blink_trace.SHOW_WITH_PIPS_STARTED == 13


def test_pips_run_counts_the_stamps_in_six_keys_in_and_does_not_limit_the_span():
    records = blink_trace.load_trace(_pips_lines([1_000_000, 2_000_000], group_gap_us=45_000))
    out = io.StringIO()

    assert blink_trace.report(records, "pips", out) is True
    text = out.getvalue()
    assert "shows 2" in text
    assert "six keys in 111.1 ms (limit 150)" in text  # 40 stamp + 100 us + 45 gap + 26 push
    assert "push span 45.0 ms (not limited)" in text


def test_pips_run_with_slow_stamps_fails():
    records = blink_trace.load_trace(_pips_lines([1_000_000], stamp_us=120_000))

    assert blink_trace.report(records, "pips", io.StringIO()) is False


def test_pips_run_with_no_show_with_pips_fails():
    records = blink_trace.load_trace(_cached_lines([1_000_000]))

    assert blink_trace.report(records, "pips", io.StringIO()) is False
