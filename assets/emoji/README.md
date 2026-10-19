# Emoji icons

One 128x128 RGBA PNG per line of `set.txt`, named `<codepoints>.png`
(task 0073). A key shows one with `iconRef: "emoji/1f98a.png"`, or one or two
with `iconEmoji`. The inbox picks them for folders and Claude Code sessions
(`inbox/src/emoji.js`).

- Source: Microsoft Fluent Emoji, flat style, MIT (see `LICENSE`), from the npm
  package `@lobehub/fluent-emoji-flat` 1.1.0.
- Each emoji is scaled so that its farthest pixel of alpha 0x80 or more lies
  50 px from the centre, inside the white round of radius 58.

To add or change an emoji, edit `set.txt` and run:

```
cd tools/emoji && npm ci && npm run build
```

The script writes the same bytes for an unchanged list, and removes the PNG of
a line that is gone. Removing or changing a line moves the folders and sessions
that had that emoji; adding one moves about 1 in the size of the set.
