package statedb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// fakeTransport records every KeyState it is sent.
type fakeTransport struct {
	mu   sync.Mutex
	sent []transport.KeyState
}

func (f *fakeTransport) SendKeyState(ks transport.KeyState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, ks)
	return nil
}
func (f *fakeTransport) SendCustomGlyph(byte, []byte) error      { return nil }
func (f *fakeTransport) ReadMessage() (transport.Message, error) { select {} }
func (f *fakeTransport) Close() error                            { return nil }

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// take returns and clears what was sent so far.
func (f *fakeTransport) take() []transport.KeyState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// newDB creates a database with the schema and a writer connection, as
// task 0052's writer would hold. Foreign keys are on, as the schema needs.
//
//	node 1: root, children 2 (pos 0) and 3 (pos 1)
//	node 10: page B, child 11 (pos 0, ERROR) and 12 (pos 5, WARN)
//
// state.active_node_id is 1.
func newDB(t *testing.T) (path string, w *sql.DB) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "state.db")
	w, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=1")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	t.Cleanup(func() { w.Close() })
	exec(t, w, Schema())
	exec(t, w, `
INSERT INTO nodes(id, parent_id, position) VALUES (1, NULL, NULL), (10, NULL, NULL);
INSERT INTO nodes(id, parent_id, position, channel, message, status) VALUES
  (2, 1, 0, 'DEV', 'build', 'RUNNING'),
  (3, 1, 1, 'GITHUB', 'pr', 'DONE'),
  (11, 10, 0, 'AI', 'agent', 'ERROR'),
  (12, 10, 5, 'LIVE', 'set', 'WARN');
INSERT INTO state(id, active_node_id) VALUES (1, 1);`)
	return path, w
}

func exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// setStatus is what a writer does: change a visible field and bump the
// parent's children_rev, in one transaction.
func setStatus(t *testing.T, w *sql.DB, id int, status any) {
	t.Helper()
	exec(t, w, `BEGIN`)
	exec(t, w, `UPDATE nodes SET status = ? WHERE id = ?`, status, id)
	exec(t, w, `UPDATE nodes SET children_rev = children_rev + 1 WHERE id = (SELECT parent_id FROM nodes WHERE id = ?)`, id)
	exec(t, w, `COMMIT`)
}

func newPoller(t *testing.T, path string, dev transport.Transport, l *logs) *Poller {
	t.Helper()
	p, err := New(path, dev, l.logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func poll(t *testing.T, p *Poller) {
	t.Helper()
	if err := p.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStatusChangeSent(t *testing.T) {
	path, w := newDB(t)
	dev := &fakeTransport{}
	p := newPoller(t, path, dev, &logs{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, 5*time.Millisecond) }()
	defer func() { cancel(); <-done }()

	waitFor(t, func() bool { return dev.count() >= KeyCount })
	dev.take()

	setStatus(t, w, 2, "ERROR")

	waitFor(t, func() bool { return dev.count() > 0 })
	got := dev.take()
	if got[0].KeyIndex != 0 || got[0].Color != ColorError {
		t.Fatalf("first message after the write = %+v, want key 0 color %#x", got[0], ColorError)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNoopWriteSendsNothing(t *testing.T) {
	path, w := newDB(t)
	dev := &fakeTransport{}
	p := newPoller(t, path, dev, &logs{})
	poll(t, p)
	dev.take()

	exec(t, w, `UPDATE nodes SET message = message`)
	poll(t, p)

	if n := dev.count(); n != 0 {
		t.Fatalf("no-op write sent %d messages", n)
	}
}

func TestPageSwitch(t *testing.T) {
	path, w := newDB(t)
	dev := &fakeTransport{}
	p := newPoller(t, path, dev, &logs{})
	poll(t, p)
	dev.take()

	exec(t, w, `UPDATE state SET active_node_id = 10`)
	poll(t, p)

	got := dev.take()
	if len(got) != KeyCount {
		t.Fatalf("page switch sent %d messages, want %d", len(got), KeyCount)
	}
	want := []uint16{ColorError, 0, 0, 0, 0, ColorWarn}
	for i, ks := range got {
		if int(ks.KeyIndex) != i || ks.Color != want[i] || ks.EmojiID != 0 || ks.Blink {
			t.Errorf("message %d = %+v, want key %d color %#x", i, ks, i, want[i])
		}
		if ks.Version != transport.ProtocolVersion {
			t.Errorf("message %d version = %d", i, ks.Version)
		}
	}
}

func TestIdleDoesNotQueryChildren(t *testing.T) {
	path, _ := newDB(t)
	dev := &fakeTransport{}
	p := newPoller(t, path, dev, &logs{})
	poll(t, p)
	before := p.childQueries
	if before != 1 {
		t.Fatalf("first poll ran the children query %d times, want 1", before)
	}
	dev.take()

	for i := 0; i < 20; i++ {
		poll(t, p)
	}
	if n := p.childQueries - before; n != 0 {
		t.Fatalf("idle ticks ran the children query %d times, want 0", n)
	}
	if n := dev.count(); n != 0 {
		t.Fatalf("idle ticks sent %d messages", n)
	}
}

func TestMissingChild(t *testing.T) {
	path, _ := newDB(t)
	dev := &fakeTransport{}
	poll(t, newPoller(t, path, dev, &logs{}))

	got := dev.take()
	if len(got) != KeyCount {
		t.Fatalf("sent %d messages, want %d", len(got), KeyCount)
	}
	for i := 2; i < KeyCount; i++ {
		if got[i].Color != 0 || got[i].EmojiID != 0 || got[i].Blink {
			t.Errorf("key %d has no child, got %+v", i, got[i])
		}
	}
}

func TestNullStatus(t *testing.T) {
	path, w := newDB(t)
	exec(t, w, `UPDATE nodes SET status = NULL WHERE id = 2`)
	dev := &fakeTransport{}
	poll(t, newPoller(t, path, dev, &logs{}))

	if got := dev.take()[0]; got.Color != 0xFFFF {
		t.Fatalf("NULL status color = %#x, want 0xFFFF", got.Color)
	}
}

// TestUnknownStatus cannot use the schema's CHECK to insert a bad name, so
// it drops the check by building a table without one.
func TestUnknownStatus(t *testing.T) {
	path, w := newDB(t)
	exec(t, w, `PRAGMA foreign_keys = OFF`)
	exec(t, w, `ALTER TABLE nodes RENAME TO nodes_old`)
	exec(t, w, strings.NewReplacer(
		"CHECK (status IN ('RUNNING','DONE','ERROR','WARN'))", "",
	).Replace(Schema()[strings.Index(Schema(), "CREATE TABLE nodes"):strings.Index(Schema(), "CREATE TABLE state")]))
	exec(t, w, `INSERT INTO nodes SELECT * FROM nodes_old`)
	exec(t, w, `UPDATE nodes SET status = 'BOGUS' WHERE id = 2`)

	dev := &fakeTransport{}
	l := &logs{}
	poll(t, newPoller(t, path, dev, l))

	if got := dev.take()[0]; got.Color != 0 {
		t.Fatalf("unknown status color = %#x, want 0", got.Color)
	}
	if len(l.lines) != 1 || !strings.Contains(l.lines[0], "BOGUS") {
		t.Fatalf("log = %q, want one line naming BOGUS", l.lines)
	}
}

func TestCascadeDelete(t *testing.T) {
	_, w := newDB(t)
	exec(t, w, `DELETE FROM state`)
	exec(t, w, `DELETE FROM nodes WHERE id = 1`)

	var n int
	if err := w.QueryRow(`SELECT COUNT(*) FROM nodes WHERE parent_id = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d children left after deleting their parent", n)
	}
}

func TestStatusColors(t *testing.T) {
	path, w := newDB(t)
	dev := &fakeTransport{}
	p := newPoller(t, path, dev, &logs{})

	cases := []struct {
		status any
		color  uint16
	}{
		{"RUNNING", 0x3AFF},
		{"DONE", 0x16C9},
		{"ERROR", 0xF800},
		{"WARN", 0xFFE0},
		{nil, 0xFFFF},
	}
	for _, c := range cases {
		setStatus(t, w, 2, c.status)
		poll(t, p)
		got := dev.take()[0]
		if got.Color != c.color || got.Blink {
			t.Errorf("status %v: got color %#x blink %v, want %#x no blink", c.status, got.Color, got.Blink, c.color)
		}
	}
}

func TestChannelAssets(t *testing.T) {
	for _, name := range []string{"live", "dev", "gmail", "github", "ai", "create"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "assets", name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		px, err := transport.DecodePNGToRGB565(b)
		if err != nil {
			t.Errorf("%s.png: %v", name, err)
			continue
		}
		if len(px) != transport.CustomGlyphPixelsSize {
			t.Errorf("%s.png: %d bytes, want %d", name, len(px), transport.CustomGlyphPixelsSize)
		}
	}
}
