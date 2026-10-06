#!/usr/bin/env python3
"""Install a tracing `code.py` on the board, and read the trace of a blink run.

`make blink-trace` runs `install`, sends a scripted run with
`driver/cmd/blinksend`, then runs `report` on the JSONL file that command
wrote. See tasks/ongoing/0044-blink-independence-persist-off-blink-path.md.

    blink_trace.py install CIRCUITPY_VOLUME
    blink_trace.py report --scenario single|burst TRACE_FILE

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
REFRESH_DONE = 8

# DoD-5: the largest allowed gap, in milliseconds, between two REFRESH_DONE
# records of one blinking key.
BLINKING_KEYS = (0, 1, 2)
UPDATED_KEY = 4
MAX_GAP_LIMIT_MS = 650

# DoD-6: how many updates the burst sends, one to each key from 0.
BURST_UPDATES = 6

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


def blink_gaps_ms(records):
    """Return every gap, in ms, between two REFRESH_DONE records of one
    blinking key, for the gaps that end after the first update to the
    updated key — the run's window starts there.
    """
    window_start = next(
        (t for code, key, _, t in records if code == HOST_MESSAGE_DECODED and key == UPDATED_KEY),
        None,
    )
    if window_start is None:
        return []

    gaps = []
    last = {}
    for code, key, _, t in records:
        if code != REFRESH_DONE or key not in BLINKING_KEYS:
            continue
        if key in last and t >= window_start:
            gaps.append((t - last[key]) / 1000)
        last[key] = t
    return gaps


def decoded_keys(records):
    """Return the key indexes that have a HOST_MESSAGE_DECODED record, and
    how many such records there are.

    The board can decode one report twice: a burst of 6 reports gave 7
    records on 2026-10-06, with key 5 twice. A repeat is harmless, since a
    key state is idempotent, so a report is delivered when its key appears.
    """
    keys = [key for code, key, _, _ in records if code == HOST_MESSAGE_DECODED]
    return set(keys), len(keys)


def report(records, scenario, out):
    """Print the run's figures to `out`. Return True when they meet the limits."""
    if scenario == "burst":
        keys, records_seen = decoded_keys(records)
        print("decoded {}/{}".format(len(keys), BURST_UPDATES), file=out)
        if records_seen != len(keys):
            print("({} records: a report was decoded more than once)".format(records_seen), file=out)
        return len(keys) == BURST_UPDATES

    gaps = blink_gaps_ms(records)
    if not gaps:
        print("no blink gaps in the trace", file=out)
        return False
    max_gap = max(gaps)
    print("max gap {:.1f} ms (limit {})".format(max_gap, MAX_GAP_LIMIT_MS), file=out)
    return max_gap <= MAX_GAP_LIMIT_MS


def main(argv, out=sys.stdout):
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)

    install = sub.add_parser("install", help="write a tracing code.py to the board")
    install.add_argument("volume")

    report_parser = sub.add_parser("report", help="print a trace's figures")
    report_parser.add_argument("--scenario", choices=("single", "burst"), required=True)
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
