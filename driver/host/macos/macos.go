// Package macos holds the macOS calls plugins share: AppleScript through
// osascript, switching desktops, bringing an app or window forward, the
// frontmost window, the selected text, and the permission checks the
// plugin host runs at start. A plugin holds a Desktop rather than calling
// System directly, so its tests run with a fake on any OS. See task 0055.
package macos

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ErrUnsupported is returned by a call that needs macOS, or needs cgo on
// macOS, when the binary has neither.
var ErrUnsupported = errors.New("macos: not supported on this OS or build")

// Permission is one macOS privacy grant a plugin can need.
type Permission int

const (
	// Accessibility lets the host read other apps' UI (selected text,
	// focused element) and post key events.
	Accessibility Permission = iota
	// ScreenRecording lets the host capture other apps' windows.
	ScreenRecording
)

func (p Permission) String() string {
	switch p {
	case Accessibility:
		return "Accessibility"
	case ScreenRecording:
		return "Screen Recording"
	}
	return fmt.Sprintf("Permission(%d)", int(p))
}

// PermissionState is which grants the running binary holds. Supported is
// false off macOS, or on a build without cgo; the other fields are then
// false too.
type PermissionState struct {
	Supported       bool
	Accessibility   bool
	ScreenRecording bool
}

// Has reports whether s holds p.
func (s PermissionState) Has(p Permission) bool {
	switch p {
	case Accessibility:
		return s.Accessibility
	case ScreenRecording:
		return s.ScreenRecording
	}
	return false
}

// Permissions reports the grants this binary holds now. It never shows a
// prompt.
func Permissions() PermissionState {
	return permissions()
}

// Executable is the path System Settings needs to grant a permission to:
// the running binary.
func Executable() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

// WindowID is a window's CoreGraphics window number
// (kCGWindowNumber), the id ScreenCaptureKit and CGWindowList use.
type WindowID uint32

// Window is one app window.
type Window struct {
	// ID is 0 when it is not known: off macOS, or on a build without cgo.
	ID    WindowID
	App   string
	Title string
}

// Desktop is the set of calls a plugin makes into macOS. System is the
// real one; a test supplies a fake.
type Desktop interface {
	// SwitchDesktop moves to Mission Control desktop n (1..9) by posting
	// its Ctrl+N "Switch to Desktop N" shortcut, which must be turned on
	// in System Settings > Keyboard > Keyboard Shortcuts.
	SwitchDesktop(n int) error
	// ActivateApp brings app's frontmost window forward, launching app
	// if it is not running.
	ActivateApp(app string) error
	// RaiseWindow activates app and raises the first of its windows
	// whose title contains titleSubstr. Needs Accessibility.
	RaiseWindow(app, titleSubstr string) error
	// Frontmost returns the frontmost app, its front window's title, and
	// that window's ID. Title is empty when the app has no window. Needs
	// Accessibility.
	Frontmost() (Window, error)
	// SelectedText returns the text selected in the focused element of
	// the frontmost app, or "" when nothing is selected or the app does
	// not expose it. Needs Accessibility.
	SelectedText() (string, error)
}

// System is the real Desktop.
var System Desktop = system{}

type system struct{}

// desktopKeyCodes are the virtual key codes of the digits 1..9 on a US
// layout, which the Ctrl+N shortcuts are bound to.
var desktopKeyCodes = [9]int{18, 19, 20, 21, 23, 22, 26, 28, 25}

func (system) SwitchDesktop(n int) error {
	if n < 1 || n > len(desktopKeyCodes) {
		return fmt.Errorf("macos: desktop %d: want 1..9", n)
	}
	return AppleScript(fmt.Sprintf(`tell application "System Events" to key code %d using control down`, desktopKeyCodes[n-1]))
}

func (system) ActivateApp(app string) error {
	return AppleScript(fmt.Sprintf(`tell application %s to activate`, Quote(app)))
}

func (system) RaiseWindow(app, titleSubstr string) error {
	return AppleScript(
		fmt.Sprintf(`tell application %s to activate`, Quote(app)),
		fmt.Sprintf(`tell application "System Events" to tell process %s`, Quote(app)),
		fmt.Sprintf(`perform action "AXRaise" of (first window whose name contains %s)`, Quote(titleSubstr)),
		`end tell`,
	)
}

func (system) Frontmost() (Window, error) {
	out, err := AppleScriptOutput(
		`tell application "System Events"`,
		`set p to first application process whose frontmost is true`,
		`set n to name of p`,
		`set t to ""`,
		`try`,
		`set t to name of front window of p`,
		`end try`,
		`end tell`,
		`return n & linefeed & t`,
	)
	if err != nil {
		return Window{}, err
	}
	app, title, _ := strings.Cut(strings.TrimRight(out, "\n"), "\n")
	w := Window{App: app, Title: title}
	if title != "" {
		w.ID = frontWindowID()
	}
	return w, nil
}

func (system) SelectedText() (string, error) {
	return selectedText()
}

// AppleScript runs lines as one AppleScript program through osascript.
func AppleScript(lines ...string) error {
	_, err := AppleScriptOutput(lines...)
	return err
}

// AppleScriptOutput runs lines as one AppleScript program through
// osascript and returns what it printed.
func AppleScriptOutput(lines ...string) (string, error) {
	argv := make([]string, 0, 2*len(lines))
	for _, l := range lines {
		argv = append(argv, "-e", l)
	}
	cmd := exec.Command("osascript", argv...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("osascript: %w: %s", err, msg)
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	return string(out), nil
}

// Quote makes s an AppleScript string literal.
func Quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
