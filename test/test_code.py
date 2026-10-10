"""Runs firmware/code.py against the stubs, with `MacroPad.run` replaced, to
check the order of its start-up.

The loop itself is tested in test_app.py. This file tests what only code.py
does: it builds the hardware objects, so no other test reaches it.
"""

import runpy
import sys
import types
from pathlib import Path

import pytest

import app
import memorymap
import pio_spi
import rp2pio

CODE_PY = Path(__file__).parent.parent / "firmware" / "code.py"


class _PWMOut:
    def __init__(self, pin, **kwargs):
        self.duty_cycle = 0


@pytest.fixture
def events(monkeypatch):
    rp2pio.reset()
    memorymap.reset()
    pwmio = types.ModuleType("pwmio")
    pwmio.PWMOut = _PWMOut
    monkeypatch.setitem(sys.modules, "pwmio", pwmio)

    log = []

    real_stop = pio_spi.stop_stale_state_machines
    real_bus = pio_spi.ParallelBus

    def stop():
        log.append("stop_stale")
        real_stop()

    class RecordingBus(real_bus):
        def __init__(self, *args, **kwargs):
            log.append("bus_built")
            super().__init__(*args, **kwargs)

        def deinit(self):
            log.append("bus_deinit")
            super().deinit()

    monkeypatch.setattr(pio_spi, "stop_stale_state_machines", stop)
    monkeypatch.setattr(pio_spi, "ParallelBus", RecordingBus)
    return log


def _run_code_py():
    runpy.run_path(str(CODE_PY), run_name="__main__")


def test_stale_state_machines_are_stopped_before_the_bus_is_built(events, monkeypatch):
    monkeypatch.setattr(app.MacroPad, "run", lambda self: events.append("run"))

    _run_code_py()

    assert events[:2] == ["stop_stale", "bus_built"]


def test_the_bus_is_not_freed_when_run_raises(events, monkeypatch):
    # Freeing the machines on the way out of a reload wedged a DMA channel on the
    # board (2026-10-09). The next run's `stop_stale_state_machines` does it.
    def run(self):
        events.append("run")
        raise KeyboardInterrupt

    monkeypatch.setattr(app.MacroPad, "run", run)

    with pytest.raises(KeyboardInterrupt):
        _run_code_py()

    assert "bus_deinit" not in events


def test_the_stale_enable_bits_are_gone_when_run_starts(events, monkeypatch):
    for address in (0x50200000, 0x50300000, 0x50400000):
        memorymap.registers[address] = 0b1111
    seen = []
    monkeypatch.setattr(
        app.MacroPad,
        "run",
        lambda self: seen.extend(memorymap.registers[a] & 0xF for a in (0x50200000, 0x50300000, 0x50400000)),
    )

    _run_code_py()

    assert seen == [0, 0, 0]
