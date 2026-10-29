package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Settings are the scoring rules for a live game.
type Settings struct {
	PointsCorrect int `json:"points_correct"`
	PenaltyWrong  int `json:"penalty_wrong"`
	AnswerSeconds int `json:"answer_seconds"`
}

// Valid ranges for each setting.
const (
	MinPoints, MaxPoints   = 1, 100
	MinPenalty, MaxPenalty = 0, 100
	MinSeconds, MaxSeconds = 5, 60
)

func (s Settings) Validate() error {
	switch {
	case s.PointsCorrect < MinPoints || s.PointsCorrect > MaxPoints:
		return fmt.Errorf("poin benar harus %d–%d", MinPoints, MaxPoints)
	case s.PenaltyWrong < MinPenalty || s.PenaltyWrong > MaxPenalty:
		return fmt.Errorf("penalti salah harus %d–%d", MinPenalty, MaxPenalty)
	case s.AnswerSeconds < MinSeconds || s.AnswerSeconds > MaxSeconds:
		return fmt.Errorf("batas waktu harus %d–%d detik", MinSeconds, MaxSeconds)
	}
	return nil
}

// Overrides are optional per-quiz settings; nil inherits the global value.
type Overrides struct {
	PointsCorrect *int `json:"points_correct"`
	PenaltyWrong  *int `json:"penalty_wrong"`
	AnswerSeconds *int `json:"answer_seconds"`
}

// Validate checks every non-nil override against its range.
func (o Overrides) Validate() error {
	if o.PointsCorrect != nil && (*o.PointsCorrect < MinPoints || *o.PointsCorrect > MaxPoints) {
		return fmt.Errorf("poin benar harus %d–%d", MinPoints, MaxPoints)
	}
	if o.PenaltyWrong != nil && (*o.PenaltyWrong < MinPenalty || *o.PenaltyWrong > MaxPenalty) {
		return fmt.Errorf("penalti salah harus %d–%d", MinPenalty, MaxPenalty)
	}
	if o.AnswerSeconds != nil && (*o.AnswerSeconds < MinSeconds || *o.AnswerSeconds > MaxSeconds) {
		return fmt.Errorf("batas waktu harus %d–%d detik", MinSeconds, MaxSeconds)
	}
	return nil
}

// Apply returns base with every non-nil override replacing its field.
func (o Overrides) Apply(base Settings) Settings {
	if o.PointsCorrect != nil {
		base.PointsCorrect = *o.PointsCorrect
	}
	if o.PenaltyWrong != nil {
		base.PenaltyWrong = *o.PenaltyWrong
	}
	if o.AnswerSeconds != nil {
		base.AnswerSeconds = *o.AnswerSeconds
	}
	return base
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	var out Settings
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, fmt.Errorf("scan setting: %w", err)
		}
		switch k {
		case "points_correct":
			out.PointsCorrect = v
		case "penalty_wrong":
			out.PenaltyWrong = v
		case "answer_seconds":
			out.AnswerSeconds = v
		}
	}
	return out, rows.Err()
}

// SetSettings stores the global settings. Callers validate first.
func (s *Store) SetSettings(ctx context.Context, v Settings) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for k, val := range map[string]int{"points_correct": v.PointsCorrect, "penalty_wrong": v.PenaltyWrong, "answer_seconds": v.AnswerSeconds} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, val); err != nil {
				return fmt.Errorf("save setting %s: %w", k, err)
			}
		}
		return nil
	})
}

// EffectiveSettings resolves a quiz's settings: each override, else the global value.
func (s *Store) EffectiveSettings(ctx context.Context, q Quiz) (Settings, error) {
	g, err := s.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return q.Overrides.Apply(g), nil
}
