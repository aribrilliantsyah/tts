package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// LiveGame is a finished live room with its final ranking.
type LiveGame struct {
	ID        int64        `json:"id"`
	QuizID    *int64       `json:"-"`
	QuizTitle string       `json:"quiz_title"`
	StartedAt int64        `json:"started_at"`
	EndedAt   int64        `json:"ended_at"`
	Settings  Settings     `json:"-"`
	Players   []LivePlayer `json:"players"`
}

type LivePlayer struct {
	UserID    *int64          `json:"-"`
	Nickname  string          `json:"nickname"`
	Avatar    json.RawMessage `json:"avatar"`
	Score     int             `json:"score"`
	Correct   int             `json:"-"`
	Wrong     int             `json:"-"`
	CorrectMS int64           `json:"-"`
	Rank      int             `json:"rank"`
}

// SaveLiveGame persists a finished game and its players atomically, returning the game id.
func (s *Store) SaveLiveGame(ctx context.Context, g LiveGame) (int64, error) {
	settings, err := json.Marshal(g.Settings)
	if err != nil {
		return 0, fmt.Errorf("encode settings: %w", err)
	}
	var id int64
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		// The quiz may have been deleted while the room ran; store NULL then.
		res, err := tx.ExecContext(ctx, `INSERT INTO live_games(quiz_id, quiz_title, started_at, ended_at, settings)
			VALUES ((SELECT id FROM quizzes WHERE id = ?), ?, ?, ?, ?)`,
			g.QuizID, g.QuizTitle, g.StartedAt, g.EndedAt, string(settings))
		if err != nil {
			return fmt.Errorf("insert live game: %w", err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("live game id: %w", err)
		}
		for _, p := range g.Players {
			if _, err := tx.ExecContext(ctx, `INSERT INTO live_players(game_id, user_id, nickname, avatar, score, correct, wrong, correct_ms, rank)
				VALUES (?, (SELECT id FROM users WHERE id = ?), ?, ?, ?, ?, ?, ?, ?)`,
				id, p.UserID, p.Nickname, string(p.Avatar), p.Score, p.Correct, p.Wrong, p.CorrectMS, p.Rank); err != nil {
				return fmt.Errorf("insert live player: %w", err)
			}
		}
		return nil
	})
	return id, err
}

// LiveGames returns the latest 50 games, newest first, players ordered by rank.
func (s *Store) LiveGames(ctx context.Context) ([]LiveGame, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, quiz_title, started_at, ended_at FROM live_games ORDER BY ended_at DESC, id DESC LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("list live games: %w", err)
	}
	games := []LiveGame{}
	index := map[int64]int{}
	for rows.Next() {
		g := LiveGame{Players: []LivePlayer{}}
		if err := rows.Scan(&g.ID, &g.QuizTitle, &g.StartedAt, &g.EndedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan live game: %w", err)
		}
		index[g.ID] = len(games)
		games = append(games, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list live games: %w", err)
	}
	if len(games) == 0 {
		return games, nil
	}
	prow, err := s.db.QueryContext(ctx, `SELECT game_id, nickname, avatar, score, rank FROM live_players
		WHERE game_id IN (SELECT id FROM live_games ORDER BY ended_at DESC, id DESC LIMIT 50)
		ORDER BY game_id, rank, nickname`)
	if err != nil {
		return nil, fmt.Errorf("list live players: %w", err)
	}
	defer prow.Close()
	for prow.Next() {
		var gid int64
		var p LivePlayer
		var avatar string
		if err := prow.Scan(&gid, &p.Nickname, &avatar, &p.Score, &p.Rank); err != nil {
			return nil, fmt.Errorf("scan live player: %w", err)
		}
		i, ok := index[gid]
		if !ok {
			continue
		}
		p.Avatar = json.RawMessage(avatar)
		games[i].Players = append(games[i].Players, p)
	}
	return games, prow.Err()
}
