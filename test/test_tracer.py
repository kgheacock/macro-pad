import tracemalloc

import tracer as tracer_module


class Recorder:
    """Collects the raw record bytes a `Tracer.drain` call hands it."""

    def __init__(self):
        self.records = []

    def __call__(self, record_bytes):
        self.records.append(bytes(record_bytes))


def _decode(record_bytes):
    code = record_bytes[0]
    key = record_bytes[1]
    payload = record_bytes[2] | (record_bytes[3] << 8)
    timestamp = int.from_bytes(record_bytes[4:12], "little")
    return code, key, payload, timestamp


def test_record_allocates_nothing():
    tracer = tracer_module.Tracer(capacity=8, enabled=True)
    buffer_id = id(tracer._buffer)
    buffer_len = len(tracer._buffer)

    # Warm up every code path `record` can take before measuring, so the
    # snapshot diff below reflects the 1000 calls it wraps, not one-time
    # setup cost (e.g. tracemalloc's own first-touch bookkeeping for each
    # line) that any single call would have paid too.
    for i in range(16):
        tracer.record(tracer_module.SWITCH_READ, i % 6, i % 64, i)

    tracemalloc.start()
    try:
        before = tracemalloc.take_snapshot()
        for i in range(1000):
            tracer.record(tracer_module.SWITCH_READ, i % 6, i % 64, i)
        after = tracemalloc.take_snapshot()
    finally:
        tracemalloc.stop()

    # The ring buffer itself is never replaced or resized — `record` only
    # ever writes into the bytearray `Tracer` allocated at construction.
    # This is the guarantee that matters for the loop's GC pressure.
    assert id(tracer._buffer) == buffer_id
    assert len(tracer._buffer) == buffer_len

    # CPython boxes every int outside -5..256, so even pure bit-shift
    # arithmetic on a growing `now_us` churns small, transient int
    # objects that pymalloc's arena allocator does not always hand
    # straight back to the OS between calls — real but bounded noise,
    # not a structure that grows with call count. 1000 calls into one
    # small ring buffer should settle far under a page of that noise.
    diff = after.compare_to(before, "lineno")
    growth = sum(stat.size_diff for stat in diff if stat.size_diff > 0)
    assert growth < 4096


def test_drop_oldest_counts():
    tracer = tracer_module.Tracer(capacity=4, enabled=True)

    for i in range(6):
        tracer.record(tracer_module.SWITCH_READ, i, i, i)

    assert tracer.dropped == 2

    recorder = Recorder()
    tracer.drain(recorder)

    assert len(recorder.records) == 5  # TRACE_DROPPED + 4 held records
    code, key, payload, _ = _decode(recorder.records[0])
    assert code == tracer_module.TRACE_DROPPED
    assert payload == 2

    held = [_decode(r) for r in recorder.records[1:]]
    assert [key for _, key, _, _ in held] == [2, 3, 4, 5]
    assert tracer.dropped == 0


def test_disabled():
    tracer = tracer_module.Tracer(capacity=4, enabled=False)

    for i in range(100):
        tracer.record(tracer_module.SWITCH_READ, i, i, i)

    recorder = Recorder()
    tracer.drain(recorder)

    assert recorder.records == []
    assert tracer.dropped == 0


def test_drain_with_no_drops_emits_no_dropped_record():
    tracer = tracer_module.Tracer(capacity=4, enabled=True)

    tracer.record(tracer_module.EVENT_WRITTEN, 1, 0, 500)

    recorder = Recorder()
    tracer.drain(recorder)

    assert len(recorder.records) == 1
    code, key, payload, timestamp = _decode(recorder.records[0])
    assert code == tracer_module.EVENT_WRITTEN
    assert (key, payload, timestamp) == (1, 0, 500)


def test_drain_resets_buffer():
    tracer = tracer_module.Tracer(capacity=4, enabled=True)
    tracer.record(tracer_module.SWITCH_READ, 0, 0, 0)

    recorder = Recorder()
    tracer.drain(recorder)
    tracer.drain(recorder)

    assert len(recorder.records) == 1
