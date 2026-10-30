package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tts/internal/crossword"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPragmasOnEveryConnection(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.db.SetMaxOpenConns(3)
	conns := make([]interface{ Close() error }, 0, 3)
	for range 3 {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		var fk int
		var mode string
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if fk != 1 || mode != "wal" {
			t.Fatalf("fk=%d mode=%s", fk, mode)
		}
	}
	for _, c := range conns {
		c.Close()
	}
}

func TestSettingsDefaultsAndEffective(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	g, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g != (Settings{10, 5, 10}) {
		t.Fatalf("defaults %+v", g)
	}
	if err := s.SetSettings(ctx, Settings{20, 0, 30}); err != nil {
		t.Fatal(err)
	}
	eff, err := s.EffectiveSettings(ctx, Quiz{Overrides: Overrides{PenaltyWrong: new(7)}})
	if err != nil {
		t.Fatal(err)
	}
	if eff != (Settings{20, 7, 30}) {
		t.Fatalf("effective %+v", eff)
	}
	// Range validation used by the API layer.
	if (Settings{0, 0, 0}).Validate() == nil || (Settings{10, 5, 10}).Validate() != nil {
		t.Fatal("validate ranges")
	}
}

func TestUsersSessions(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "Dewi", "h", RolePlayer, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "dewi", "h", RolePlayer, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	got, hash, err := s.UserByUsername(ctx, "DEWI")
	if err != nil || got.ID != u.ID || hash != "h" {
		t.Fatalf("lookup %v %v %q", got, err, hash)
	}
	if err := s.CreateSession(ctx, "tok", u.ID, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "tok", 50); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "tok", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM sessions").Scan(&n)
	if n != 0 {
		t.Fatal("cascade did not delete session")
	}
}

func TestAvatarValidation(t *testing.T) {
	if _, ok := ValidAvatar(json.RawMessage(`[1]`)); ok {
		t.Fatal("array accepted")
	}
	if _, ok := ValidAvatar(json.RawMessage(`null`)); ok {
		t.Fatal("null accepted")
	}
	big := `{"a":"` + strings.Repeat("x", 600) + `"}`
	if _, ok := ValidAvatar(json.RawMessage(big)); ok {
		t.Fatal("big accepted")
	}
	if v, ok := ValidAvatar(json.RawMessage(`{ "hair": "short" }`)); !ok || string(v) != `{"hair":"short"}` {
		t.Fatalf("got %s %v", v, ok)
	}
}

func TestQuizAttemptsLive(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "budi", "h", RolePlayer, 1)
	words := []crossword.PlacedWord{
		{Answer: "KUDA", Clue: "kuda", Row: 0, Col: 0, Direction: crossword.Across},
		{Answer: "KAKI", Clue: "kaki", Row: 0, Col: 0, Direction: crossword.Down},
	}
	id, err := s.CreateQuiz(ctx, Quiz{Title: "T", Width: 4, Height: 4, Words: words, Published: true, CreatedBy: &u.ID, CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.StartAttempt(ctx, u.ID, id, 1000)
	if err != nil || a.StartedAt != 1000 || a.CompletedAt != nil {
		t.Fatalf("start %+v %v", a, err)
	}
	if err := s.SaveProgress(ctx, u.ID, id, map[string]string{"0,0": "K"}, 2000); err != nil {
		t.Fatal(err)
	}
	a, err = s.RecordCheck(ctx, u.ID, id, map[string]string{"0,0": "K", "0,1": "U"}, true, 5000)
	if err != nil || a.Checks != 1 || a.CompletedAt == nil || *a.CompletedAt != 5000 {
		t.Fatalf("check %+v %v", a, err)
	}
	// After completion, progress is ignored.
	s.SaveProgress(ctx, u.ID, id, map[string]string{}, 6000)
	a, _ = s.StartAttempt(ctx, u.ID, id, 7000)
	if len(a.Cells) != 2 || a.StartedAt != 1000 {
		t.Fatalf("completed attempt changed: %+v", a)
	}
	lb, err := s.Leaderboard(ctx, id)
	if err != nil || len(lb) != 1 || lb[0].DurationMS != 4000 {
		t.Fatalf("leaderboard %+v %v", lb, err)
	}
	solo, err := s.SoloQuizzes(ctx, u.ID)
	if err != nil || len(solo) != 1 || solo[0].Status != "done" || *solo[0].DurationMS != 4000 || solo[0].WordCount != 2 {
		t.Fatalf("solo %+v %v", solo, err)
	}
	// KUDA across + KAKI down share (0,0): 7 distinct squares, reading order, no letters exposed.
	wantCells := [][2]int{{0, 0}, {0, 1}, {0, 2}, {0, 3}, {1, 0}, {2, 0}, {3, 0}}
	if !slices.Equal(solo[0].Cells, wantCells) {
		t.Fatalf("cells %v, want %v", solo[0].Cells, wantCells)
	}
	if raw, _ := json.Marshal(solo[0]); strings.Contains(string(raw), "KUDA") || strings.Contains(string(raw), "KAKI") {
		t.Fatalf("solo row leaks answers: %s", raw)
	}

	q, _ := s.Quiz(ctx, id)
	q.Words[0].Clue = "kuda poni"
	q.UpdatedAt = 9
	reset, err := s.UpdateQuiz(ctx, q)
	if err != nil || reset {
		t.Fatalf("clue edit reset=%v err=%v", reset, err)
	}
	q.Words[1].Answer = "KAPI"
	reset, err = s.UpdateQuiz(ctx, q)
	if err != nil || !reset {
		t.Fatalf("answer edit reset=%v err=%v", reset, err)
	}
	if res, _ := s.SoloResults(ctx, id); len(res) != 0 {
		t.Fatal("attempts not reset")
	}

	gid, err := s.SaveLiveGame(ctx, LiveGame{QuizID: &id, QuizTitle: "T", StartedAt: 1, EndedAt: 2, Settings: Settings{10, 5, 10},
		Players: []LivePlayer{{Nickname: "b", Avatar: json.RawMessage(`{}`), Score: 3, Rank: 2}, {UserID: &u.ID, Nickname: "budi", Avatar: json.RawMessage(`{}`), Score: 10, Rank: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	games, err := s.LiveGames(ctx)
	if err != nil || len(games) != 1 || games[0].ID != gid || len(games[0].Players) != 2 || games[0].Players[0].Nickname != "budi" {
		t.Fatalf("games %+v %v", games, err)
	}
	rows, _ := s.AdminQuizzes(ctx)
	if len(rows) != 1 || rows[0].LiveGames != 1 {
		t.Fatalf("admin rows %+v", rows)
	}
	if err := s.DeleteQuiz(ctx, id); err != nil {
		t.Fatal(err)
	}
	if games, _ := s.LiveGames(ctx); len(games) != 1 {
		t.Fatal("live game should survive quiz delete")
	}
}
