# driver

The host controller. It runs on the computer the macro pad plugs into, and
talks to the firmware over USB HID and CDC serial.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

**Status: in progress.** [`transport/`](transport/) defines the `Transport`
interface, an in-memory emulator for driver tests, and `Device`, the
hardware implementation. [`plugin/`](plugin/) implements the local
WebSocket API described below, served by the
[`macropadd`](cmd/macropadd/) daemon. [`e2e/`](e2e/) is the end-to-end
scenario harness described below, run with `make e2e`.

## Testing without hardware

No board is wired up yet (see
[task 0010](../tasks/backlog/0010-hardware-bring-up-single-key.md)).
`transport.Emulator`
implements `Transport` in memory, encoding and decoding the exact byte
layout in [`docs/wire-protocol.md`](../docs/wire-protocol.md) instead of a
simplified stand-in. Test code calls `InjectEvent` or `InjectAudioChunk` to
simulate the device side, and `LastKeyState` to inspect what the driver
sent, all without a board attached.

Run the driver tests from the repository root:

```bash
go test ./driver/transport/...
```

Task 0010 checks `transport.Device`, below, against real hardware.

## Connecting to a real board

`transport.Device` implements `Transport` over a real macro pad, using the
operating system's own HID and CDC class drivers instead of claiming USB
interfaces directly:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
dev, err := transport.Open(ctx, transport.Options{
	// Confirmed at bring-up (task 0021's open question) from
	// `ioreg -p IOUSB -l` with the board attached, running CircuitPython.
	VendorID:  0x2E8A,
	ProductID: 0x10A3,
})
```

`Open` matches `Options.VendorID` and `Options.ProductID` against attached
HID devices, reads the match's USB serial number, then opens the CDC
serial port carrying the same serial number — never by listing mounted
volumes. It retries until `ctx` is done, so a caller can hold `Open`
across the reboot that flashing new firmware causes. If more than one
matching device is attached, set `Options.SerialNumber` to pick one;
otherwise `Open` returns `ErrAmbiguousDevice`.

**cgo requirement.** `Device` binds `github.com/sstallion/go-hid`, a cgo
wrapper around hidapi, to reach the HID output report. Building or testing
any package that imports `transport` therefore requires `CGO_ENABLED=1`
and a C toolchain (Xcode Command Line Tools on macOS). `go.bug.st/serial`,
which carries the CDC channel, is pure Go and does not add this
requirement on its own.

**macOS only.** `Open`'s HID/CDC device-matching relies on macOS binding
its class drivers to the composite device task 0008 describes; the other
platforms hidapi and go.bug.st/serial support are untested here and out of
scope for this project.

## Connectivity check

`driver/cmd/pingpong` sends a ping — a Key state message with the
reserved index `255` and a random nonce in the Emoji ID byte — over
`transport.Device`, then waits for the matching pong and prints `PASS` or
`FAIL`, exiting with a matching code. Run it with `make ping-pong` from
the repo root, next to `make debug`; that target flashes the minimal
firmware image in `firmware/ping_pong.py` first, so no board setup beyond
having `CIRCUITPY` mounted is needed. See
[`docs/wire-protocol.md`](../docs/wire-protocol.md#ping) for the Ping and
Pong message layout, and [`firmware/README.md`](../firmware/README.md)
for the firmware side of the check.

## Plugin API

Nothing stops two processes from opening the same `transport.Device` at
once, and third-party automation has no way to react to a key press or set
a key's color and emoji without opening the raw HID and CDC connection
itself. `driver/cmd/macropadd` is the one long-running daemon meant to hold
that connection; every other process — a plugin, written in any language —
reaches the board through it instead.

Run it with:

```bash
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3
```

`macropadd` opens one `transport.Device` (see "Connecting to a real
board," above) and runs `plugin.Server`, which accepts WebSocket
connections on `127.0.0.1:8765` by default (override with `--port`). A
WebSocket needs no OS-specific client code — a plugin can be a Python or
Node script, a shell script speaking a WebSocket library, or a browser
tab. The server accepts connections from `localhost` only; it never binds
a non-loopback interface, regardless of `--port`.

### Protocol

Every message on the connection is JSON, shaped by
[`plugin/protocol.go`](plugin/protocol.go):

- **`event`** — device to client. Wraps one decoded `transport.Event`:

  ```json
  {"kind": "event", "event": {"keyIndex": 2, "type": "press", "timestamp": 123456}}
  ```

- **`setKeyState`** — client to device. Becomes one
  `transport.Transport.SendKeyState` call; the server fills in the wire
  protocol's version byte, so a plugin never has to track it. The server
  also rebroadcasts the message to every connected client, itself
  included, so any observer sees the resulting key state without reading
  it back off the device:

  ```json
  {"kind": "setKeyState", "setKeyState": {"keyIndex": 2, "color": 63488, "emojiId": 7, "blink": true}}
  ```

- **`injectEvent`** — client to device, virtual pad only. Becomes one
  `Injector.InjectEvent` call, simulating a press or release with no
  board attached. `NewServer`'s `injector` argument is `nil` on a
  real-hardware run, so the server drops this message instead of
  applying it — `transport.Device` never satisfies `Injector`, so a real
  run cannot be misconfigured to accept one:

  ```json
  {"kind": "injectEvent", "injectEvent": {"keyIndex": 2, "type": "press"}}
  ```

- **`setCustomGlyph`** — client to device. Becomes one
  `transport.Transport.SendCustomGlyph` call: the server decodes
  `image`'s PNG bytes with `transport.DecodePNGToRGB565` and rejects it,
  with no wire traffic, if it is not exactly 128×128. `image` is a plain
  PNG file's bytes; `encoding/json` base64-encodes and decodes a `[]byte`
  field automatically, so a plugin author never has to know the wire's
  RGB565 pixel format. The server also rebroadcasts the message to every
  connected client, matching `setKeyState`. See task 0030.

  ```json
  {"kind": "setCustomGlyph", "setCustomGlyph": {"keyIndex": 2, "image": "<base64 PNG bytes>"}}
  ```

- **`signal`** — client to server, and server to every client. No
  `transport.Transport` call results from it — the server only
  rebroadcasts it, exactly like `setKeyState`'s rebroadcast, to every
  connected client, itself included. Two things emit a `signal`:

  1. The server itself, once it resolves a key's raw press/release pairs
     into a click pattern — see "Click-pattern resolution," below.
  2. A client — `macrodriver signal`, most often — broadcasting a
     hook-driven status. See "Signal vocabulary," below.

  ```json
  {"kind": "signal", "signal": {"keyIndex": 0, "name": "processDone"}}
  ```

  An in-process observer that already lives inside `macropadd` — task
  0016's OSC 133 scanner, task 0015's iTerm2 bridge — calls
  `Server.Broadcast` directly instead of dialing its own daemon over a
  socket.

- **`subscribeAudio`** / **`unsubscribeAudio`** — client to server, no
  payload. Start or stop delivery of every
  `transport.MessageTypeAudioChunk` the daemon reads to this client. A
  client that never sends `subscribeAudio` never receives one, and pays
  no cost for a recording in progress — see "Audio delivery," below.

  ```json
  {"kind": "subscribeAudio"}
  {"kind": "unsubscribeAudio"}
  ```

### Audio delivery

A subscribed client receives one **binary** WebSocket frame per
`transport.AudioChunk` the daemon reads off the wire, on a queue separate
from every JSON message above: `StreamID` byte, then the chunk's raw PCM
bytes, then a final-chunk byte (0 or 1) — the same layout
[`docs/wire-protocol.md`](../docs/wire-protocol.md)'s "Audio chunk" frame
uses, unwrapped from any JSON envelope. `StreamID` is forwarded as an
opaque byte; this API does not yet fix what it means (for example whether
it equals a `keyIndex`) — see task 0007 and task 0031's Open questions.

A plugin author's WebSocket library must therefore branch on frame type —
binary versus the text frames every other message above uses — instead of
calling `JSON.parse` on everything it receives. See task 0031.

### Click-pattern resolution

The firmware only ever reports a raw press or release (see "Press/release
event" in [`docs/wire-protocol.md`](../docs/wire-protocol.md)); the driver
resolves click patterns from a sequence of them. `plugin.Server` watches
every key's events and broadcasts a `signal` named `singlePress`,
`doublePress`, or `longPress`:

- A release **500ms or more** after its press resolves as `longPress`,
  broadcast the moment the release arrives.
- A release under 500ms starts a **400ms** window, waiting to see whether
  a second press follows. A second short click inside that window
  resolves the pair as `doublePress`. No second press inside the window
  resolves the first click as `singlePress`, broadcast once the window
  elapses.

These are fixed timing thresholds (`driver/plugin/resolver.go`), not
adaptive to how fast a person presses — see the task 0013 spec's Risks.

### Signal vocabulary

| Name | Emitted by | Meaning |
|---|---|---|
| `singlePress` | `plugin.Server`, resolved from raw events | The key was pressed and released once, with no second press following inside the double-press window |
| `doublePress` | `plugin.Server`, resolved from raw events | Two short clicks on the same key, the second starting inside the double-press window |
| `longPress` | `plugin.Server`, resolved from raw events | The key was held 500ms or more before release |
| `processWaiting` | `macrodriver signal`, from a Claude Code `Notification` hook | The owned session is waiting on input |
| `processDone` | `macrodriver signal`, from a Claude Code `Stop` hook | The owned session finished its turn |

`name` is not restricted to this table — a plugin may broadcast its own
vocabulary — but only these five names carry a meaning any other plugin
in this repository recognizes.

### Go plugin helpers

[`driver/api`](../api) gives a Go-based plugin small, typed helpers over
this protocol, instead of building a `setKeyState` or `signal` message by
hand:

```go
conn, err := api.Dial("127.0.0.1:8765") // plugin.DefaultPort
if err != nil {
	log.Fatal(err)
}
defer conn.Close()

conn.SetEmoji(0, 0xF1)             // key 0 shows digit 1
conn.SetState(0, "Waiting", nil)   // key 0 turns amber and blinks
conn.Signal(0, plugin.SignalProcessDone)
```

`SetState`'s named states are a closed set — `Alert` (red, blinking),
`Waiting` (amber, blinking), and `Done` (green, solid) — each mapping to
a fixed `Color` and `Blink` flag; `color`, when not `nil`, overrides the
named state's color. A name outside this set returns
`api.ErrUnknownState`. Adding a new named state needs a driver code
change, matching the wire protocol's own fixed-width messages having no
room for open-ended state names either.

### `macrodriver signal`

`driver/cmd/macrodriver`'s `signal` subcommand opens a short-lived plugin
connection, sends one `signal` message, and exits:

```bash
go run ./driver/cmd/macrodriver signal --key 0 --event stop
```

`--event` accepts `notification` (broadcasts `processWaiting`) and `stop`
(broadcasts `processDone`) — the two Claude Code hook events task 0013
wires up. `--addr` overrides the default `127.0.0.1:8765`.

A hook runs `macrodriver` as a plain shell command, not `go run`, so
install it onto `PATH` first: `go install
./driver/cmd/macrodriver`.

**Claude Code hook example.** Add these to `.claude/settings.json` so a
`Notification` or `Stop` hook event runs `macrodriver signal`, and run
[`driver/examples/claude-status`](../examples/claude-status) — the
reference plugin below — to see the result on a key:

```json
{
  "hooks": {
    "Notification": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "macrodriver signal --key 0 --event notification"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "macrodriver signal --key 0 --event stop"
          }
        ]
      }
    ]
  }
}
```

### Reference plugin: `driver/examples/claude-status`

[`driver/examples/claude-status`](../examples/claude-status) stays
connected to `macropadd` and reacts to `processWaiting` and `processDone`
signals named for one key by calling `SetState`: the key blinks amber
while the session waits on input, and turns solid green once it's done.

```bash
go run ./driver/examples/claude-status --key 0
```

Run it alongside `macropadd` and the hook example above; a real Claude
Code session's `Notification` and `Stop` hooks then drive the key's
color with no other process in between.

### Virtual pad, with no board attached

`--emulate` opens an in-memory `transport.Emulator` instead of a real
`transport.Device`, and wires it in as the plugin server's `Injector`:

```bash
go run ./driver/cmd/macropadd --emulate
```

[`driver/plugin/web/virtualpad.html`](plugin/web/virtualpad.html) is a
static page with no build step — open it directly in a browser (it
connects to `ws://127.0.0.1:8765` by default). It renders six key tiles;
clicking one sends `injectEvent` (a press, then a release), and every
tile updates its color, glyph, and blink state from any `setKeyState`
message it sees, from any connected client. The same page, and the same
plugin code reacting to it, works unchanged against a real board once
keys are wired — it is an ordinary WebSocket client of this API, not a
separate tool. See task 0029.

### Bounds

A slow or stalled client cannot grow the daemon's memory past a fixed
bound, and cannot block delivery to other clients:

- **16 clients** (`maxClients`) — the server rejects a connection past
  this cap, with a WebSocket close reason, instead of accepting it.
- **32 messages per client** (`clientQueueSize`) — each client gets a
  fixed-size, buffered send queue, sized like `deviceQueueSize` in
  `driver/transport`. The dispatch loop's send to it is non-blocking: a
  full queue drops the next message for that client only, so one slow
  client never stalls delivery to the rest.
- **8 drops** (`maxDrops`) — a client whose queue has dropped this many
  messages is disconnected. The daemon keeps running for every other
  client. Dropping an audio frame counts against the same cap as
  dropping a JSON message.
- **8 audio frames per subscribed client** (`audioQueueSize`) — a
  subscribed client's audio frames queue separately from its
  `clientQueueSize`-bounded JSON queue, so one active recording cannot
  fill the queue that bounds delivery of `event` and other JSON
  messages. A client that never subscribes never has a frame queued for
  it. See task 0031.

Total memory for queued-but-undelivered messages therefore never exceeds
`maxClients × (clientQueueSize + audioQueueSize)` messages.

**Out of scope for this API:** auth beyond the `localhost` bind and the
client cap. Click-pattern resolution (single, double, long press) is in
scope as of task 0013 — see "Click-pattern resolution," above.

### Recording a trace alongside the plugin API

`transport.Device.ReadMessage` supports exactly one caller — it drains
one internal channel, not a broadcast. `macropadd` needs two, once task
0025's flight recorder is turned on: `plugin.Server` for its clients, and
`driver/recorder.Recorder` for the JSONL file. `transport.Fanout`
(`transport/fanout.go`) is the one caller of `ReadMessage` in that case;
it hands each of them their own subscription — itself a `Transport` — so
neither one calling `ReadMessage` starves the other. Pass `--trace-file`
to turn recording on:

```bash
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3 \
  --trace-file=/tmp/macropad-trace.jsonl
```

`--trace-file` is empty, and recording off, by default — the recorder
buffers every line in memory until the daemon stops, per task 0025's
design, so leaving it on for a long session is a deliberate choice, not
a default.

## End-to-end scenarios

[`driver/e2e`](e2e/) is a `Pad` facade over `transport.Transport`: a
scenario names a key and a glyph instead of building a `transport.KeyState`
by hand, and waits for a press without hand-rolling a read loop. The same
scenario source runs two ways — against a real board, and against
`transport.Emulator` with no board attached — so a scenario is also a check
on the driver API itself: friction in a scenario is evidence the API needs
to change. See
[task 0020](../tasks/ongoing/0020-e2e-hardware-test-harness.md) for the
design decision.

```go
pad := e2e.Attach(t)               // opens the device, waits out re-enumeration
defer pad.Close()

for _, k := range pad.Keys() {     // 6 keys light one at a time
	k.Set(e2e.Digit(k.Index()+1), e2e.Slate)
	k.On(e2e.Press, func() { k.SetColor(e2e.Amber) })
	k.On(e2e.Release, func() { k.SetColor(e2e.Slate) })
}

pad.Ask("Press each key once, left to right.")
for _, k := range pad.Keys() {
	k.ExpectPress(t, 10*time.Second)
}
pad.Confirm(t, "Did every key keep its number while the background turned amber?")
```

`Pad.Ask` prints its prompt and returns immediately; a scenario's own
`Key.ExpectPress` and `Pad.Confirm` calls do the actual waiting, so they are
already watching before a person has time to act on the prompt.
`Pad.Confirm` ends the scenario in a person's verdict — every hardware
scenario does, since nothing yet checks the picture on the key's display
itself — and every prompt and verdict is appended to a JSON-lines file
under [`driver/e2e/runs/`](e2e/runs/), so a reviewer can see exactly what
was asked.

Run every scenario against the attached board with:

```bash
make e2e
```

This flashes the current firmware first, then runs
`go test -tags hardware ./driver/e2e/...`. It needs a person at the
terminal for `Pad.Confirm`, so it never runs unattended in CI. With no
board attached, it fails fast: `make flash`'s `check-circuitpy`
prerequisite finds no `CIRCUITPY` volume and exits non-zero naming the
missing device, well under 30 seconds.

The same scenario runs with no board attached, driven by a
`ScriptedOperator` instead of a person, wherever the emulator-backed test
file for a scenario lives (see `driver/e2e/pad_emulator_test.go`):

```bash
go test ./driver/e2e/...
```

## Scope

Language: Go.

The driver does the following:

- Sends the emoji, task ID, color, and blink state for each key to the
  firmware.
- Resolves press timing from the firmware's raw, timestamped events —
  classifies single press, double press, and long press (hold).
- Maps resolved events to host-defined actions.
- Decodes the chunked audio stream and reassembles it using the final-chunk
  flag.

## Out of scope

- Rendering, debouncing, and raw event emission. The firmware
  ([`firmware/`](../firmware/)) does that.
