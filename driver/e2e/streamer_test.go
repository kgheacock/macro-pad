package e2e

import (
	"bytes"
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// buildStreamer compiles cmd/macro_streamer, so the test runs the program a
// person runs. It strips symbols, as go run does: with go1.22.0 and a recent
// macOS linker, an unstripped binary is killed at launch.
func buildStreamer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "macro_streamer")
	if out, err := exec.Command("go", "build", "-ldflags=-s -w", "-o", bin, "../cmd/macro_streamer").CombinedOutput(); err != nil {
		t.Fatalf("go build macro_streamer: %v\n%s", err, out)
	}
	return bin
}

// streamerDaemon runs the daemon's parts in this process, on an emulator,
// and returns the port, the layer tree, and the emulator.
func streamerDaemon(t *testing.T) (int, *layers.Controller, *transport.Emulator) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	emu := transport.NewEmulator()
	srv := plugin.NewServer(emu, emu)
	ctl, err := layers.Open(layers.Options{AssetsDir: "../../assets", Out: srv, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetLayers(ctl)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Run(ctx, port)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		ctl.Close()
		emu.Close()
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon not listening: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	return port, ctl, emu
}

// runStreamer runs bin with the given channel and stdin, and returns its
// exit code and standard error.
func runStreamer(t *testing.T, bin string, port int, env []string, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, "--port", strconv.Itoa(port))
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, stderr.String()
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), stderr.String()
	}
	t.Fatalf("run macro_streamer: %v", err)
	return -1, ""
}

func waitKeyColor(t *testing.T, emu *transport.Emulator, key byte, color uint16) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if ks, ok := emu.LastKeyState(); ok && ks.KeyIndex == key && ks.Color == color {
			return
		}
		if time.Now().After(deadline) {
			ks, _ := emu.LastKeyState()
			t.Fatalf("key %d never showed color %#04x; last key state %+v", key, color, ks)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestStreamer proves task 0057's DoD-7 against the real binary: a JSON
// error line sets the key to ERROR, a bad line is logged and skipped, and an
// empty CHANNEL fails.
func TestStreamer(t *testing.T) {
	bin := buildStreamer(t)

	t.Run("error line sets the key to ERROR", func(t *testing.T) {
		port, ctl, emu := streamerDaemon(t)
		code, stderr := runStreamer(t, bin, port, []string{"CHANNEL=github"}, `{"level":"error","msg":"x"}`+"\n")
		if code != 0 {
			t.Fatalf("exit code %d, stderr %q", code, stderr)
		}
		waitKeyColor(t, emu, 0, layers.ColorError)
		keys := ctl.Snapshot().Keys
		if keys[0] == nil || keys[0].ID != "github" || keys[0].Status != layers.StatusError {
			t.Errorf("key 0 is %+v, want github in ERROR", keys[0])
		}
	})

	t.Run("bad lines are logged and skipped", func(t *testing.T) {
		port, ctl, emu := streamerDaemon(t)
		in := "this is not json\n" +
			`{"level":"loud"}` + "\n" +
			`{"msg":"no level"}` + "\n" +
			"\n" +
			`{"level":"warn","count":4}` + "\n"
		code, stderr := runStreamer(t, bin, port, []string{"CHANNEL=dev"}, in)
		if code != 0 {
			t.Fatalf("exit code %d, stderr %q", code, stderr)
		}
		for _, want := range []string{"line 1 skipped", "line 2 skipped", "line 3 skipped"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
		if strings.Contains(stderr, "line 4") {
			t.Errorf("stderr reports the blank line:\n%s", stderr)
		}
		waitKeyColor(t, emu, 0, layers.ColorWarn)
		if k := ctl.Snapshot().Keys[0]; k.Count != 4 {
			t.Errorf("count %d, want 4", k.Count)
		}
	})

	t.Run("a child line updates the child", func(t *testing.T) {
		port, ctl, _ := streamerDaemon(t)
		in := `{"level":"info","child":"build"}` + "\n" + `{"level":"error","child":"build","count":2}` + "\n"
		if code, stderr := runStreamer(t, bin, port, []string{"CHANNEL=github"}, in); code != 0 {
			t.Fatalf("exit code %d, stderr %q", code, stderr)
		}
		deadline := time.Now().Add(3 * time.Second)
		for {
			k := ctl.Snapshot().Keys[0]
			if k != nil && len(k.Children) == 1 && k.Children[0].Status == layers.StatusError {
				if k.Children[0].Count != 2 {
					t.Errorf("child count %d, want 2", k.Children[0].Count)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("child never reached ERROR: %+v", k)
			}
			time.Sleep(time.Millisecond)
		}
	})

	t.Run("an empty CHANNEL exits non-zero", func(t *testing.T) {
		port, _, _ := streamerDaemon(t)
		code, stderr := runStreamer(t, bin, port, []string{"CHANNEL="}, `{"level":"error"}`+"\n")
		if code == 0 {
			t.Fatal("exit code 0 with an empty CHANNEL")
		}
		if !strings.Contains(stderr, "CHANNEL") {
			t.Errorf("stderr does not name CHANNEL: %q", stderr)
		}
	})

	t.Run("an unset CHANNEL exits non-zero", func(t *testing.T) {
		port, _, _ := streamerDaemon(t)
		if code, _ := runStreamer(t, bin, port, nil, ""); code == 0 {
			t.Fatal("exit code 0 with no CHANNEL")
		}
	})
}
