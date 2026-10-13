# window-switcher

A `macropadd` plugin that binds each key to a macOS desktop, app, or
window and brings it forward when the key is pressed. Each bound key lights
in its own color: bright for the last target it switched to, dim for the
rest. See task 0053.

```bash
go run ./driver/examples/window-switcher                       # key N -> desktop N+1
go run ./driver/examples/window-switcher --config switcher.json
go run ./driver/examples/window-switcher --dry-run             # print, don't switch
```

| Flag | Default | Meaning |
|---|---|---|
| `--config PATH` | none | JSON bindings, below. With none, keys 0–5 switch to desktops 1–6. |
| `--addr HOST:PORT` | `127.0.0.1:8765` | The running `macropadd`. |
| `--dry-run` | off | Print each `osascript` command instead of running it. Works on any OS. |

## Config

```json
{
  "trigger": "press",
  "keys": [
    {"key": 0, "desktop": 1},
    {"key": 1, "desktop": 2, "color": "#12D84A"},
    {"key": 2, "app": "Safari"},
    {"key": 3, "app": "iTerm2", "window": "build"},
    {"key": 4, "app": "Visual Studio Code", "window": "macro-pad"}
  ]
}
```

- `trigger`: `press` (default) switches on the raw press, with no delay.
  `singlePress`, `doublePress`, or `longPress` switch on the server's
  resolved signal instead, which frees the other patterns for other
  plugins but adds the 400 ms double-press wait to a single press.
- Each key sets exactly one of `desktop` (1–9) or `app`.
- `window` narrows `app` to its first window whose title contains the text.
  Without it, the app is activated with whatever window it puts in front.
- `color` is `#RRGGBB`. Without it, each key gets a distinct default.
- An unknown field, a key outside 0–5, or a key bound twice is an error at
  startup.

## How each target switches

| Target | AppleScript | macOS permission |
|---|---|---|
| `desktop` | System Events sends Ctrl+N | Accessibility, plus the Mission Control shortcut below |
| `app` | `tell application X to activate` | Automation (first run asks) |
| `app` + `window` | activate, then System Events `AXRaise` on the matching window | Accessibility and Automation |

App and window names are passed to `osascript` as arguments, never spliced
into the script, so a title with quotes in it cannot change what runs.

## Testing on a Mac

1. **Create desktops and turn on their shortcuts.** In Mission Control,
   add desktops until there are six. Then in System Settings > Keyboard >
   Keyboard Shortcuts > Mission Control, turn on "Switch to Desktop 1"
   through "Switch to Desktop 6" (they appear only once those desktops
   exist). Press Ctrl+2 by hand to confirm it moves you.
2. **Start the daemon.** With the board: `go run ./driver/cmd/macropadd
   --vendor-id=0x2E8A --product-id=0x10A3`. Without it: `go run
   ./driver/cmd/macropadd --emulate`, then open
   `driver/plugin/web/virtualpad.html` in a browser.
3. **Dry run first.** `go run ./driver/examples/window-switcher --dry-run`.
   Six keys turn dim colors. Pressing a key (or clicking a tile in the
   virtual pad) prints the `osascript` command and lights that key bright.
4. **Run for real.** Drop `--dry-run`. The first press asks for
   Accessibility permission for the terminal app running the plugin
   (System Settings > Privacy & Security > Accessibility). Grant it, then
   restart the plugin. Pressing key 1 should move you to desktop 2.
5. **Try apps and windows.** Write a config like the one above, open two
   iTerm2 windows with one titled "build", and check that key 3 raises
   that one even when the other is in front.
6. **Check the failure path.** Bind a key to an app that is not
   installed. Pressing it logs `window-switcher: key N -> App: osascript:
   ...` and leaves the lit key unchanged.

## Limits

- The bright key shows the last switch made from the pad. Switching
  desktops by trackpad or keyboard does not update it.
- `AXRaise` on a window that sits on a different desktop from the app's
  front window may raise it without moving you there. Keeping each app's
  windows on one desktop, or binding desktops instead, avoids this.
- Fullscreen apps take their own desktop, which shifts the numbers Ctrl+N
  reaches.
- Showing thumbnails of hidden desktops or windows is out of scope here.
