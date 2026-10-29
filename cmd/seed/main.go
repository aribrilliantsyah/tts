// Command seed fills a database with sample quizzes, players, solo results and
// one finished live game, so the app can be demoed without manual setup.
//
// It goes through the same parser and store as the app (mdimport + store), so
// seeded data obeys every validation rule. Running it twice is safe: quizzes and
// users that already exist (same title / username) are skipped.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"

	"tts/internal/auth"
	"tts/internal/crossword"
	"tts/internal/mdimport"
	"tts/internal/store"
)

//go:embed samples/*.md
var samples embed.FS

// Quizzes left as drafts (not published) to show the draft state in the admin list.
var drafts = map[string]bool{"Sains Dasar": true}

type player struct {
	username string
	avatar   map[string]string
}

var players = []player{
	{"sari", map[string]string{"hair": "braids", "eyes": "winking", "mouth": "kawaii", "accessories": "catEars", "skinColor": "f5d7b1", "hairColor": "220f00", "backgroundColor": "ff5a36"}},
	{"budi", map[string]string{"hair": "mohawk", "eyes": "cheery", "mouth": "teethSmile", "accessories": "none", "skinColor": "c99c62", "hairColor": "1c2541", "backgroundColor": "2ec4a6"}},
	{"dewi", map[string]string{"hair": "wavyBob", "eyes": "starstruck", "mouth": "openedSmile", "accessories": "glasses", "skinColor": "efcc9f", "hairColor": "71472d", "backgroundColor": "ffd23f"}},
	{"rina", map[string]string{"hair": "curlyBob", "eyes": "normal", "mouth": "gapSmile", "accessories": "sunglasses", "skinColor": "e2ba87", "hairColor": "e9b729", "backgroundColor": "c9b6ff"}},
	{"agus", map[string]string{"hair": "shortHair", "eyes": "sleepy", "mouth": "awkwardSmile", "accessories": "mustache", "skinColor": "8c5a2b", "hairColor": "1c2541", "backgroundColor": "ddf0ff"}},
}

func main() {
	dbPath := flag.String("db", envOr("DB_PATH", "data.sqlite"), "path to the SQLite database")
	password := flag.String("password", "rahasia123", "password for every sample player")
	flag.Parse()
	if err := run(context.Background(), *dbPath, *password); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context, dbPath, password string) error {
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	now := time.Now()

	quizIDs, err := seedQuizzes(ctx, st, now)
	if err != nil {
		return err
	}
	userIDs, err := seedPlayers(ctx, st, password, now)
	if err != nil {
		return err
	}
	if err := seedSolo(ctx, st, quizIDs, userIDs, now); err != nil {
		return err
	}
	if err := seedLiveGame(ctx, st, quizIDs, userIDs, now); err != nil {
		return err
	}
	fmt.Printf("\nSelesai. Database: %s\nLogin pemain contoh: %s / %s (juga budi, dewi, rina, agus)\n", dbPath, players[0].username, password)
	return nil
}

// seedQuizzes imports every embedded sample; returns quiz id by title (new and existing).
func seedQuizzes(ctx context.Context, st *store.Store, now time.Time) (map[string]int64, error) {
	existing, err := st.AdminQuizzes(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, q := range existing {
		ids[q.Title] = q.ID
	}
	files, err := fs.Glob(samples, "samples/*.md")
	if err != nil {
		return nil, fmt.Errorf("list samples: %w", err)
	}
	for _, name := range files {
		raw, err := samples.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		res := mdimport.Parse(name, string(raw))
		if !res.OK {
			return nil, fmt.Errorf("sample %s invalid: %+v", name, res.Errors)
		}
		d := res.Quiz
		if len(d.Layout.Unplaced) > 0 {
			return nil, fmt.Errorf("sample %s: words do not fit the grid: %+v", name, d.Layout.Unplaced)
		}
		if id, ok := ids[d.Title]; ok {
			fmt.Printf("lewati kuis %q (sudah ada, id %d)\n", d.Title, id)
			continue
		}
		g, err := crossword.Build(d.Layout.Words)
		if err != nil {
			return nil, fmt.Errorf("sample %s: %w", name, err)
		}
		id, err := st.CreateQuiz(ctx, store.Quiz{
			Title:       d.Title,
			Description: d.Description,
			Width:       g.Width,
			Height:      g.Height,
			Words:       d.Layout.Words,
			Published:   !drafts[d.Title],
			Overrides:   d.Overrides,
			CreatedAt:   now.UnixMilli(),
			UpdatedAt:   now.UnixMilli(),
		})
		if err != nil {
			return nil, fmt.Errorf("create quiz %q: %w", d.Title, err)
		}
		ids[d.Title] = id
		status := "terbit"
		if drafts[d.Title] {
			status = "draf"
		}
		fmt.Printf("kuis %q dibuat (id %d, %d kata, %dx%d, %s)\n", d.Title, id, len(d.Layout.Words), g.Width, g.Height, status)
	}
	return ids, nil
}

// seedPlayers creates sample players; returns user id by username (new and existing).
func seedPlayers(ctx context.Context, st *store.Store, password string, now time.Time) (map[string]int64, error) {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for i, p := range players {
		u, _, err := st.UserByUsername(ctx, p.username)
		switch {
		case err == nil:
			fmt.Printf("lewati pemain %q (sudah ada)\n", p.username)
		case errors.Is(err, store.ErrNotFound):
			u, err = st.CreateUser(ctx, p.username, hash, "player", now.Add(-time.Duration(len(players)-i)*time.Hour).UnixMilli())
			if err != nil {
				return nil, fmt.Errorf("create %s: %w", p.username, err)
			}
			avatar, err := json.Marshal(p.avatar)
			if err != nil {
				return nil, fmt.Errorf("encode avatar: %w", err)
			}
			if err := st.SetAvatar(ctx, u.ID, avatar); err != nil {
				return nil, err
			}
			fmt.Printf("pemain %q dibuat\n", p.username)
		default:
			return nil, fmt.Errorf("lookup %s: %w", p.username, err)
		}
		ids[p.username] = u.ID
	}
	return ids, nil
}

// solved returns the full solution map of a quiz ("r,c" -> letter).
func solved(q store.Quiz) (map[string]string, error) {
	g, err := crossword.Build(q.Words)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(g.Letters))
	for c, l := range g.Letters {
		out[c.Key()] = string(l)
	}
	return out, nil
}

// seedSolo gives "Kebun Binatang" a leaderboard and leaves one in-progress attempt.
// Players who already have an attempt on that quiz are left untouched.
func seedSolo(ctx context.Context, st *store.Store, quizIDs map[string]int64, userIDs map[string]int64, now time.Time) error {
	quizID, ok := quizIDs["Kebun Binatang"]
	if !ok {
		return nil
	}
	q, err := st.Quiz(ctx, quizID)
	if err != nil {
		return err
	}
	solution, err := solved(q)
	if err != nil {
		return err
	}
	// secs = 0 leaves the attempt in progress.
	runs := []struct {
		user         string
		agoMin, secs int
		checks       int
	}{
		{"sari", 90, 118, 1},
		{"dewi", 80, 161, 2},
		{"budi", 70, 195, 1},
		{"rina", 60, 242, 3},
		{"agus", 20, 0, 0},
	}
	results, err := st.SoloResults(ctx, quizID)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, r := range results {
		has[r.Username] = true
	}
	for _, r := range runs {
		if has[r.user] {
			continue
		}
		uid := userIDs[r.user]
		start := now.Add(-time.Duration(r.agoMin) * time.Minute)
		if _, err := st.StartAttempt(ctx, uid, quizID, start.UnixMilli()); err != nil {
			return err
		}
		if r.secs == 0 {
			partial := map[string]string{}
			for k, v := range solution {
				if len(partial) >= len(solution)/3 {
					break
				}
				partial[k] = v
			}
			if err := st.SaveProgress(ctx, uid, quizID, partial, start.UnixMilli()); err != nil {
				return err
			}
			fmt.Printf("solo: %s sedang mengerjakan %q\n", r.user, q.Title)
			continue
		}
		for range r.checks - 1 {
			if _, err := st.RecordCheck(ctx, uid, quizID, map[string]string{}, false, start.UnixMilli()); err != nil {
				return err
			}
		}
		end := start.Add(time.Duration(r.secs) * time.Second)
		if _, err := st.RecordCheck(ctx, uid, quizID, solution, true, end.UnixMilli()); err != nil {
			return err
		}
		fmt.Printf("solo: %s selesai %q dalam %ds\n", r.user, q.Title, r.secs)
	}
	return nil
}

// seedLiveGame stores one finished live game for the history page, unless one exists.
func seedLiveGame(ctx context.Context, st *store.Store, quizIDs map[string]int64, userIDs map[string]int64, now time.Time) error {
	games, err := st.LiveGames(ctx)
	if err != nil {
		return err
	}
	if len(games) > 0 {
		fmt.Println("lewati riwayat live (sudah ada)")
		return nil
	}
	const title = "Jajanan Pasar"
	quizID, ok := quizIDs[title]
	if !ok {
		return nil
	}
	q, err := st.Quiz(ctx, quizID)
	if err != nil {
		return err
	}
	settings, err := st.EffectiveSettings(ctx, q)
	if err != nil {
		return err
	}
	guestAvatar, err := json.Marshal(map[string]string{"hair": "bunHair", "eyes": "cheery", "mouth": "kawaii", "accessories": "sailormoonCrown", "backgroundColor": "fff1b8"})
	if err != nil {
		return fmt.Errorf("encode avatar: %w", err)
	}
	avatarOf := func(name string) json.RawMessage {
		for _, p := range players {
			if p.username == name {
				b, _ := json.Marshal(p.avatar) // map[string]string never fails to encode
				return b
			}
		}
		return guestAvatar
	}
	type row struct {
		name           string
		guest          bool
		correct, wrong int
		correctMS      int64
	}
	rows := []row{
		{"dewi", false, 3, 0, 14200},
		{"budi", false, 2, 1, 9800},
		{"Tante Lina", true, 2, 1, 12500},
		{"sari", false, 1, 2, 4100},
	}
	g := store.LiveGame{
		QuizID:    &quizID,
		QuizTitle: q.Title,
		StartedAt: now.Add(-3 * time.Hour).UnixMilli(),
		EndedAt:   now.Add(-3*time.Hour + 12*time.Minute).UnixMilli(),
		Settings:  settings,
	}
	for i, r := range rows {
		p := store.LivePlayer{
			Nickname:  r.name,
			Avatar:    avatarOf(r.name),
			Score:     r.correct*settings.PointsCorrect - r.wrong*settings.PenaltyWrong,
			Correct:   r.correct,
			Wrong:     r.wrong,
			CorrectMS: r.correctMS,
			Rank:      i + 1,
		}
		if !r.guest {
			uid := userIDs[r.name]
			p.UserID = &uid
		}
		g.Players = append(g.Players, p)
	}
	if _, err := st.SaveLiveGame(ctx, g); err != nil {
		return err
	}
	fmt.Printf("riwayat live %q disimpan (%d pemain, juara %s)\n", title, len(rows), rows[0].name)
	return nil
}
