"""A preallocated ring buffer of trace records, drained to the host.

`Tracer` exists so a person debugging the board can see what the firmware
saw at points that never cross the wire on their own — a rejected bounce,
a render that never ran. `record` costs no allocation once a `Tracer` is
constructed, so leaving tracing enabled does not add a GC pause to the
loop that task 0022 measures; leaving it disabled costs one attribute
read and an early return.

See tasks/ongoing/0025-trace-ring-buffer-flight-recorder.md for the design
decision, and docs/wire-protocol.md's "Trace record" section for the
12-byte layout this module writes and `driver/recorder` reads.
"""

RECORD_SIZE = 12  # code (1) + key (1) + payload (2) + timestamp (8)

# Trace code registry — mirrors docs/wire-protocol.md's "Trace record"
# section. TRACE_DROPPED is emitted by `drain`, not by `record`; the other
# four mark the points `firmware/app.py`'s `MacroPad.step` records at.
TRACE_DROPPED = 0
HOST_MESSAGE_DECODED = 1
SWITCH_READ = 2
DEBOUNCE_VERDICT = 3
EVENT_WRITTEN = 4


class Tracer:
    """A fixed-capacity ring buffer of 12-byte trace records.

    `record` writes a fixed-width record by index into one `bytearray`
    allocated at construction. A full buffer overwrites its oldest record
    and counts it as dropped, rather than blocking or growing, so an
    unattached host never stalls the loop. `drain` hands every held
    record to `write`, oldest first, preceded by a `TRACE_DROPPED` record
    when any were lost since the last drain.
    """

    def __init__(self, capacity, enabled=False):
        self.capacity = capacity
        self.enabled = enabled
        self.dropped = 0
        self._buffer = bytearray(capacity * RECORD_SIZE)
        self._head = 0  # index of the oldest held record
        self._count = 0  # number of held records, at most capacity

    def record(self, code, key, payload, now_us):
        """Write one record. No-op, and no allocation, when disabled."""
        if not self.enabled:
            return

        write_index = (self._head + self._count) % self.capacity
        self._write_record(write_index, code, key, payload, now_us)

        if self._count < self.capacity:
            self._count += 1
        else:
            # The buffer was already full, so this write just overwrote
            # the oldest record. The next-oldest is now one slot forward.
            self._head = (self._head + 1) % self.capacity
            self.dropped += 1

    def drain(self, write):
        """Hand every held record to `write(record_bytes)`, oldest first.

        Emits a `TRACE_DROPPED` record carrying the drop count first, but
        only when the count is nonzero — a `Tracer` that dropped nothing
        drains exactly the records it holds, no more.
        """
        if self.dropped:
            dropped_record = bytearray(RECORD_SIZE)
            self._encode(dropped_record, TRACE_DROPPED, 0, self.dropped, 0)
            write(bytes(dropped_record))
            self.dropped = 0

        for offset in range(self._count):
            index = (self._head + offset) % self.capacity
            start = index * RECORD_SIZE
            write(bytes(self._buffer[start : start + RECORD_SIZE]))

        self._head = 0
        self._count = 0

    def _write_record(self, index, code, key, payload, now_us):
        start = index * RECORD_SIZE
        self._encode(self._buffer, code, key, payload, now_us, offset=start)

    @staticmethod
    def _encode(buffer, code, key, payload, now_us, offset=0):
        # Packed byte by byte, not via `int.to_bytes`, which allocates a
        # new `bytes` object on every call — cheap under CPython's
        # refcounting, but real allocation pressure on CircuitPython's
        # mark-and-sweep GC, the exact cost this module exists to avoid.
        buffer[offset] = code
        buffer[offset + 1] = key
        buffer[offset + 2] = payload & 0xFF
        buffer[offset + 3] = (payload >> 8) & 0xFF
        buffer[offset + 4] = now_us & 0xFF
        buffer[offset + 5] = (now_us >> 8) & 0xFF
        buffer[offset + 6] = (now_us >> 16) & 0xFF
        buffer[offset + 7] = (now_us >> 24) & 0xFF
        buffer[offset + 8] = (now_us >> 32) & 0xFF
        buffer[offset + 9] = (now_us >> 40) & 0xFF
        buffer[offset + 10] = (now_us >> 48) & 0xFF
        buffer[offset + 11] = (now_us >> 56) & 0xFF
