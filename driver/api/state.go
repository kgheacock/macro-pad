// Package api gives a Go-based plugin small, typed helpers over
// driver/plugin's WebSocket protocol, so it never has to build a
// setKeyState or signal JSON message by hand. See task 0013 and
// driver/README.md's "Plugin API" section.
package api

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/plugin"
)

// wsConn is the subset of *websocket.Conn Conn needs. Tests substitute a
// fake, so SetEmoji, SetState, and Signal are checked with no live
// macropadd process or WebSocket connection.
type wsConn interface {
	WriteJSON(v any) error
	ReadJSON(v any) error
	Close() error
}

// Conn is a Go-based plugin's connection to macropadd's plugin WebSocket
// API.
type Conn struct {
	ws wsConn
}

// Dial opens a plugin connection to the driver's WebSocket server at addr,
// e.g. "127.0.0.1:8765" (see plugin.DefaultPort).
func Dial(addr string) (*Conn, error) {
	u := url.URL{Scheme: "ws", Host: addr, Path: "/"}
	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("api: dial %s: %w", addr, err)
	}
	return &Conn{ws: ws}, nil
}

// Close releases the underlying connection.
func (c *Conn) Close() error {
	return c.ws.Close()
}

// ReadMessage reads and decodes one message from the connection — an
// event or a signal broadcast, most often. A worked example reacting to
// signal messages lives in driver/examples/claude-status.
func (c *Conn) ReadMessage() (plugin.Message, error) {
	var msg plugin.Message
	if err := c.ws.ReadJSON(&msg); err != nil {
		return plugin.Message{}, fmt.Errorf("api: read: %w", err)
	}
	return msg, nil
}

// SetEmoji sets key's emoji to id. Because the wire protocol's Key state
// message (docs/wire-protocol.md) replaces a key's color, emoji, and
// blink state all at once, this also resets the key's color and blink
// flag — there is no way to change only the emoji on the wire. Call
// SetState instead when color or blink matters too.
func (c *Conn) SetEmoji(key int, id byte) error {
	return c.send(plugin.Message{
		Kind: plugin.KindSetKeyState,
		SetKeyState: &plugin.SetKeyStatePayload{
			KeyIndex: byte(key),
			EmojiID:  id,
		},
	})
}

// namedState is one entry in the closed set SetState recognizes: a color
// and blink state a plugin selects by name instead of building a
// setKeyState payload by hand. Color and Blink become the Key state
// message's Color and Blink flag fields — see docs/wire-protocol.md.
type namedState struct {
	color uint16 // RGB565
	blink bool
}

// namedStates is SetState's closed vocabulary. Alert is red and blinking.
// Waiting and Done are the pair driver/examples/claude-status uses to
// turn a key amber while a Claude Code session waits on input and green
// once it's done, matching the task spec's worked example.
var namedStates = map[string]namedState{
	"Alert":   {color: 0xF800, blink: true},  // red, blink
	"Waiting": {color: 0xFEA0, blink: true},  // amber, blink
	"Done":    {color: 0x07E0, blink: false}, // green, solid
}

// ErrUnknownState is returned when SetState is called with a name
// namedStates does not recognize. The named-state set is closed — a new
// state needs a driver code change. See task 0013's Risks.
var ErrUnknownState = errors.New("api: unknown named state")

// SetState sets key's color and blink state from the named state state,
// leaving its emoji at 0 (the placeholder glyph — see "Emoji IDs" in
// docs/wire-protocol.md). color, when not nil, overrides the named
// state's color; its blink flag is unchanged either way.
func (c *Conn) SetState(key int, state string, color *uint16) error {
	ns, ok := namedStates[state]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownState, state)
	}
	col := ns.color
	if color != nil {
		col = *color
	}
	return c.send(plugin.Message{
		Kind: plugin.KindSetKeyState,
		SetKeyState: &plugin.SetKeyStatePayload{
			KeyIndex: byte(key),
			Color:    col,
			Blink:    ns.blink,
		},
	})
}

// Signal broadcasts a plugin.KindSignal message naming name for key. A
// one-shot caller such as `macrodriver signal` uses this to trigger every
// other connected plugin's reaction, with no direct call into any of
// them — see task 0013's Decision.
func (c *Conn) Signal(key int, name string) error {
	return c.send(plugin.Message{
		Kind:   plugin.KindSignal,
		Signal: &plugin.SignalPayload{KeyIndex: byte(key), Name: name},
	})
}

func (c *Conn) send(msg plugin.Message) error {
	if err := c.ws.WriteJSON(msg); err != nil {
		return fmt.Errorf("api: send: %w", err)
	}
	return nil
}
