// Package layers holds the JSON object that says what the macro pad's six
// keys show, and the child layer each key can open. See task 0057 and the
// "Layers" section of driver/README.md.
//
// The daemon owns one Controller. Clients change it with setLayerKey
// messages, a press opens or closes a child layer, and the
// Controller draws only the keys that changed.
package layers

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	// Version is the only layers.json version this package reads.
	Version = 1
	// KeyCount is the number of keys on the pad, and the most top-level
	// keys a document holds.
	KeyCount = 6
	// MaxChildren is the most children one node holds. A child layer with
	// more than OnePage children has pages (task 0078).
	MaxChildren = 20
	// MaxDepth is the most levels a tree has: a top-level key is level 1,
	// and a node on level MaxDepth has no children (task 0086).
	MaxDepth = 4
	// MaxNodes is the most nodes the whole tree holds (task 0086).
	MaxNodes = 200
	// UpKey is the key that shows the shift icon in a child layer. A
	// press on it returns to the parent layer. On a page after the first it
	// shows the left arrow instead.
	UpKey = 3
	// LeftKey shows the left arrow on a page after the first. A press on it
	// shows the page before.
	LeftKey = UpKey
	// RightKey shows the right arrow on every page but the last. A press on
	// it shows the next page.
	RightKey = 5
)

// Status names. An empty status is also valid: the key has no status.
const (
	StatusRunning = "RUNNING"
	StatusDone    = "DONE"
	StatusError   = "ERROR"
	StatusWarn    = "WARN"
)

// ValidStatus reports whether s is a status a node may carry.
func ValidStatus(s string) bool {
	switch s {
	case "", StatusRunning, StatusDone, StatusError, StatusWarn:
		return true
	}
	return false
}

// Node is a top-level key or a child. A child has the same fields, and
// children of its own down to MaxDepth (task 0086).
type Node struct {
	ID      string `json:"id"`
	Icon    byte   `json:"icon"`
	Status  string `json:"status"`
	Count   int    `json:"count"`
	IconRef string `json:"iconRef"`
	// IconText is words drawn as the icon instead of a PNG (task 0069). It
	// excludes IconRef.
	IconText string `json:"iconText,omitempty"`
	// IconEmoji is one or two pictures under assets/emoji/, with IconText as
	// a caption of one line (task 0073). A top-level key shows them side by
	// side on the round; a key in a child layer shows a hero and a sticker
	// over a strip of the key color (task 0082). It excludes IconRef.
	IconEmoji []string `json:"iconEmoji,omitempty"`
	Children  []Node   `json:"children,omitempty"`
}

// hasContent reports whether n shows anything. A key with no content is a
// reserved slot: the controller does not draw it.
func (n *Node) hasContent() bool {
	return n.Status != "" || n.Icon != 0 || n.Count != 0 || n.IconRef != "" || n.IconText != "" || len(n.IconEmoji) > 0 || len(n.Children) > 0
}

// Doc is the whole layers.json object.
type Doc struct {
	Version int `json:"version"`
	// Active is nil for the top layer. Otherwise it is the path of the node
	// whose children the pad shows: the id of a top-level key, then the id
	// of each child down to it (task 0086).
	Active []string `json:"active"`
	// Page is the page of the active layer the pad shows, from 0. It is 0
	// on the top layer (task 0078).
	Page int `json:"page"`
	// ParentPages is the page each layer above the active one showed when
	// the next one opened, so it has one entry fewer than Active.
	ParentPages []int `json:"parentPages,omitempty"`
	// Keys is indexed by key position. A nil entry is an empty key.
	Keys []*Node `json:"keys"`
}

// newDoc returns an empty document.
func newDoc() Doc {
	return Doc{Version: Version, Keys: make([]*Node, KeyCount)}
}

// UnmarshalJSON reads a Doc. An active saved before task 0086 is a string,
// and reads as a path of one id.
func (d *Doc) UnmarshalJSON(data []byte) error {
	type plain Doc
	var raw struct {
		plain
		Active json.RawMessage `json:"active"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*d = Doc(raw.plain)
	d.Active = nil
	if len(raw.Active) == 0 || string(raw.Active) == "null" {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw.Active, &one); err == nil {
		d.Active = []string{one}
		return nil
	}
	return json.Unmarshal(raw.Active, &d.Active)
}

// clone returns a deep copy of d.
func (d Doc) clone() Doc {
	out := Doc{Version: d.Version, Page: d.Page, Keys: make([]*Node, len(d.Keys))}
	if d.Active != nil {
		out.Active = append([]string{}, d.Active...)
	}
	if d.ParentPages != nil {
		out.ParentPages = append([]int{}, d.ParentPages...)
	}
	for i, k := range d.Keys {
		if k == nil {
			continue
		}
		n := k.clone()
		out.Keys[i] = &n
	}
	return out
}

// clone returns a deep copy of n and its children.
func (n Node) clone() Node {
	if n.Children == nil {
		return n
	}
	children := make([]Node, len(n.Children))
	for i := range n.Children {
		children[i] = n.Children[i].clone()
	}
	n.Children = children
	return n
}

// padKeys makes d.Keys exactly KeyCount long, so a position always indexes.
func (d *Doc) padKeys() {
	for len(d.Keys) < KeyCount {
		d.Keys = append(d.Keys, nil)
	}
}

// find returns the position of the top-level key with this id, or -1.
func (d *Doc) find(id string) int {
	for i, k := range d.Keys {
		if k != nil && k.ID == id {
			return i
		}
	}
	return -1
}

// nodeAt returns the node at path, the id of a top-level key and then the id
// of each child down to it, or nil.
func (d *Doc) nodeAt(path []string) *Node {
	if len(path) == 0 {
		return nil
	}
	i := d.find(path[0])
	if i < 0 {
		return nil
	}
	n := d.Keys[i]
	for _, id := range path[1:] {
		n = n.child(id)
		if n == nil {
			return nil
		}
	}
	return n
}

// child returns the child of n with this id, or nil.
func (n *Node) child(id string) *Node {
	for i := range n.Children {
		if n.Children[i].ID == id {
			return &n.Children[i]
		}
	}
	return nil
}

// open returns the node whose children the pad shows, or nil on the top
// layer.
func (d *Doc) open() *Node {
	return d.nodeAt(d.Active)
}

// Parse decodes and validates a layers.json file.
func Parse(data []byte) (Doc, error) {
	var d Doc
	if err := json.Unmarshal(data, &d); err != nil {
		return Doc{}, fmt.Errorf("layers: parse: %w", err)
	}
	if err := d.Validate(); err != nil {
		return Doc{}, err
	}
	d.padKeys()
	return d, nil
}

// Validate checks d against the rules in task 0057: at most six keys, at
// most MaxChildren children each, no node deeper than MaxDepth and no more
// than MaxNodes in all (task 0086), known statuses, ids unique among
// siblings, iconRefs that stay under assets/, an active path that ends on a
// node with children, and pages that its layers have.
func (d *Doc) Validate() error {
	if d.Version != Version {
		return fmt.Errorf("layers: version %d, want %d", d.Version, Version)
	}
	if len(d.Keys) > KeyCount {
		return fmt.Errorf("layers: %d keys, at most %d", len(d.Keys), KeyCount)
	}
	seen := make(map[string]bool)
	nodes := 0
	for i, k := range d.Keys {
		if k == nil {
			continue
		}
		where := fmt.Sprintf("key %d", i)
		if err := k.validate(); err != nil {
			return fmt.Errorf("layers: %s: %w", where, err)
		}
		if seen[k.ID] {
			return fmt.Errorf("layers: %s: duplicate id %q", where, k.ID)
		}
		seen[k.ID] = true
		if err := k.validateChildren(where, 1, &nodes); err != nil {
			return err
		}
	}
	if d.Active != nil {
		if len(d.ParentPages) != len(d.Active)-1 {
			return fmt.Errorf("layers: %d parent pages for an active path of %d, want %d", len(d.ParentPages), len(d.Active), len(d.Active)-1)
		}
		for depth := 1; depth <= len(d.Active); depth++ {
			path := d.Active[:depth]
			n := d.nodeAt(path)
			if n == nil || len(n.Children) == 0 {
				return fmt.Errorf("layers: active %q is not a key with children", strings.Join(path, "/"))
			}
			page := d.Page
			if depth < len(d.Active) {
				page = d.ParentPages[depth-1]
			}
			if pages := PageCount(len(n.Children)); page < 0 || page >= pages {
				return fmt.Errorf("layers: page %d of %q, which has %d pages", page, strings.Join(path, "/"), pages)
			}
		}
	} else if d.Page != 0 || len(d.ParentPages) != 0 {
		return fmt.Errorf("layers: page %d on the top layer, want 0", d.Page)
	}
	return nil
}

// validateChildren checks the children of n, a node on level depth, and
// theirs, and adds n and every node under it to *nodes.
func (n *Node) validateChildren(where string, depth int, nodes *int) error {
	*nodes++
	if *nodes > MaxNodes {
		return fmt.Errorf("layers: more than %d nodes", MaxNodes)
	}
	if len(n.Children) == 0 {
		return nil
	}
	if depth >= MaxDepth {
		return fmt.Errorf("layers: %s (%s): a node on level %d has no children", where, n.ID, MaxDepth)
	}
	if len(n.Children) > MaxChildren {
		return fmt.Errorf("layers: %s (%s): %d children, at most %d", where, n.ID, len(n.Children), MaxChildren)
	}
	childSeen := make(map[string]bool)
	for j := range n.Children {
		c := &n.Children[j]
		cwhere := fmt.Sprintf("%s (%s) child %d", where, n.ID, j)
		if err := c.validate(); err != nil {
			return fmt.Errorf("layers: %s: %w", cwhere, err)
		}
		if childSeen[c.ID] {
			return fmt.Errorf("layers: %s: duplicate id %q", cwhere, c.ID)
		}
		childSeen[c.ID] = true
		if err := c.validateChildren(cwhere, depth+1, nodes); err != nil {
			return err
		}
	}
	return nil
}

// validate checks the fields one node carries on its own.
func (n *Node) validate() error {
	if n.ID == "" {
		return errors.New("empty id")
	}
	if !ValidStatus(n.Status) {
		return fmt.Errorf("unknown status %q", n.Status)
	}
	if n.Count < 0 {
		return fmt.Errorf("negative count %d", n.Count)
	}
	if err := ValidateIconRef(n.IconRef); err != nil {
		return err
	}
	if err := ValidateIconText(n.IconText); err != nil {
		return err
	}
	if err := ValidateIconEmoji(n.IconEmoji); err != nil {
		return err
	}
	if len(n.IconEmoji) > 0 {
		if n.IconRef != "" {
			return errors.New("iconRef and iconEmoji are exclusive")
		}
		if err := validateIconCaption(n.IconText); err != nil {
			return err
		}
	} else if n.IconRef != "" && n.IconText != "" {
		return errors.New("iconRef and iconText are exclusive")
	}
	return nil
}

// ValidateIconRef accepts an empty ref, or a relative path that stays under
// the assets directory. A path that is absolute or leaves assets/ is
// rejected.
func ValidateIconRef(ref string) error {
	if ref == "" {
		return nil
	}
	if strings.ContainsRune(ref, '\\') || path.IsAbs(ref) {
		return fmt.Errorf("iconRef %q must be a relative path under assets/", ref)
	}
	clean := path.Clean(ref)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("iconRef %q leaves assets/", ref)
	}
	return nil
}
