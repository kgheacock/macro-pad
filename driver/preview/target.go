package preview

import (
	"fmt"
	"strconv"
	"strings"
)

// Selector picks one window from the window list. ID, when set, wins.
// Otherwise App must equal the window's app name and Title, when set,
// must appear in its title. Both comparisons ignore case.
type Selector struct {
	ID    WindowID
	App   string
	Title string
}

// Target maps one key to one window.
type Target struct {
	Key      int
	Selector Selector
}

// ParseTarget reads "KEY=SELECTOR". SELECTOR is "#ID" for a window ID,
// "App" for an app's front window, or "App:Title" to require Title in the
// window title, e.g. "0=Safari", "1=iTerm2:build", "2=#4512".
func ParseTarget(s string) (Target, error) {
	keyStr, sel, ok := strings.Cut(s, "=")
	if !ok {
		return Target{}, fmt.Errorf("preview: %q: want KEY=SELECTOR", s)
	}
	key, err := strconv.Atoi(strings.TrimSpace(keyStr))
	if err != nil || key < 0 || key > 254 {
		return Target{}, fmt.Errorf("preview: %q: key must be 0-254", s)
	}
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return Target{}, fmt.Errorf("preview: %q: empty selector", s)
	}
	if rest, ok := strings.CutPrefix(sel, "#"); ok {
		id, err := strconv.ParseUint(rest, 10, 32)
		if err != nil || id == 0 {
			return Target{}, fmt.Errorf("preview: %q: bad window ID", s)
		}
		return Target{Key: key, Selector: Selector{ID: WindowID(id)}}, nil
	}
	app, title, _ := strings.Cut(sel, ":")
	return Target{Key: key, Selector: Selector{App: strings.TrimSpace(app), Title: strings.TrimSpace(title)}}, nil
}

func (s Selector) matches(w Window) bool {
	if s.ID != 0 {
		return w.ID == s.ID
	}
	if !strings.EqualFold(w.App, s.App) {
		return false
	}
	return s.Title == "" || strings.Contains(strings.ToLower(w.Title), strings.ToLower(s.Title))
}

// resolve picks the window s names. prev, the window this selector
// resolved to last time, wins while it still matches, so a key does not
// jump between two windows of one app. Otherwise the frontmost on-screen
// match wins, then the frontmost match. windows is in front-to-back order.
func (s Selector) resolve(windows []Window, prev WindowID) (Window, bool) {
	var first, firstOn *Window
	for i := range windows {
		w := &windows[i]
		if !s.matches(*w) {
			continue
		}
		if prev != 0 && w.ID == prev {
			return *w, true
		}
		if first == nil {
			first = w
		}
		if firstOn == nil && w.OnScreen {
			firstOn = w
		}
	}
	switch {
	case firstOn != nil:
		return *firstOn, true
	case first != nil:
		return *first, true
	}
	return Window{}, false
}
