---
id: "0055"
title: "Run every Go plugin in one plugin host with a shared helper API"
status: "ongoing"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-trlli9"
related: ["0013", "0028", "0030", "0051", "0053", "0054"]
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
more plugins than keys, there is no way to switch between sets of keys.

Plugins cannot call each other. `windowpreview` exposes
`SnapshotWindow` for a switcher to call just before it hides a window,
but `window-switcher` runs in another process and cannot call it.

macOS grants Accessibility and Screen Recording per binary. Each new
plugin binary needs its own grant in System Settings, and a rebuild can
drop it.

## Goals

- One command starts every configured plugin.
- One config file says which plugin runs on which keys, and on which
  page.
- A plugin is a Go type with a small interface. It writes no `main`,
  no flag parsing, no dial, and no read loop.
- The host gives plugins helpers for the work they repeat today: key
  output, input routing, colors, images, and macOS calls.
- A plugin can only write to its own keys, and only gets input from
  its own keys.
- More plugins than keys fit, through named pages of keys.
- One plugin's panic, error, or slow handler does not stop the others.
- A daemon restart does not stop the host. It reconnects and repaints.
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
- Merging the state database page tree (task 0051) with host pages.
  See Risks.
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

- Good, because the daemon stays unchanged and isolated from plugin
  crashes.
- Good, because there is one binary to grant Accessibility and Screen
  Recording to, once.
- Good, because plugins call each other through an in-process bus, with
  no wire hop.
- Good, because the host sees every write, so it can enforce key
  ownership and pages.
- Bad, because all Go plugins share one process. A panic in a goroutine
  the plugin starts itself cannot be recovered by the host, and kills
  every plugin.
- Bad, because adding a plugin means one import line and a rebuild of
  the host.
- Bad, because the host repeats some work the daemon does, such as
  caching the last state per key.

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
  pages, the bus, and the `Host` handle.
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
	Keys() []int    // physical key indexes this instance owns
	Config(v any) error
	Log(format string, args ...any) // prefixed with the instance id

	// Output, to one owned key. A key the instance does not own
	// returns ErrNotOwner and sends nothing.
	Key(i int) Key

	// Input, for owned keys on the active page only.
	OnPress(fn func(key int))
	OnRelease(fn func(key int))
	OnClick(kind ClickKind, fn func(key int)) // Single, Double, Long
	OnSignal(name string, fn func(key int, s plugin.SignalPayload))

	// Pages.
	Visible() bool
	OnVisible(fn func(visible bool))
	Page() string
	Pages() []string
	ShowPage(name string) error
	OnPageChange(fn func(page string)) // every instance, after any switch

	// Cross-plugin bus, in process only.
	Publish(topic string, v any)
	Subscribe(topic string, fn func(v any))

	// Goroutines that recover a panic and log it, and stop with ctx.
	Go(fn func(ctx context.Context))
	Every(d time.Duration, fn func())

	// An error when the host binary lacks a macOS grant. Call in Start.
	Require(p macos.Permission) error
}

type Key interface {
	SetColor(c keyimg.Color) error      // solid color, no glyph
	SetState(name string) error         // Alert, Waiting, Done (api.SetState)
	SetBlink(on bool) error
	SetImage(img image.Image) error     // scaled and encoded to 128x128 PNG
	SetPNG(png []byte) error
	Clear() error
}
```

`Key` calls never block on the network. The host keeps the last state
per key per instance. A call on a visible key also queues a send; a
call on a hidden key only updates the cache.

### Routing and threading

The host reads every message from the daemon once. For a press,
release, or signal, it finds the instance that owns that key on the
active page and queues the handler call on that instance's own queue.
Each instance has one goroutine that runs its handlers in order, so a
plugin needs no locks for its own state. A queue holds 64 calls; a full
queue drops the next call and logs it, matching the server's
drop-rather-than-block rule (task 0028). A 250 ms Accessibility call in
one plugin does not delay another.

Presses, releases, and click signals (`singlePress`, `doublePress`,
`longPress`) go only to the owner on the active page. Every other
signal, such as `processWaiting`, goes to every instance that owns the
key on any page, so a status plugin on a hidden page stays current and
shows the right state when its page comes back. A signal on a key no
instance owns, such as a broadcast from a non-Go plugin, goes to every
instance that subscribed to that name with `OnSignal`.

### Config file

`macropad-plugins --config ~/.config/macropad/plugins.json`:

```json
{
  "addr": "127.0.0.1:8765",
  "startPage": "work",
  "plugins": [
    {"id": "desks",   "use": "window-switcher", "page": "work", "keys": [0, 1, 2, 3],
     "config": {"trigger": "press"}},
    {"id": "claude",  "use": "claude-status",   "page": "work", "keys": [4]},
    {"id": "pager",   "use": "pager",           "page": "*",    "keys": [5],
     "config": {"pages": ["work", "previews"]}},
    {"id": "thumbs",  "use": "windowpreview",   "page": "previews", "keys": [0, 1, 2, 3, 4]}
  ]
}
```

Validation at start, before any connection:

- every `use` names a registered plugin;
- every `id` is unique;
- every key is in 0..5;
- no two instances on one page claim the same key, and a key on page
  `"*"` (every page) is not claimed by any other page;
- `startPage` names a page that exists.

A failure prints every problem found and exits with code 2.

`--list` prints every registered plugin with a one-line description.
`--check` validates the config and exits.

### Pages

A page is a name. An instance lives on one page, or on `"*"`. When the
page changes, the host:

1. calls `OnVisible(false)` on instances leaving view and
   `OnVisible(true)` on instances coming into view;
2. sends the cached state of each key of the new page; a key no
   instance owns on that page is cleared to color 0;
3. broadcasts a `signal` named `pageChanged` on key 0, so the virtual
   pad and other clients can see it.

The built-in `pager` plugin moves to the next page on a single press
of its key and shows the page's index as a glyph. `h.ShowPage` lets
any plugin switch page, for example a switcher that jumps to its
previews.

A hidden instance keeps running. `windowpreview` should stop
capturing while hidden; `Visible` and `OnVisible` let it.

### Reconnect

When the connection drops, the host redials with backoff from 250 ms
to 5 s, and logs once per state change, not once per try. While down,
`Key` calls only update the cache. After reconnect, it sends the
cached state of every visible key.

### Helpers

`keyimg` (any OS):

- `Hex("#RRGGBB") (Color, error)`, `MustHex`, `Color.RGB565()`,
  `Color.Dim(f float64)`;
- `Palette` — six distinct default colors, one per key;
- `Thumbnail(img) *image.RGBA` — scale and letterbox to 128x128;
- `Stale(img)` — the dimmed, amber-strip look from task 0054;
- `Text(s string, fg, bg Color) image.Image` — one or two short lines
  drawn with a built-in font, for labels such as a page name.

These move from `window-switcher` and `driver/preview`; those callers
switch to `keyimg`.

`macos` (each behind an interface so tests use fakes):

- `AppleScript(lines ...string) error` — the `osascript` runner;
- `SwitchDesktop(n int) error`, `ActivateApp(name string) error`,
  `RaiseWindow(app, titleSubstr string) error`;
- `Frontmost() (Window, error)` — app, title, window id;
- `SelectedText() (string, error)` — the Accessibility path of task
  0053;
- `Permissions() PermissionState` — Accessibility and Screen Recording
  granted or not, from `AXIsProcessTrusted` and
  `CGPreflightScreenCaptureAccess`.

At start, the host calls `Permissions` once and logs one line per
missing grant, naming the host binary's path. A plugin that needs a
grant calls `h.Require(macos.Accessibility)` in `Start`, which returns
an error that disables only that instance.

Window capture stays in `driver/preview` for now. It moves into
`macos` when task 0054 merges. Saving and restoring the pasteboard
(task 0053's Cmd+C fallback) joins `macos` with that task's plugin,
which is its only caller.

### Bus topics

Topics are strings. The host documents the ones its plugins use, in
`driver/host/topics.go`:

- `window.willHide` — payload a window id. A switcher publishes it just
  before it raises another window; `windowpreview` snapshots it.
- `selection.captured` — payload text and source, from task 0053.

Delivery is on the subscriber's own queue, like any other handler.

### Testing

`hosttest.New(t, keys...)` returns a fake `Host`. A test sends input
with `h.Press(k)`, `h.Click(k, Double)`, `h.Signal(k, name)`, and reads
output with `h.KeyState(k)`. Plugins drop their own fake connection
interfaces.

### Port

This task ports `claude-status` to `driver/plugins/claudestatus` and
adds `pager`. Any of `window-switcher`, `selection-capture`, and
`windowpreview` already on `main` when this task starts is ported too.
The old `driver/examples/claude-status` binary is removed, and the
README points to the host.

Files to change:

- `driver/host/*.go` — interface, registry, router, pages, bus, reconnect
- `driver/host/hosttest/*.go` — the fake host
- `driver/host/keyimg/*.go` — color and image helpers
- `driver/host/macos/*_darwin.go`, `*_other.go` — macOS helpers and stubs
- `driver/plugins/claudestatus/*.go` — the ported plugin
- `driver/plugins/pager/*.go` — the page switcher
- `driver/cmd/macropad-plugins/main.go`, `plugins.go` — the binary and the plugin list
- `driver/README.md` — "Plugin host" section: writing a plugin, config, pages, permissions

## Definition of done

An outside reviewer verifies each item without help from the
implementer. Each item names its proof. The task moves to `complete/`
only when every box is ticked.

- [ ] **DoD-1** — A press on a key reaches only the instance that owns
  it on the active page, and a press on an unowned key reaches no one.
  **Proof:** `cd driver && go test ./host -run TestRouter_RoutesToOwner`
- [ ] **DoD-2** — `h.Key(k)` on a key the instance does not own returns
  `ErrNotOwner` and sends nothing to the daemon. **Proof:**
  `go test ./host -run TestKey_RejectsNonOwner`
- [ ] **DoD-3** — A config with two instances on one key and page, an
  unknown `use`, a duplicate `id`, and a key of 7 exits with code 2 and
  names all four problems. **Proof:**
  `go test ./cmd/macropad-plugins -run TestConfig_ReportsEveryProblem`
- [ ] **DoD-4** — A page switch sends the cached state of every key on
  the new page, clears unowned keys, and calls `OnVisible` on both
  sides. A `Key` call on a hidden instance sends nothing until its page
  shows. **Proof:** `go test ./host -run TestPages`
- [ ] **DoD-5** — A handler that panics is logged with its instance id;
  the other instances keep receiving input, and the panicking instance
  receives its next event. **Proof:**
  `go test ./host -run TestRouter_RecoversPanic`
- [ ] **DoD-6** — A handler that blocks for 1 s does not delay another
  instance's handler by more than 50 ms. **Proof:**
  `go test ./host -run TestRouter_SlowPluginIsolated`
- [ ] **DoD-7** — After the fake daemon drops and accepts the
  connection again, the host resends the cached state of every visible
  key. **Proof:** `go test ./host -run TestReconnect_Repaints`
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
  the virtual pad, `claude-status` and `pager` run from one config: the
  pager key flips pages, and `macrodriver signal processWaiting` turns
  the Claude key amber only while its page shows. **Proof:** manual
  run, logged in the PR description
- [ ] **DoD-13** — With Accessibility not granted, the host logs one
  line naming its binary path, and an instance that calls
  `h.Require(macos.Accessibility)` is disabled while the others run.
  **Proof:** manual run on macOS, logged in the PR description
- [ ] **DoD-14** — `driver/README.md` has a "Plugin host" section: a
  plugin in under 30 lines, the config format, pages, the bus, and the
  permission step. **Proof:** `driver/README.md`
- [ ] **DoD-15** — `cd driver && go build ./... && go vet ./...` exits 0
  on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-16** — The PR in the `pr` field links to this spec.
  **Proof:** PR body

## Risks

- A plugin's own goroutine panics → the whole host exits. `h.Go` and
  `h.Every` recover; the README tells plugin authors to use them. A
  launchd `KeepAlive` in a later task restarts the host.
- The state database poller (task 0051) also writes keys 0–5 → the
  host and the poller fight, last write wins. The README says to run
  one or the other on a given key. A later task can make the poller a
  host plugin.
- Non-Go plugins write any key directly → the host cannot stop them.
  On its next page switch or reconnect, the host repaints only its own
  keys; the README says to leave keys free for them in the config.
- A rebuilt host binary may lose its macOS grants → one grant to redo
  instead of one per plugin. The README shows how to sign the binary
  with a stable identity so the grant survives rebuilds.
- A page switch sends up to six key states, plus glyphs, in a burst →
  task 0051 measured about 155 ms per write on the board, so a switch
  with glyphs may take over 1 s. The host sends color first, then
  glyphs, so the page reads as switched early.
- Per-instance queues of 64 drop input under a flood → a person cannot
  press keys that fast; a drop logs, so it is visible.

## Open questions

- [ ] Should a plugin see physical key indexes, or slots 0..n-1 that
  the config maps to physical keys? The default is physical indexes,
  to match today's plugins and `--map` flags. — kgheacock
- [ ] Should `pager` use single press, or long press so its key can
  also do something else? The default is single press on a dedicated
  key. — kgheacock
- [ ] Should the host watch its config file and reload on change? The
  default is no; restart the host. — kgheacock
