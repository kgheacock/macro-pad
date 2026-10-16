---
id: "0055"
title: "Run every Go plugin in one plugin host with a shared helper API"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/63"
branch: "claude/project-thread-trlli9"
related: ["0013", "0028", "0030", "0053", "0054", "0057"]
tags: ["driver", "plugin", "api", "macos"]
---

# 0055 — Run every Go plugin in one plugin host with a shared helper API

## Problem

Each plugin is its own binary with its own `main`. Each one parses
`--addr`, dials the daemon, runs a `ReadMessage` loop, and filters by
`Kind` and `KeyIndex` by hand. The four plugins so far
(`claude-status`, `window-switcher` in PR #60, `selection-capture` in
PR #61, `windowpreview` in PR #62) repeat the same work:

- a dial with no reconnect, so a daemon restart kills every plugin;
- a press, release, or signal filter for "my keys";
- a hex-to-RGB565 parser and a `dim` function (`window-switcher`);
- PNG scaling and encoding to 128x128 (`windowpreview`);
- an `osascript` runner (`window-switcher`) and cgo calls into
  Accessibility and ScreenCaptureKit (`selection-capture`,
  `windowpreview`);
- a fake connection interface for tests.

Nothing loads them together. A person starts each binary by hand, in
its own terminal.

Nothing decides which plugin owns which key. The pad has six keys. Two
plugins that write the same key fight, and the last write wins. With
more plugins than keys, there is no way to open a second set of keys.
Task 0057 now gives the daemon a layer tree for this. The host must use
it and must not add a second one.

Plugins cannot call each other. `windowpreview` exposes
`SnapshotWindow` for a switcher to call just before it hides a window,
but `window-switcher` runs in another process and cannot call it.

macOS grants Accessibility and Screen Recording per binary. Each new
plugin binary needs its own grant in System Settings, and a rebuild can
drop it.

## Goals

- One command starts every configured plugin.
- One config file says which plugin runs on which top-level keys of
  the layer tree (task 0057).
- A plugin is a Go type with a small interface. It writes no `main`,
  no flag parsing, no dial, and no read loop.
- The host gives plugins helpers for the work they repeat today: key
  output, input routing, colors, images, and macOS calls.
- A plugin can only write to its own keys, and only gets input from
  its own keys.
- More plugins than keys fit, through the child layers of task 0057.
  A key opens up to five children. The daemon changes layers.
- One plugin's panic, error, or slow handler does not stop the others.
- A daemon restart does not stop the host. It reconnects, and the
  daemon still holds the state it saved (task 0057).
- Plugins can publish to and subscribe to each other in process.
- A plugin's logic is testable with a fake host, with no WebSocket.

## Non-goals

- Changes to the daemon or the wire protocol. The host is one more
  WebSocket client of `macropadd`.
- Plugins in other languages. They keep using the WebSocket protocol
  directly (task 0028). The host does not start or supervise them; a
  later task can add that.
- Loading plugins at run time (Go `plugin` package, WASM, scripts).
  Plugins are compiled into the host binary.
- Starting the host at login (launchd). A later task can add a plist.
- Pages, a page switcher, or a second copy of key state. Task 0057
  owns the layer tree, the active layer, navigation, and saving. The
  host is a client of it.
- Porting plugins still in open PRs. Each one adopts the host in its
  own PR, or in a follow-up once merged.

## Approaches considered

### Approach A — Compile plugins into `macropadd`

The daemon imports each plugin package and runs it in process, next to
the transport and the WebSocket server.

- Good, because there is one process and one macOS permission grant.
- Good, because plugins call the transport with no JSON hop.
- Bad, because a plugin panic, or a cgo crash in Accessibility code,
  kills the process that owns the board.
- Bad, because the daemon gains cgo and macOS frameworks. Today it
  builds and runs the same on Linux and macOS.
- Bad, because a change to any plugin restarts the daemon.

### Approach B — Keep one binary per plugin, add an SDK and a launcher

Add `api.Run(plugin)` to `driver/api`. It parses common flags, dials
with reconnect, and calls typed handlers. A launcher reads a config
file and starts each listed binary, restarting it on exit.

- Good, because plugins stay isolated. A crash restarts one plugin.
- Good, because the launcher can start a Python or Node plugin too.
- Bad, because every binary still needs its own macOS permission
  grant, and the grant list grows with every plugin.
- Bad, because plugins still cannot call each other. A switcher that
  wants a preview snapshot must go through a WebSocket signal and a
  race.
- Bad, because key ownership lives in the launcher's config, but each
  process talks to the daemon directly. Nothing enforces it.

### Approach C — One plugin host process with in-process Go plugins

A new binary, `macropad-plugins`, imports every plugin package. It
holds one WebSocket connection to `macropadd`, reads one config file,
and starts one instance per entry. Each instance gets a `Host` handle:
its keys, its config, and the helpers. The host routes input to the
owner of each key, and drops writes to keys a plugin does not own.
State lives in the daemon's layer tree (task 0057), not in the host.

- Good, because the daemon stays unchanged and isolated from plugin
  crashes.
- Good, because there is one binary to grant Accessibility and Screen
  Recording to, once.
- Good, because plugins call each other through an in-process bus, with
  no wire hop.
- Good, because the host sees every write, so it can enforce key
  ownership.
- Bad, because all Go plugins share one process. A panic in a goroutine
  the plugin starts itself cannot be recovered by the host, and kills
  every plugin.
- Bad, because adding a plugin means one import line and a rebuild of
  the host.
- Bad, because the host repeats some routing work the daemon does,
  such as finding which node a press hit.

## Decision

Chosen: **Approach C — one plugin host process with in-process Go
plugins**.

The plugins on the roadmap need Accessibility and Screen Recording, and
want to call each other. Only C gives one permission grant and direct
calls without putting cgo in the daemon. The task accepts the shared
failure domain: the host recovers panics in every call it makes into a
plugin, and gives plugins `h.Go` to start goroutines that recover too.
A plugin that starts a bare goroutine and panics takes the host down;
the README says so.

## Design

### Layout

- `driver/host` — the `Plugin` interface, the registry, the router,
  the layer client, the bus, and the `Host` handle.
- `driver/host/hosttest` — a fake `Host` for plugin tests.
- `driver/host/keyimg` — color and image helpers (pure Go, any OS).
- `driver/host/macos` — macOS helpers behind interfaces, with a
  `darwin` cgo file and a stub for other OSes.
- `driver/plugins/<name>` — each plugin as a package, not a `main`.
- `driver/cmd/macropad-plugins` — the host binary. `plugins.go` lists
  every plugin package in one slice.

### Plugin interface

```go
package host

type Plugin interface {
	// Start runs once. The plugin registers handlers and starts any
	// work on h. Returning an error disables this instance only.
	Start(ctx context.Context, h Host) error
}

// Factory builds one instance. The registry maps a name, such as
// "window-switcher", to a Factory.
type Factory struct {
	Name string
	New  func() Plugin
}
```

Config for an instance is decoded into the plugin's own struct with
`h.Config(&cfg)`, which returns a decode error naming the instance and
the field.

### Host handle

```go
type Host interface {
	ID() string     // instance id from the config
	Slots() []int   // top-level keys (0..5) this instance owns
	Config(v any) error
	Log(format string, args ...any) // prefixed with the instance id

	// Output, to one owned top-level key. A slot the instance does not
	// own returns ErrNotOwner and sends nothing.
	Key(i int) Key

	// Input, for the nodes this instance owns. A node is one owned
	// top-level key, or one child of it.
	OnPress(fn func(n Node))
	OnRelease(fn func(n Node))
	OnClick(kind ClickKind, fn func(n Node)) // Single, Double, Long
	OnSignal(name string, fn func(n Node, s plugin.SignalPayload))

	// Layers (task 0057). Visible is true while an owned node is on screen.
	Visible() bool
	OnVisible(fn func(visible bool))

	// Cross-plugin bus, in process only.
	Publish(topic string, v any)
	Subscribe(topic string, fn func(v any))

	// Goroutines that recover a panic and log it, and stop with ctx.
	Go(fn func(ctx context.Context))
	Every(d time.Duration, fn func())

	// An error when the host binary lacks a macOS grant. Call in Start.
	Require(p macos.Permission) error
}

// Node names one owned key. Child is -1 for the top-level key itself,
// and 0..4 for one of its children.
type Node struct{ Slot, Child int }

type Key interface {
	Child(n int) Key                    // n in 0..4, on a top-level Key only
	SetStatus(s Status)                 // RUNNING, DONE, ERROR, WARN, or none
	SetCount(n int)
	SetIcon(id byte)                    // built-in Emoji ID
	SetState(name string)               // Alert, Waiting, Done map to ERROR, WARN, DONE
	SetColor(c keyimg.Color)            // solid tile, rendered to a PNG
	SetImage(img image.Image) error     // scaled to 128x128, rendered to a PNG
	SetPNG(png []byte) error
	Clear()
}
```

### State

The host keeps no key state of its own. The daemon owns the layer tree,
the active layer, and the saving (task 0057). Each `Key` call becomes
one `setLayerKey` message that upserts a node. Each node `id` is the
instance id, a dot, and the slot, such as `desks.0`. A child adds a dot
and its number, such as `desks.0.2`.

`SetColor`, `SetImage`, and `SetPNG` write a PNG to
`assets/.generated/<node id>.png` and set `iconRef` to it, because a
node holds a file reference and not pixels. `SetIcon` sets `icon`.
`SetStatus` and `SetState` set `status`. The model has no blink, so
`SetState` drops blink (see Open questions).

At start, an instance sends one empty `setLayerKey` with its slot for
each slot it owns. This reserves the slot. A streamer that picks the
first free key then skips it.

`Key` calls never block on the network. The host queues them and keeps
only the newest values for each node, so a burst sends once. The daemon
draws only what is on screen.

### Routing and threading

The host reads every message from the daemon once. For a press,
release, or signal, it finds the node on that key in the active layer
and queues the handler call on the owner's own queue. In the top layer,
key N is slot N. In a child layer, keys 0, 1, 2, 3, and 5 are children
0 to 4 of the open slot, and key 4 is the daemon's up arrow, which has
no owner. The host learns the active layer from the `layerChanged`
message that task 0057 adds.

A single press on a node that has children opens its child layer. The
daemon handles it, and the plugin gets `OnVisible`, not the click.
Double and long presses go to the plugin.
Each instance has one goroutine that runs its handlers in order, so a
plugin needs no locks for its own state. A queue holds 64 calls; a full
queue drops the next call and logs it, matching the server's
drop-rather-than-block rule (task 0028). A 250 ms Accessibility call in
one plugin does not delay another.

Signals not tied to an owned key, such as a broadcast from a non-Go
plugin, go to every instance that subscribed to that name with
`OnSignal`.

### Config file

`macropad-plugins --config ~/.config/macropad/plugins.json`:

```json
{
  "addr": "127.0.0.1:8765",
  "plugins": [
    {"id": "desks",  "use": "window-switcher", "slots": [0, 1, 2, 3],
     "config": {"trigger": "press"}},
    {"id": "claude", "use": "claude-status",   "slots": [4]},
    {"id": "thumbs", "use": "windowpreview",   "slots": [5]}
  ]
}
```

Here `thumbs` shows up to five window previews as the children of key 5.

Validation at start, before any connection:

- every `use` names a registered plugin;
- every `id` is unique;
- every slot is in 0..5;
- no two instances claim the same slot.

A failure prints every problem found and exits with code 2.

`--list` prints every registered plugin with a one-line description.
`--check` validates the config and exits.

### Layers

The daemon changes layers, on a single press of a key with children or
of the up arrow (task 0057). It sends `layerChanged` with the new
active layer. The host then calls `OnVisible(true)` on instances whose
nodes came on screen, and `OnVisible(false)` on instances whose nodes
left. The daemon redraws the keys. The host sends nothing.

A hidden instance keeps running. `windowpreview` should stop capturing
while hidden; `Visible` and `OnVisible` let it.

### Reconnect

When the connection drops, the host redials with backoff from 250 ms
to 5 s, and logs once per state change, not once per try. While down,
the host keeps the newest values for each node. After reconnect, it
reserves its slots again, sends those values, and asks for the active
layer. It does not repaint other nodes. The daemon holds them.

### Helpers

`keyimg` (any OS):

- `Hex("#RRGGBB") (Color, error)`, `MustHex`, `Color.RGB565()`,
  `Color.Dim(f float64)`;
- `Palette` — six distinct default colors, one per key;
- `Thumbnail(img) *image.RGBA` — scale and letterbox to 128x128;
- `Stale(img)` — the dimmed, amber-strip look from task 0054;
- `Text(s string, fg, bg Color) image.Image` — one or two short lines
  drawn with a built-in font, for labels.

These move from `window-switcher` and `driver/preview`; those callers
switch to `keyimg`.

`macos` (each behind an interface so tests use fakes):

- `AppleScript(lines ...string) error` — the `osascript` runner;
- `SwitchDesktop(n int) error`, `ActivateApp(name string) error`,
  `RaiseWindow(app, titleSubstr string) error`;
- `Frontmost() (Window, error)` — app, title, window id;
- `SelectedText() (string, error)` — the Accessibility path of task
  0053;
- `Pasteboard` — `Save() Saved`, `Restore(Saved)`, `ChangeCount()`;
- `Permissions() PermissionState` — Accessibility and Screen Recording
  granted or not, from `AXIsProcessTrusted` and
  `CGPreflightScreenCaptureAccess`.

At start, the host calls `Permissions` once and logs one line per
missing grant, naming the host binary's path. A plugin that needs a
grant calls `h.Require(macos.Accessibility)` in `Start`, which returns
an error that disables only that instance.

Window capture stays in `driver/preview` for now. It moves into
`macos` when task 0054 merges.

### Bus topics

Topics are strings. The host documents the ones its plugins use, in
`driver/host/topics.go`:

- `window.willHide` — payload a window id. A switcher publishes it just
  before it raises another window; `windowpreview` snapshots it.
- `selection.captured` — payload text and source, from task 0053.

Delivery is on the subscriber's own queue, like any other handler.

### Testing

`hosttest.New(t, slots...)` returns a fake `Host`. A test sends input
with `h.Press(n)`, `h.Click(n, Double)`, and `h.Signal(n, name)`, moves
the active layer with `h.Layer(open)`, and reads output with `h.Node(n)`. Plugins drop their own fake connection
interfaces.

### Port

This task ports `claude-status` to `driver/plugins/claudestatus`. Any of `window-switcher`, `selection-capture`, and
`windowpreview` already on `main` when this task starts is ported too.
The old `driver/examples/claude-status` binary is removed, and the
README points to the host.

Files to change:

- `driver/host/*.go` — interface, registry, router, layer client, bus, reconnect
- `driver/host/hosttest/*.go` — the fake host
- `driver/host/keyimg/*.go` — color and image helpers
- `driver/host/macos/*_darwin.go`, `*_other.go` — macOS helpers and stubs
- `driver/plugins/claudestatus/*.go` — the ported plugin
- `driver/cmd/macropad-plugins/main.go`, `plugins.go` — the binary and the plugin list
- `driver/README.md` — "Plugin host" section: writing a plugin, config, layers, permissions

## Definition of done

An outside reviewer verifies each item without help from the
implementer. Each item names its proof. The task moves to `complete/`
only when every box is ticked.

- [ ] **DoD-1** — A press reaches only the instance that owns the node
  on that key in the active layer. A press on an unowned key, or on the
  up arrow of a child layer, reaches no one. **Proof:** `cd driver && go test ./host -run TestRouter_RoutesToOwner`
- [ ] **DoD-2** — `h.Key(i)` on a slot the instance does not own returns
  `ErrNotOwner` and sends no `setLayerKey` to the daemon. **Proof:**
  `go test ./host -run TestKey_RejectsNonOwner`
- [ ] **DoD-3** — A config with two instances on one slot, an unknown
  `use`, a duplicate `id`, and a slot of 7 exits with code 2 and
  names all four problems. **Proof:**
  `go test ./cmd/macropad-plugins -run TestConfig_ReportsEveryProblem`
- [ ] **DoD-4** — A `layerChanged` message calls `OnVisible(true)` on
  instances whose nodes come on screen, and `OnVisible(false)` on those
  that leave. Ten `Key` calls in one burst on one node send one
  `setLayerKey` with the newest values. **Proof:**
  `go test ./host -run 'TestLayerVisibility|TestKey_CoalescesBurst'`
- [ ] **DoD-5** — A handler that panics is logged with its instance id;
  the other instances keep receiving input, and the panicking instance
  receives its next event. **Proof:**
  `go test ./host -run TestRouter_RecoversPanic`
- [ ] **DoD-6** — A handler that blocks for 1 s does not delay another
  instance's handler by more than 50 ms. **Proof:**
  `go test ./host -run TestRouter_SlowPluginIsolated`
- [ ] **DoD-7** — After the fake daemon drops and accepts the
  connection again, the host reserves its slots again and sends the
  newest values of every node written while it was down. **Proof:**
  `go test ./host -run TestReconnect_Resends`
- [ ] **DoD-8** — A `Publish` reaches every subscriber in other
  instances, on each subscriber's own queue. **Proof:**
  `go test ./host -run TestBus`
- [ ] **DoD-9** — `keyimg.Hex`, `Dim`, `Thumbnail`, and `Text` are
  covered by tests, and `window-switcher` and `driver/preview` (if on
  `main`) use them instead of their own copies. **Proof:**
  `go test ./host/keyimg` and `grep -rn "func dim\|func parseHexColor" driver`
  returns nothing
- [ ] **DoD-10** — `claudestatus` passes its old test cases rewritten
  on `hosttest`, with no fake connection type of its own. **Proof:**
  `go test ./plugins/claudestatus`
- [ ] **DoD-11** — `macropad-plugins --list` prints every registered
  plugin, and `--check` validates the README's example config.
  **Proof:** the commands' output, pasted in the PR description
- [ ] **DoD-12** — On a Mac with a board or `macropadd --emulate` and
  the virtual pad, `claude-status` runs from one config: `macrodriver
  signal processWaiting` sets its node to `WARN` (yellow), and
  `processDone` sets it to `DONE`. **Proof:** manual run, logged in the
  PR description
- [ ] **DoD-13** — With Accessibility not granted, the host logs one
  line naming its binary path, and an instance that calls
  `h.Require(macos.Accessibility)` is disabled while the others run.
  **Proof:** manual run on macOS, logged in the PR description
- [ ] **DoD-14** — `driver/README.md` has a "Plugin host" section: a
  plugin in under 30 lines, the config format, layers, the bus, and the
  permission step. **Proof:** `driver/README.md`
- [ ] **DoD-15** — `cd driver && go build ./... && go vet ./...` exits 0
  on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-16** — The PR in the `pr` field links to this spec.
  **Proof:** PR body

## Risks

- A plugin's own goroutine panics → the whole host exits. `h.Go` and
  `h.Every` recover; the README tells plugin authors to use them. A
  launchd `KeepAlive` in a later task restarts the host.
- A client other than the host writes a key with `setKeyState`, which
  bypasses the layer tree → the daemon redraws only changed nodes, so
  that key stays until its node changes. The README tells such clients
  to use `setLayerKey` and a slot that the config leaves free.
- The host writes a PNG for each `SetColor` or image change → a flood
  of changes fills `assets/.generated`. The host names each file by
  node id, so one node keeps one file, and it clears the folder at start.
- A rebuilt host binary may lose its macOS grants → one grant to redo
  instead of one per plugin. The README shows how to sign the binary
  with a stable identity so the grant survives rebuilds.
- A layer change sends up to six key states, plus glyphs, in a burst →
  task 0057 covers this cost. The host sends nothing on a layer change.
- Per-instance queues of 64 drop input under a flood → a person cannot
  press keys that fast; a drop logs, so it is visible.

## Open questions

- [ ] The layer model has no blink, so `SetState(Alert)` loses its
  blink. Should task 0057 add a `blink` field? The default is no. —
  kgheacock
- [ ] Should a plugin open a layer itself, as `ShowPage` did? The
  default is no; only a press does. — kgheacock
- [ ] Should the host watch its config file and reload on change? The
  default is no; restart the host. — kgheacock
