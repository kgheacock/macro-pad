// Package statelog writes the state database that macropadd's poller reads.
// See task 0052 and the "Event log" section of driver/README.md.
//
// Every change is one row in the append-only events table. nodes and state
// are a projection of that log. Append adds an event and updates the
// projection in one transaction, and Rebuild replays the whole log into an
// empty projection. Both call apply, so they cannot drift apart.
package statelog

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/kgheacock/macro-pad/driver/statedb"
)

//go:embed events.sql
var eventsFS embed.FS

// Event types.
const (
	TypeNodeSet    = "node_set"
	TypeNodeDelete = "node_delete"
	TypeActivate   = "activate"
)

// NodeSet is the payload of a node_set event. It holds the full new values
// of the node, not a delta. A nil pointer is NULL.
type NodeSet struct {
	ID       int64   `json:"id"`
	ParentID *int64  `json:"parent_id"`
	Position *int64  `json:"position"`
	Channel  *string `json:"channel"`
	Message  *string `json:"message"`
	Status   *string `json:"status"`
}

// NodeDelete is the payload of a node_delete event.
type NodeDelete struct {
	ID int64 `json:"id"`
}

// Activate is the payload of an activate event. A nil NodeID clears the
// active node.
type Activate struct {
	NodeID *int64 `json:"node_id"`
}

// Event is one change to append. Payload is marshaled to JSON, so it is
// normally a NodeSet, NodeDelete or Activate.
type Event struct {
	Type    string
	Payload any
}

// Log is a state database opened for writing.
type Log struct {
	db  *sql.DB
	now func() time.Time
}

// DSN returns the connection string that opens path for writing. Foreign
// keys are on because node_delete relies on ON DELETE CASCADE. WAL lets the
// read-only poller read while a writer works. Transactions start with
// BEGIN IMMEDIATE so two writers wait for each other instead of failing.
func DSN(path string) string {
	return "file:" + path + "?_foreign_keys=1&_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"
}

// Open opens the database at path for writing. A new file gets the nodes
// and state tables from statedb and the events table, and the single state
// row.
func Open(path string) (*Log, error) {
	db, err := sql.Open("sqlite3", DSN(path))
	if err != nil {
		return nil, err
	}
	// Foreign keys are per connection; one connection keeps the rule simple.
	db.SetMaxOpenConns(1)
	l := &Log{db: db, now: time.Now}
	if err := l.init(); err != nil {
		db.Close()
		return nil, err
	}
	return l, nil
}

// Close releases the database connection.
func (l *Log) Close() error { return l.db.Close() }

func (l *Log) init() error {
	tx, err := l.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'nodes'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := tx.Exec(statedb.Schema()); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	events, err := eventsFS.ReadFile("events.sql")
	if err != nil {
		return err // embedded at build time
	}
	if _, err := tx.Exec(string(events)); err != nil {
		return fmt.Errorf("create events table: %w", err)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO state(id, active_node_id) VALUES (1, NULL)`); err != nil {
		return err
	}
	return tx.Commit()
}

// Append adds ev to the log and reduces it into nodes and state, in one
// transaction. It returns the new event's id. If the event is unknown or
// invalid, nothing is written, not even the event row.
func (l *Log) Append(ev Event) (int64, error) {
	payload, err := json.Marshal(ev.Payload)
	if err != nil {
		return 0, fmt.Errorf("marshal %s payload: %w", ev.Type, err)
	}
	tx, err := l.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO events(at, type, payload) VALUES (?, ?, ?)`,
		l.now().UTC().Format(time.RFC3339Nano), ev.Type, string(payload))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := apply(tx, ev.Type, payload); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Rebuild empties nodes and state, then replays every event in id order. It
// runs in one transaction, so the poller sees either the old projection or
// the new one. A rebuild gives the rows that live writes gave, because
// children_rev depends on the order of events.
func (l *Log) Rebuild() error {
	tx, err := l.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// state references nodes, so it goes first.
	if _, err := tx.Exec(`DELETE FROM state`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO state(id, active_node_id) VALUES (1, NULL)`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id, type, payload FROM events ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var typ, payload string
		if err := rows.Scan(&id, &typ, &payload); err != nil {
			return err
		}
		if err := apply(tx, typ, []byte(payload)); err != nil {
			return fmt.Errorf("replay event %d: %w", id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	return tx.Commit()
}

// apply reduces one event into nodes and state. It is the only code that
// writes those tables. The caller owns the transaction.
func apply(tx *sql.Tx, typ string, payload []byte) error {
	switch typ {
	case TypeNodeSet:
		var p NodeSet
		if err := decode(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", typ, err)
		}
		return applyNodeSet(tx, p)
	case TypeNodeDelete:
		var p NodeDelete
		if err := decode(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", typ, err)
		}
		return applyNodeDelete(tx, p)
	case TypeActivate:
		var p Activate
		if err := decode(payload, &p); err != nil {
			return fmt.Errorf("%s: %w", typ, err)
		}
		_, err := tx.Exec(`UPDATE state SET active_node_id = ? WHERE id = 1`, p.NodeID)
		if err != nil {
			return fmt.Errorf("%s: %w", typ, err)
		}
		return nil
	}
	return fmt.Errorf("unknown event type %q", typ)
}

// decode rejects unknown fields, so a misspelled field is an error and not
// a silent NULL.
func decode(payload []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// applyNodeSet upserts one node. It uses UPDATE and not INSERT OR REPLACE,
// because a replace deletes the row and cascades to the node's children.
func applyNodeSet(tx *sql.Tx, p NodeSet) error {
	if p.ParentID != nil && *p.ParentID == p.ID {
		return fmt.Errorf("node_set: node %d is its own parent", p.ID)
	}
	var oldParent, oldPos sql.NullInt64
	var oldChannel, oldMessage, oldStatus sql.NullString
	err := tx.QueryRow(`SELECT parent_id, position, channel, message, status FROM nodes WHERE id = ?`, p.ID).
		Scan(&oldParent, &oldPos, &oldChannel, &oldMessage, &oldStatus)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.Exec(`INSERT INTO nodes(id, parent_id, position, channel, message, status) VALUES (?, ?, ?, ?, ?, ?)`,
			p.ID, p.ParentID, p.Position, p.Channel, p.Message, p.Status); err != nil {
			return fmt.Errorf("node_set: %w", err)
		}
		return bump(tx, p.ParentID)
	}
	if err != nil {
		return fmt.Errorf("node_set: %w", err)
	}

	newParent := nullInt(p.ParentID)
	if oldParent == newParent && oldPos == nullInt(p.Position) &&
		oldChannel == nullString(p.Channel) && oldMessage == nullString(p.Message) &&
		oldStatus == nullString(p.Status) {
		return nil // no visible field changed
	}
	if _, err := tx.Exec(`UPDATE nodes SET parent_id = ?, position = ?, channel = ?, message = ?, status = ? WHERE id = ?`,
		p.ParentID, p.Position, p.Channel, p.Message, p.Status, p.ID); err != nil {
		return fmt.Errorf("node_set: %w", err)
	}
	if err := bump(tx, p.ParentID); err != nil {
		return err
	}
	if oldParent != newParent {
		return bump(tx, nullIntPtr(oldParent)) // a move changes the old page too
	}
	return nil
}

func applyNodeDelete(tx *sql.Tx, p NodeDelete) error {
	var parent sql.NullInt64
	err := tx.QueryRow(`SELECT parent_id FROM nodes WHERE id = ?`, p.ID).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("node_delete: node %d does not exist", p.ID)
	}
	if err != nil {
		return fmt.Errorf("node_delete: %w", err)
	}
	// Foreign keys remove the children. If the active node is among them,
	// the delete fails and the whole event rolls back.
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, p.ID); err != nil {
		return fmt.Errorf("node_delete: %w", err)
	}
	return bump(tx, nullIntPtr(parent))
}

// bump adds one to children_rev of the node id. A nil id is a root, which
// has no parent to bump.
func bump(tx *sql.Tx, id *int64) error {
	if id == nil {
		return nil
	}
	if _, err := tx.Exec(`UPDATE nodes SET children_rev = children_rev + 1 WHERE id = ?`, *id); err != nil {
		return fmt.Errorf("bump children_rev: %w", err)
	}
	return nil
}

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func nullIntPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func nullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}
