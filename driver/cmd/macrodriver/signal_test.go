package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

func TestSignalNameForEvent(t *testing.T) {
	cases := []struct {
		event   string
		want    string
		wantOk  bool
		explain string
	}{
		{event: "notification", want: plugin.SignalProcessWaiting, wantOk: true},
		{event: "stop", want: plugin.SignalProcessDone, wantOk: true},
		{event: "bogus", wantOk: false},
	}
	for _, c := range cases {
		got, ok := signalNameForEvent(c.event)
		if ok != c.wantOk || (ok && got != c.want) {
			t.Errorf("signalNameForEvent(%q) = (%q, %v), want (%q, %v)", c.event, got, ok, c.want, c.wantOk)
		}
	}
}

func TestRunSignal_UnrecognizedEvent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runSignal([]string{"--event", "bogus"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("got exit code %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "bogus") {
		t.Fatalf("stderr = %q, want it to name the bad --event value", stderr.String())
	}
}

// TestRunSignal_ReachesRunningServer proves `macrodriver signal` sent
// against a real plugin.Server, over an actual WebSocket connection,
// reaches a connected client with the translated signal name — matching
// task 0013's DoD-3, exercised here end-to-end instead of only at the
// plugin package level.
func TestRunSignal_ReachesRunningServer(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	srv := plugin.NewServer(dev, dev)

	ts := httptest.NewServer(srv)
	defer ts.Close()
	addr := strings.TrimPrefix(ts.URL, "http://")

	observerURL := "ws://" + addr + "/"
	observer, _, err := websocket.DefaultDialer.Dial(observerURL, nil)
	if err != nil {
		t.Fatalf("dial observer: %v", err)
	}
	defer observer.Close()

	var stdout, stderr bytes.Buffer
	code := runSignal([]string{"--key", "0", "--event", "stop", "--addr", addr}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runSignal exit code = %d, stderr = %q", code, stderr.String())
	}

	observer.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg plugin.Message
	if err := observer.ReadJSON(&msg); err != nil {
		t.Fatalf("observer ReadJSON: %v", err)
	}
	if msg.Kind != plugin.KindSignal || msg.Signal == nil {
		t.Fatalf("got %+v, want a signal message", msg)
	}
	if msg.Signal.KeyIndex != 0 || msg.Signal.Name != plugin.SignalProcessDone {
		t.Fatalf("got %+v, want {KeyIndex: 0, Name: %q}", msg.Signal, plugin.SignalProcessDone)
	}
}
