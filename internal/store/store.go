// Package store persists users, sessions, quizzes, settings, solo attempts and
// live game results in SQLite.
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

const schema = `
CREATE TABLE IF NOT EXISTS users(
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL CHECK(role IN('admin','player')),
	avatar TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions(
	token TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings(
	key TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS quizzes(
	id INTEGER PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	width INTEGER NOT NULL,
	height INTEGER NOT NULL,
	words TEXT NOT NULL,
	published INTEGER NOT NULL DEFAULT 0,
	points_correct INTEGER NULL,
	penalty_wrong INTEGER NULL,
	answer_seconds INTEGER NULL,
	created_by INTEGER NULL REFERENCES users ON DELETE SET NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS attempts(
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	quiz_id INTEGER NOT NULL REFERENCES quizzes ON DELETE CASCADE,
	cells TEXT NOT NULL DEFAULT '{}',
	checks INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL,
	completed_at INTEGER NULL,
	PRIMARY KEY(user_id, quiz_id)
);
CREATE TABLE IF NOT EXISTS live_games(
	id INTEGER PRIMARY KEY,
	quiz_id INTEGER NULL REFERENCES quizzes ON DELETE SET NULL,
	quiz_title TEXT NOT NULL,
	started_at INTEGER NOT NULL,
	ended_at INTEGER NOT NULL,
	settings TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_players(
	game_id INTEGER NOT NULL REFERENCES live_games ON DELETE CASCADE,
	user_id INTEGER NULL REFERENCES users ON DELETE SET NULL,
	nickname TEXT NOT NULL,
	avatar TEXT NOT NULL,
	score INTEGER NOT NULL,
	correct INTEGER NOT NULL,
	wrong INTEGER NOT NULL,
	correct_ms INTEGER NOT NULL,
	rank INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS attempts_quiz ON attempts(quiz_id);
CREATE INDEX IF NOT EXISTS live_players_game ON live_players(game_id);
CREATE INDEX IF NOT EXISTS live_games_quiz ON live_games(quiz_id);
INSERT OR IGNORE INTO settings(key, value) VALUES ('points_correct', 10), ('penalty_wrong', 5), ('answer_seconds', 10);
`

type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path. Pragmas are part of the
// DSN so every pooled connection gets foreign keys, WAL and a busy timeout.
func Open(path string) (*Store, error) {
	q := url.Values{"_pragma": {"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func isUnique(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && (se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || se.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY)
}

// ValidAvatar reports whether raw is a JSON object of at most 512 bytes once
// compacted, returning the compacted form.
func ValidAvatar(raw json.RawMessage) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, false
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil || b.Len() > 512 {
		return nil, false
	}
	return json.RawMessage(b.Bytes()), true
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
