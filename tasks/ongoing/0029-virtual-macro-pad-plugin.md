---
id: "0029"
title: "Virtual macro pad: browser plugin for a keyless, emulator-backed macropadd"
status: "ongoing"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/27"
branch: "0029-virtual-macro-pad-plugin"
related: ["0028", "0013", "0020"]
tags: ["driver", "plugin", "emulator", "ui"]
---

# 0029 — Virtual macro pad: browser plugin for a keyless, emulator-backed macropadd

## Problem

The board's controller is attached, but no keys are wired. `driver/e2e`
cannot exercise a real press. Per-key rendering (glyph, color, blink) is
also unverified, because it lives on the unwired keys. Nobody can exercise
the press-event-render loop, or show the pad in use, before wiring
finishes.

## Goals

- A person can press a virtual key in a browser tab and see the resulting
  `event` and any `setKeyState` reply, with no board attached.
- The virtual pad is a plugin: an ordinary WebSocket client of
  `driver/plugin`, not a separate tool.
- The same plugin code that reacts to the virtual pad also works, unchanged,
  against a real board once keys are wired.

## Non-goals

- Click-pattern resolution (single, double, long press). That is task 0013's
  job, and out of scope for `driver/plugin` per its README.
- Injecting audio chunks or trace records. Only key press and release.
- A production-grade UI framework. A static page with no build step is
  enough.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — In-band `injectEvent` message, gated by an explicit Injector

Add `KindInjectEvent` to `driver/plugin`'s existing protocol and server.
`plugin.NewServer` takes a new, optional `Injector` argument
(`InjectEvent(transport.Event) error`, a shape `*transport.Emulator` already
implements). `macropadd` gains an `--emulate` flag. When set, it opens
`transport.NewEmulator()` instead of `transport.Open`, and passes that
emulator as the server's `Injector`. A real-hardware run passes `nil`, so
the server drops any `injectEvent` message it receives.

- Good, because a plugin written against the virtual pad speaks the same
  protocol a real-hardware plugin speaks: the same `event` and
  `setKeyState` messages, plus one added kind. Nothing to port later.
- Good, because the gate is a nil check on a typed field, set once in
  `main.go`. It is not a runtime type check on `transport.Transport`,
  scattered through the server. A real `Device` run never receives an
  `Injector`.
- Bad, because `driver/plugin`'s protocol is the most stable, documented
  surface in the daemon. It now carries one message kind that only means
  something in emulator mode.
- Bad, because every real-hardware `plugin.Server` still allocates the
  `injectEvent` read path, even though it always drops the message.

### Approach B — A second, parallel server for virtual mode

Leave `driver/plugin`'s protocol and `Server` untouched. Add a distinct
binary or mode instead — for example, `macropadd --virtual` dispatches to a
new `plugin/virtual` package. This package runs its own small WebSocket
server, built directly on `driver/e2e.Pad` and `transport.Emulator`. It
exposes the same JSON shapes, plus an inject message.

- Good, because the real `plugin.Server` and protocol never change, so
  real-hardware behavior carries zero risk from this work.
- Good, because `driver/e2e.Pad` already tracks each key's glyph and color
  (`driver/e2e/pad.go`). The virtual server can reuse this directly,
  instead of deriving key state again from raw `setKeyState` traffic.
- Bad, because two servers now implement overlapping message shapes from
  one hand-maintained copy each. A protocol change to one is easy to
  forget in the other.
- Bad, because a plugin author who builds against the virtual server does
  not provably build against the same server a real board uses. "Durable"
  weakens to "parallel."

### Approach C — HTTP side-channel for injection, WebSocket unchanged for everything else

Keep `driver/plugin`'s WebSocket protocol exactly as it is today. Add a
separate, clearly labeled HTTP endpoint (`POST /debug/inject`) to
`macropadd`, active only in `--emulate` mode, that calls
`transport.Emulator.InjectEvent` directly. The browser UI becomes a
WebSocket client for `event`/`setKeyState`, plus a plain `fetch` call for
injection.

- Good, because `driver/plugin`'s protocol package gains nothing new. A
  reader of `protocol.go` sees only the two message kinds that exist
  today.
- Good, because an HTTP debug endpoint is a familiar shape for manual
  tests. A person can call it with `curl`, with no JavaScript required.
- Bad, because the virtual pad UI now speaks two different wire protocols
  for one small tool: a WebSocket connection, and a separate HTTP call.
  Approach A needs only one.
- Bad, because `macropadd` grows an HTTP surface it does not otherwise
  have, next to its WebSocket one, for a single debug-only route.

## Decision

Chosen: **Approach A — in-band `injectEvent`, gated by an explicit
Injector**.

The stated goal is a plugin that ports to real hardware unchanged. This is
the same reason task 0013 dropped its in-process handler registry for a
WebSocket `signal` broadcast. Approach A is the only option where the
virtual pad and a real-hardware plugin use the same client and the same
server, byte for byte. The cost accepted is a protocol message that only
means something in one of the daemon's two modes.

## Design

Files to change:

- `driver/plugin/protocol.go` — add `KindInjectEvent` and
  `InjectEventPayload{KeyIndex byte; Type string}` (mirrors `EventPayload`
  minus `Timestamp`, which the server fills from its own clock).
- `driver/plugin/server.go` — add an `Injector` interface
  (`InjectEvent(transport.Event) error`). `NewServer` takes one, nilable.
  `readPump` calls it on an `injectEvent` message when the field is not
  nil, and drops the message otherwise.
- `driver/cmd/macropadd/main.go` — add an `--emulate` flag. When set, open
  `transport.NewEmulator()` in place of `transport.Open`, and pass it to
  `plugin.NewServer` as the `Injector`.
- New static file, `driver/plugin/web/virtualpad.html` — one page, no
  build step, with six key tiles. Each tile renders the last
  `setKeyState`/`event` seen for that index, and is clickable to send an
  `injectEvent` press, then a release.

## Definition of done

- [x] **DoD-1** — `macropadd --emulate` runs with no board attached and
  accepts WebSocket connections. **Proof:** `go run ./driver/cmd/macropadd
  --emulate` prints `macropadd: listening on 127.0.0.1:8765` with no USB
  device present.
- [x] **DoD-2** — A click on a virtual key sends `injectEvent`, and every
  connected client receives the resulting `event` message. **Proof:**
  manual run: open `virtualpad.html` in two tabs, click a key in one tab,
  see the `event` logged in both tabs' consoles.
- [x] **DoD-3** — An `injectEvent` message sent to a `plugin.Server` with a
  `nil` `Injector` is dropped, not applied. **Proof:**
  `go test ./driver/plugin/... -run TestServer_InjectEvent_NilInjector`
- [x] **DoD-4** — A `setKeyState` message sent by any client updates the
  matching tile's color, glyph, and blink state in the browser. **Proof:**
  manual run: send `setKeyState` from a second WebSocket client (for
  example `wscat`), see the tile update in `virtualpad.html`.
- [x] **DoD-5** — Tests cover the new `Injector` gate and the `injectEvent`
  decoding. **Proof:** `go test ./driver/plugin/...` passes. `git stash &&
  go test ./driver/plugin/... -run InjectEvent` fails on `main`.
- [x] **DoD-6** — `driver/README.md`'s "Plugin API" section documents
  `injectEvent` and `--emulate`, and states that real-hardware runs drop
  the message. **Proof:** `driver/README.md`, "Plugin API" section.
- [x] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:**
  PR body

## Risks

- If a future change misconfigures the real-hardware `Injector`, a plugin
  can spoof a press. The type system closes this risk: `Injector` is a
  distinct type that only `transport.Emulator` satisfies.
  `transport.Device` never implements `InjectEvent`, so the mistake cannot
  compile.
- `virtualpad.html` can drift from the protocol as `driver/plugin` evolves,
  for example when task 0013 adds its `signal` kind. To limit this, the
  page reads `Message.Kind` generically, and ignores any kind it does not
  render, so an unknown kind does not break the page.

## Open questions

- [ ] Does `virtualpad.html` ship inside the `driver/plugin` package
  (`go:embed`), or stay a standalone file a person opens directly? The
  owner decides this during implementation. It affects whether `macropadd`
  serves the page over HTTP, or a person opens it with `file://`.

## Notes

This spec follows a brainstorm that compared this approach (a durable
plugin) against a standalone `driver/e2e`-only dev tool (a `WebOperator`
that bypasses `driver/plugin` entirely). The team rejected the standalone
tool: only the e2e harness itself can use it, not a third-party plugin
author.
