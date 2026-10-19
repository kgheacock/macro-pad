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
	// MaxChildren is the most children one key holds.
	MaxChildren = 5
	// UpKey is the key that shows the shift icon in a child layer. A
	// press on it returns to the parent layer.
	UpKey = 3
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

// Node is a top-level key or a child. A child has the same fields, and no
// children of its own.
type Node struct {
	ID      string `json:"id"`
	Icon    byte   `json:"icon"`
	Status  string `json:"status"`
	Count   int    `json:"count"`
	IconRef string `json:"iconRef"`
	// IconText is words drawn as the icon instead of a PNG (task 0069). It
	// excludes IconRef.
	IconText string `json:"iconText,omitempty"`
	// IconEmoji is one or two pictures under assets/emoji/, side by side on
	// the round, with IconText as a caption of one line (task 0073). It
	// excludes IconRef.
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
	// Active is nil for the top layer. Otherwise it is the id of the
	// top-level key whose children the pad shows.
	Active *string `json:"active"`
	// Keys is indexed by key position. A nil entry is an empty key.
	Keys []*Node `json:"keys"`
}

// newDoc returns an empty document.
func newDoc() Doc {
	return Doc{Version: Version, Keys: make([]*Node, KeyCount)}
}

// clone returns a deep copy of d.
func (d Doc) clone() Doc {
	out := Doc{Version: d.Version, Keys: make([]*Node, len(d.Keys))}
	if d.Active != nil {
		a := *d.Active
		out.Active = &a
	}
	for i, k := range d.Keys {
		if k == nil {
			continue
		}
		n := *k
		n.Children = append([]Node(nil), k.Children...)
		out.Keys[i] = &n
	}
	return out
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
// most five children each, no grandchildren, known statuses, unique ids,
// iconRefs that stay under assets/, and an active layer that names a key
// with children.
func (d *Doc) Validate() error {
	if d.Version != Version {
		return fmt.Errorf("layers: version %d, want %d", d.Version, Version)
	}
	if len(d.Keys) > KeyCount {
		return fmt.Errorf("layers: %d keys, at most %d", len(d.Keys), KeyCount)
	}
	seen := make(map[string]bool)
	for i, k := range d.Keys {
		if k == nil {
			continue
		}
		if err := k.validate(); err != nil {
			return fmt.Errorf("layers: key %d: %w", i, err)
		}
		if seen[k.ID] {
			return fmt.Errorf("layers: key %d: duplicate id %q", i, k.ID)
		}
		seen[k.ID] = true
		if len(k.Children) > MaxChildren {
			return fmt.Errorf("layers: key %d (%s): %d children, at most %d", i, k.ID, len(k.Children), MaxChildren)
		}
		childSeen := make(map[string]bool)
		for j := range k.Children {
			c := &k.Children[j]
			if err := c.validate(); err != nil {
				return fmt.Errorf("layers: key %d (%s) child %d: %w", i, k.ID, j, err)
			}
			if len(c.Children) > 0 {
				return fmt.Errorf("layers: key %d (%s) child %d: a child has no children", i, k.ID, j)
			}
			if childSeen[c.ID] {
				return fmt.Errorf("layers: key %d (%s) child %d: duplicate id %q", i, k.ID, j, c.ID)
			}
			childSeen[c.ID] = true
		}
	}
	if d.Active != nil {
		i := d.find(*d.Active)
		if i < 0 || len(d.Keys[i].Children) == 0 {
			return fmt.Errorf("layers: active %q is not a key with children", *d.Active)
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
