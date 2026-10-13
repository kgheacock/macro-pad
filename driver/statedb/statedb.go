// Package statedb shows a SQLite page tree on the macro pad's keys. See
// task 0051 and the "State database" section of driver/README.md.
//
// The poller only reads. It opens the database read-only, holds one
// connection so PRAGMA data_version works, and reads the children of the
// active node only when (active_node_id, children_rev) changes.
package statedb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// Schema is the SQL that creates the tables the poller reads. Tests and
// task 0052's writer use it to create a database.
//
//go:embed schema.sql
var schemaFS embed.FS

// Schema returns the contents of schema.sql.
func Schema() string {
	b, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		panic(err) // embedded at build time
	}
	return string(b)
}

// KeyCount is the number of keys on the pad. A child's position is its key
// index, so positions 0 through KeyCount-1 are shown.
const KeyCount = 6

// Key colors, RGB565, for each status. Picked by rendering every channel
// icon on every color; pure blue hid the dark icons, so RUNNING is lifted.
const (
	ColorRunning uint16 = 0x3AFF // blue #3D5CFF
	ColorDone    uint16 = 0x16C9 // green #12D84A
	ColorError   uint16 = 0xF800 // red
	ColorWarn    uint16 = 0xFFE0 // yellow
	ColorNone    uint16 = 0xFFFF // white: status is NULL
)

var statusColors = map[string]uint16{
	"RUNNING": ColorRunning,
	"DONE":    ColorDone,
	"ERROR":   ColorError,
	"WARN":    ColorWarn,
}

// Child is one row of nodes read for the active node. Channel and Message
// are read and compared but not drawn yet; see task 0051's non-goals.
type Child struct {
	Position int
	Channel  sql.NullString
	Message  sql.NullString
	Status   sql.NullString
}

// Poller reads a state database and sends key states to a Transport.
type Poller struct {
	db   *sql.DB
	conn *sql.Conn
	dev  transport.Transport
	logf func(format string, args ...any)

	lastDataVersion int64
	haveDataVersion bool
	lastActive      int64
	lastRev         int64
	havePair        bool

	// childQueries counts reads of the children rows, for tests.
	childQueries int
}

// DSN returns the connection string that opens path read-only with
// foreign keys on.
func DSN(path string) string {
	return "file:" + path + "?mode=ro&_foreign_keys=1"
}

// New opens the database at path read-only and returns a Poller for it.
// logf may be nil.
func New(path string, dev transport.Transport, logf func(format string, args ...any)) (*Poller, error) {
	db, err := sql.Open("sqlite3", DSN(path))
	if err != nil {
		return nil, err
	}
	// data_version is per connection, so every read must use the same one.
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		return nil, err
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Poller{db: db, conn: conn, dev: dev, logf: logf}, nil
}

// Close releases the database connection.
func (p *Poller) Close() error {
	p.conn.Close()
	return p.db.Close()
}

// Run polls every interval until ctx ends. A read error is logged and
// retried on the next tick; the board keeps its last state.
func Run(ctx context.Context, path string, interval time.Duration, dev transport.Transport, logf func(format string, args ...any)) error {
	p, err := New(path, dev, logf)
	if err != nil {
		return err
	}
	defer p.Close()
	return p.Run(ctx, interval)
}

// Run polls every interval until ctx ends. It does not close p.
func (p *Poller) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := p.Poll(ctx); err != nil && ctx.Err() == nil {
			p.logf("state-db: %v", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// Poll does one tick. It sends nothing when data_version, or the
// (active_node_id, children_rev) pair, has not changed. On an error it
// keeps its last values, so the next tick tries again.
func (p *Poller) Poll(ctx context.Context) error {
	var dv int64
	if err := p.conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&dv); err != nil {
		return fmt.Errorf("read data_version: %w", err)
	}
	if p.haveDataVersion && dv == p.lastDataVersion {
		return nil
	}

	var active, rev int64
	err := p.conn.QueryRowContext(ctx,
		`SELECT s.active_node_id, n.children_rev FROM state s JOIN nodes n ON n.id = s.active_node_id`,
	).Scan(&active, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		// No active node: nothing to show.
		p.lastDataVersion, p.haveDataVersion, p.havePair = dv, true, false
		return nil
	}
	if err != nil {
		return fmt.Errorf("read active node: %w", err)
	}
	if p.havePair && active == p.lastActive && rev == p.lastRev {
		p.lastDataVersion, p.haveDataVersion = dv, true
		return nil
	}

	children, err := p.readChildren(ctx, active)
	if err != nil {
		return err
	}
	if err := p.send(children); err != nil {
		return err
	}
	p.lastDataVersion, p.haveDataVersion = dv, true
	p.lastActive, p.lastRev, p.havePair = active, rev, true
	return nil
}

func (p *Poller) readChildren(ctx context.Context, parent int64) ([]Child, error) {
	p.childQueries++
	rows, err := p.conn.QueryContext(ctx,
		`SELECT position, channel, message, status FROM nodes WHERE parent_id = ? ORDER BY position`, parent)
	if err != nil {
		return nil, fmt.Errorf("read children: %w", err)
	}
	defer rows.Close()
	var out []Child
	for rows.Next() {
		var c Child
		if err := rows.Scan(&c.Position, &c.Channel, &c.Message, &c.Status); err != nil {
			return nil, fmt.Errorf("read children: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read children: %w", err)
	}
	return out, nil
}

// send writes one KeyState for each of keys 0 through KeyCount-1. A key
// with no child gets color 0, emoji 0, no blink.
func (p *Poller) send(children []Child) error {
	byPos := make(map[int]Child, len(children))
	for _, c := range children {
		byPos[c.Position] = c
	}
	for i := 0; i < KeyCount; i++ {
		ks := transport.KeyState{KeyIndex: byte(i), Version: transport.ProtocolVersion}
		if c, ok := byPos[i]; ok {
			ks.Color = p.color(i, c.Status)
		}
		if err := p.dev.SendKeyState(ks); err != nil {
			return fmt.Errorf("send key %d: %w", i, err)
		}
	}
	return nil
}

func (p *Poller) color(key int, status sql.NullString) uint16 {
	if !status.Valid {
		return ColorNone
	}
	if c, ok := statusColors[status.String]; ok {
		return c
	}
	p.logf("state-db: key %d: unknown status %q", key, status.String)
	return 0
}
