package statelog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/statedb"
	"github.com/kgheacock/macro-pad/driver/transport"
)

func ptr[T any](v T) *T { return &v }

func newLog(t *testing.T) (*Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, path
}

func append1(t *testing.T, l *Log, typ string, payload any) {
	t.Helper()
	if _, err := l.Append(Event{Type: typ, Payload: payload}); err != nil {
		t.Fatalf("append %s %+v: %v", typ, payload, err)
	}
}

// page makes root node 1 with two children: 2 at position 0 and 3 at 1.
func page(t *testing.T, l *Log) {
	t.Helper()
	append1(t, l, TypeNodeSet, NodeSet{ID: 1})
	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("RUNNING")})
	append1(t, l, TypeNodeSet, NodeSet{ID: 3, ParentID: ptr(int64(1)), Position: ptr(int64(1)), Channel: ptr("GITHUB"), Message: ptr("pr"), Status: ptr("DONE")})
}

func rev(t *testing.T, l *Log, id int64) int64 {
	t.Helper()
	var r int64
	if err := l.db.QueryRow(`SELECT children_rev FROM nodes WHERE id = ?`, id).Scan(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, l *Log, table string) int {
	t.Helper()
	var n int
	if err := l.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNodeSetBumpsParent(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	before := rev(t, l, 1)

	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("ERROR")})

	var status string
	if err := l.db.QueryRow(`SELECT status FROM nodes WHERE id = 2`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ERROR" {
		t.Errorf("status = %q, want ERROR", status)
	}
	if got := rev(t, l, 1); got != before+1 {
		t.Errorf("parent children_rev = %d, want %d", got, before+1)
	}
}

func TestNoopEventNoBump(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	before := rev(t, l, 1)

	// The same values as node 2 holds.
	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("RUNNING")})

	if got := rev(t, l, 1); got != before {
		t.Errorf("children_rev = %d after a no-op event, want %d", got, before)
	}
	if n := count(t, l, "events"); n != 4 {
		t.Errorf("events = %d, want 4: a no-op event is still logged", n)
	}
}

func TestNodeSetKeepsChildren(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	// Change the root's own fields. Its children must stay.
	append1(t, l, TypeNodeSet, NodeSet{ID: 1, Message: ptr("home")})
	if n := count(t, l, "nodes"); n != 3 {
		t.Fatalf("nodes = %d, want 3", n)
	}
}

func TestMoveBumpsBothParents(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	append1(t, l, TypeNodeSet, NodeSet{ID: 10})
	a, b := rev(t, l, 1), rev(t, l, 10)

	append1(t, l, TypeNodeSet, NodeSet{ID: 3, ParentID: ptr(int64(10)), Position: ptr(int64(0)), Channel: ptr("GITHUB"), Message: ptr("pr"), Status: ptr("DONE")})

	if got := rev(t, l, 1); got != a+1 {
		t.Errorf("old parent children_rev = %d, want %d", got, a+1)
	}
	if got := rev(t, l, 10); got != b+1 {
		t.Errorf("new parent children_rev = %d, want %d", got, b+1)
	}
}

func TestNodeDeleteBumpsParentAndCascades(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	append1(t, l, TypeNodeSet, NodeSet{ID: 20, ParentID: ptr(int64(2)), Position: ptr(int64(0))})
	before := rev(t, l, 1)

	append1(t, l, TypeNodeDelete, NodeDelete{ID: 2})

	if n := count(t, l, "nodes"); n != 2 {
		t.Errorf("nodes = %d, want 2 (root and node 3)", n)
	}
	if got := rev(t, l, 1); got != before+1 {
		t.Errorf("parent children_rev = %d, want %d", got, before+1)
	}
}

func TestActivateDoesNotBump(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	before := rev(t, l, 1)

	append1(t, l, TypeActivate, Activate{NodeID: ptr(int64(1))})

	var active int64
	if err := l.db.QueryRow(`SELECT active_node_id FROM state WHERE id = 1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Errorf("active_node_id = %d, want 1", active)
	}
	if got := rev(t, l, 1); got != before {
		t.Errorf("children_rev = %d, want %d", got, before)
	}
}

func TestUnknownTypeRollsBack(t *testing.T) {
	l, _ := newLog(t)
	if _, err := l.Append(Event{Type: "explode", Payload: map[string]int{"id": 1}}); err == nil {
		t.Fatal("unknown type returned no error")
	}
	if n := count(t, l, "events"); n != 0 {
		t.Fatalf("events = %d after a failed append, want 0", n)
	}
}

func TestFailedEventRollsBack(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	append1(t, l, TypeActivate, Activate{NodeID: ptr(int64(1))})
	events, before := count(t, l, "events"), rev(t, l, 1)

	cases := []struct {
		name string
		ev   Event
	}{
		{"missing parent", Event{TypeNodeSet, NodeSet{ID: 50, ParentID: ptr(int64(99)), Position: ptr(int64(0))}}},
		{"position taken", Event{TypeNodeSet, NodeSet{ID: 50, ParentID: ptr(int64(1)), Position: ptr(int64(0))}}},
		{"bad status", Event{TypeNodeSet, NodeSet{ID: 50, ParentID: ptr(int64(1)), Position: ptr(int64(2)), Status: ptr("LOUD")}}},
		{"own parent", Event{TypeNodeSet, NodeSet{ID: 50, ParentID: ptr(int64(50))}}},
		{"misspelled field", Event{TypeNodeSet, map[string]any{"id": 50, "stauts": "DONE"}}},
		{"delete missing", Event{TypeNodeDelete, NodeDelete{ID: 99}}},
		{"delete active", Event{TypeNodeDelete, NodeDelete{ID: 1}}},
		{"activate missing", Event{TypeActivate, Activate{NodeID: ptr(int64(99))}}},
	}
	for _, c := range cases {
		if _, err := l.Append(c.ev); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
	if n := count(t, l, "events"); n != events {
		t.Errorf("events = %d, want %d", n, events)
	}
	if got := rev(t, l, 1); got != before {
		t.Errorf("children_rev = %d, want %d", got, before)
	}
	if n := count(t, l, "nodes"); n != 3 {
		t.Errorf("nodes = %d, want 3", n)
	}
}

func TestEventsAppendOnly(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	if _, err := l.db.Exec(`UPDATE events SET type = 'x'`); err == nil {
		t.Error("UPDATE on events succeeded")
	}
	if _, err := l.db.Exec(`DELETE FROM events`); err == nil {
		t.Error("DELETE on events succeeded")
	}
}

// dump reads every row of nodes and state, including children_rev.
func dump(t *testing.T, l *Log) []string {
	t.Helper()
	var out []string
	rows, err := l.db.Query(`SELECT 'node', id, parent_id, position, channel, message, status, children_rev FROM nodes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		var id, rev int64
		var parent, pos sql.NullInt64
		var ch, msg, st sql.NullString
		if err := rows.Scan(&kind, &id, &parent, &pos, &ch, &msg, &st, &rev); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(kind, id, parent, pos, ch, msg, st, rev))
	}
	rows.Close()
	var active sql.NullInt64
	if err := l.db.QueryRow(`SELECT active_node_id FROM state WHERE id = 1`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	return append(out, fmt.Sprint("state", active), fmt.Sprint("state rows", count(t, l, "state")))
}

func TestRebuildEqualsLive(t *testing.T) {
	l, _ := newLog(t)
	page(t, l)
	append1(t, l, TypeNodeSet, NodeSet{ID: 10})
	append1(t, l, TypeNodeSet, NodeSet{ID: 11, ParentID: ptr(int64(10)), Position: ptr(int64(0)), Status: ptr("WARN")})
	append1(t, l, TypeActivate, Activate{NodeID: ptr(int64(1))})
	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("ERROR")})
	// A no-op, a move, a delete with a cascade, and a page switch.
	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("ERROR")})
	append1(t, l, TypeNodeSet, NodeSet{ID: 3, ParentID: ptr(int64(10)), Position: ptr(int64(1)), Status: ptr("DONE")})
	append1(t, l, TypeNodeSet, NodeSet{ID: 12, ParentID: ptr(int64(11)), Position: ptr(int64(0))})
	append1(t, l, TypeNodeDelete, NodeDelete{ID: 11})
	append1(t, l, TypeActivate, Activate{NodeID: ptr(int64(10))})
	live := dump(t, l)

	// Damage the projection, as a writer that bypasses statelog would.
	if _, err := l.db.Exec(`UPDATE nodes SET children_rev = 99`); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(live, dump(t, l)) {
		t.Fatal("setup: damage changed nothing")
	}

	if err := l.Rebuild(); err != nil {
		t.Fatal(err)
	}
	if got := dump(t, l); !reflect.DeepEqual(live, got) {
		t.Errorf("rebuilt rows differ\nlive:    %v\nrebuilt: %v", live, got)
	}
}

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

func (f *fakeTransport) take() []transport.KeyState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not met within 1s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPollerSeesAppend(t *testing.T) {
	l, path := newLog(t)
	page(t, l)
	append1(t, l, TypeActivate, Activate{NodeID: ptr(int64(1))})

	dev := &fakeTransport{}
	p, err := statedb.New(path, dev, func(f string, a ...any) { t.Logf(f, a...) })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx, 5*time.Millisecond) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "first page", func() bool { return dev.count() >= statedb.KeyCount })
	dev.take()

	append1(t, l, TypeNodeSet, NodeSet{ID: 2, ParentID: ptr(int64(1)), Position: ptr(int64(0)), Channel: ptr("DEV"), Message: ptr("build"), Status: ptr("ERROR")})

	waitFor(t, "status change", func() bool { return dev.count() > 0 })
	got := dev.take()
	if got[0].KeyIndex != 0 || got[0].Color != statedb.ColorError {
		t.Fatalf("first message after the append = %+v, want key 0 color %#x", got[0], statedb.ColorError)
	}
}

func TestOpenKeepsExistingFile(t *testing.T) {
	l, path := newLog(t)
	page(t, l)
	l.Close()

	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if n := count(t, l2, "nodes"); n != 3 {
		t.Errorf("nodes = %d after reopen, want 3", n)
	}
}
