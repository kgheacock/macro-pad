package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/kgheacock/macro-pad/driver/plugin"
)

// fakeConn is a signalConn test double: it replays a fixed queue of
// messages from ReadMessage, then returns io.EOF, and records every
// SetState call so a test can check watch's reaction with no live
// WebSocket connection.
type fakeConn struct {
	queue     []plugin.Message
	setStates []setStateCall
}

type setStateCall struct {
	key   int
	state string
}

func (f *fakeConn) ReadMessage() (plugin.Message, error) {
	if len(f.queue) == 0 {
		return plugin.Message{}, io.EOF
	}
	msg := f.queue[0]
	f.queue = f.queue[1:]
	return msg, nil
}

func (f *fakeConn) SetState(key int, state string, color *uint16) error {
	f.setStates = append(f.setStates, setStateCall{key: key, state: state})
	return nil
}

func TestWatch_ReactsToProcessWaitingAndDone(t *testing.T) {
	conn := &fakeConn{
		queue: []plugin.Message{
			{Kind: plugin.KindEvent, Event: &plugin.EventPayload{KeyIndex: 0, Type: "press"}}, // ignored
			{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 1, Name: plugin.SignalProcessWaiting}}, // wrong key, ignored
			{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 0, Name: plugin.SignalProcessWaiting}},
			{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 0, Name: plugin.SignalSinglePress}}, // unrecognized name, ignored
			{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 0, Name: plugin.SignalProcessDone}},
		},
	}

	var stdout, stderr bytes.Buffer
	code := watch(conn, 0, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("got exit code %d, want 1 (queue exhausted, io.EOF)", code)
	}

	want := []setStateCall{{key: 0, state: "Waiting"}, {key: 0, state: "Done"}}
	if len(conn.setStates) != len(want) {
		t.Fatalf("got %d SetState calls %+v, want %+v", len(conn.setStates), conn.setStates, want)
	}
	for i, w := range want {
		if conn.setStates[i] != w {
			t.Errorf("SetState call %d = %+v, want %+v", i, conn.setStates[i], w)
		}
	}
}

// erroringConn's ReadMessage always fails, proving watch exits instead of
// looping when the connection is already gone.
type erroringConn struct{}

func (erroringConn) ReadMessage() (plugin.Message, error) {
	return plugin.Message{}, errors.New("connection closed")
}
func (erroringConn) SetState(key int, state string, color *uint16) error { return nil }

func TestWatch_ExitsOnReadError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := watch(erroringConn{}, 0, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("got exit code %d, want 1", code)
	}
}
