# test

Test suites for the macro pad.

**Framework:** [pytest](https://docs.pytest.org/), run from the repo root:

```bash
python3 -m venv .venv
.venv/bin/pip install pytest
.venv/bin/pytest
```

## CircuitPython hardware mocks

Firmware imports CircuitPython-only modules (`board`, `digitalio`,
`usb_hid`, `usb_cdc`, `audiobusio`, `displayio`) that do not exist on a dev
machine. Hand-written fakes for these live in [`stubs/`](stubs/), one file
per module. `conftest.py` puts `stubs/` on `sys.path` before any test
collects, so firmware code can `import board` (etc.) unmodified in a test.

Each stub implements only the classes and methods firmware code actually
calls — see [`tasks/ongoing/0001-test-harness-circuitpython-mocks.md`](
../tasks/ongoing/0001-test-harness-circuitpython-mocks.md) for the design
decision behind hand-writing them instead of adopting Adafruit-Blinka.
When firmware code starts calling a stub method that doesn't exist yet, add
it to the relevant file in `stubs/`.

## Adding a test

Put firmware unit tests in `test/`, named `test_*.py`. Import the
CircuitPython modules you need directly (`import board`) — `conftest.py`
makes the stubs resolve automatically.

## Scope

Expected to cover, once populated:

- Firmware behavior that does not need the physical board (event
  debouncing logic, audio chunk framing, idle-timeout logic).
- Driver logic (press-timing resolution, action mapping, audio
  reassembly).

## Out of scope

- Manual, on-hardware validation (soldering, wiring, SKU confirmation).
  That belongs with the build steps in
  [`hardware/README.md`](../hardware/README.md).
