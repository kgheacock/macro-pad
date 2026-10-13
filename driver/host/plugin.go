// Package host runs many Go plugins in one process, over one connection
// to macropadd's plugin WebSocket server. A config file gives each plugin
// instance its keys and its page. The host routes input to the instance
// that owns each key, drops writes to keys an instance does not own,
// repaints the keys of a page when it shows, and reconnects when the
// daemon restarts. See task 0055 and driver/README.md's "Plugin host"
// section.
package host

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/kgheacock/macro-pad/driver/host/keyimg"
	"github.com/kgheacock/macro-pad/driver/host/macos"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// NumKeys is the macro pad's key count. Keys are 0..NumKeys-1.
const NumKeys = 6

// Plugin is one plugin instance.
type Plugin interface {
	// Start runs once, before the host connects. The plugin reads its
	// config, registers handlers, and starts any long-running work with
	// h.Go or h.Every. Start must return promptly. An error disables this
	// instance only; the others still run.
	Start(ctx context.Context, h Host) error
}

// Factory names a plugin and builds instances of it. The host binary
// lists every Factory it carries (driver/cmd/macropad-plugins/plugins.go).
type Factory struct {
	// Name is what a config entry's "use" field names.
	Name string
	// Description is one line for `macropad-plugins --list`.
	Description string
	// New builds one instance.
	New func() Plugin
}

// ClickKind is a click pattern the daemon's resolver reports.
type ClickKind int

const (
	Single ClickKind = iota
	Double
	Long
)

var clickSignals = map[string]ClickKind{
	plugin.SignalSinglePress: Single,
	plugin.SignalDoublePress: Double,
	plugin.SignalLongPress:   Long,
}

// Host is one instance's handle on the plugin host.
//
// Handlers registered with On... and Subscribe, and functions passed to
// Every, run one at a time on the instance's own goroutine, in order. A
// plugin needs no lock for state only its handlers touch. A handler that
// panics is logged and the instance keeps running.
type Host interface {
	// ID is the instance's id from the config.
	ID() string
	// Keys is the physical key indexes this instance owns, in config
	// order.
	Keys() []int
	// Config decodes the instance's "config" object into v. An unknown
	// field is an error. An absent config leaves v as is.
	Config(v any) error
	// Log writes one line, prefixed with the instance id.
	Log(format string, args ...any)

	// Key returns an owned key's output. Every call on a key this
	// instance does not own returns ErrNotOwner and sends nothing.
	Key(i int) Key

	// OnPress and OnRelease fire for raw presses and releases on an
	// owned key, while its page shows.
	OnPress(fn func(key int))
	OnRelease(fn func(key int))
	// OnClick fires for a resolved click on an owned key, while its page
	// shows.
	OnClick(kind ClickKind, fn func(key int))
	// OnSignal fires for any other signal named name on an owned key,
	// whether or not its page shows, so a status plugin stays current
	// while hidden. A signal on a key no instance owns goes to every
	// instance that registered for name.
	OnSignal(name string, fn func(key int, s plugin.SignalPayload))

	// Visible reports whether the instance's page shows now.
	Visible() bool
	// OnVisible fires when the instance's page shows or hides.
	OnVisible(fn func(visible bool))
	// Page is the page showing now; Pages lists every page, in order.
	Page() string
	Pages() []string
	// ShowPage switches every key to page name.
	ShowPage(name string) error
	// OnPageChange fires on every instance after any page switch.
	OnPageChange(fn func(page string))

	// Publish sends v to every Subscribe handler for topic, in every
	// instance, on each subscriber's own goroutine. See topics.go.
	Publish(topic string, v any)
	Subscribe(topic string, fn func(v any))

	// Go runs fn on a new goroutine that recovers and logs a panic. ctx
	// ends when the host stops. Use it instead of a bare go statement: a
	// panic on a bare goroutine stops every plugin.
	Go(fn func(ctx context.Context))
	// Every calls fn every d, on the instance's own goroutine like a
	// handler, until the host stops.
	Every(d time.Duration, fn func())

	// Require returns an error when the host binary lacks a macOS grant,
	// naming the binary to grant it to. Call it in Start and return its
	// error to disable the instance.
	Require(p macos.Permission) error
}

// Key is one key's output. Calls never block on the network: the host
// caches the key's state and sends it when the key's page shows.
type Key interface {
	// SetColor shows a solid color, with no glyph and no blink.
	SetColor(c keyimg.Color) error
	// SetState shows a named state: "Alert", "Waiting", or "Done" (see
	// api.SetState).
	SetState(name string) error
	// SetBlink turns blink on or off, keeping the color and image.
	SetBlink(on bool) error
	// SetImage shows img, scaled and letterboxed to 128x128.
	SetImage(img image.Image) error
	// SetPNG shows a 128x128 PNG file's bytes.
	SetPNG(png []byte) error
	// Clear turns the key off.
	Clear() error
}

// ErrNotOwner is returned by every Key call on a key the instance does
// not own.
var ErrNotOwner = errors.New("host: key not owned by this instance")
