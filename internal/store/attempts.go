package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Attempt is one user's solo progress on a quiz. Cells maps "r,c" to a letter.
type Attempt struct {
	Cells       map[string]string `json:"cells"`
	Checks      int               `json:"checks"`
	StartedAt   int64             `json:"started_at"`
	CompletedAt *int64            `json:"completed_at"`
}

type LeaderboardEntry struct {
	Username    string          `json:"username"`
	Avatar      json.RawMessage `json:"avatar"`
	DurationMS  int64           `json:"duration_ms"`
	Checks      int             `json:"checks"`
	CompletedAt int64           `json:"completed_at"`
}

// SoloResult is one user's attempt as listed for the admin.
type SoloResult struct {
	Username    string `json:"username"`
	Checks      int    `json:"checks"`
	StartedAt   int64  `json:"started_at"`
	CompletedAt *int64 `json:"completed_at"`
	Filled      int    `json:"filled"`
	DurationMS  *int64 `json:"duration_ms"`
}

func decodeCells(raw string) (map[string]string, error) {
	cells := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &cells); err != nil {
		return nil, fmt.Errorf("decode cells: %w", err)
	}
	return cells, nil
}

// StartAttempt returns the user's attempt on a quiz, creating an empty one at now if none exists.
func (s *Store) StartAttempt(ctx context.Context, userID, quizID, now int64) (Attempt, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO attempts(user_id, quiz_id, started_at) VALUES (?, ?, ?)`,
		userID, quizID, now); err != nil {
		return Attempt{}, fmt.Errorf("start attempt: %w", err)
	}
	return s.attempt(ctx, s.db, userID, quizID)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) attempt(ctx context.Context, db queryer, userID, quizID int64) (Attempt, error) {
	var a Attempt
	var cells string
	var completed sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT cells, checks, started_at, completed_at FROM attempts WHERE user_id = ? AND quiz_id = ?`,
		userID, quizID).Scan(&cells, &a.Checks, &a.StartedAt, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrNotFound
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("load attempt: %w", err)
	}
	if a.Cells, err = decodeCells(cells); err != nil {
		return Attempt{}, err
	}
	if completed.Valid {
		a.CompletedAt = &completed.Int64
	}
	return a, nil
}

// SaveProgress stores cells for an uncompleted attempt, creating it if needed. Completed attempts are left untouched.
func (s *Store) SaveProgress(ctx context.Context, userID, quizID int64, cells map[string]string, now int64) error {
	raw, err := json.Marshal(cells)
	if err != nil {
		return fmt.Errorf("encode cells: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO attempts(user_id, quiz_id, cells, started_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, quiz_id) DO UPDATE SET cells = excluded.cells WHERE completed_at IS NULL`,
		userID, quizID, string(raw), now); err != nil {
		return fmt.Errorf("save progress: %w", err)
	}
	return nil
}

// RecordCheck counts one check on an uncompleted attempt and stores cells; when
// completed is true the attempt is finished at now. Already-completed attempts are
// returned unchanged.
func (s *Store) RecordCheck(ctx context.Context, userID, quizID int64, cells map[string]string, completed bool, now int64) (Attempt, error) {
	raw, err := json.Marshal(cells)
	if err != nil {
		return Attempt{}, fmt.Errorf("encode cells: %w", err)
	}
	var doneAt *int64
	if completed {
		doneAt = &now
	}
	var a Attempt
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attempts(user_id, quiz_id, started_at) VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, userID, quizID, now); err != nil {
			return fmt.Errorf("ensure attempt: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attempts SET cells = ?, checks = checks + 1, completed_at = ?
			WHERE user_id = ? AND quiz_id = ? AND completed_at IS NULL`, string(raw), doneAt, userID, quizID); err != nil {
			return fmt.Errorf("record check: %w", err)
		}
		a, err = s.attempt(ctx, tx, userID, quizID)
		return err
	})
	return a, err
}

// Leaderboard returns the top 50 completed attempts by duration, then checks.
func (s *Store) Leaderboard(ctx context.Context, quizID int64) ([]LeaderboardEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username, u.avatar, a.completed_at - a.started_at AS d, a.checks, a.completed_at
		FROM attempts a JOIN users u ON u.id = a.user_id
		WHERE a.quiz_id = ? AND a.completed_at IS NOT NULL
		ORDER BY d, a.checks, a.completed_at LIMIT 50`, quizID)
	if err != nil {
		return nil, fmt.Errorf("leaderboard: %w", err)
	}
	defer rows.Close()
	out := []LeaderboardEntry{}
	for rows.Next() {
		var e LeaderboardEntry
		var avatar string
		if err := rows.Scan(&e.Username, &avatar, &e.DurationMS, &e.Checks, &e.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		e.Avatar = json.RawMessage(avatar)
		out = append(out, e)
	}
	return out, rows.Err()
}

// SoloResults lists every attempt on a quiz, completed first (fastest first), then by start time.
func (s *Store) SoloResults(ctx context.Context, quizID int64) ([]SoloResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.username, a.checks, a.started_at, a.completed_at, a.cells
		FROM attempts a JOIN users u ON u.id = a.user_id WHERE a.quiz_id = ?
		ORDER BY a.completed_at IS NULL, a.completed_at - a.started_at, a.started_at`, quizID)
	if err != nil {
		return nil, fmt.Errorf("solo results: %w", err)
	}
	defer rows.Close()
	out := []SoloResult{}
	for rows.Next() {
		var r SoloResult
		var completed sql.NullInt64
		var raw string
		if err := rows.Scan(&r.Username, &r.Checks, &r.StartedAt, &completed, &raw); err != nil {
			return nil, fmt.Errorf("scan solo result: %w", err)
		}
		cells, err := decodeCells(raw)
		if err != nil {
			return nil, err
		}
		for _, v := range cells {
			if v != "" {
				r.Filled++
			}
		}
		if completed.Valid {
			r.CompletedAt = &completed.Int64
			r.DurationMS = new(completed.Int64 - r.StartedAt)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
