---
id: "0073"
title: "An open emoji set under assets/emoji/, and a stable emoji for each folder and Claude Code session"
status: "backlog"
created: "2026-10-19"
updated: "2026-10-19"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-dlib2h"
related: ["0034", "0038", "0046", "0055", "0062", "0069", "0070", "0071", "0072"]
tags: ["assets", "inbox", "plugin", "emoji", "driver"]
---

# 0073 — An open emoji set, and a stable emoji per folder and session

The spec has two parts. Part 1 adds emoji pictures to the asset library. Part 2
picks one of them, always the same one, for a folder or a Claude Code session.
They share one spec because Part 2 is only as good as the set Part 1 draws.

## Problem

The asset library is 13 hand-picked PNGs in `assets/`. A key shows one of them
through `iconRef`, or words through `iconText` (task 0069). Neither tells two
inbox channels apart at a glance: every channel child of `inbox` and `ai-inbox`
is the same white tile with two lines of small text, and the white tile hides
the status color (an accepted risk of 0069).

Emoji would do it, but the pad has no emoji a layer can use:

- `macrodriver emoji` (task 0034) renders one emoji with Pillow and Apple Color
  Emoji. It runs only on macOS, needs Python and Pillow, and sends a custom glyph
  to one key, not a layer node. Apple's glyphs may not be copied into the repo.
- `iconText` draws with `gobold`, which has no emoji: `😀` shows `?` (0069, DoD-4).
- `golang.org/x/image/font/sfnt` cannot read color emoji tables (CBDT, COLR, sbix).

The owner also wants each folder and each session to get its own emoji, the same
one every time, so a key and a terminal can be matched by eye.

## Goals

- `iconRef: "emoji/1f98a.png"` shows a fox on the white round, in any layer,
  with no change to the driver, the wire protocol, or `ValidateIconRef`.
- The emoji are open licensed, committed as PNGs, and rebuilt by a committed script.
- One function maps a string to an emoji. The same input gives the same emoji on
  every machine, in every run, and after the set grows by a few emoji.
- An inbox channel child shows an emoji: its session's for a Claude Code session
  key, else its channel's. `inbox emoji` prints the emoji of a folder or a
  session, the same one the key shows, for a status line or a badge.

## Non-goals

- Any emoji at run time (a font renderer in Go). Only the committed set exists.
- Skin tones, flags, keycaps, and ZWJ sequences. Task 0046 covers typed emoji.
- Changing how the Claude Code hook names channels. Task 0072 (PR #92) does that.
- The Go helper API of task 0055 (PR #65, not merged). See Part 2's Design.
- Pinning a channel to an emoji by hand. See Risks.

## Part 1 — Emoji pictures in the asset library

### Facts that decide it

- `assetPath` already resolves a ref with a folder (`emoji/x.png`) under `assets/`,
  and `renderGlyph` puts any 128x128 RGBA PNG on the white round of radius 58.
  The frame cache keys on the ref (`frameKey`), so 6 keys of the same emoji use one slot.
- `TestIconRound` globs `assets/*.png` only, so files in `assets/emoji/` are not checked yet.
- Spike, 2026-10-19: 16 emoji (fox, octopus, cactus, mushroom, lemon, rocket,
  guitar, key, bee, penguin, rainbow, anchor, compass, basketball, doughnut, light
  bulb) from three sets, rendered with `@resvg/resvg-js` 2.6.2 and scaled so no
  pixel of alpha 0x80 or more lies past 50 px from the centre, on the round of 58
  over the RUNNING blue. All 48 drew; the contact sheet is in the PR thread and at
  `/mnt/project-files/specs/0073-emoji-sets.png`. The flat sets (Fluent, Twemoji)
  read better at 128 px than Noto's shaded one; Noto's compass and bee lose detail.

| Set | Picture licence | npm source | Style |
|---|---|---|---|
| Microsoft Fluent Emoji, flat | MIT | `@lobehub/fluent-emoji-flat` 1.1.0 (SVG) | flat, bold outlines |
| Twemoji (jdecked fork) | CC-BY 4.0, credit required | `@twemoji/svg` 15.0.0 (SVG) | flat |
| Google Noto Emoji | Apache 2.0 | `@svgmoji/noto` 3.2.0 (one sprite) or the repo's `png/128` | shaded |
| OpenMoji | CC-BY-SA 4.0, share-alike | `openmoji` 17.0.0 | line art |

### Approaches considered

#### Approach A — Commit a curated set of PNGs, built once by a script (chosen)

A committed script, `tools/build_emoji_assets.mjs`, reads a pinned npm package,
renders each emoji in `assets/emoji/set.txt` with `@resvg/resvg-js`, scales it to
fit radius 50, and writes `assets/emoji/<codepoints>.png`. The PNGs are committed.

- Good, because the driver, the wire protocol, `iconRef`, and the frame cache do not change.
- Good, because nothing new runs on the owner's machine: no Python, no font, no network at run time.
- Good, because every emoji passes the same round check as the other icons.
- Bad, because only the curated set exists. A new emoji is a script run and a commit.
- Bad, because the repo grows by about 1 MB for 128 PNGs (spike: 4 to 12 KB each).

#### Approach B — A color emoji font in the driver

Embed Noto Color Emoji (CBDT, Apache 2.0, about 10 MB) and read its PNG strikes
in Go: a new `iconEmoji` field, drawn by `renderIcon`.

- Good, because every emoji in the font works, with no curated list.
- Good, because typed emoji (0046) and `iconText` with emoji could use it later.
- Bad, because `x/image` cannot read CBDT; the driver needs its own table parser.
- Bad, because the binary grows by about 10 MB, and the field touches four structs (as `iconText` did).

#### Approach C — Render at run time with the existing Python script

Reuse `tools/render_emoji.py`, writing PNGs into `assets/.generated/`.

- Good, because the script exists and any emoji works.
- Bad, because it is macOS only and Apple's glyphs may not be shipped or committed.
- Bad, because 0069 removed exactly this pattern from `inbox`: generated files that pile up and a shared absolute assets path.

### Decision

Chosen: **Approach A, with Microsoft Fluent Emoji flat as the default set**.

It is the only approach that adds emoji without touching the driver. Fluent is MIT,
needs no credit line on the pad, and reads best at 128 px in the spike. The task
accepts a fixed set and about 1 MB of PNGs. Twemoji is the drop-in alternative:
the script takes `--set twemoji` and writes the same file names.

### Design

- `assets/emoji/set.txt`: one emoji per line, `<codepoints> <name>`, in a fixed
  order, comments with `#`. The v1 set is 128 emoji from the categories animals,
  plants, food, objects, transport, and sport. Rules for a line:
  - one code point, or one plus U+FE0F; no skin tone, flag, keycap, or ZWJ;
  - no face, hand, person, or heart: they look alike at 128 px;
  - no weapon, flag, religious, or political symbol, and no 🍆 🍑 💩;
  - no near twin of an emoji already in the set (🐶 and 🐕, 🍊 and 🍑, 🌕 and 🌝);
  - not mostly white, which vanishes on the round (☁️, 🥚, 🦷), and not mostly
    the RUNNING blue.
- `tools/build_emoji_assets.mjs` (Node 22, run with `npx`, not part of `inbox`):
  pinned `@lobehub/fluent-emoji-flat@1.1.0` and `@resvg/resvg-js@2.6.2`. Per line:
  render at 512 px, crop to the alpha box, scale with premultiplied Lanczos so the
  farthest pixel of alpha 0x80 or more is 50 px from the centre, centre on a
  transparent 128x128, write `assets/emoji/<cp>.png` (lower-case hex joined by `-`,
  as in the npm packages). It also writes `assets/emoji/LICENSE` (the set's licence
  text) and `assets/emoji/README.md` (source, version, the command). A run with no
  change to `set.txt` gives byte-identical files.
- `driver/layers/pip_test.go`, `TestIconRound`: glob `assets/*.png` and
  `assets/emoji/*.png`. An emoji has no dark disc of its own, so only the reach check applies.
- `driver/README.md`: `iconRef: "emoji/<cp>.png"`, and where the set comes from.

## Part 2 — The same emoji for the same folder or session

### Facts that decide it

- `inbox` has no folder. The store keeps the channel and the "here"
  (`iterm2:UUID`, `app:`, `url:`). The Claude Code hook turns the folder into a
  channel: the path below `~/source` joined by dots, else the folder name
  (`inferChannel` in `inbox/src/channel.js`, the same rule in `claude-ai-inbox.sh`).
- So a key can only hash its channel. For a folder and its key to show the same
  emoji, a folder must hash to its channel's emoji.
- A Claude Code session id is a UUID (the `session_id` of every hook payload).
  Task 0072 (PR #92, open) names an ai channel `PROJECT.SID`, where SID is the
  first 8 characters of the session id, so each session gets its own key. The
  store holds only those 8 characters, so a key can hash no more of the id.
- With 128 emoji and 5 children shown, two children share an emoji with
  probability 1 − ∏(1 − i/128) for i in 0..4, about 7.6 %. With 256 it is about 3.9 %.

### Approaches considered

#### Approach A — Rendezvous hashing over the set (chosen)

For input `s`, score each emoji `e` as the first 8 bytes of
`SHA-256(s + "\n" + e)`, read as a big-endian unsigned integer, and pick the
emoji with the highest score (ties: the earlier line of `set.txt`).

- Good, because adding an emoji moves only the inputs that now score it highest
  (about 1 in N+1), and removing one moves only the inputs that had it.
- Good, because SHA-256 is in `node:crypto` and Go's `crypto/sha256`, so a Go copy
  for 0055 can match it byte for byte, with no new dependency.
- Bad, because a lookup hashes N times (128 SHA-256 of a few bytes; under 1 ms,
  and the renderer caches per channel).

#### Approach B — `hash mod N`

`SHA-256(s)` read as an integer, mod the length of the set.

- Good, because it is one hash and three lines.
- Bad, because adding one emoji to the set moves almost every input to a new emoji.

#### Approach C — Assign in order and remember it in the store

The first channel gets the first unused emoji, saved in a new table.

- Good, because the 5 shown children never share an emoji until 128 channels exist.
- Bad, because it is not a function of the input: another machine, or a fresh
  store, gives another emoji, and a status line cannot compute it without the store.
- Bad, because it needs schema 5 and a migration.

### Decision

Chosen: **Approach A — rendezvous hashing with SHA-256**.

It is the only approach where an emoji follows the input alone and survives a
growing set. The task accepts that two shown children share an emoji about 1 time
in 13, and keeps the channel name on the key to tell them apart (see the open
question on the layout).

### Design

**Inputs.** Each input is a kind, a colon, and a normalized value, so a folder and
a session never meet by chance.

| Kind | String hashed | Example |
|---|---|---|
| channel | `channel:` + the channel name | `channel:macro-pad.driver` |
| folder | the channel `inferChannel` gives for the folder, hashed as a channel | `~/source/macro-pad/driver` → `channel:macro-pad.driver` |
| session | `session:` + the first 8 characters of the id, in lower case | `session:3f2a91c0` |

**Helper.** New `inbox/src/emoji.js`:

- `loadEmojiSet(assetsRoot)` reads `assets/emoji/set.txt` once. The inbox finds
  it at `../assets/emoji/set.txt` from its own source, so `--assets-dir` stays unused.
- `emojiFor(input, set)` returns `{ codepoints, char, name, iconRef }`.
- `inbox/test/emoji-vectors.json`: 20 inputs and their emoji. `emoji.test.js`
  checks them, and a later Go copy must pass the same file.

**inbox.** A plugin's rules get `emoji(channel)`, which returns the input to hash.
`basic` returns `channel:<channel>`. `ai` returns `session:<SID>` when the last
segment of the channel is 8 hex digits (a `PROJECT.SID` channel from 0072), else
`channel:<channel>`. `render.js` `setChild` sends that emoji as `iconRef` (and
the name as decided below). The top keys keep `gmail.png` and `ai.png`. So a
session key and that session's status line show the same emoji. Because the round lets the key color show, a channel
child shows its status color again.

**CLI.** `inbox emoji [--channel NAME | --folder PATH | --session UUID] [--format char|name|ref|json]`.
With no flag it uses the current folder. It prints `🦊` by default. It needs no
store and no daemon, so a Claude Code status line can call it on every refresh.
`-h/--help` and the zsh completion cover it.

**Go.** No Go copy in this task. When 0055 lands, its helper API gets
`EmojiFor(kind, value)` that reads the same `set.txt` and passes
`emoji-vectors.json`.

Files to change:

- new `assets/emoji/*.png`, `assets/emoji/set.txt`, `assets/emoji/LICENSE`, `assets/emoji/README.md`
- new `tools/build_emoji_assets.mjs`
- `driver/layers/pip_test.go` — check `assets/emoji/` in `TestIconRound`
- new `inbox/src/emoji.js`, `inbox/test/emoji.test.js`, `inbox/test/emoji-vectors.json`
- `inbox/src/plugins/basic.js`, `inbox/src/plugins/ai.js` — `emoji(channel)`
- `inbox/src/render.js`, `inbox/src/cli.js`, `inbox/completions/_inbox`, `inbox/test/render.test.js`, `inbox/test/daemon.test.js`
- `inbox/README.md`, `driver/README.md`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — `assets/emoji/` holds one 128x128 RGBA PNG per line of `set.txt`, at least 128, plus `LICENSE` and `README.md`. **Proof:** `file assets/emoji/*.png | grep -vc '128 x 128, 8-bit/color RGBA'` prints 0; `wc -l` of the non-comment lines of `set.txt` equals the PNG count.
- [ ] **DoD-2** — The script rebuilds the set byte for byte. **Proof:** `npx node tools/build_emoji_assets.mjs && git status --porcelain assets/emoji` prints nothing.
- [ ] **DoD-3** — Every emoji lies inside the round: no pixel of alpha 0x80 or more past 57.5 px from the centre. **Proof:** `go test ./layers -run TestIconRound` (now globbing `assets/emoji/`).
- [ ] **DoD-4** — `{"kind":"setLayerKey","setLayerKey":{"id":"t","iconRef":"emoji/1f98a.png"}}` shows the fox on the white round with no driver change. **Proof:** `git diff main...HEAD --stat -- driver/layers/*.go` lists only `pip_test.go`; a golden `driver/layers/testdata/icon-emoji.png` from `TestIconEmoji`.
- [ ] **DoD-5** — `set.txt` has no skin tone, flag, keycap, ZWJ, face, hand, or person. **Proof:** `node --test inbox/test/emoji.test.js` (a check of each line against those code point ranges).
- [ ] **DoD-6** — `emojiFor` gives the emoji in `emoji-vectors.json` for all 20 inputs, and the same emoji for `--folder ~/source/macro-pad/driver` and `--channel macro-pad.driver`. **Proof:** `node --test inbox/test/emoji.test.js`
- [ ] **DoD-7** — Adding one emoji to the end of the set moves fewer than 3 % of 10 000 random inputs; removing one moves only the inputs that had it. **Proof:** the stability case in `inbox/test/emoji.test.js`.
- [ ] **DoD-8** — The inbox sends each channel child its emoji as `iconRef`, and the top keys keep `gmail.png` and `ai.png`. **Proof:** `cd inbox && npm test` (`render.test.js`, `daemon.test.js`).
- [ ] **DoD-9** — `inbox emoji --session 3F2A91C0-…` prints the emoji of `session:3f2a91c0`, the same one `--plugin ai` sends for channel `macro-pad.3f2a91c0`, and exits 0 with no store file; a bad UUID exits 2 with a usage line; `--help` lists the command. **Proof:** `inbox/test/cli.test.js`
- [ ] **DoD-10** — `inbox/README.md` and `driver/README.md` describe `emoji/<cp>.png`, the set's source and licence, the hash, and `inbox emoji`. **Proof:** `grep -n "emoji" inbox/README.md driver/README.md`
- [ ] **DoD-11** — On the board, the channel children of `inbox` and `ai-inbox` show their emoji on the round with the status color around it, and the owner can tell 5 children apart. **Proof:** the owner's note in this spec's Notes.

## Risks

- Two shown children share an emoji about 1 time in 13 → the name stays on the key
  (open question 2); if it bites, a later task adds `inbox icon CHANNEL EMOJI` to pin one.
- Fluent is a Microsoft set whose npm copy is a third-party repackage → the script
  pins the version and the README names the upstream repo
  (`microsoft/fluentui-emoji`, MIT); Twemoji is the fallback.
- A future upstream redraw changes the pictures, not the mapping → the version is
  pinned; a bump is its own commit, and the codepoints, so the hash, stay.
- 128 new frames could crowd the board's 64 slots (task 0063) → only shown keys
  use slots, and the frame cache is keyed by ref, as today.

## Open questions

1. ~~**Set (owner):** Fluent flat, Twemoji, or Noto?~~ Settled 2026-10-19: the
   owner picked **Fluent flat** after the contact sheet.
2. **Layout of a channel child (owner):** the emoji alone (default: no driver
   change, the status color shows again), or the emoji with the channel name in
   one small line under it? The second needs `iconRef` and `iconText` together,
   which 0069 made exclusive, and a new text box below the emoji.
3. **What a session key hashes (owner):** the session (default: each session its
   own emoji, matching its status line), or the folder (every session of one
   project shares an emoji, and the sessions are told apart by the name only)?

## Notes

- Spike files (not committed): `render.mjs` and `sheet.py` rendered the contact sheet.
