---
id: "0077"
title: "Press a key to translate, summarise, or ask Claude about the selected text"
status: "backlog"
created: "2026-10-22"
updated: "2026-10-22"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-f9iu7g"
related: ["0053", "0055", "0057", "0064", "0071", "0075"]
tags: ["driver", "plugin", "selection", "inbox", "claude"]
---

# 0077 — Press a key to translate, summarise, or ask Claude about the selected text

## Prerequisite

Two tasks must be on `main` before work on this task starts:

- Task 0055, the plugin host (draft PR #65). This task is a host plugin in
  `driver/plugins/selectionactions`, registered in
  `driver/cmd/macropad-plugins/plugins.go`.
- Task 0053, selection capture as a host service. This task reads the
  selection with `h.SelectedText(ctx)`.

On 2026-10-22, neither is on `main`. PR #65 is 198 commits behind `main` and
predates the layer tree (task 0057), so its pages become layers when it is
rebased. Names in this spec follow PR #65 and spec 0053; they are updated to
match whatever lands before work starts.

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
- The person can add, remove, or change actions in the host config, without
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
  plugin logs the "unsupported" error on other OSes and does nothing.

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

`driver/plugins/selectionactions` exports `Factory`, named
`selection-actions`. It is registered in
`driver/cmd/macropad-plugins/plugins.go`.

### Config

```json
{
  "id": "sel",
  "use": "selection-actions",
  "keys": [3],
  "config": {
    "channel": "selection",
    "timeout": "60s",
    "actions": [
      {"key": 3, "click": "single", "name": "summarise",
       "command": ["claude", "-p", "Summarise this text in three sentences or fewer."]},
      {"key": 3, "click": "double", "name": "translate",
       "command": ["claude", "-p", "Translate this text into English. If it is already English, translate it into French. Reply with the translation only."]},
      {"key": 3, "click": "long", "name": "ask",
       "command": ["claude", "-p", "Explain this text, and answer any question in it."]}
    ]
  }
}
```

- `actions` lists every action. Each has the key it runs on (one of the
  instance's `keys`), the click (`single`, `double`, or `long`), a `name`, and
  a `command` (argv, no shell). Two actions on the same key and click are a
  config error, as is a key the instance does not own.
- With no `actions`, the plugin uses the three above, all on the instance's
  first key.
- `channel` is the inbox channel. Each answer goes to `CHANNEL.NAME`, such as
  `selection.translate`, so the inbox key shows one child per action
  (task 0066). Default `selection`.
- `timeout` stops a command that runs too long. Default `60s`.
- `inbox` is the path of the `inbox` command. Default: `inbox` on `PATH`.

### A press

On a click that matches an action, on the instance's queue:

1. Call `h.SelectedText(ctx)`. Empty text: blink the key red once
   (`SetState("Alert")` for 1 s, then back) and stop. Nothing is sent.
2. Set the key to `Waiting` (amber blink), and run the rest with `h.Go`, so
   the queue is free for other presses.
3. Run the command with the text on standard input, with the timeout.
4. Exit 0 with output: `inbox send --channel CHANNEL.NAME --status info -- ANSWER`.
   The key goes back to its idle look.
5. A non-zero exit, a timeout, or a missing command:
   `inbox send --channel CHANNEL.NAME --status error -- "NAME failed: REASON"`,
   with the first line of standard error as the reason. The key shows
   `Alert` until its next press.

Only one action runs per key at a time. A press on a key whose action still
runs is ignored and logged. Actions on different keys run in parallel.

The answer is trimmed of surrounding blank lines. An answer over 16 KB is cut
to 16 KB and ends with `…`, because the inbox is for short messages.

### Idle look

Each key shows its first action's name as a text icon (task 0069), so a
person can tell which key summarises. A key with no image support falls
back to a solid color.

### At start

`Start` checks each action's `command[0]` with `exec.LookPath`, and the
`inbox` command too. A missing one is logged with the `PATH` the host sees,
because a login service's `PATH` is short. The instance still starts; that
action fails on press as in step 5. `Start` also calls
`h.Require(macos.Accessibility)` through task 0053's service.

### Privacy

The selected text goes to whatever the command sends it to. For the
defaults, that is Anthropic, through Claude Code. The README says so, and
warns against using the keys on passwords or secrets.

Files to change:

- `driver/plugins/selectionactions/selectionactions.go` — the plugin
- `driver/plugins/selectionactions/selectionactions_test.go` — tests with
  `hosttest`, a fake runner, and a fake `inbox`
- `driver/cmd/macropad-plugins/plugins.go` — register the factory
- `driver/README.md` — the plugin, its config, the privacy note

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — With `hosttest.SetSelection("bonjour")` and a fake command
  that prints `hello`, a single click on the action's key runs the command
  with `bonjour` on standard input and sends `hello` to
  `selection.summarise` with status `info`. **Proof:**
  `cd driver && go test ./plugins/selectionactions -run TestPress_SendsAnswer`
- [ ] **DoD-2** — Single, double, and long clicks on one key run three
  different actions. **Proof:** `go test ./plugins/selectionactions -run TestPress_ClickPicksAction`
- [ ] **DoD-3** — With no selection, a click runs no command and sends no
  message. **Proof:** `go test ./plugins/selectionactions -run TestPress_NoSelection`
- [ ] **DoD-4** — A command that exits 1, a command that outlives the
  timeout, and a missing command each send one `error` message naming the
  action and leave the key in `Alert`. **Proof:**
  `go test ./plugins/selectionactions -run TestPress_Failures`
- [ ] **DoD-5** — While an action runs, the key is `Waiting`, and a second
  click on it starts no second command. **Proof:**
  `go test ./plugins/selectionactions -race -run TestPress_OneAtATime`
- [ ] **DoD-6** — A config with two actions on the same key and click, or an
  action on a key the instance does not own, fails `Start` and names the
  problem. No `actions` gives the three defaults. **Proof:**
  `go test ./plugins/selectionactions -run TestConfig`
- [ ] **DoD-7** — `macropad-plugins --list` shows `selection-actions`.
  **Proof:** the command's output
- [ ] **DoD-8** — On a Mac, with text selected in Safari and in VS Code, each
  default action's answer appears under the inbox key in its own channel
  child within 15 s. **Proof:** manual run, logged in the PR description
- [ ] **DoD-9** — `driver/README.md` documents the plugin, its config, the
  defaults, and the privacy note. **Proof:** `driver/README.md`
- [ ] **DoD-10** — `cd driver && go build ./... && go vet ./...` exits 0 on
  Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:**
  PR body

## Risks

- `claude` is not on the login service's `PATH` → `Start` logs it with the
  `PATH` it saw, and the README shows how to set `PATH` in the service.
- `claude -p` asks for a permission or a login interactively → it has no
  terminal, so it fails; the error message reaches the inbox. The defaults
  use no tools, so no permission prompt is expected.
- Each press costs Claude usage → the README says so. Nothing runs without a
  press.
- The selection holds secrets → see Privacy.
- Task 0053's capture takes the clipboard for up to 250 ms → inherited, as
  documented there.

## Open questions

- [ ] Which language should translate target? The default is English, and
  French when the text is already English. — kgheacock
- [ ] Should the answer also go to the clipboard? The default is no. — kgheacock
- [ ] Should "ask" open a Claude Code session in iTerm with the text instead
  of a one-shot answer, using the inbox's `--here`? The default is a
  one-shot answer. — kgheacock
