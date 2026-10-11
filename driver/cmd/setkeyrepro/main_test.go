package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// A daemon that applies every message gives `applied 3/3`, and the test
// reads the answer from keys.json, as the command does against the board.
func TestRun_CountsAppliedMessages(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	if err := os.Mkdir(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	emu := transport.NewEmulator()
	defer emu.Close()

	srv := plugin.NewServer(emu, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: &savingHandler{Server: srv, dev: emu, path: filepath.Join(stateDir, "keys.json")}}
	go hs.Serve(ln)
	defer hs.Shutdown(context.Background())

	var out, errOut bytes.Buffer
	code := run([]string{"-url", "ws://" + ln.Addr().String(), "-state-dir", stateDir, "-count", "3", "-interval", "300ms"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("run exited %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "applied 3/3") {
		t.Fatalf("output has no `applied 3/3`:\n%s", out.String())
	}
}

// savingHandler stands in for macropadd's state directory: after the
// server handles a connection, it writes the emulator's last key state to
// keys.json in the shape Reconnecting saves.
type savingHandler struct {
	*plugin.Server
	dev  *transport.Emulator
	path string
}

func (h *savingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			if ks, ok := h.dev.LastKeyState(); ok {
				data := []byte(`{"keys":[{"key_index":` + strconv.Itoa(int(ks.KeyIndex)) + `,"color":` + strconv.Itoa(int(ks.Color)) + `}]}`)
				os.WriteFile(h.path, data, 0o644)
			}
		}
	}()
	h.Server.ServeHTTP(w, r)
}
