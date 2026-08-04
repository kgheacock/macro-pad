# test

Test suites for the macro pad.

**Status: not yet defined.** No test strategy or framework has been chosen
yet. This folder is a placeholder.

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
