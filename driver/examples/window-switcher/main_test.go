package main

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/kgheacock/macro-pad/driver/plugin"
)

// fakeConn replays a fixed queue of messages from ReadMessage, then
// returns io.EOF, and records every SetKeyState call.
type fakeConn struct {
	queue  []plugin.Message
	states []keyState
}

type keyState struct {
	key   int
	color uint16
}

func (f *fakeConn) ReadMessage() (plugin.Message, error) {
	if len(f.queue) == 0 {
		return plugin.Message{}, io.EOF
	}
	msg := f.queue[0]
	f.queue = f.queue[1:]
	return msg, nil
}

func (f *fakeConn) SetKeyState(key int, color uint16, emojiID byte, blink bool) error {
	f.states = append(f.states, keyState{key: key, color: color})
	return nil
}

// fakeRunner records each script it is asked to run and fails the ones
// whose first argument is in fail.
type fakeRunner struct {
	ran  []switchScript
	fail map[string]bool
}

func (f *fakeRunner) Run(s switchScript) error {
	f.ran = append(f.ran, s)
	if len(s.args) > 0 && f.fail[s.args[0]] {
		return errors.New("boom")
	}
	return nil
}

func press(key byte) plugin.Message {
	return plugin.Message{Kind: plugin.KindEvent, Event: &plugin.EventPayload{KeyIndex: key, Type: "press"}}
}

func release(key byte) plugin.Message {
	return plugin.Message{Kind: plugin.KindEvent, Event: &plugin.EventPayload{KeyIndex: key, Type: "release"}}
}

func signal(key byte, name string) plugin.Message {
	return plugin.Message{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: key, Name: name}}
}

func mustConfig(t *testing.T, js string) Config {
	t.Helper()
	cfg, err := parseConfig([]byte(js))
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	return cfg
}

func TestDefaultConfigBindsKeysToDesktops(t *testing.T) {
	cfg := defaultConfig()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	for i, b := range cfg.Keys {
		if b.Key != i || b.Desktop != i+1 {
			t.Errorf("key %d = %+v, want desktop %d", i, b, i+1)
		}
	}
}

func TestParseConfigRejects(t *testing.T) {
	cases := map[string]string{
		"no keys":       `{"keys": []}`,
		"key range":     `{"keys": [{"key": 6, "desktop": 1}]}`,
		"duplicate":     `{"keys": [{"key": 0, "desktop": 1}, {"key": 0, "desktop": 2}]}`,
		"both":          `{"keys": [{"key": 0, "desktop": 1, "app": "Safari"}]}`,
		"neither":       `{"keys": [{"key": 0}]}`,
		"desktop range": `{"keys": [{"key": 0, "desktop": 10}]}`,
		"window no app": `{"keys": [{"key": 0, "desktop": 1, "window": "x"}]}`,
		"bad color":     `{"keys": [{"key": 0, "desktop": 1, "color": "red"}]}`,
		"bad trigger":   `{"trigger": "hold", "keys": [{"key": 0, "desktop": 1}]}`,
		"unknown field": `{"keys": [{"key": 0, "desktop": 1, "title": "x"}]}`,
		"not json":      `keys`,
	}
	for name, js := range cases {
		if _, err := parseConfig([]byte(js)); err == nil {
			t.Errorf("%s: parseConfig(%s) succeeded, want error", name, js)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	for hex, want := range map[string]uint16{"#FF0000": 0xF800, "#00FF00": 0x07E0, "#0000FF": 0x001F, "#FFFFFF": 0xFFFF, "#3D5CFF": 0x3AFF} {
		got, err := parseHexColor(hex)
		if err != nil || got != want {
			t.Errorf("parseHexColor(%s) = %#04x, %v; want %#04x", hex, got, err, want)
		}
	}
}

func TestScriptForDesktopUsesControlDigit(t *testing.T) {
	for desktop, code := range map[int]string{1: "18", 5: "23", 6: "22", 9: "25"} {
		s := scriptFor(Binding{Desktop: desktop})
		want := `tell application "System Events" to key code ` + code + ` using control down`
		if !reflect.DeepEqual(s.lines, []string{want}) || len(s.args) != 0 {
			t.Errorf("desktop %d: got %+v, want %q", desktop, s, want)
		}
	}
}

func TestScriptForPassesNamesAsArguments(t *testing.T) {
	s := scriptFor(Binding{App: `Evil" to quit`, Window: `a "quoted" title`})
	if !reflect.DeepEqual(s.args, []string{`Evil" to quit`, `a "quoted" title`}) {
		t.Fatalf("args = %q", s.args)
	}
	for _, l := range s.lines {
		if strings.Contains(l, "Evil") || strings.Contains(l, "quoted\"") {
			t.Errorf("name spliced into script line %q", l)
		}
	}
	argv := s.argv()
	if argv[0] != "osascript" || argv[len(argv)-2] != s.args[0] || argv[len(argv)-1] != s.args[1] {
		t.Errorf("argv = %q, want osascript ... followed by the two names", argv)
	}

	app := scriptFor(Binding{App: "Safari"})
	if !reflect.DeepEqual(app.args, []string{"Safari"}) {
		t.Errorf("app-only args = %q", app.args)
	}
}

func TestWatchSwitchesOnPressAndLightsActiveKey(t *testing.T) {
	cfg := mustConfig(t, `{"keys": [
		{"key": 0, "desktop": 1, "color": "#FF0000"},
		{"key": 1, "app": "Safari", "color": "#00FF00"},
		{"key": 2, "app": "iTerm2", "window": "build", "color": "#0000FF"}
	]}`)
	conn := &fakeConn{queue: []plugin.Message{
		press(0), release(0),
		signal(1, plugin.SignalSinglePress), // not the trigger, ignored
		press(5),                            // unbound, ignored
		press(2),
		press(2), // already active: switches again, no relight
	}}
	r := &fakeRunner{}
	var stdout, stderr bytes.Buffer
	if code := newSwitcher(cfg, conn, r, &stdout, &stderr).watch(); code != 1 {
		t.Fatalf("exit code %d, want 1 (queue exhausted)", code)
	}

	if len(r.ran) != 3 {
		t.Fatalf("ran %d scripts, want 3: %+v", len(r.ran), r.ran)
	}
	if r.ran[0].args != nil || !reflect.DeepEqual(r.ran[1].args, []string{"iTerm2", "build"}) {
		t.Errorf("ran %+v", r.ran)
	}

	want := []keyState{
		{0, dim(0xF800)}, {1, dim(0x07E0)}, {2, dim(0x001F)}, // startup
		{0, 0xF800},                   // press 0
		{0, dim(0xF800)}, {2, 0x001F}, // press 2
	}
	if !reflect.DeepEqual(conn.states, want) {
		t.Errorf("key states = %+v, want %+v", conn.states, want)
	}
}

func TestWatchUsesResolvedSignalTrigger(t *testing.T) {
	cfg := mustConfig(t, `{"trigger": "longPress", "keys": [{"key": 3, "desktop": 4}]}`)
	conn := &fakeConn{queue: []plugin.Message{
		press(3),
		signal(3, plugin.SignalSinglePress),
		signal(3, plugin.SignalLongPress),
	}}
	r := &fakeRunner{}
	newSwitcher(cfg, conn, r, io.Discard, io.Discard).watch()
	if len(r.ran) != 1 || r.ran[0].lines[0] != `tell application "System Events" to key code 21 using control down` {
		t.Errorf("ran %+v, want one switch to desktop 4", r.ran)
	}
}

func TestWatchKeepsActiveKeyWhenSwitchFails(t *testing.T) {
	cfg := mustConfig(t, `{"keys": [{"key": 0, "app": "Notes"}, {"key": 1, "app": "Gone"}]}`)
	conn := &fakeConn{queue: []plugin.Message{press(0), press(1)}}
	r := &fakeRunner{fail: map[string]bool{"Gone": true}}
	var stderr bytes.Buffer
	newSwitcher(cfg, conn, r, io.Discard, &stderr).watch()

	if !strings.Contains(stderr.String(), "key 1 -> Gone: boom") {
		t.Errorf("stderr = %q, want the failed switch logged", stderr.String())
	}
	// Startup lights 2 keys, press 0 lights key 0; the failed press 1
	// changes nothing.
	if len(conn.states) != 3 || conn.states[2].key != 0 {
		t.Errorf("key states = %+v", conn.states)
	}
}
