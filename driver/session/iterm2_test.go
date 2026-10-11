package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/plugin"
)

// mockBridge stands in for bridge.py: the test writes JSON lines into
// lines, and reads what the driver wrote to the bridge from commands.
type mockBridge struct {
	lines    *io.PipeWriter // test to driver
	commands chan map[string]string
	conn     *mockConn
}

type mockConn struct {
	io.Reader
	w      *io.PipeWriter // driver to bridge
	r      *io.PipeReader
	closed sync.Once
}

func (c *mockConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *mockConn) Close() error {
	c.closed.Do(func() { c.w.Close(); c.r.Close() })
	return nil
}

// newMockBridge returns a launcher backed by an in-memory bridge. first
// is written as soon as the driver starts the bridge.
func newMockBridge(t *testing.T, first string) (BridgeLauncher, *mockBridge) {
	t.Helper()
	out, outW := io.Pipe() // bridge stdout
	inR, inW := io.Pipe()  // bridge stdin
	mb := &mockBridge{lines: outW, commands: make(chan map[string]string, 8)}
	mb.conn = &mockConn{Reader: out, w: inW, r: out}
	go func() {
		sc := bufio.NewScanner(inR)
		for sc.Scan() {
			var cmd map[string]string
			if json.Unmarshal(sc.Bytes(), &cmd) == nil {
				mb.commands <- cmd
			}
		}
	}()
	launch := func(ctx context.Context, sessionID string) (io.ReadWriteCloser, error) {
		go func() {
			if first != "" {
				io.WriteString(outW, first+"\n")
			}
		}()
		return mb.conn, nil
	}
	t.Cleanup(func() { outW.Close(); inR.Close() })
	return launch, mb
}

func (m *mockBridge) emit(line string) { io.WriteString(m.lines, line+"\n") }

type fakeBroadcaster struct{ got chan plugin.Message }

func newFakeBroadcaster() *fakeBroadcaster {
	return &fakeBroadcaster{got: make(chan plugin.Message, 8)}
}
func (f *fakeBroadcaster) Broadcast(m plugin.Message) { f.got <- m }

func bind(t *testing.T, first string) (*ITerm2, *mockBridge, *fakeBroadcaster) {
	t.Helper()
	launch, mb := newMockBridge(t, first)
	bc := newFakeBroadcaster()
	s, err := BindITerm2(context.Background(), ITerm2Config{
		KeyIndex: 0, SessionID: "ABC", Broadcaster: bc, Launch: launch,
		ReadyTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("BindITerm2: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, mb, bc
}

func expectSignal(t *testing.T, bc *fakeBroadcaster, name string) {
	t.Helper()
	select {
	case m := <-bc.got:
		if m.Kind != plugin.KindSignal || m.Signal == nil {
			t.Fatalf("got %+v, want a signal", m)
		}
		if m.Signal.Name != name || m.Signal.KeyIndex != 0 {
			t.Fatalf("signal = key %d %q, want key 0 %q", m.Signal.KeyIndex, m.Signal.Name, name)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no %q signal broadcast", name)
	}
}

func TestITerm2_SendText(t *testing.T) {
	s, mb, _ := bind(t, `{"event":"ready"}`)
	if err := s.SendKeys("y\n"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	select {
	case cmd := <-mb.commands:
		if cmd["op"] != "send_text" || cmd["text"] != "y\n" {
			t.Fatalf("bridge got %v, want send_text \"y\\n\"", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bridge received no command")
	}
}

func TestITerm2_PromptBroadcastsWaiting(t *testing.T) {
	_, mb, bc := bind(t, `{"event":"ready"}`)
	mb.emit(`{"event":"prompt"}`)
	expectSignal(t, bc, "processWaiting")
}

func TestITerm2_CommandStartBroadcastsRunning(t *testing.T) {
	_, mb, bc := bind(t, `{"event":"ready"}`)
	mb.emit(`{"event":"command_start"}`)
	expectSignal(t, bc, "processRunning")
}

func TestITerm2_CommandFinishedMapsExitCode(t *testing.T) {
	_, mb, bc := bind(t, `{"event":"ready"}`)
	mb.emit(`{"event":"command_end","status":0}`)
	expectSignal(t, bc, "processDone")
	mb.emit(`{"event":"command_end","status":2}`)
	expectSignal(t, bc, "processExited")
}

func TestITerm2_APIDisabled(t *testing.T) {
	launch, _ := newMockBridge(t, `{"event":"error","code":"api_disabled","message":"connection refused"}`)
	_, err := BindITerm2(context.Background(), ITerm2Config{
		SessionID: "ABC", Broadcaster: newFakeBroadcaster(), Launch: launch,
		ReadyTimeout: 2 * time.Second,
	})
	if !errors.Is(err, ErrITerm2APIDisabled) {
		t.Fatalf("err = %v, want ErrITerm2APIDisabled", err)
	}
}

func TestITerm2_SilentBridgeTimesOut(t *testing.T) {
	launch, _ := newMockBridge(t, "")
	start := time.Now()
	_, err := BindITerm2(context.Background(), ITerm2Config{
		SessionID: "ABC", Broadcaster: newFakeBroadcaster(), Launch: launch,
		ReadyTimeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, ErrITerm2Bridge) {
		t.Fatalf("err = %v, want ErrITerm2Bridge", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("bind took %s, want it bounded by ReadyTimeout", time.Since(start))
	}
}

func TestITerm2_SessionNotFound(t *testing.T) {
	launch, _ := newMockBridge(t, `{"event":"error","code":"session_not_found","message":"no iTerm2 session with ID ABC"}`)
	_, err := BindITerm2(context.Background(), ITerm2Config{
		SessionID: "ABC", Broadcaster: newFakeBroadcaster(), Launch: launch,
		ReadyTimeout: 2 * time.Second,
	})
	if !errors.Is(err, ErrITerm2SessionNotFound) {
		t.Fatalf("err = %v, want ErrITerm2SessionNotFound", err)
	}
}
