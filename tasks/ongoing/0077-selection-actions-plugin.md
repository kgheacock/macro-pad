---
id: "0077"
title: "Press a key to translate, summarise, or ask Claude about the selected text"
status: "ongoing"
created: "2026-10-22"
updated: "2026-10-23"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/98"
branch: "claude/project-thread-f9iu7g"
related: ["0053", "0055", "0057", "0064", "0071", "0075"]
tags: ["driver", "plugin", "selection", "inbox", "claude"]
---

# 0077 — Press a key to translate, summarise, or ask Claude about the selected text

## Where it runs

On 2026-10-22, neither task this plugin was meant to build on is on `main`:

- Task 0055, the plugin host (draft PR #65), is 198 commits behind `main` and
  predates the layer tree (task 0057).
- Task 0053, selection capture as a host service, has a spec and no code.

Keith chose on 2026-10-23 to build it now as a standalone binary,
`driver/examples/selection-actions`, and port it to
`driver/plugins/selectionactions` once 0055 and 0053 land. The binary does
its own selection capture with `osascript`, the same AX-then-Cmd+C order as
task 0053, and the port replaces that with `h.SelectedText`.

## Problem

Task 0053 makes the selected text available to plugins, but nothing uses it
yet. The common things a person does with a selection are the same few:
translate it, summarise it, or ask Claude about it. Today each one means
copying the text, switching to another app or a terminal, pasting it, and
waiting there for the answer.

The inbox (tasks 0064 to 0075) already gives the pad a place for answers to
land. A channel key lights up when a message arrives, and a press marks it
seen. So the answer does not need a window of its own: it can arrive in an
inbox channel while the person keeps working.

## Goals

- Select text in any app, press a pad key, and keep working. The answer
  arrives in an inbox channel a few seconds later.
- Three actions out of the box: translate, summarise, and ask Claude.
- The person can add, remove, or change actions in a config file, without
  a rebuild.
- The key shows that an action is running, and whether it failed.
- No API key in the config or the repo. The default actions use the
  person's existing Claude Code login.

## Non-goals

- Showing the answer on the key. Keys are 128x128; the inbox is the reader.
- Replacing the selection with the answer (for example, translate in place).
  That needs a paste, which is a later task.
- A chat. Each press is one question and one answer, with no history.
- Images or rich text. Plain text only, as in task 0053.
- Linux and Windows. Selection capture is macOS only (task 0053), so the
  binary logs the missing `osascript` on other OSes and shows `ERROR`.

## Approaches considered

### Approach A — Call the Anthropic Messages API from Go

The plugin sends the text and a prompt to `https://api.anthropic.com/v1/messages`
with `net/http`, using `ANTHROPIC_API_KEY` from the environment.

- Good, because it is one HTTP call with no other process, and the fastest.
- Good, because the plugin controls the model and the token limit.
- Bad, because it needs an API key and API billing, apart from the person's
  Claude subscription. The key lives in the login service's environment.
- Bad, because every new action is Go code or a prompt in the config, and
  an action that is not an LLM call (a dictionary lookup, a `trans` call)
  cannot be added.

### Approach B — Run `claude -p` for every action

The plugin runs Claude Code headless, `claude -p "PROMPT"`, with the text on
standard input, and sends standard output to the inbox.

- Good, because it uses the person's Claude Code login. No key to manage.
- Good, because prompts are plain text in the config.
- Bad, because `claude -p` starts a Node process, which adds a second or two
  to each answer.
- Bad, because every action is a Claude call, so a non-LLM tool cannot be
  used.

### Approach C — Each action is a command; the defaults run `claude -p`

An action in the config is a command line. The plugin runs it with the text
on standard input, with no shell, and sends standard output to the inbox. The
three default actions are `claude -p` with a prompt each.

- Good, because the defaults need no key, as in Approach B.
- Good, because any tool works: `trans`, a local model through `ollama run`,
  a script of the person's own.
- Good, because the plugin's own code stays small: run a command, send its
  output.
- Bad, because the plugin depends on `claude` (or the configured command)
  being on the login service's `PATH`. A missing command is a red key and a
  log line, not an answer.
- Bad, because of the same startup cost as Approach B for the defaults.

## Decision

Chosen: **Approach C — each action is a command; the defaults run `claude -p`**.

It needs no API key, and it lets the person point a key at any tool. The
cost is a second or two of startup per answer, and a dependency on `claude`
being found, which the plugin checks at start.

## Design

### Where it lives

`driver/examples/selection-actions`, a `main` package beside
`claude-status`. It dials `macropadd`'s plugin WebSocket (`--addr`), puts one
top-level key in the layer tree with `setLayerKey`, and reads `signal` and
`layerChanged` messages.

### Config

`--config PATH` names a JSON file. Every field is optional; with no file, key
3 runs the three defaults.

```json
{
  "id": "selection",
  "slot": 3,
  "label": "selection",
  "channel": "selection",
  "timeout": "60s",
  "inbox": "inbox",
  "actions": [
    {"click": "single", "name": "summarise",
     "command": ["claude", "-p", "Summarise the text on standard input in three sentences or fewer. Reply with the summary only."]},
    {"click": "double", "name": "translate",
     "command": ["claude", "-p", "Translate the text on standard input into English. If it is already English, translate it into French. Reply with the translation only."]},
    {"click": "long", "name": "ask",
     "command": ["claude", "-p", "Explain the text on standard input, and answer any question in it. Be brief."]}
  ]
}
```

- `id` and `slot` place the key in the layer tree. `label` is its `iconText`.
- `actions` lists at most one action per click (`single`, `double`, `long`).
  Each has a `name` (one word, no dot) and a `command` (argv, no shell).
- Each answer goes to the inbox channel `CHANNEL.NAME`, such as
  `selection.translate`, so the inbox key shows one child per action
  (task 0066).
- `timeout` stops a command that runs too long. `inbox` is the inbox command.
- An unknown field, a slot outside 0 to 5, a bad timeout, a repeated click, a
  bad name, or an empty command stops the binary with exit 2, naming every
  problem.

Several keys: run one binary per key, each with its own `id` and `slot`.

### A press

The daemon's resolver turns a press into `singlePress`, `doublePress`, or
`longPress` for the key. On the one that matches an action, while the top
layer shows (`layerChanged` with `active: null`), on a goroutine of its own
so the read loop keeps reading:

1. Capture the selection (below). Nothing selected: the key shows `WARN`
   for 1 s, and nothing is sent.
2. The key shows `RUNNING`. Run the command with the text on standard
   input, within the timeout.
3. Exit 0 with output: `inbox send --channel CHANNEL.NAME --status info --
   ANSWER`, and the key goes back to no status.
4. A non-zero exit, a timeout, a missing command, or no output:
   `inbox send ... --status error -- "NAME failed: REASON"`, with the first
   line of standard error as the reason. The key shows `ERROR` until the
   next answer.

A press while an action runs is ignored and logged. The answer is trimmed,
and an answer over 16 KB is cut to 16 KB ending in `…`.

### Selection capture

Two `osascript` calls, until task 0053 gives the host this service:

1. AppleScript asks System Events for the frontmost process's
   `AXFocusedUIElement`, then its `AXSelectedText`.
2. If that is empty, JavaScript for Automation saves every item on the
   general pasteboard, posts Cmd+C, polls `changeCount` every 10 ms for up
   to 250 ms, reads the string, and writes the saved items back.

Both need the Accessibility grant for the process that runs the binary (the
terminal, or the binary under a login service).

### At start

Every action's `command[0]` and the `inbox` command are checked with
`exec.LookPath`. A missing one is logged with the `PATH` the binary sees,
because a login service's `PATH` is short. The binary still runs.

### Privacy

The selected text goes to whatever the command sends it to. For the
defaults, that is Anthropic, through Claude Code. The README says so, and
warns against using the key on passwords or secrets.

Files:

- `driver/examples/selection-actions/main.go` — config, the key, a press
- `driver/examples/selection-actions/capture.go` — the `osascript` capture
- `driver/examples/selection-actions/main_test.go` — tests with a fake
  connection, selection, and runner
- `driver/README.md` — the plugin, its config, the privacy note

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — With a fake selection `bonjour` and a fake command that
  prints `hello`, a single press on the key runs the command with `bonjour`
  on standard input, sends `hello` to `selection.summarise` with status
  `info`, and shows `RUNNING` then no status. **Proof:**
  `cd driver && go test ./examples/selection-actions -run TestPress_SendsAnswer`
- [ ] **DoD-2** — Single, double, and long presses on the key run three
  different actions. **Proof:** `cd driver && go test ./examples/selection-actions -run TestPress_ClickPicksAction`
- [ ] **DoD-3** — A press on another key, or on this key while a child layer
  shows, runs nothing. **Proof:** `cd driver && go test ./examples/selection-actions -run TestPress_IgnoresOtherKeysAndChildLayers`
- [ ] **DoD-4** — With nothing selected, a press runs no command, sends no
  message, and shows `WARN` then no status. **Proof:**
  `cd driver && go test ./examples/selection-actions -run TestPress_NoSelection`
- [ ] **DoD-5** — A command that exits 1, one that outlives the timeout, a
  missing command, and one that prints nothing each send one `error`
  message naming the action, and leave the key `ERROR`. **Proof:**
  `cd driver && go test ./examples/selection-actions -run TestPress_Failures`
- [ ] **DoD-6** — While an action runs, a second press starts no second
  command. **Proof:** `cd driver && go test -race ./examples/selection-actions -run TestPress_OneAtATime`
- [ ] **DoD-7** — No config gives the three defaults on key 3. A config with
  a bad slot, timeout, click, name, or command, or an unknown field, fails
  and names every problem. **Proof:** `cd driver && go test ./examples/selection-actions -run TestConfig`
- [ ] **DoD-8** — Against `macropadd --emulate`, with stub `osascript`,
  `claude`, and `inbox` commands on `PATH`, a single and a long press
  injected on key 3 put the key in `layers.json` and run `inbox send` with
  the summarise and ask answers. **Proof:** the run logged in the PR
  description
- [ ] **DoD-9** — On a Mac, with Accessibility granted and text selected in
  TextEdit, Safari, and VS Code, each default action's answer appears under
  the inbox key in its own channel child within 15 s, and the clipboard
  holds its prior contents afterwards. **Proof:** manual run, noted on the PR
- [ ] **DoD-10** — `driver/README.md` documents the binary, its config, the
  defaults, the Accessibility grant, and the privacy note. **Proof:**
  `driver/README.md`
- [ ] **DoD-11** — `cd driver && go build ./... && go vet ./...` exits 0 on
  Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-12** — The PR in the `pr` field links to this spec. **Proof:**
  PR body

## Risks

- `claude` is not on the login service's `PATH` → the binary logs it at
  start with the `PATH` it saw.
- `claude -p` asks for a permission or a login interactively → it has no
  terminal, so it fails; the error message reaches the inbox. The defaults
  use no tools, so no permission prompt is expected.
- Each press costs Claude usage → the README says so. Nothing runs without a
  press.
- The selection holds secrets → see Privacy.
- The Cmd+C fallback takes the clipboard for up to 250 ms, as in task 0053 →
  a copy the person makes in that window is lost; a clipboard manager may
  record the captured text.
- The `osascript` capture is untested on a real Mac until DoD-9 → it is two
  small scripts, and task 0053's port replaces it.
- The standalone binary is thrown away when 0055 and 0053 land → Keith
  accepted this; the config, the press logic, and the tests carry over.

## Open questions

- [ ] Which language should translate target? The default is English, and
  French when the text is already English. — kgheacock
- [ ] Should the answer also go to the clipboard? The default is no. — kgheacock
- [ ] Should "ask" open a Claude Code session in iTerm with the text instead
  of a one-shot answer, using the inbox's `--here`? The default is a
  one-shot answer. — kgheacock
