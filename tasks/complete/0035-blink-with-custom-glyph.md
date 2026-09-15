---
id: "0035"
title: "Let a key blink while it shows a custom-glyph image"
status: "complete"
created: "2026-09-13"
updated: "2026-09-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/39"
branch: "0035-blink-with-custom-glyph"
related: ["0030", "0034"]
tags: ["firmware", "driver", "display"]
---

# 0035 — Let a key blink while it shows a custom-glyph image

## Problem

A key cannot blink while it shows a custom image today. A Key state
message sets color, emoji, and blink together, and always clears the
custom image as a side effect. A Set custom glyph message never sets
blink. Confirmed live: turning on blink erases the custom image.

## Goals

- A key blinks between its custom image and a blank frame, driven by
  one command from a plugin.
- A blink toggle does not resend the 32KB image on every toggle.

## Non-goals

- A blink rate other than the fixed interval `BLINK_INTERVAL_US`
  already sets for the built-in glyph table.
- Blink for more than one key at a time. This task changes the
  single-key wire path and logic, not a multi-key scheduler.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Key state keeps a custom image when it names the sentinel

Change `firmware/app.py`'s `_apply_host_report`. When a Key state
message's Emoji ID is `wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID`, keep
`key_state.pixels` instead of clearing it. A plugin then sends one Key
state message with `EmojiID=0xFE` and `Blink=true` to blink an image
already in place.

- Good, because it needs one guard clause in `firmware/app.py`, with no
  new wire message.
- Good, because a caller sends 6 bytes (Key state) to toggle blink, not
  32,768 bytes (Set custom glyph).
- Bad, because `docs/wire-protocol.md` says the driver never sends
  `0xFE` itself. This task must lift that rule for the blink case, and
  update the doc.
- Bad, because `driver/api/state.go`'s `SetEmoji` and `SetState` do not
  let a caller name `0xFE`. This task needs a new or changed driver
  helper.

### Approach B — Add a Blink flag to the Set custom glyph message

Add one byte to the Set custom glyph payload (`docs/wire-protocol.md`,
`firmware/wire.py`, `driver/transport/glyph.go`) that carries the blink
flag with the image, in the same message.

- Good, because one message still sets the full state for a custom
  key, matching how Key state already sets color, emoji, and blink
  together.
- Good, because no rule about `0xFE` changes. The sentinel still means
  what `docs/wire-protocol.md` says today.
- Bad, because it changes an already-implemented wire message. Every
  caller of `SendCustomGlyph` and `toPixels` needs a new parameter.
- Bad, because toggling blink still means one full 32,768-byte payload
  over CDC serial for every toggle, unless a caller also builds a
  small "same image, new blink flag" path.

### Approach C — The driver times the blink on the host, and resends

`macropadd` owns a blink timer per key. It sends the full image, then a
blank Set custom glyph payload, on a fixed interval, with no firmware
change.

- Good, because firmware and the wire protocol stay exactly as they
  are. All the work sits in `driver/plugin/server.go`.
- Good, because a plugin author sees a plain `blink: true` field on the
  existing `setCustomGlyph` message, with no new wire type to learn.
- Bad, because it writes to flash on every blink toggle.
  `firmware/glyph_state.py`'s `_persist_key_state` writes only on a
  change, so a blink timer looks like a real change each time. Task
  0030's Risks already name this flash-wear cost.
- Bad, because the CDC link now carries a 32,768-byte frame on the
  fixed blink interval, for as long as that key keeps blinking.

## Decision

Chosen: **Approach A — Key state keeps a custom image when it names the
sentinel**.

Goal 2 rules out Approach B and C: both resend the full image on a
toggle, or on a schedule. Approach A's cost is a documented exception
to the "driver never sends `0xFE`" rule, accepted because that rule
protects against a driver claiming a custom image it never uploaded,
not against a driver toggling blink on one it already did.

## Design

Files to change:

- `firmware/app.py` — in `_apply_host_report`, set `key_state.pixels =
  None` only when `message.emoji_id != wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID`.
- `docs/wire-protocol.md` — change the Emoji IDs table's `0xFE` row.
  State that a Key state message can name it, to toggle blink or color
  on an image already in place, and that doing so with no prior Set
  custom glyph message for that key leaves the key blank.
- `driver/api/state.go` — add `SetCustomGlyphBlink(key int, blink bool)
  error`, sending a Key state message with `EmojiID:
  transport.CustomGlyphSentinelEmojiID` and the given blink flag, at
  the key's current color.
- `driver/transport/transport.go` — export
  `CustomGlyphSentinelEmojiID = 0xFE`, so driver code names the
  sentinel instead of a bare literal.

## Definition of done

- [x] **DoD-1** — After a Set custom glyph message, a Key state message
  naming `0xFE` and `Blink=true` keeps the image and blinks it.
  **Proof:** a firmware pytest sends both messages through `MacroPad`
  and asserts `key_state.pixels` is unchanged and `key_state.blink` is
  `True`.
- [x] **DoD-2** — A Key state message naming a built-in Emoji ID still
  clears `pixels`, matching today's behavior. **Proof:** a firmware
  pytest asserts `key_state.pixels is None` after such a message.
- [x] **DoD-3** — `SetCustomGlyphBlink` sends no image bytes, only the
  6-byte Key state message. **Proof:** a driver test asserts the sent
  message has no `SetCustomGlyph` payload.
- [x] **DoD-4** — Tests cover the sentinel-preserving path and the
  built-in-ID-clearing path. **Proof:** `pytest test/test_app.py -k
  custom_glyph` passes. `git stash && pytest test/test_app.py -k
  custom_glyph` fails on `main`.
- [x] **DoD-5** — `docs/wire-protocol.md` records the changed meaning
  of a Key state message naming `0xFE`. **Proof:** `docs/wire-
  protocol.md`, "Emoji IDs".
- [x] **DoD-6** — The PR in the `pr` field links to this spec.
  **Proof:** PR body.

## Risks

- A Key state message naming `0xFE` for a key with no stored image
  today has no defined behavior → `render_key`'s existing `pixels is
  None` branch falls back to `emoji_lookup`, which already treats an
  unknown ID as the placeholder glyph. Document this as the defined
  behavior, not an error case.

## Notes

Found live, this session, testing task 0030's custom-glyph pipeline on
real hardware: sending `blink=true` after a working custom image
erased it. `firmware/display_render.py`'s `render_key` already renders
a blinking custom image correctly when both fields are set on one
`KeyState` — the renderer was never the blocker.
