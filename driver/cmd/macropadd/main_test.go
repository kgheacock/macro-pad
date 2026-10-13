package main

import (
	"context"
	"database/sql"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/statedb"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// TestStateDBFlag starts the daemon on an emulator with --state-db and
// checks that a child's status color reaches the emulator. The child sits
// at position 5 because the poller sends keys 0 to 5 in order and the
// emulator keeps only the last message.
func TestStateDBFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	w, err := sql.Open("sqlite3", "file:"+path+"?_foreign_keys=1")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	for _, q := range []string{
		statedb.Schema(),
		`INSERT INTO nodes(id) VALUES (1)`,
		`INSERT INTO nodes(id, parent_id, position, channel, status) VALUES (2, 1, 5, 'DEV', 'DONE')`,
		`INSERT INTO state(id, active_node_id) VALUES (1, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	emuC := make(chan *transport.Emulator, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runCtx(ctx, []string{
			"--emulate", "--state-dir", "",
			"--port", strconv.Itoa(port),
			"--state-db", path, "--state-db-poll", "10ms",
		}, io.Discard, io.Discard, func(e *transport.Emulator) { emuC <- e })
	}()
	emu := <-emuC

	deadline := time.Now().Add(time.Second)
	for {
		if ks, ok := emu.LastKeyState(); ok && ks.KeyIndex == 5 && ks.Color == 0x16C9 {
			break
		}
		if time.Now().After(deadline) {
			ks, _ := emu.LastKeyState()
			t.Fatalf("emulator did not show DONE green within 1s; last key state %+v", ks)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("exit code %d", code)
	}
}
