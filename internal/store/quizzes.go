package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"tts/internal/crossword"
)

// Quiz is a stored crossword. Words are already validated by crossword.Build.
type Quiz struct {
	ID          int64
	Title       string
	Description string
	Width       int
	Height      int
	Words       []crossword.PlacedWord
	Published   bool
	Overrides   Overrides
	CreatedBy   *int64
	CreatedAt   int64
	UpdatedAt   int64
}

// AdminQuizRow is one line of the admin quiz list.
type AdminQuizRow struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Published bool   `json:"published"`
	WordCount int    `json:"wordCount"`
	Players   int    `json:"players"`
	Solvers   int    `json:"solvers"`
	LiveGames int    `json:"liveGames"`
	UpdatedAt int64  `json:"updated_at"`
}

// SoloQuizRow is one published quiz as seen by a player.
type SoloQuizRow struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	WordCount   int    `json:"wordCount"`
	Solvers     int    `json:"solvers"`
	Status      string `json:"status"`
	DurationMS  *int64 `json:"duration_ms"`
	// Cells lists open squares as [row, col] for the card preview. Shape only, never letters.
	Cells [][2]int `json:"cells"`
}

const quizCols = `id, title, description, width, height, words, published, points_correct, penalty_wrong, answer_seconds, created_by, created_at, updated_at`

func scanQuiz(row interface{ Scan(...any) error }) (Quiz, error) {
	var q Quiz
	var words string
	var pc, pw, as, by sql.NullInt64
	if err := row.Scan(&q.ID, &q.Title, &q.Description, &q.Width, &q.Height, &words, &q.Published,
		&pc, &pw, &as, &by, &q.CreatedAt, &q.UpdatedAt); err != nil {
		return Quiz{}, err
	}
	if err := json.Unmarshal([]byte(words), &q.Words); err != nil {
		return Quiz{}, fmt.Errorf("quiz %d words: %w", q.ID, err)
	}
	q.Overrides = Overrides{PointsCorrect: nullInt(pc), PenaltyWrong: nullInt(pw), AnswerSeconds: nullInt(as)}
	if by.Valid {
		q.CreatedBy = &by.Int64
	}
	return q, nil
}

func nullInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	return new(int(v.Int64))
}

func (s *Store) Quiz(ctx context.Context, id int64) (Quiz, error) {
	q, err := scanQuiz(s.db.QueryRowContext(ctx, `SELECT `+quizCols+` FROM quizzes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Quiz{}, ErrNotFound
	}
	if err != nil {
		return Quiz{}, fmt.Errorf("load quiz %d: %w", id, err)
	}
	return q, nil
}

func (s *Store) CreateQuiz(ctx context.Context, q Quiz) (int64, error) {
	words, err := json.Marshal(q.Words)
	if err != nil {
		return 0, fmt.Errorf("encode words: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO quizzes(title, description, width, height, words, published,
		points_correct, penalty_wrong, answer_seconds, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.Title, q.Description, q.Width, q.Height, string(words), q.Published,
		q.Overrides.PointsCorrect, q.Overrides.PenaltyWrong, q.Overrides.AnswerSeconds, q.CreatedBy, q.CreatedAt, q.UpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("insert quiz: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("quiz id: %w", err)
	}
	return id, nil
}

// placement is the answer-and-position part of a word; clue edits do not change it.
type placement struct {
	answer   string
	row, col int
	dir      crossword.Direction
}

func placements(words []crossword.PlacedWord) []placement {
	out := make([]placement, len(words))
	for i, w := range words {
		out[i] = placement{w.Answer, w.Row, w.Col, w.Direction}
	}
	slices.SortFunc(out, func(a, b placement) int {
		return cmp.Or(cmp.Compare(a.row, b.row), cmp.Compare(a.col, b.col),
			cmp.Compare(a.dir, b.dir), cmp.Compare(a.answer, b.answer))
	})
	return out
}

// UpdateQuiz replaces a quiz's content (CreatedBy/CreatedAt are kept). When any
// answer or position changed, solo attempts are deleted and resetAttempts is true.
func (s *Store) UpdateQuiz(ctx context.Context, q Quiz) (resetAttempts bool, err error) {
	words, err := json.Marshal(q.Words)
	if err != nil {
		return false, fmt.Errorf("encode words: %w", err)
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		old, err := scanQuiz(tx.QueryRowContext(ctx, `SELECT `+quizCols+` FROM quizzes WHERE id = ?`, q.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load quiz %d: %w", q.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE quizzes SET title = ?, description = ?, width = ?, height = ?, words = ?,
			published = ?, points_correct = ?, penalty_wrong = ?, answer_seconds = ?, updated_at = ? WHERE id = ?`,
			q.Title, q.Description, q.Width, q.Height, string(words), q.Published,
			q.Overrides.PointsCorrect, q.Overrides.PenaltyWrong, q.Overrides.AnswerSeconds, q.UpdatedAt, q.ID); err != nil {
			return fmt.Errorf("update quiz: %w", err)
		}
		if slices.Equal(placements(old.Words), placements(q.Words)) {
			return nil
		}
		resetAttempts = true
		if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE quiz_id = ?`, q.ID); err != nil {
			return fmt.Errorf("reset attempts: %w", err)
		}
		return nil
	})
	return resetAttempts, err
}

func (s *Store) DeleteQuiz(ctx context.Context, id int64) error {
	return s.execOne(ctx, "delete quiz", `DELETE FROM quizzes WHERE id = ?`, id)
}

func (s *Store) AdminQuizzes(ctx context.Context) ([]AdminQuizRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.id, q.title, q.published, json_array_length(q.words),
		       (SELECT count(*) FROM attempts a WHERE a.quiz_id = q.id),
		       (SELECT count(*) FROM attempts a WHERE a.quiz_id = q.id AND a.completed_at IS NOT NULL),
		       (SELECT count(*) FROM live_games g WHERE g.quiz_id = q.id),
		       q.updated_at
		FROM quizzes q ORDER BY q.updated_at DESC, q.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list quizzes: %w", err)
	}
	defer rows.Close()
	out := []AdminQuizRow{}
	for rows.Next() {
		var r AdminQuizRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Published, &r.WordCount, &r.Players, &r.Solvers, &r.LiveGames, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan quiz: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SoloQuizzes lists published quizzes with the given user's attempt status.
func (s *Store) SoloQuizzes(ctx context.Context, userID int64) ([]SoloQuizRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT q.id, q.title, q.description, q.width, q.height, q.words,
		       (SELECT count(*) FROM attempts a WHERE a.quiz_id = q.id AND a.completed_at IS NOT NULL),
		       me.started_at, me.completed_at
		FROM quizzes q LEFT JOIN attempts me ON me.quiz_id = q.id AND me.user_id = ?
		WHERE q.published = 1 ORDER BY q.created_at DESC, q.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list solo quizzes: %w", err)
	}
	defer rows.Close()
	out := []SoloQuizRow{}
	for rows.Next() {
		var r SoloQuizRow
		var words string
		var started, completed sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Title, &r.Description, &r.Width, &r.Height, &words, &r.Solvers, &started, &completed); err != nil {
			return nil, fmt.Errorf("scan solo quiz: %w", err)
		}
		var placed []crossword.PlacedWord
		if err := json.Unmarshal([]byte(words), &placed); err != nil {
			return nil, fmt.Errorf("decode words of quiz %d: %w", r.ID, err)
		}
		r.WordCount = len(placed)
		r.Cells = shape(placed)
		switch {
		case completed.Valid:
			r.Status = "done"
			r.DurationMS = new(completed.Int64 - started.Int64)
		case started.Valid:
			r.Status = "playing"
		default:
			r.Status = "new"
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// shape returns each distinct open square of the placed words as [row, col], in reading order.
func shape(words []crossword.PlacedWord) [][2]int {
	seen := map[crossword.Cell]bool{}
	out := [][2]int{}
	for _, w := range words {
		for _, c := range w.Cells() {
			if !seen[c] {
				seen[c] = true
				out = append(out, [2]int{c.Row, c.Col})
			}
		}
	}
	slices.SortFunc(out, func(a, b [2]int) int { return cmp.Or(cmp.Compare(a[0], b[0]), cmp.Compare(a[1], b[1])) })
	return out
}
