"""Fake `rp2pio` module. See ../README.md for how these stubs are used.

It implements what `firmware/pio_spi.py` calls on `StateMachine`. A
`background_write` starts a transfer that stays in flight until a test calls
`finish`, or until it has been polled `polls_to_finish` times through
`writing`. `writes` keeps every buffer the state machine was given, as the
same object, so a test can tell a buffer that was copied apart from one that
was passed on.
"""


class StateMachine:
    def __init__(self, program, frequency, **kwargs):
        self.program = bytes(memoryview(program))
        self.frequency = frequency
        self.kwargs = kwargs
        self.writes = []
        self.tx_fifo = 0
        self.polls_to_finish = 0
        self._in_flight = False
        self._polls = 0

    def background_write(self, once=None, **kwargs):
        assert not self._in_flight, "a background write is already running"
        self.writes.append(once)
        self._in_flight = True
        self._polls = 0

    @property
    def writing(self):
        if self._in_flight and self._polls >= self.polls_to_finish:
            self._in_flight = False
        self._polls += 1
        return self._in_flight

    def finish(self):
        self._in_flight = False
