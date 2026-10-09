"""Fake `rp2pio` module. See ../README.md for how these stubs are used.

It implements what `firmware/pio_spi.py` calls on `StateMachine`. A
`background_write` starts a transfer that stays in flight until a test calls
`finish`, or until it has been polled `polls_to_finish` times through
`writing`. `writes` keeps every buffer the state machine was given, as the
same object, so a test can tell a buffer that was copied apart from one that
was passed on.

`clear_txstall` clears the stall flag. The state machine stalls again
`stall_polls` reads of `txstall` after the transfer ends, which stands for the
last bytes still shifting out. An idle state machine is stalled, so the flag
is set at once when no transfer is in flight and no bytes are left. A
`background_write` does not clear the flag: it stays set from the idle time
before, until a test or `PioBus.done` clears it.

`created` lists every `StateMachine` built since the last `reset`, in order, so
a test can see which were built first and which were freed (`deinitialized`).
A test makes a follower lose sync in one of two ways, after the transfer
starts: `polls_to_finish = 10**9` keeps `writing` true (its DMA still runs), and
`never_stalls = True` keeps `txstall` false (it waits for a clock edge).
"""

created = []


def reset():
    del created[:]


class StateMachine:
    def __init__(self, program, frequency, **kwargs):
        self.program = bytes(memoryview(program))
        self.frequency = frequency
        self.kwargs = kwargs
        self.writes = []
        self.stall_polls = 0
        self.polls_to_finish = 0
        self._stalled = True  # idle
        self._drain_left = 0
        self._in_flight = False
        self._polls = 0
        self.never_stalls = False
        self.deinitialized = False
        self.stops = 0
        created.append(self)

    def background_write(self, once=None, **kwargs):
        assert not self._in_flight, "a background write is already running"
        self.writes.append(once)
        self._in_flight = True
        self._polls = 0

    @property
    def writing(self):
        if self._in_flight and self._polls >= self.polls_to_finish:
            self._end_transfer()
        self._polls += 1
        return self._in_flight

    def _end_transfer(self):
        self._in_flight = False
        self._drain_left = self.stall_polls

    def stop_background_write(self):
        self.stops += 1
        self._in_flight = False
        self._drain_left = 0

    def deinit(self):
        assert not self.deinitialized, "deinit twice"
        self.deinitialized = True

    def clear_txstall(self):
        self._stalled = False

    @property
    def txstall(self):
        if self.never_stalls:
            return False
        if not self._in_flight:
            if self._drain_left > 0:
                self._drain_left -= 1
            if self._drain_left == 0 and not self._stalled:
                self._stalled = True
        return self._stalled

    def finish(self):
        if self._in_flight:
            self._end_transfer()
