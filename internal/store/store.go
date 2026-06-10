// Package store is Torchio's state layer: a SQLite database (the pure-Go
// modernc.org/sqlite driver, so CGO_ENABLED=0 holds) holding printers, jobs,
// the append-only job event log and the ephemeral discovery cache, behind WAL
// and transactions.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies the
// schema. WAL, foreign keys and a busy timeout are set via the DSN so every
// pooled connection inherits them. Use ":memory:" for tests, but note that an
// in-memory DB is per-connection, so the pool is pinned to one connection.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if path == ":memory:" {
		// A :memory: database lives in a single connection; pin the pool so
		// the schema is visible to every query.
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for advanced callers (e.g. doctor --fsck).
func (s *Store) DB() *sql.DB { return s.db }

// toMs converts a time to unix milliseconds, treating the zero time as 0.
func toMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// fromMs converts unix milliseconds back to a time, mapping 0 to the zero time.
func fromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
