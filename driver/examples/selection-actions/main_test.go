package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// fakeConn records every key status the plugin sends.
type fakeConn struct {
	mu       sync.Mutex
	statuses []string
}

func (f *fakeConn) WriteJSON(v any) error {
	msg := v.(plugin.Message)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses = append(f.statuses, msg.SetLayerKey.Status)
	return nil
}

func (f *fakeConn) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.statuses...)
}

// fakeRun answers commands by argv[0]: "inbox" is recorded, and anything
// else calls cmd.
type fakeRun struct {
	mu    sync.Mutex
	cmd   func(ctx context.Context, argv []string, stdin string) (string, string, error)
	calls [][]string
	stdin []string
	sends [][]string
}

func (f *fakeRun) run(ctx context.Context, argv []string, stdin string) (string, string, error) {
	f.mu.Lock()
	if argv[0] == "inbox" {
		f.sends = append(f.sends, argv)
		f.mu.Unlock()
		return "", "", nil
	}
	f.calls = append(f.calls, argv)
	f.stdin = append(f.stdin, stdin)
	f.mu.Unlock()
	return f.cmd(ctx, argv, stdin)
}

func selected(text string) selector {
	return func(context.Context) (string, error) { return text, nil }
}

func testConfig(t *testing.T) config {
	t.Helper()
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Actions = []action{
		{Click: "single", Name: "summarise", Command: []string{"sum"}},
		{Click: "double", Name: "translate", Command: []string{"tr"}},
		{Click: "long", Name: "ask", Command: []string{"ask"}},
	}
	return cfg
}

func click(p *selPlugin, name string) {
	p.handle(plugin.Message{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 3, Name: name}})
}

func newTest(t *testing.T, sel selector, cmd func(context.Context, []string, string) (string, string, error)) (*selPlugin, *fakeConn, *fakeRun) {
	conn := &fakeConn{}
	fr := &fakeRun{cmd: cmd}
	p := newPlugin(testConfig(t), conn, sel, fr.run, t.Logf)
	return p, conn, fr
}

func echo(answer string) func(context.Context, []string, string) (string, string, error) {
	return func(context.Context, []string, string) (string, string, error) { return answer + "\n", "", nil }
}

func TestPress_SendsAnswer(t *testing.T) {
	p, conn, fr := newTest(t, selected("bonjour"), echo("hello"))
	click(p, plugin.SignalSinglePress)
	p.wg.Wait()

	if len(fr.stdin) != 1 || fr.stdin[0] != "bonjour" {
		t.Fatalf("command stdin = %q, want [bonjour]", fr.stdin)
	}
	want := []string{"inbox", "send", "--channel", "selection.summarise", "--status", "info", "--", "hello"}
	if len(fr.sends) != 1 || strings.Join(fr.sends[0], " ") != strings.Join(want, " ") {
		t.Fatalf("inbox sends = %q, want %q", fr.sends, want)
	}
	if got := conn.got(); strings.Join(got, ",") != layers.StatusRunning+"," {
		t.Fatalf("statuses = %q, want RUNNING then idle", got)
	}
}

func TestPress_ClickPicksAction(t *testing.T) {
	p, _, fr := newTest(t, selected("x"), echo("y"))
	for _, s := range []string{plugin.SignalSinglePress, plugin.SignalDoublePress, plugin.SignalLongPress} {
		click(p, s)
		p.wg.Wait()
	}
	var got []string
	for _, c := range fr.calls {
		got = append(got, c[0])
	}
	if strings.Join(got, ",") != "sum,tr,ask" {
		t.Fatalf("commands = %q, want sum,tr,ask", got)
	}
}

func TestPress_IgnoresOtherKeysAndChildLayers(t *testing.T) {
	p, _, fr := newTest(t, selected("x"), echo("y"))
	p.handle(plugin.Message{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: 2, Name: plugin.SignalSinglePress}})
	active := "inbox"
	p.handle(plugin.Message{Kind: plugin.KindLayerChanged, LayerChanged: &plugin.LayerChangedPayload{Active: &active}})
	click(p, plugin.SignalSinglePress)
	p.wg.Wait()
	if len(fr.calls) != 0 {
		t.Fatalf("ran %q, want nothing", fr.calls)
	}

	p.handle(plugin.Message{Kind: plugin.KindLayerChanged, LayerChanged: &plugin.LayerChangedPayload{}})
	click(p, plugin.SignalSinglePress)
	p.wg.Wait()
	if len(fr.calls) != 1 {
		t.Fatalf("ran %d commands after the top layer came back, want 1", len(fr.calls))
	}
}

func TestPress_NoSelection(t *testing.T) {
	p, conn, fr := newTest(t, selected("  \n"), echo("y"))
	click(p, plugin.SignalSinglePress)
	p.wg.Wait()
	if len(fr.calls) != 0 || len(fr.sends) != 0 {
		t.Fatalf("calls %q, sends %q; want none", fr.calls, fr.sends)
	}
	if got := conn.got(); strings.Join(got, ",") != layers.StatusWarn+"," {
		t.Fatalf("statuses = %q, want WARN then idle", got)
	}
}

func TestPress_Failures(t *testing.T) {
	cases := map[string]struct {
		cmd  func(context.Context, []string, string) (string, string, error)
		want string
	}{
		"exit 1": {
			cmd: func(context.Context, []string, string) (string, string, error) {
				return "", "boom\nmore", errors.New("exit status 1")
			},
			want: "summarise failed: boom",
		},
		"timeout": {
			cmd: func(ctx context.Context, _ []string, _ string) (string, string, error) {
				<-ctx.Done()
				return "", "", ctx.Err()
			},
			want: "summarise failed: no answer after 50ms",
		},
		"missing": {
			cmd: func(context.Context, []string, string) (string, string, error) {
				return "", "", errors.New(`exec: "sum": executable file not found in $PATH`)
			},
			want: `summarise failed: exec: "sum": executable file not found in $PATH`,
		},
		"no output": {
			cmd:  echo(""),
			want: "summarise failed: the command printed nothing",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, conn, fr := newTest(t, selected("x"), tc.cmd)
			p.cfg.Timeout, p.cfg.timeout = "50ms", 50*time.Millisecond
			click(p, plugin.SignalSinglePress)
			p.wg.Wait()
			if len(fr.sends) != 1 {
				t.Fatalf("sends = %q, want one", fr.sends)
			}
			s := fr.sends[0]
			if s[5] != "error" || s[7] != tc.want {
				t.Fatalf("send = %q, want status error and %q", s, tc.want)
			}
			got := conn.got()
			if got[len(got)-1] != layers.StatusError {
				t.Fatalf("statuses = %q, want ERROR last", got)
			}
		})
	}
}

func TestPress_OneAtATime(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	p, conn, fr := newTest(t, selected("x"), func(context.Context, []string, string) (string, string, error) {
		started <- struct{}{}
		<-release
		return "y", "", nil
	})
	click(p, plugin.SignalSinglePress)
	<-started
	click(p, plugin.SignalDoublePress)
	if got := conn.got(); len(got) != 1 || got[0] != layers.StatusRunning {
		t.Fatalf("statuses while running = %q, want RUNNING", got)
	}
	close(release)
	p.wg.Wait()
	if len(fr.calls) != 1 {
		t.Fatalf("ran %d commands, want 1", len(fr.calls))
	}
}

func TestConfig(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Actions) != 3 || cfg.Actions[0].Command[0] != "claude" || *cfg.Slot != 3 || cfg.timeout != time.Minute {
		t.Fatalf("defaults = %+v", cfg)
	}

	path := filepath.Join(t.TempDir(), "c.json")
	bad := `{"slot": 7, "timeout": "soon", "actions": [
		{"click": "single", "name": "a", "command": ["x"]},
		{"click": "single", "name": "b.c", "command": []},
		{"click": "triple", "name": "d", "command": ["y"]}]}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = loadConfig(path)
	if err == nil {
		t.Fatal("bad config loaded")
	}
	for _, want := range []string{"slot 7", `timeout "soon"`, `second action on click "single"`, `name "b.c"`, "command is empty", `click "triple"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}

	if err := os.WriteFile(path, []byte(`{"slots": 1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("unknown field loaded")
	}
}

func TestTruncate(t *testing.T) {
	s := strings.Repeat("é", 10) // 20 bytes
	got := truncate(s, 8)
	if len(got) > 8 || !strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "éé") {
		t.Fatalf("truncate = %q", got)
	}
	if truncate("short", 8) != "short" {
		t.Fatal("short string changed")
	}
}
