---
id: "0086"
title: "Nest a child layer inside a child layer"
status: "ongoing"
created: "2026-10-27"
updated: "2026-10-27"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-z0nk55"
related: ["0057", "0066", "0067", "0075", "0078", "0085"]
tags: ["layers", "driver", "protocol", "inbox"]
---

# 0086 — Nest a child layer inside a child layer

## Problem

A top-level key opens a child layer, and a child has no children of its
own (task 0057). `layers.Doc.Validate` rejects a grandchild with "a child has
no children", so every client keeps its tree two levels deep.

The apps plugin of task 0085 (PR #109) shows the cost. It wants one "Apps"
key whose children are the active app repos, each of which opens five
actions: Code, Claude, Run, Pull, and Term. With no grandchildren, each app
takes a whole top-level key, so three apps and the three inbox keys fill the
pad, and a fourth app has nowhere to go.

Keith asked on 2026-10-27: "We need to support a nested layer."

## Key numbers

As in task 0078, Keith counts keys from 1 and the code from 0. Key 4/3 is
the bottom-left key, the up key, and key 6/5 the bottom-right key.

## Goals

- A child can hold children of its own, down to a fixed depth.
- A press on a child with children opens its layer, on page 1, as a press on
  a top-level key with children does today.
- The up key on page 1 of a layer goes back one level, to the layer it was
  opened from, on the page it showed then. From a first-level layer it goes
  to the top layer, as today.
- Paging (task 0078) works the same on every level.
- A client names a node at any depth, and learns which layer is open and
  which child was pressed at any depth.
- A tree two levels deep looks, acts, and sends exactly what it does today.
- The open layer, with its page and the pages above it, survives a
  `macropadd` restart.

## Non-goals

- Moving the apps plugin under one "Apps" key. That is task 0085's change,
  on PR #109, once this lands.
- A breadcrumb, a depth marker, or a "this key opens" badge on a key. See
  Open questions.
- A long press on the up key to go to the top layer. See Open questions.
- Firmware changes. Nothing new is drawn.

## Approaches considered

### Approach A — A path of ids

A node is named by the ids from the top-level key down to it. Ids stay
unique among siblings, as today. `setLayerKey` and `removeLayerKey` take a
`path` of ancestor ids beside the `parent` they have today; `parent: "x"`
means `path: ["x"]`. `layers.json` saves `active` as the path of the open
layer.

- Pro: no id that is valid today becomes invalid. The inbox's children are
  channel names, and two keys can hold a child with the same name.
- Pro: a two-level tree sends the same messages as today.
- Con: every message about a deep node carries the whole path.

### Approach B — Ids unique across the whole tree

`parent` names any node by its id, at any depth.

- Pro: the messages stay as short as today.
- Con: ids that are fine today collide. Two inbox plugins could each show a
  channel named `main`. Every client would need to prefix its ids, and a
  `layers.json` saved today could fail to load.

### Approach C — Clients fake depth

A client swaps the children of its key when a child is pressed, as if the
child had opened a layer, and keeps its own stack for the up key.

- Pro: no change to `macropadd`.
- Con: every client writes its own navigation, and the up key, which
  `macropadd` owns, would return to the top layer, not one level.
- Con: each swap redraws and resends every child, so it is slow and it
  flashes.

## Decision

Approach A. The cost is a `path` field on three messages, and a Doc whose
`active` is a list.

## Design

### Limits

- `layers.MaxDepth` is 4: a top-level key, its children, their children, and
  one more level. A node on level 4 has no children.
- `layers.MaxChildren` stays 20 for every node.
- `layers.MaxNodes` is 200 for the whole tree, so a tree cannot hold 20⁴
  nodes and starve the preload.

A tree that breaks a limit is rejected, as a sixth child was before task
0078.

### Layer object

```json
{"version": 1, "active": ["apps", "app.pad"], "page": 0, "parentPages": [1],
 "keys": [{"id": "apps", "iconEmoji": ["1f4e6"], "iconText": "apps",
           "children": [{"id": "app.pad", "iconText": "macro-pad",
                         "children": [{"id": "code", "iconText": "code"}]}]},
          null, null, null, null, null]}
```

- `active` is `null` on the top layer. Otherwise it is the path of the open
  node: the id of a top-level key, then the id of each child down to it.
  Every node on it has children.
- `page` is the page of the open layer, from 0, as in task 0078.
- `parentPages` holds the page each layer above the open one showed when the
  next one opened, one entry per layer above it, so it has one fewer entry
  than `active`. It is left out at depth 1.
- A `layers.json` saved before this task has `active` as a string. It still
  loads, as a path of one id.

### Presses

| Press | Result |
|---|---|
| top layer, a key with children | opens that key's layer, on page 1 |
| a layer, a child with children | opens that child's layer, on page 1; remembers this page |
| a layer, a child with no children | sends `childPressed`; the layer stays |
| a layer, key 4/3 on page 1 | goes up one level, to the page it showed |
| a layer, the left or the right arrow | the page before, or the next page |
| anything else | nothing |

A press that opens a layer sends `layerChanged`, not `childPressed`, as a
press on a top-level key does today. A child with children looks like any
other child.

### Changes while a layer is open

- An update to a child keeps its children, as an update to a top-level key
  does today.
- A removal of the open node, of an ancestor of it, or of the last child of a
  layer on the path, goes up to the deepest layer that is still on the path
  and still has children, on the page it showed (clamped to its last page),
  and sends `layerChanged`. At depth 1 that is the top layer, as today.
- Every page in `page` and `parentPages` is clamped to its layer's last page
  after each change, as `page` is today.

### Protocol

- `setLayerKey` and `removeLayerKey` take `path`, the ids from a top-level
  key down to the parent of the node: `{"id": "code", "path": ["apps",
  "app.pad"]}`. `parent: "apps"` is still the same as `path: ["apps"]`. A
  message with both is dropped. A path to a node that does not exist drops a
  `setLayerKey`, and makes a `removeLayerKey` change nothing.
- `layerChanged` gains `path`: the path of the open layer, `[]` on the top
  layer. `active` stays the id of the open layer, the last id of `path`, or
  `null`, so a client that reads only `active` sees what it saw before at
  depth 1.
- `childPressed` gains `path`: the ids from the top-level key down to the
  pressed child's parent. `parent` stays the id of that parent, the last id
  of `path`.

```json
{"kind": "layerChanged", "layerChanged": {"active": "app.pad", "path": ["apps", "app.pad"], "page": 0, "pages": 1}}
{"kind": "childPressed", "childPressed": {"keyIndex": 0, "parent": "app.pad", "child": "code", "path": ["apps", "app.pad"]}}
```

### Driver API

- `layers.Update` gains `Path []string`; `Parent` stays as a shorthand for a
  path of one id.
- `Controller.RemoveNode(id, path)` removes a node at any depth.
  `RemoveKey(id, parent)` stays and calls it.
- `Controller.PressNode(key)` returns the path to a pressed child's parent
  and the child's id. `Press(key)` stays and returns the parent's id.
- `Controller.LayerPath()` returns the open path, its page, and its page
  count. `Layer()` and `Active()` stay, with the open node's id.
- `Out.LayerChanged` takes the path.

### Frame cache

The preload stores the top layer, then page 1 of every layer at every depth,
breadth first, then the later pages, until the frame table is full, as task
0078 does for one level.

### Inbox

- `inbox/src/protocol.js` reads `path` from `layerChanged` (falling back to
  `[active]` when it is missing) and from `childPressed`, and
  `removeLayerKeyMessage` takes a path as well as a parent.
- The renderer treats its key's branch as open when the open path starts at
  its key, not only when its own key is the open layer, so it moves no child
  while a person is inside a grandchild layer.
- The daemon hands `path` to `onEvent` with `active`.

The inbox plugins on main send no grandchildren, so nothing they draw
changes.

## Definition of done

- [x] DoD-1: `layers.Doc.Validate` accepts children down to `MaxDepth`, and
  rejects a node deeper than that, a tree of more than `MaxNodes` nodes, a
  duplicate id among siblings at any depth, and an `active` path that does
  not end on a node with children.
- [x] DoD-2: A `layers.json` with `active` as a string loads as a path of one
  id, and one with a path loads and reopens the same layer, page, and
  `parentPages`.
- [x] DoD-3: A `setLayerKey` with `path` adds or updates a grandchild, and an
  update to a child keeps the child's children.
- [x] DoD-4: A press on a child with children opens its layer and sends
  `layerChanged` with the path, and no `childPressed`.
- [x] DoD-5: The up key on page 1 of a grandchild layer returns to the child
  layer, on the page it showed before.
- [x] DoD-6: A press on a grandchild with no children sends `childPressed`
  with `parent` and `path`.
- [x] DoD-7: Removing the open node, an ancestor of it, or the last child of
  a layer on the path, goes up to the deepest layer left on the path.
- [x] DoD-8: A tree two levels deep sends the same messages as before; the
  existing driver and inbox tests pass unchanged except for the new `path`
  fields.
- [x] DoD-9: The inbox reads `path` from `layerChanged` and `childPressed`,
  and keeps its children still while its branch is open at any depth.
- [x] DoD-10: `driver/README.md` and `inbox/README.md` document the depth,
  the `path` fields, and the press table.
- [ ] DoD-11: On the board, a three-level tree opens to the grandchild layer
  and returns one level at a time with key 4.

## Risks

- A deep tree has more frames than the frame table holds, so the preload
  misses some and a first visit sends images. `MaxNodes` bounds it, and
  breadth-first order keeps the first pages cached.
- A client that reads `childPressed.parent` and assumes it is a top-level key
  could mistake a grandchild for a child of its own key with the same id.
  Only a client that adds grandchildren can see this, and it should read
  `path`.

## Open questions

- [ ] Should a key that opens a layer look different from a leaf, such as a
  small corner mark? The default is no. — kgheacock
- [ ] Should a long press on the up key go straight to the top layer? The
  default is no: one press, one level. — kgheacock
- [ ] Is a depth of 4 right? The apps plugin needs 3. The default is 4. —
  kgheacock

## Notes

Keith asked on 2026-10-27, from the apps thread (task 0085), for nested
layers so that apps sit under one key. The spec and the code are in one PR
with the defaults above, so Keith can review both together.

Implementation notes, 2026-10-27:

- DoD-1 to DoD-10 are ticked by the author and wait for a reviewer.
  `driver/layers/nested_test.go` covers DoD-2 to DoD-7; the plugin fixture
  test and `inbox/test/protocol.test.js` read the same `path` examples.
- `cd driver && go vet ./... && go test ./...` passes but for `TestIconText`,
  which fails the same way on main in this container (one gray level of font
  antialiasing). `cd inbox && npm test` passes, 139 tests, on Node 22.22.
- DoD-11, the check on the board, waits for Keith.
