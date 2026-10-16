---
id: "0057"
title: "One JSON object holds the keys and their child layers, and persists the active layer"
status: "ongoing"
created: "2026-10-15"
updated: "2026-10-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0057-json-layer-state-with-child-layers"
related: ["0051", "0052", "0055", "0028", "0013"]
tags: ["driver", "layers", "state", "json", "streamer"]
---

# 0057 — One JSON object holds the keys and their child layers, and persists the active layer

## Problem

Tasks 0051 and 0052 store key state in SQLite, behind an event log. That is too much for six keys. Nothing yet lets a process that logs JSON show its status on the pad. A person cannot open a key to see its children.

## Goals

- One JSON object holds six keys. Each key has up to five children.
- A key and a child have the same fields: `icon`, `status`, `count`, `iconRef`.
- `iconRef` names a PNG in `./assets`. The pad shows it as the key image.
- The daemon tracks the active layer. The active layer is what the pad shows.
- A single press on a key with children opens its child layer.
- In a child layer, key 3 shows a shift icon. A single press on it returns to the parent layer.
- The daemon saves the object to disk at intervals, and on exit. A restart shows the same layer.
- `CHANNEL=github macro_streamer` reads JSON lines on stdin and updates the key for that channel.
- A client can learn the active layer, and can reserve a key slot, so task 0055's plugin host can share the keys.
- `driver/statedb` and the `--state-db` flags are removed. The layer tree replaces them.

## Non-goals

- Layers deeper than two. A child has no children.
- Actions on a press other than layer changes.
- A new firmware or wire-protocol change. The pad receives ordinary key states and glyphs.

## Approaches considered

Three approaches follow. They differ in who owns the JSON object.

### Approach A — The JSON file is the interface

`macro_streamer` rewrites the file. The daemon polls it, as the 0051 poller does.

- Good, because the daemon needs no protocol change.
- Good, because any tool can write the file, for example `jq`.
- Bad, because both sides write the file. The streamers write content and the daemon writes `active`. Two renames at once lose one update, so the design needs a lock file.
- Bad, because each streamer reads and rewrites the whole object for each log line.

### Approach B — The streamer owns the object

`macro_streamer` keeps the object, draws the keys, and reads presses. It is a plain WebSocket client, so the daemon does not change.

- Good, because it uses `setKeyState`, `setCustomGlyph`, and `singlePress`, which exist today.
- Good, because it is one process and needs no IPC.
- Bad, because a second streamer (`CHANNEL=dev`) cannot join the object. Two processes then fight over the keys.
- Bad, because clicks stop working when the pipe closes and the process exits.

### Approach C — The daemon owns the object

`macropadd` holds the object, draws the keys, and handles presses. A new message, `setLayerKey`, lets clients update one key or child.

- Good, because the daemon runs all updates one at a time. Any number of streamers can write.
- Good, because layer changes work with no streamer running.
- Good, because the daemon already resolves `singlePress` and already saves state in `--state-dir`.
- Bad, because the WebSocket protocol gains one message kind. The README and the Go client must change.
- Bad, because the daemon now reads PNG files and draws the count. That needs a font package.

## Decision

Chosen: **Approach C — the daemon owns the object**.

Several streamers must share six keys, and navigation must work with none running. Only C gives both. The task accepts one new protocol message and image code in the daemon.

## Design

`layers.json` lives in `--state-dir`, beside `keys.json`.

```json
{"version": 1, "active": null,
 "keys": [{"id": "github", "icon": 0, "status": "RUNNING", "count": 3,
           "iconRef": "github.png",
           "children": [{"id": "build", "status": "ERROR", "count": 1}]}]}
```

- `keys` has at most six entries. Index is the key position. `null` is an empty key.
- `children` has at most five entries.
- `active` is `null` for the top layer. Otherwise it is the `id` of the parent key.
- `status` is `RUNNING`, `DONE`, `ERROR`, `WARN`, or empty. Colors follow `driver/statedb`.
- If `iconRef` is empty, `icon` is a built-in Emoji ID.
- In a child layer, child N shows on keys 0, 1, 2, 4, 5. Key 3 shows `assets/shift.png`.
- `iconRef` is a path under `assets/`. A path that leaves `assets/` is rejected.
- The count is drawn as a badge on the image.
- Only changed keys are sent when the object changes.
- A dirty object is saved every 5 s and on `Close`. A save writes a temporary file, then renames it.
- A file that does not parse moves to `layers.json.bad`. The daemon starts empty.

`setLayerKey` upserts one key or child with its full values. It may carry `slot` (0 to 5). With `slot`, the key goes in that slot. If another `id` holds the slot, the daemon logs it and drops the message. Without `slot`, the key takes the first free slot. A message with only `id` and `slot` reserves the slot.

The daemon sends `layerChanged` with `active` to every client when the layer changes, and to a client when it connects.

`macro_streamer` reads `CHANNEL` and `STATE` from the environment. It sends one `setLayerKey` per JSON line.

Files to change:

- `driver/layers/` — the object, validation, rendering, navigation, saving
- `driver/plugin/protocol.go`, `driver/plugin/server.go` — the `setLayerKey` message
- `driver/cmd/macropadd/main.go` — start the controller, drop `--state-db` and `--state-db-poll`
- `driver/statedb/` — delete
- `driver/cmd/macro_streamer/` — the stdin client
- `assets/shift.png` — the 128x128 shift icon
- `driver/README.md` — a "Layers" section, and delete "State database"

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — A file with seven keys, six children, or an unknown status is rejected. A valid file loads. **Proof:** `cd driver && go test ./layers -run TestDocValidation`
- [ ] **DoD-2** — The top layer sends six key states: status color, image from `iconRef`, and count badge. **Proof:** `go test ./layers -run TestRenderTopLayer`
- [ ] **DoD-3** — A single press on a key with children shows its children on keys 0, 1, 2, 4, 5 and the shift icon on key 3. **Proof:** `go test ./layers -run TestEnterChildLayer`
- [ ] **DoD-4** — A single press on key 3 in a child layer shows the top layer. **Proof:** `go test ./layers -run TestUpReturnsToParent`
- [ ] **DoD-5** — A single press on a key with no children sends nothing and keeps `active`. **Proof:** `go test ./layers -run TestPressWithoutChildren`
- [ ] **DoD-6** — A dirty object is saved within 5 s. A restart shows the saved active layer. A corrupt file moves to `layers.json.bad`. **Proof:** `go test ./layers -run 'TestPersistAndRestore|TestCorruptFileMovedAside'`
- [ ] **DoD-7** — `printf '{"level":"error","msg":"x"}\n' | CHANNEL=github macro_streamer` sets key `github` to `ERROR`. A bad line is logged and skipped. An empty `CHANNEL` exits non-zero. **Proof:** `go test ./e2e -run TestStreamer`
- [ ] **DoD-8** — A client gets `layerChanged` on connect and after each layer change. A `setLayerKey` with `slot` reserves the slot, and a second `id` on that slot is refused. **Proof:** `go test ./plugin -run 'TestLayerChanged|TestSlotReserved'`
- [ ] **DoD-9** — `driver/statedb` is gone, and `macropadd --state-db x` exits with a flag error. **Proof:** `test ! -d driver/statedb` and `go test ./cmd/macropadd -run TestStateDBFlagRemoved`
- [ ] **DoD-10** — `driver/README.md` records the file format, the key layout, and the save interval. **Proof:** `driver/README.md`, section "Layers"
- [ ] **DoD-11** — `cd driver && go build ./... && go vet ./...` exits 0 on go 1.22.0. **Proof:** the command's exit code
- [ ] **DoD-12** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- `singlePress` waits 400 ms for a second click → a layer change lags by 400 ms. Task 0013 explains the window.
- Each glyph takes time to cross to the board → a layer change with six images may take about 1 s (0051 measured 155 ms per write in a burst). DoD-2 and DoD-3 send only changed keys.
- The crash window loses up to 5 s of changes → accepted, because the host disk does not wear.
- A font package may need a newer Go than 1.22.0 → check its `GoVersion` before `go get`, and pin an older release if needed.
- Task 0055's host also writes keys → it uses `setLayerKey` with `slot`, and reserves its slots at start. A streamer without `slot` then skips them.
- A deleted poller leaves a database file on disk → harmless. Nothing reads it, and the README says to delete it.

## Open questions

- [ ] Which JSON fields give the key `status` and the child `id` in a log line? The default is `level` for status and `child` for the id. — kgheacock
- [ ] Should a child layer return on its own after a timeout? The default is no. — kgheacock

## Notes

This task replaces tasks 0051 (the SQLite poller) and 0052 (the event log). Task 0052's PR was closed unmerged. Task 0055 builds on this one for state and layers.
