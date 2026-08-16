package plugin

import (
	"errors"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// MessageKind identifies which of the two JSON message shapes a Message
// carries, over the WebSocket connection between the daemon and a plugin.
type MessageKind string

const (
	// KindEvent is a device→client message wrapping a decoded
	// transport.Event.
	KindEvent MessageKind = "event"
	// KindSetKeyState is a client→device message that becomes one
	// transport.Transport.SendKeyState call. The server also rebroadcasts
	// it to every connected client, itself included, so any observer —
	// for example the virtual pad in driver/plugin/web/virtualpad.html —
	// sees the resulting key state without reading it back off the
	// device.
	KindSetKeyState MessageKind = "setKeyState"
	// KindInjectEvent is a client→device message that becomes one
	// Injector.InjectEvent call, simulating a press or release with no
	// board attached. A Server with a nil Injector drops it. See task
	// 0029.
	KindInjectEvent MessageKind = "injectEvent"
)

// Message is the JSON envelope every message on the connection uses. Kind
// says which of Event, SetKeyState, or InjectEvent is populated; the
// others are left nil.
type Message struct {
	Kind        MessageKind         `json:"kind"`
	Event       *EventPayload       `json:"event,omitempty"`
	SetKeyState *SetKeyStatePayload `json:"setKeyState,omitempty"`
	InjectEvent *InjectEventPayload `json:"injectEvent,omitempty"`
}

// EventPayload is transport.Event as JSON. Type is "press" or "release",
// not the numeric transport.EventType, so a plugin never has to know the
// wire protocol's encoding.
type EventPayload struct {
	KeyIndex  byte   `json:"keyIndex"`
	Type      string `json:"type"`
	Timestamp uint64 `json:"timestamp"`
}

// SetKeyStatePayload is transport.KeyState as JSON, minus the Version
// byte — the server fills in transport.ProtocolVersion itself, so a
// plugin never has to track the wire protocol's version.
type SetKeyStatePayload struct {
	KeyIndex byte   `json:"keyIndex"`
	Color    uint16 `json:"color"`
	EmojiID  byte   `json:"emojiId"`
	Blink    bool   `json:"blink"`
}

// InjectEventPayload is transport.Event as JSON, minus Timestamp: the
// server fills it in from its own clock when the message is applied, so
// an injecting client never has to track the wire protocol's timestamp
// units.
type InjectEventPayload struct {
	KeyIndex byte   `json:"keyIndex"`
	Type     string `json:"type"`
}

// errMissingPayload is returned by SetKeyStatePayload.toKeyState when a
// setKeyState message carries no payload to convert.
var errMissingPayload = errors.New("plugin: setKeyState message carries no payload")

// errMissingInjectPayload is returned by InjectEventPayload.toEvent when
// an injectEvent message carries no payload to convert.
var errMissingInjectPayload = errors.New("plugin: injectEvent message carries no payload")

// eventMessage wraps a decoded transport.Event as the JSON message a
// client receives.
func eventMessage(ev transport.Event) Message {
	evType := "press"
	if ev.Type == transport.EventRelease {
		evType = "release"
	}
	return Message{
		Kind: KindEvent,
		Event: &EventPayload{
			KeyIndex:  ev.KeyIndex,
			Type:      evType,
			Timestamp: ev.Timestamp,
		},
	}
}

// toKeyState turns a setKeyState message's payload into the
// transport.KeyState transport.Transport.SendKeyState expects.
func (p *SetKeyStatePayload) toKeyState() (transport.KeyState, error) {
	if p == nil {
		return transport.KeyState{}, errMissingPayload
	}
	return transport.KeyState{
		KeyIndex: p.KeyIndex,
		Version:  transport.ProtocolVersion,
		Color:    p.Color,
		EmojiID:  p.EmojiID,
		Blink:    p.Blink,
	}, nil
}

// toEvent turns an injectEvent message's payload into the transport.Event
// an Injector expects. Timestamp is left zero; the server fills it in
// from its own clock before calling Injector.InjectEvent.
func (p *InjectEventPayload) toEvent() (transport.Event, error) {
	if p == nil {
		return transport.Event{}, errMissingInjectPayload
	}
	evType := transport.EventPress
	if p.Type == "release" {
		evType = transport.EventRelease
	}
	return transport.Event{
		KeyIndex: p.KeyIndex,
		Type:     evType,
	}, nil
}
