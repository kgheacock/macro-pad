// Package preview shows a live thumbnail of a window on a key. Each tick
// it captures every mapped window that is on screen, scales it to
// 128x128, and sends it with setCustomGlyph when the picture changed. A
// window that cannot be captured right now — minimized, on another
// desktop, or its app hidden — keeps the last snapshot taken of it,
// dimmed. See task 0054.
//
// The package does not decide which window belongs to which key. A caller
// passes a []Target, so a switcher plugin that owns the key-to-window
// mapping can drive it.
package preview

import (
	"errors"
	"image"
)

// WindowID is a macOS CGWindowID: kCGWindowNumber in the window list,
// SCWindow.windowID in ScreenCaptureKit.
type WindowID uint32

// Window is one entry in the system window list.
type Window struct {
	ID    WindowID
	App   string // owning app's name, kCGWindowOwnerName
	Title string // window title; empty without the Screen Recording permission
	// OnScreen is false for a minimized window, a window on another
	// desktop, and every window of a hidden app. ScreenCaptureKit returns
	// no useful picture for those, so they show their last snapshot.
	OnScreen bool
}

// Capturer reads the window list and captures windows. The macOS
// implementation, NewCapturer, uses CGWindowList and ScreenCaptureKit; a
// test substitutes a fake.
type Capturer interface {
	// Windows lists normal (layer 0) windows, on screen or not, front to
	// back.
	Windows() ([]Window, error)
	// Focused returns the frontmost on-screen window, or 0 if none.
	Focused() (WindowID, error)
	// Capture returns the window's contents, scaled to fit inside
	// maxW x maxH with its aspect ratio kept. It captures a window that
	// other windows cover.
	Capture(id WindowID, maxW, maxH int) (image.Image, error)
}

// ErrUnsupported is returned by NewCapturer on a platform with no
// ScreenCaptureKit.
var ErrUnsupported = errors.New("preview: window capture needs macOS 14 or later")

// ErrPermission is returned by NewCapturer when the process lacks the
// Screen Recording permission. macOS shows its prompt the first time; the
// user grants it in System Settings > Privacy & Security > Screen &
// System Audio Recording, then restarts the process.
var ErrPermission = errors.New("preview: Screen Recording permission not granted; allow this program (or the terminal running it) in System Settings > Privacy & Security > Screen & System Audio Recording, then restart it")
