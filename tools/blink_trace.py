#!/usr/bin/env python3
"""Install a tracing `code.py` on the board, and read the trace of a blink run.

`make blink-trace` runs `install`, sends a scripted run with
`driver/cmd/blinksend`, then runs `report` on the JSONL file that command
wrote. See tasks/complete/0044-blink-independence-persist-off-blink-path.md.

    blink_trace.py install CIRCUITPY_VOLUME
    blink_trace.py report --scenario single|burst|busyburst|sync|batch|batchloss|cached|icons|glyphs TRACE_FILE

`install` edits `firmware/code.py` in memory and writes the result to the
board only. The repo's `code.py` is not touched. `make flash` puts the
untraced one back.

`report` exits 0 when the run meets its limits and 1 when it does not.
"""

import argparse
import json
import sys
from pathlib import Path

# Trace codes, as in firmware/tracer.py.
HOST_MESSAGE_DECODED = 1
CUSTOM_GLYPH_DECODED = 5
PUSH_STARTED = 9
REFRESH_DONE = 8
BATCH_TIMED_OUT = 10
# A record that says the board lost trace records (firmware/tracer.py).
TRACE_DROPPED = 0
GLYPH_BUILT = 7
# Task 0063: an open batch asked the host for what it lacks.
BATCH_INCOMPLETE = 12
# The most ms between the last report of the first pass and the release, when a
# key state was lost and sent again.
MAX_RESEND_RELEASE_MS = 1000
FRAMES_SHOWN = 11

# The largest allowed gap, in milliseconds, between two PUSH_STARTED records
# of one blinking key. Task 0044's DoD-5 allowed 650. Task 0045's DoD-4 allows
# 600: a blink toggles every 500 ms, within 100 ms.
BLINKING_KEYS = (0, 1, 2)
UPDATED_KEY = 4
MAX_GAP_LIMIT_MS = 600

# Task 0048's `sync` scenario: all six keys blink, and key 4 is updated every
# 2 s. DoD-6 limits the figures below. The skew is measured on the five keys
# that are never updated, because an update to key 4 pushes at any moment.
SYNC_KEYS = (0, 1, 2, 3, 4, 5)
MAX_SKEW_LIMIT_MS = 35
MAX_SPAN_LIMIT_MS = 50

# Two pushes of one blink slot start less than this far apart. Slots are
# 500 ms apart and a serial push of all six keys spreads over 108 ms.
SLOT_CLUSTER_MS = 250

# Pushes of one group start less than this far apart. `app.MacroPad` starts
# the pushes of a group one after another, a few microseconds apart.
GROUP_CLUSTER_MS = 5

# DoD-6: how many updates the burst sends, one to each key from 0.
BURST_UPDATES = 6

# The busy burst tags key k's update with Emoji ID BUSY_BURST_EMOJI_BASE + k,
# which the board's HOST_MESSAGE_DECODED record carries as its payload, so a
# burst report can be told from the setup that makes keys 0 to 2 blink.
BUSY_BURST_EMOJI_BASE = 0x20

# Task 0060's `batch` scenario: a layer change of six keys, sent after Begin
# batch. DoD-1 limits the span between the first PUSH_STARTED of the six keys.
BATCH_KEYS = (0, 1, 2, 3, 4, 5)
MAX_PUSH_SPAN_LIMIT_MS = 40

# Task 0061's `cached` scenario: six Show frames messages of six stored frames.
# DoD-2 limits the time from the board's Show to the end of the last push, and
# the span between the first PUSH_STARTED of the six keys.
MAX_SHOW_LIMIT_MS = 150
ALL_KEYS_MASK = 0b111111

_REPO_ROOT = Path(__file__).resolve().parent.parent

# Where `install` adds the tracer to `firmware/code.py`.
_IMPORT_ANCHOR = "import st7735\n"
_IMPORT_LINE = "import tracer as tracer_module\n"
_ARGUMENT_ANCHOR = "    serial=usb_cdc.data,\n"
_ARGUMENT_LINE = "    tracer=tracer_module.Tracer(capacity=256, enabled=True),\n"


def traced_code(code):
    """Return `code`, the text of `firmware/code.py`, with a `Tracer` added."""
    for anchor in (_IMPORT_ANCHOR, _ARGUMENT_ANCHOR):
        if code.count(anchor) != 1:
            raise ValueError(
                "firmware/code.py has no single line {!r} to add the tracer after".format(
                    anchor.strip()
                )
            )
    code = code.replace(_IMPORT_ANCHOR, _IMPORT_ANCHOR + _IMPORT_LINE)
    return code.replace(_ARGUMENT_ANCHOR, _ARGUMENT_ANCHOR + _ARGUMENT_LINE)


def load_trace(lines):
    """Return the trace records in `lines`, a recorder JSONL file, as
    `(code, key, payload, device_us)` tuples sorted by device time.
    """
    records = []
    for line in lines:
        line = line.strip()
        if not line:
            continue
        entry = json.loads(line)
        if entry.get("type") != "trace":
            continue
        records.append(
            (entry["code"], entry.get("key_index", 0), entry.get("payload", 0), entry["device_us"])
        )
    records.sort(key=lambda record: record[3])
    return records


def _first_update_time(records, skip=0):
    """The device time of the `skip + 1`-th report decoded for the updated
    key, or `None`."""
    times = [
        t for code, key, _, t in records if code == HOST_MESSAGE_DECODED and key == UPDATED_KEY
    ]
    return times[skip] if len(times) > skip else None


def blink_gaps_ms(records, keys=BLINKING_KEYS, window_start=None):
    """Return every gap, in ms, between two PUSH_STARTED records of one
    blinking key, for the gaps that end after the first update to the
    updated key — the run's window starts there.
    """
    if window_start is None:
        window_start = _first_update_time(records)
    if window_start is None:
        return []

    gaps = []
    last = {}
    for code, key, _, t in records:
        if code != PUSH_STARTED or key not in keys:
            continue
        if key in last and t >= window_start:
            gaps.append((t - last[key]) / 1000)
        last[key] = t
    return gaps


def sync_window_start(records):
    """Where the `sync` run's window starts: its first update to key 4. The
    run's setup decodes key 4 once, to make it blink, so the update is the
    second report decoded for it.
    """
    return _first_update_time(records, skip=1)


def slot_skews_ms(records, window_start):
    """Return, for each blink slot in the window, the ms between the first
    and the last PUSH_STARTED of the keys that blink and are not updated.
    """
    keys = [key for key in SYNC_KEYS if key != UPDATED_KEY]
    starts = sorted(
        t for code, key, _, t in records if code == PUSH_STARTED and key in keys and t >= window_start
    )
    skews = []
    cluster = []
    for t in starts:
        if cluster and (t - cluster[-1]) / 1000 > SLOT_CLUSTER_MS:
            skews.append((cluster[-1] - cluster[0]) / 1000)
            cluster = []
        cluster.append(t)
    if cluster:
        skews.append((cluster[-1] - cluster[0]) / 1000)
    return skews


def group_spans_ms(records, window_start):
    """Return, for each group of pushes in the window, the ms from its first
    PUSH_STARTED to its last REFRESH_DONE. A group is the pushes that start
    within GROUP_CLUSTER_MS of each other.
    """
    events = [
        (t, code, key)
        for code, key, _, t in records
        if code in (PUSH_STARTED, REFRESH_DONE) and t >= window_start
    ]
    events.sort()
    groups = []  # each: {"start": t, "last_start": t, "keys": {key}, "end": t}
    open_by_key = {}
    for t, code, key in events:
        if code == PUSH_STARTED:
            group = groups[-1] if groups else None
            if group is None or (t - group["last_start"]) / 1000 > GROUP_CLUSTER_MS:
                group = {"start": t, "last_start": t, "end": t}
                groups.append(group)
            group["last_start"] = t
            open_by_key[key] = group
        elif key in open_by_key:
            group = open_by_key.pop(key)
            group["end"] = max(group["end"], t)
    return [(group["end"] - group["start"]) / 1000 for group in groups]


def decoded_keys(records):
    """Return the key indexes that have a HOST_MESSAGE_DECODED record, and
    how many such records there are.

    The board can decode one report twice: a burst of 6 reports gave 7
    records on 2026-10-06, with key 5 twice. A repeat is harmless, since a
    key state is idempotent, so a report is delivered when its key appears.
    """
    keys = [key for code, key, _, _ in records if code == HOST_MESSAGE_DECODED]
    return set(keys), len(keys)


def busy_burst_keys(records):
    """Return the keys whose tagged busy-burst report was decoded."""
    return {
        key
        for code, key, payload, _ in records
        if code == HOST_MESSAGE_DECODED and payload == BUSY_BURST_EMOJI_BASE + key
    }


def report(records, scenario, out):
    """Print the run's figures to `out`. Return True when they meet the limits."""
    if scenario == "busyburst":
        keys = busy_burst_keys(records)
        print("decoded {}/{}".format(len(keys), BURST_UPDATES), file=out)
        missing = sorted(set(range(BURST_UPDATES)) - keys)
        if missing:
            print("lost the reports of keys {}".format(missing), file=out)
        return len(keys) == BURST_UPDATES

    if scenario == "burst":
        keys, records_seen = decoded_keys(records)
        print("decoded {}/{}".format(len(keys), BURST_UPDATES), file=out)
        if records_seen != len(keys):
            print("({} records: a report was decoded more than once)".format(records_seen), file=out)
        return len(keys) == BURST_UPDATES

    if scenario == "sync":
        return report_sync(records, out)

    if scenario == "batch":
        return report_batch(records, out)

    if scenario == "batchloss":
        return report_batchloss(records, out)

    if scenario == "cached":
        return report_cached(records, out)

    if scenario in ("icons", "glyphs"):
        return report_icons(records, out)

    gaps = blink_gaps_ms(records)
    if not gaps:
        print("no blink gaps in the trace", file=out)
        return False
    max_gap = max(gaps)
    print("max gap {:.1f} ms (limit {})".format(max_gap, MAX_GAP_LIMIT_MS), file=out)
    return max_gap <= MAX_GAP_LIMIT_MS


def batch_window_start(records):
    """Where the `batch` run's window starts: the first glyph the board
    decoded. The power-on paint comes before it."""
    for code, _, _, t in records:
        if code == CUSTOM_GLYPH_DECODED:
            return t
    return None


def push_span_ms(records, window_start, keys=BATCH_KEYS):
    """Return the ms between the first PUSH_STARTED of the earliest key and
    that of the latest key, counting the first push of each key at or after
    `window_start`. Returns `None` when a key has no push in the window.
    """
    first = {}
    for code, key, _, t in records:
        if code == PUSH_STARTED and key in keys and t >= window_start and key not in first:
            first[key] = t
    if set(first) != set(keys):
        return None
    return (max(first.values()) - min(first.values())) / 1000


def report_batch(records, out):
    """Print the `batch` run's `push span`. Return True when it is within
    the limit and the board did not time the batch out."""
    window_start = batch_window_start(records)
    if window_start is None:
        print("no custom glyph in the trace", file=out)
        return False
    span = push_span_ms(records, window_start)
    if span is None:
        print("not every key of {} has a push in the trace".format(list(BATCH_KEYS)), file=out)
        return False
    timed_out = any(code == BATCH_TIMED_OUT for code, _, _, _ in records)
    print("push span {:.1f} ms (limit {})".format(span, MAX_PUSH_SPAN_LIMIT_MS), file=out)
    if timed_out:
        print("the board released the batch after its time-out", file=out)
    return span <= MAX_PUSH_SPAN_LIMIT_MS and not timed_out


def report_icons(records, out):
    """Print the `icons` run's figures (task 0059): for each key, the time from
    the decoded glyph to the built frame, and the trace records the board lost.
    Return True when all six keys decoded and built a glyph and no trace record
    was dropped. The `six keys in` line is `blinksend`'s own.
    """
    decoded = {}
    built = {}
    for code, key, _, device_us in records:
        if code == CUSTOM_GLYPH_DECODED:
            decoded.setdefault(key, device_us)
        elif code == GLYPH_BUILT:
            built.setdefault(key, device_us)
    dropped = sum(1 for code, _, _, _ in records if code == TRACE_DROPPED)
    keys = sorted(set(decoded) & set(built))
    for key in keys:
        print("key {} compose {:.1f} ms".format(key, (built[key] - decoded[key]) / 1000), file=out)
    print("keys with a glyph {}/{}".format(len(keys), len(BATCH_KEYS)), file=out)
    print("TRACE_DROPPED records {}".format(dropped), file=out)
    return len(keys) == len(BATCH_KEYS) and dropped == 0


def report_batchloss(records, out):
    """Print the `batchloss` run's resend. A key state was dropped on purpose,
    so the board must ask for it with Batch incomplete (trace code 12), the host
    must send it again, and the board must release the batch before its 2 s
    time-out. Return True when it did, and within MAX_RESEND_RELEASE_MS of the
    last report that the first pass delivered.
    """
    window_start = batch_window_start(records)
    if window_start is None:
        print("no custom glyph in the trace", file=out)
        return False
    asks = [t for code, _, _, t in records if code == BATCH_INCOMPLETE and t >= window_start]
    timed_out = any(code == BATCH_TIMED_OUT for code, _, _, _ in records)
    if not asks:
        print("the board never asked for the missing key state", file=out)
        return False
    first_ask = asks[0]
    reports = [t for code, _, _, t in records if code == HOST_MESSAGE_DECODED and window_start <= t <= first_ask]
    pushes = [t for code, _, _, t in records if code == PUSH_STARTED and t > first_ask]
    if not reports or not pushes:
        print("no push after the board asked, or no report before it", file=out)
        return False
    released = (min(pushes) - max(reports)) / 1000
    print(
        "batch incomplete {}, released {:.0f} ms after the last report (limit {})".format(
            len(asks), released, MAX_RESEND_RELEASE_MS
        ),
        file=out,
    )
    if timed_out:
        print("the board released the batch after its time-out", file=out)
    return released <= MAX_RESEND_RELEASE_MS and not timed_out


def show_figures(records, keys=BATCH_KEYS):
    """Return `(six_keys_ms, push_span_ms)` for each Show frames the board
    applied to all six keys, in time order, or `None` for a Show whose pushes
    are not all in the trace.

    A Show's window runs from its FRAMES_SHOWN record to the next one.
    `six_keys_ms` is the time from the record to the last REFRESH_DONE of the
    keys, and `push_span_ms` the time between the first PUSH_STARTED of the
    earliest key and that of the latest.
    """
    shows = sorted(
        t for code, _, payload, t in records if code == FRAMES_SHOWN and payload == ALL_KEYS_MASK
    )
    figures = []
    for index, start in enumerate(shows):
        end = shows[index + 1] if index + 1 < len(shows) else float("inf")
        first = {}
        done = {}
        for code, key, _, t in records:
            if key not in keys or not start <= t < end:
                continue
            if code == PUSH_STARTED:
                first.setdefault(key, t)
            elif code == REFRESH_DONE:
                done[key] = max(done.get(key, t), t)
        if set(first) != set(keys) or set(done) != set(keys):
            figures.append(None)
            continue
        figures.append(
            ((max(done.values()) - start) / 1000, (max(first.values()) - min(first.values())) / 1000)
        )
    return figures


def report_cached(records, out):
    """Print the `cached` run's `six keys in` and `push span`, the worst of
    its Shows. Return True when both are within their limits and every Show
    has all its pushes in the trace."""
    figures = show_figures(records)
    if not figures:
        print("no Show frames of six keys in the trace", file=out)
        return False
    complete = [figure for figure in figures if figure is not None]
    print("shows {}".format(len(figures)), file=out)
    if len(complete) != len(figures):
        print("{} of the Shows have no push of some key in the trace".format(len(figures) - len(complete)), file=out)
    if not complete:
        return False
    six_keys = max(figure[0] for figure in complete)
    span = max(figure[1] for figure in complete)
    print("six keys in {:.1f} ms (limit {})".format(six_keys, MAX_SHOW_LIMIT_MS), file=out)
    print("push span {:.1f} ms (limit {})".format(span, MAX_PUSH_SPAN_LIMIT_MS), file=out)
    return (
        len(complete) == len(figures)
        and six_keys <= MAX_SHOW_LIMIT_MS
        and span <= MAX_PUSH_SPAN_LIMIT_MS
    )


def report_sync(records, out):
    """Print the `sync` run's figures. Return True when all three meet their
    limits."""
    window_start = sync_window_start(records)
    if window_start is None:
        print("no update to key {} in the trace".format(UPDATED_KEY), file=out)
        return False
    skews = slot_skews_ms(records, window_start)
    spans = group_spans_ms(records, window_start)
    gaps = blink_gaps_ms(records, keys=SYNC_KEYS, window_start=window_start)
    if not skews or not spans or not gaps:
        print("no blink pushes in the trace", file=out)
        return False
    max_skew, max_span, max_gap = max(skews), max(spans), max(gaps)
    print("slots {}".format(len(skews)), file=out)
    print("max skew {:.1f} ms (limit {})".format(max_skew, MAX_SKEW_LIMIT_MS), file=out)
    print("max span {:.1f} ms (limit {})".format(max_span, MAX_SPAN_LIMIT_MS), file=out)
    print("max gap {:.1f} ms (limit {})".format(max_gap, MAX_GAP_LIMIT_MS), file=out)
    return (
        max_skew <= MAX_SKEW_LIMIT_MS
        and max_span <= MAX_SPAN_LIMIT_MS
        and max_gap <= MAX_GAP_LIMIT_MS
    )


def main(argv, out=sys.stdout):
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)

    install = sub.add_parser("install", help="write a tracing code.py to the board")
    install.add_argument("volume")

    report_parser = sub.add_parser("report", help="print a trace's figures")
    report_parser.add_argument("--scenario", choices=("single", "burst", "busyburst", "sync", "batch", "batchloss", "cached", "icons", "glyphs"), required=True)
    report_parser.add_argument("trace_file")

    args = parser.parse_args(argv)

    if args.command == "install":
        code = (_REPO_ROOT / "firmware" / "code.py").read_text()
        (Path(args.volume) / "code.py").write_text(traced_code(code))
        print("tracing code.py written to {}".format(args.volume), file=out)
        return 0

    with open(args.trace_file) as f:
        records = load_trace(f)
    return 0 if report(records, args.scenario, out) else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
