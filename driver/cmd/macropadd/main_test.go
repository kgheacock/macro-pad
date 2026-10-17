package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// TestStateDBFlagRemoved checks that --state-db, which task 0057 removed,
// is a flag error.
func TestStateDBFlagRemoved(t *testing.T) {
	var stderr strings.Builder
	code := runCtx(context.Background(), []string{"--emulate", "--state-db", "x"}, io.Discard, &stderr, nil)
	if code == 0 {
		t.Fatal("--state-db was accepted; want a flag error")
	}
	if !strings.Contains(stderr.String(), "state-db") {
		t.Errorf("stderr does not name the flag: %q", stderr.String())
	}
}

// freePort returns a TCP port that nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestLayersFlow starts the daemon on an emulator with a saved layer tree,
// presses a key with children, and checks that the child layer shows and the
// active layer is saved on exit.
func TestLayersFlow(t *testing.T) {
	dir := t.TempDir()
	const doc = `{"version": 1, "active": null, "keys": [
	  {"id": "github", "status": "RUNNING", "iconRef": "github.png",
	   "children": [{"id": "build", "status": "ERROR", "count": 1}]},
	  null, null, null, null, null]}`
	if err := os.WriteFile(filepath.Join(dir, layers.FileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	emuC := make(chan *transport.Emulator, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- runCtx(ctx, []string{
			"--emulate", "--state-dir", dir, "--assets-dir", "../../../assets",
			"--port", strconv.Itoa(freePort(t)),
		}, io.Discard, io.Discard, func(e *transport.Emulator) { emuC <- e })
	}()
	emu := <-emuC

	waitKey := func(key byte, color uint16) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if ks, ok := emu.LastKeyState(); ok && ks.KeyIndex == key && ks.Color == color {
				return
			}
			if time.Now().After(deadline) {
				ks, _ := emu.LastKeyState()
				t.Fatalf("emulator never showed key %d color %#04x; last key state %+v", key, color, ks)
			}
			time.Sleep(time.Millisecond)
		}
	}

	waitKey(0, layers.ColorRunning)

	// A short click on key 0. singlePress follows 400 ms after the release.
	emu.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 1000})
	emu.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventRelease, Timestamp: 51000})
	waitKey(layers.UpKey, layers.ColorUp)

	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit code %d", code)
	}
	saved, err := os.ReadFile(filepath.Join(dir, layers.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `"active": "github"`) {
		t.Errorf("saved file does not hold the active layer:\n%s", saved)
	}
}
