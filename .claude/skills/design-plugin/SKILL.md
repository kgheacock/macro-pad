---
name: design-plugin
description: Design or build a macro-pad plugin for the macropad-plugins host. Covers the host architecture, where a plugin's code goes, its config, keys and pages, helpers, macOS permissions, tests, and its task spec.
---

# Designing a macro-pad plugin

Use this when an idea for something the pad should do turns into a
plugin, whether you're writing its spec or its code. The authority is
task 0055 (`tasks/*/0055-plugin-host-and-helper-api.md`) and the
"Plugin host" section of `driver/README.md`. Read both before you
design anything. If this skill disagrees with them, they win.

## 1. Architecture in one picture

```
board ⇄ macropadd (daemon, owns USB, WebSocket on 127.0.0.1:8765)
            ⇅ one WebSocket
        macropad-plugins (the host: driver/cmd/macropad-plugins)
            ├─ instance "claude"  (plugins/claudestatus, page work, key 0)
            ├─ instance "pager"   (plugins/pager, page *, key 5)
            └─ instance …         (one per config entry)
```

- **macropadd** is the only process that touches the board. It has no
  macOS code and no plugin logic. A plugin never changes it. If a design
  seems to need a daemon change, it's a protocol change and needs its own
  task.
- **The host** is a single process. It runs every Go plugin in-process,
  reads `~/.config/macropad/plugins.json`, and holds the one connection.
  It routes input to whichever instance owns the key and caches every
  key's state. When a page switches or the daemon reconnects, it
  repaints from that cache.
- **A plugin** is a Go package exposing a `host.Factory`. Each config
  entry becomes an *instance*, with its own keys, page, config, and
  goroutine.
- **Platform features** belong to the host, not to a plugin. This covers
  anything more than one plugin uses, or anything Keith calls a "platform
  feature", such as text selection (task 0053). They go in
  `driver/host/...` and are exposed on `host.Host`.
- **Non-Go plugins** (Python, a browser tab) talk to macropadd directly
  over the WebSocket protocol. The host can't enforce key ownership for
  them, so keep their keys out of the host config.

## 2. Decide what kind of thing it is

Ask these in order:

1. Do other plugins need it, or would they? Then it's a **host helper or
   service** (`driver/host/<pkg>` plus a method on `host.Host` and
   `hosttest.Host`), not a plugin.
2. Does it need the board, the wire protocol, or the daemon's resolver?
   Then it's a **daemon or protocol task**, not a plugin.
3. Otherwise it's a **plugin**: `driver/plugins/<name>`.

Write down which kind it is and why. Put that in the task spec's Design
section.

## 3. Plugin conventions

**Layout**

```
driver/plugins/<name>/        package <name> (no hyphen, e.g. windowswitcher)
  <name>.go                   Factory, Config, Plugin, Start
  <name>_test.go              tests on hosttest
  <topic>.go                  anything big enough to split out
driver/cmd/macropad-plugins/plugins.go   add <name>.Factory to registry
```

- `Factory.Name` is kebab-case, like `window-switcher`. That's what a
  config's `"use"` names, and the README and `--list` show it.
  `Description` is one line saying what the person sees on the pad.
- Never add a new `main` per plugin, and never add anything under
  `driver/examples/`.

**Shape**

```go
var Factory = host.Factory{Name: "x", Description: "…", New: func() host.Plugin { return &Plugin{} }}

type Config struct { /* json tags, omitempty, documented defaults */ }

type Plugin struct {
	h   host.Host
	cfg Config
	// injected seams, e.g. desk macos.Desktop
}

func (p *Plugin) Start(ctx context.Context, h host.Host) error {
	if err := h.Config(&p.cfg); err != nil { return err }
	// validate p.cfg; return an error naming the field
	// h.Require(...) if it needs a macOS grant
	// register handlers; draw the initial key state
	return nil
}
```

- **Start returns promptly.** It reads config, validates it, registers
  handlers, and draws. Put long work in `h.Go`, and polling in `h.Every`.
- **An error from Start turns off only this instance.** A bad config, a
  missing grant, or no keys should fail there with a clear message. Don't
  panic and don't log-and-continue.
- **No locks for the plugin's own state.** Handlers, `Every` functions,
  and bus deliveries all run one at a time on the instance's goroutine.
  Only code started with `h.Go` runs concurrently. If you use it, hand
  the result back with `h.Publish` or a channel, not shared fields.
- **Never use a bare `go` statement.** A panic in one kills every plugin.
  Use `h.Go`.
- **Slow work** (more than about 50 ms, like osascript, Accessibility, or
  a capture) only delays this instance's own queue, which is fine. Work
  that takes seconds belongs in `h.Go`, so input isn't dropped from the
  64-call queue.

**Keys and input**

- Use only `h.Keys()`. Never hardcode a key index. Config decides keys,
  and the same plugin may run as several instances on different keys or
  pages.
- Choose the trigger on purpose:
  - `OnPress` and `OnRelease` give an instant response, without
    single/double/long clicks.
  - `OnClick(Single|Double|Long)` arrives after the resolver: a single
    click lands about 400 ms late, a long press needs 500 ms.
  - If a plugin offers both, make the trigger a config field (the window
    switcher has `"trigger"`).
- `OnSignal` is for status events, like `processWaiting` or a hook. It
  arrives even while the page is hidden. Update the key anyway: the host
  caches the change and shows it when the page appears.

**Output**

- Use `h.Key(k).SetColor`, `SetState`, `SetBlink`, `SetImage`, `SetPNG`,
  or `Clear`. Check the error, and log it with `h.Log`.
- Every write to the board costs about 155 ms, and an image costs more.
  Don't redraw a key that hasn't changed. Compare against what you last
  set, and keep image refreshes on a slow `Every` interval (seconds).
- Use the colors in `keyimg`: `keyimg.Hex` for config strings,
  `keyimg.Palette[i]` as the default per key, and `Color.Dim(0.25-0.4)`
  for "inactive". Never hand-roll RGB565.
- Images go through `keyimg.Thumbnail` (`SetImage` calls it for you).
  `keyimg.Text` makes short labels, and `keyimg.Stale` makes "last known,
  now hidden".
- A plugin that refreshes in the background should check `h.Visible()`
  or use `OnVisible` to stop capturing while its page is hidden.

**Pages**

- A plugin doesn't own pages. The config puts instances on pages. Only
  call `h.ShowPage` when switching pages is the plugin's purpose, as with
  `pager`, or a deliberate jump.
- Use `OnPageChange` only if the key shows something about the page.

**Talking to other plugins**

- Use `h.Publish` and `h.Subscribe` on a topic constant in
  `driver/host/topics.go`. When you add a topic, document its payload
  type there. Payloads are plain Go values; don't share mutable
  pointers.
- To reach non-Go clients, a signal over the WebSocket is a protocol
  surface. Name it in the spec and add it to `driver/README.md`'s
  "Signal vocabulary".

**macOS**

- Go through `driver/host/macos`. A plugin holds a `macos.Desktop` field,
  set to `macos.System` in `Start` unless a test already set it, so tests
  can pass a fake.
- If you need a macOS call that isn't there, add it to `host/macos`.
  Put the cgo code in a `//go:build darwin && cgo` file, and a stub that
  returns `macos.ErrUnsupported` in `!darwin || !cgo`. Don't put it in
  the plugin.
- If a plugin needs Accessibility or Screen Recording, call
  `h.Require(...)` in `Start`. The host binary holds the grant, not the
  plugin. Mention it in the README.

**Config**

- Use a JSON struct with `omitempty`, and write a doc comment for each
  field that names its default. An empty config must work and do
  something sensible, the way the window switcher defaults to key N
  showing desktop N+1.
- Unknown fields are rejected by the host, so a typo fails loudly.
- Write keys as physical indexes, the same as `h.Keys()`.

## 4. Tests

- Test on `hosttest`, never with a fake WebSocket. Use
  `hosttest.New(t, keys...)`, then `.SetConfig(...)`, `.SetPages(...)`
  and `.SetPermissions(...)`, and then `h.Start(&Plugin{desk: fake})`.
- Drive input with `Press`, `Release`, `Click`, `Signal`, `Deliver`
  (bus), `SetVisible`, and `Tick` (which runs `Every` functions once).
  Assert with `KeyState(k)` and `h.Published`. Handlers run
  synchronously, so no sleeps are needed.
- Cover these:
  - the happy path for each trigger;
  - keys the instance doesn't own are ignored;
  - each config validation error;
  - a missing grant (`SetPermissions`), which makes `Start` fail;
  - the behavior while hidden, if the plugin refreshes.
- If you change something in `driver/host`, add a test in
  `host/runner_test.go` that uses the fake connection there.
- Before you push, run `cd driver && go vet ./... && go test ./...`.

## 5. Ship it

1. Write the task spec first with `/task-spec` (the format is in
   `tasks/README.md`). Use the next free number across `tasks/` *and* the
   open branches, and include this skill's section 2 decision in Design.
2. Implement it on the task branch. Register the Factory, add an example
   instance to `driver/cmd/macropad-plugins/example.json` if it helps,
   and confirm `macropad-plugins --check` still passes on that file.
3. Add the plugin to the README's "Plugin host" section: what it does,
   its config table, which trigger it uses, and any macOS grant.
4. Verify end to end without hardware. Run
   `go run ./cmd/macropadd --emulate --port 8799 --state-dir ""`, then
   run the host with `--addr 127.0.0.1:8799`, and inject presses or
   watch the output through `driver/plugin/web/virtualpad.html` or a
   short `api.Dial` client. Put what you saw in the PR description.
   Note any macOS-only path you couldn't run.

## Checklist before calling a design done

- [ ] Kind decided: plugin, host helper or service, or daemon task, with
      the reason written down.
- [ ] Keys come from config, and the trigger is chosen and justified.
- [ ] The config struct has defaults, and an empty config works.
- [ ] Every macOS call goes through `host/macos`, with `Require` for its
      grants.
- [ ] There are no bare goroutines and no locks around handler state.
- [ ] Keys are only redrawn when they change, and image refreshes are
      throttled.
- [ ] Any new bus topic is in `topics.go`, and any new signal is in the
      README.
- [ ] Tests run on `hosttest` and cover ownership, config errors, and
      grants.
- [ ] The README, `example.json`, `--check`, and the spec's definition of
      done are all updated.
