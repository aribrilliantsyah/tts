package live

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"tts/internal/crossword"
	"tts/internal/store"
)

// fakeClock fires timers only when Advance is called.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Time
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves time forward and runs due timers (each posts into a room, which
// blocks until the room goroutine has accepted it).
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var due []func()
	keep := c.timers[:0]
	for _, t := range c.timers {
		switch {
		case t.stopped:
		case !t.at.After(c.now):
			t.stopped = true
			due = append(due, t.f)
		default:
			keep = append(keep, t)
		}
	}
	c.timers = keep
	c.mu.Unlock()
	for _, f := range due {
		f()
	}
}

type fakeStore struct {
	mu    sync.Mutex
	games []store.LiveGame
}

func (s *fakeStore) SaveLiveGame(_ context.Context, g store.LiveGame) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games = append(s.games, g)
	return int64(len(s.games)), nil
}

func (s *fakeStore) saved() []store.LiveGame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.games)
}

// Grid: KUDA across at (0,0) "1-across", KAKI down at (0,0) "1-down", ADA down at (0,3) "2-down".
var words = []crossword.PlacedWord{
	{Answer: "KUDA", Clue: "Ditunggangi joki", Row: 0, Col: 0, Direction: crossword.Across},
	{Answer: "KAKI", Clue: "Untuk berjalan", Row: 0, Col: 0, Direction: crossword.Down},
	{Answer: "ADA", Clue: "Tidak kosong", Row: 0, Col: 3, Direction: crossword.Down},
}

type env struct {
	t     *testing.T
	clock *fakeClock
	store *fakeStore
	hub   *Hub
	room  *Room
	host  *Client
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, clock: &fakeClock{now: time.UnixMilli(1_700_000_000_000)}, store: &fakeStore{}}
	e.hub = NewHub(e.clock, e.store)
	r, err := e.hub.Create(Config{QuizID: 7, Title: "Hewan", Words: words, Settings: store.Settings{PointsCorrect: 10, PenaltyWrong: 5, AnswerSeconds: 10}})
	if err != nil {
		t.Fatal(err)
	}
	e.room = r
	t.Cleanup(e.hub.Close)
	e.host = NewClient()
	r.Join(e.host, Hello{Type: "hello", Role: "host"}, &store.User{ID: 1, Username: "admin", Role: store.RoleAdmin})
	e.state(e.host)
	return e
}

// sync waits until the room has processed everything queued before it.
func (e *env) sync() {
	ch := make(chan struct{})
	e.room.post(func() { close(ch) })
	<-ch
}

type frame map[string]json.RawMessage

// snapshotSync returns the current state as clients would see it (round-tripped through JSON).
func (r *Room) snapshotSync() RoomState {
	ch := make(chan []byte)
	r.post(func() {
		b, _ := json.Marshal(r.snapshot())
		ch <- b
	})
	var st RoomState
	json.Unmarshal(<-ch, &st)
	return st
}

func (r *Room) playerByID(id string) player {
	ch := make(chan player)
	r.post(func() {
		i := slices.IndexFunc(r.players, func(p *player) bool { return p.id == id })
		ch <- *r.players[i]
	})
	return <-ch
}

func (f frame) typ() string {
	var s string
	json.Unmarshal(f["type"], &s)
	return s
}

// drain returns every frame queued for c.
func (e *env) drain(c *Client) []frame {
	e.sync()
	var out []frame
	for {
		select {
		case b, ok := <-c.out:
			if !ok {
				return out
			}
			var f frame
			if err := json.Unmarshal(b, &f); err != nil {
				e.t.Fatal(err)
			}
			out = append(out, f)
		default:
			return out
		}
	}
}

// state returns the latest state snapshot queued for c.
func (e *env) state(c *Client) RoomState {
	e.t.Helper()
	var st RoomState
	found := false
	for _, f := range e.drain(c) {
		if f.typ() == "state" {
			json.Unmarshal(f["state"], &st)
			found = true
		}
	}
	if !found {
		e.t.Fatal("no state frame")
	}
	return st
}

type joined struct {
	c   *Client
	you You
}

func (e *env) join(nick string, u *store.User, token string) joined {
	e.t.Helper()
	c := NewClient()
	e.room.Join(c, Hello{Type: "hello", Role: "player", Nickname: nick, Token: token, Avatar: json.RawMessage(`{"hair":"x"}`)}, u)
	for _, f := range e.drain(c) {
		if f.typ() == "welcome" {
			var w welcomeMsg
			json.Unmarshal(mustJSON(f), &w)
			return joined{c, w.You}
		}
	}
	e.t.Fatalf("join %q: no welcome", nick)
	return joined{}
}

func mustJSON(f frame) []byte {
	b, _ := json.Marshal(f)
	return b
}

func (e *env) host_(m Msg) { e.room.Message(e.host, m) }

func (e *env) start(nicks ...string) []joined {
	var ps []joined
	for _, n := range nicks {
		ps = append(ps, e.join(n, nil, ""))
	}
	e.host_(Msg{Type: "start"})
	if st := e.state(e.host); st.Phase != PhasePlaying {
		e.t.Fatalf("phase %s", st.Phase)
	}
	return ps
}

func (e *env) errors(c *Client) []string {
	var out []string
	for _, f := range e.drain(c) {
		if f.typ() == "error" {
			var m errorMsg
			json.Unmarshal(mustJSON(f), &m)
			out = append(out, m.Message)
		}
	}
	return out
}

func findClue(st RoomState, id string) StateClue {
	for _, c := range st.Quiz.Clues {
		if c.ID == id {
			return c
		}
	}
	return StateClue{}
}

func findPlayer(st RoomState, id string) StatePlayer {
	for _, p := range st.Players {
		if p.ID == id {
			return p
		}
	}
	return StatePlayer{}
}

func TestFirstBuzzWins(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[1].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	var buzzes []string
	for _, f := range e.drain(e.host) {
		if f.typ() == "buzz" {
			var b buzzMsg
			json.Unmarshal(mustJSON(f), &b)
			buzzes = append(buzzes, b.PlayerID)
		}
	}
	if !slices.Equal(buzzes, []string{ps[1].you.PlayerID}) {
		t.Fatalf("buzzes %v", buzzes)
	}
	st := e.room.snapshotSync()
	if st.Active.State != "answering" || *st.Active.AnsweringPlayer != ps[1].you.PlayerID {
		t.Fatalf("active %+v", st.Active)
	}
	if *st.Active.Deadline != e.clock.Now().Add(10*time.Second).UnixMilli() {
		t.Fatal("deadline")
	}
}

func TestCorrectAnswer(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.sync()
	e.clock.Advance(2500 * time.Millisecond)
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: " kuda "})
	st := e.state(e.host)
	c := findClue(st, "1-across")
	if c.Status != StatusSolved || *c.Answer != "KUDA" || *c.SolvedBy != ps[0].you.PlayerID {
		t.Fatalf("clue %+v", c)
	}
	if st.Letters["0,0"] != "K" || st.Letters["0,3"] != "A" || len(st.Letters) != 4 {
		t.Fatalf("letters %v", st.Letters)
	}
	if p := findPlayer(st, ps[0].you.PlayerID); p.Score != 10 || p.Correct != 1 {
		t.Fatalf("player %+v", p)
	}
	if st.Active != nil || st.LastOutcome.Kind != OutcomeSolved || *st.LastOutcome.Answer != "KUDA" {
		t.Fatalf("outcome %+v active %+v", st.LastOutcome, st.Active)
	}
	if e.room.playerByID(ps[0].you.PlayerID).correctMS != 2500 {
		t.Fatal("correct_ms")
	}
}

func TestWrongLockoutReopen(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "KUDU"})
	st := e.state(e.host)
	if st.Active == nil || st.Active.State != "waiting" || !slices.Equal(st.Active.LockedOut, []string{ps[0].you.PlayerID}) {
		t.Fatalf("active %+v", st.Active)
	}
	if p := findPlayer(st, ps[0].you.PlayerID); p.Score != -5 || p.Wrong != 1 {
		t.Fatalf("player %+v", p)
	}
	// Locked-out player cannot buzz again; the other can.
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	if st := e.room.snapshotSync(); st.Active.State != "waiting" {
		t.Fatal("locked-out buzz accepted")
	}
	e.room.Message(ps[1].c, Msg{Type: "buzz"})
	if st := e.room.snapshotSync(); *st.Active.AnsweringPlayer != ps[1].you.PlayerID {
		t.Fatal("reopened buzz rejected")
	}
}

func TestWrongLengthIsWrong(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "KUDAA"})
	if p := findPlayer(e.room.snapshotSync(), ps[0].you.PlayerID); p.Wrong != 1 {
		t.Fatal("length mismatch not wrong")
	}
}

func TestTimeoutIsWrong(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.sync()
	e.clock.Advance(9 * time.Second)
	if st := e.room.snapshotSync(); st.Active.State != "answering" {
		t.Fatal("timed out early")
	}
	e.clock.Advance(time.Second)
	st := e.room.snapshotSync()
	if st.Active.State != "waiting" || findPlayer(st, ps[0].you.PlayerID).Score != -5 {
		t.Fatalf("timeout not wrong: %+v", st.Active)
	}
	// A late answer after timeout is rejected.
	e.drain(ps[0].c)
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "KUDA"})
	if errs := e.errors(ps[0].c); len(errs) != 1 {
		t.Fatalf("errors %v", errs)
	}
}

func TestStaleTimerAfterCorrect(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "KUDA"})
	e.host_(Msg{Type: "open_clue", ClueID: "1-down"})
	e.room.Message(ps[1].c, Msg{Type: "buzz"})
	e.sync()
	e.clock.Advance(10 * time.Second) // only budi's timer is live
	st := e.room.snapshotSync()
	if findPlayer(st, ps[0].you.PlayerID).Score != 10 || findPlayer(st, ps[1].you.PlayerID).Score != -5 {
		t.Fatalf("scores %+v", st.Players)
	}
}

func TestAllHadTurnExhausted(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	for _, p := range ps {
		e.room.Message(p.c, Msg{Type: "buzz"})
		e.room.Message(p.c, Msg{Type: "answer", Text: "SAPI"})
	}
	st := e.state(e.host)
	c := findClue(st, "1-across")
	if st.Active != nil || c.Status != StatusRevealed || *c.Answer != "KUDA" || c.SolvedBy != nil {
		t.Fatalf("clue %+v active %+v", c, st.Active)
	}
	if st.LastOutcome.Kind != OutcomeExhausted || st.LastOutcome.PlayerID != nil || *st.LastOutcome.Answer != "KUDA" {
		t.Fatalf("outcome %+v", st.LastOutcome)
	}
}

func TestAnsweringDisconnectIsWrong(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Leave(ps[0].c)
	st := e.room.snapshotSync()
	p := findPlayer(st, ps[0].you.PlayerID)
	if p.Connected || p.Score != -5 || st.Active.State != "waiting" {
		t.Fatalf("player %+v active %+v", p, st.Active)
	}
}

func TestDisconnectedExcludedFromTurnCount(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi", "cici")
	e.room.Leave(ps[2].c)
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	for _, p := range ps[:2] {
		e.room.Message(p.c, Msg{Type: "buzz"})
		e.room.Message(p.c, Msg{Type: "answer", Text: "SAPI"})
	}
	if st := e.room.snapshotSync(); findClue(st, "1-across").Status != StatusRevealed {
		t.Fatal("disconnected player still counted")
	}
	// Disconnecting the last eligible waiting player also exhausts.
	e.host_(Msg{Type: "open_clue", ClueID: "1-down"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "SAPI"})
	e.room.Leave(ps[1].c)
	if st := e.room.snapshotSync(); findClue(st, "1-down").Status != StatusRevealed || st.Active != nil {
		t.Fatal("leave of last eligible player did not exhaust")
	}
}

func TestSkipKeepsCluePickable(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani")
	e.host_(Msg{Type: "open_clue", ClueID: "2-down"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.host_(Msg{Type: "skip"})
	st := e.state(e.host)
	c := findClue(st, "2-down")
	if c.Status != StatusSkipped || c.Answer != nil || st.LastOutcome.Kind != OutcomeSkipped || st.LastOutcome.Answer != nil {
		t.Fatalf("clue %+v outcome %+v", c, st.LastOutcome)
	}
	e.sync()
	e.clock.Advance(time.Minute) // cancelled timer must not penalise
	if findPlayer(e.room.snapshotSync(), ps[0].you.PlayerID).Score != 0 {
		t.Fatal("skip timer fired")
	}
	e.host_(Msg{Type: "open_clue", ClueID: "2-down"})
	if st := e.room.snapshotSync(); st.Active == nil || st.Active.ClueID != "2-down" {
		t.Fatal("skipped clue not pickable")
	}
}

func TestReveal(t *testing.T) {
	e := newEnv(t)
	e.start("ani")
	e.host_(Msg{Type: "reveal", ClueID: "2-down"})
	st := e.state(e.host)
	if c := findClue(st, "2-down"); c.Status != StatusRevealed || *c.Answer != "ADA" || st.Letters["2,3"] != "A" {
		t.Fatalf("clue %+v", c)
	}
	e.host_(Msg{Type: "open_clue", ClueID: "2-down"})
	if errs := e.errors(e.host); len(errs) != 1 {
		t.Fatalf("revealed clue reopened: %v", errs)
	}
}

func TestEndRankingAndPersist(t *testing.T) {
	e := newEnv(t)
	u := &store.User{ID: 42, Username: "Zaki", Role: store.RolePlayer, Avatar: json.RawMessage(`{}`)}
	z := e.join("ignored", u, "")
	if z.you.Nickname != "Zaki" {
		t.Fatalf("logged-in nickname %q", z.you.Nickname)
	}
	ps := e.start("budi", "ani", "cici")
	answer := func(p joined, clue, text string, ms time.Duration) {
		e.host_(Msg{Type: "open_clue", ClueID: clue})
		e.room.Message(p.c, Msg{Type: "buzz"})
		e.sync()
		e.clock.Advance(ms)
		e.room.Message(p.c, Msg{Type: "answer", Text: text})
	}
	answer(z, "1-across", "KUDA", 3*time.Second) // Zaki 10, 3000ms
	answer(ps[0], "1-down", "KAKI", time.Second) // budi 10, 1000ms
	e.host_(Msg{Type: "end"})
	st := e.state(e.host)
	if st.Phase != PhaseFinished {
		t.Fatal("not finished")
	}
	if c := findClue(st, "2-down"); c.Status != StatusRevealed || *c.Answer != "ADA" {
		t.Fatal("end did not reveal")
	}
	var order []string
	for _, p := range st.Podium {
		order = append(order, p.Nickname)
	}
	// score desc, correct_ms asc, nickname asc
	if !slices.Equal(order, []string{"budi", "Zaki", "ani", "cici"}) {
		t.Fatalf("podium %v", order)
	}
	games := e.store.saved()
	if len(games) != 1 || games[0].QuizID == nil || *games[0].QuizID != 7 || len(games[0].Players) != 4 {
		t.Fatalf("saved %+v", games)
	}
	for _, p := range games[0].Players {
		if (p.Nickname == "Zaki") != (p.UserID != nil) {
			t.Fatalf("user id for %s: %v", p.Nickname, p.UserID)
		}
		if p.Nickname == "Zaki" && (p.Rank != 2 || p.CorrectMS != 3000 || *p.UserID != 42) {
			t.Fatalf("zaki %+v", p)
		}
	}
}

func TestAutoFinish(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani")
	e.host_(Msg{Type: "reveal", ClueID: "1-across"})
	e.host_(Msg{Type: "reveal", ClueID: "1-down"})
	e.host_(Msg{Type: "open_clue", ClueID: "2-down"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "ada"})
	st := e.state(e.host)
	if st.Phase != PhaseFinished || len(st.Podium) != 1 || st.Podium[0].Rank != 1 || st.Podium[0].Score != 10 {
		t.Fatalf("phase %s podium %+v", st.Phase, st.Podium)
	}
	if len(e.store.saved()) != 1 {
		t.Fatal("not persisted")
	}
	// Room is removed 30 min after finishing.
	e.clock.Advance(FinishedTTL)
	<-e.room.Done()
	if _, ok := e.hub.Get(e.room.Code()); ok {
		t.Fatal("room not removed")
	}
}

func TestIdleCleanup(t *testing.T) {
	e := newEnv(t)
	e.clock.Advance(time.Hour)
	e.host_(Msg{Type: "settings", PointsCorrect: 20, PenaltyWrong: 5, AnswerSeconds: 10})
	e.sync()
	e.clock.Advance(IdleTTL - time.Hour) // first check: only 1h idle, re-armed
	e.sync()
	if _, ok := e.hub.Get(e.room.Code()); !ok {
		t.Fatal("removed while active")
	}
	e.clock.Advance(time.Hour)
	<-e.room.Done()
	if _, ok := e.hub.Get(e.room.Code()); ok {
		t.Fatal("idle room not removed")
	}
}

func TestSettingsLockedAfterStart(t *testing.T) {
	e := newEnv(t)
	e.host_(Msg{Type: "settings", PointsCorrect: 0, PenaltyWrong: 5, AnswerSeconds: 10})
	if errs := e.errors(e.host); len(errs) != 1 {
		t.Fatalf("invalid settings accepted: %v", errs)
	}
	e.host_(Msg{Type: "settings", PointsCorrect: 20, PenaltyWrong: 3, AnswerSeconds: 30})
	if st := e.state(e.host); st.Settings != (store.Settings{PointsCorrect: 20, PenaltyWrong: 3, AnswerSeconds: 30}) {
		t.Fatalf("settings %+v", st.Settings)
	}
	e.start("ani")
	e.host_(Msg{Type: "settings", PointsCorrect: 50, PenaltyWrong: 3, AnswerSeconds: 30})
	if errs := e.errors(e.host); len(errs) != 1 || !strings.Contains(errs[0], "terkunci") {
		t.Fatalf("errors %v", errs)
	}
	if e.room.snapshotSync().Settings.PointsCorrect != 20 {
		t.Fatal("settings changed after start")
	}
}

func TestStartNeedsPlayer(t *testing.T) {
	e := newEnv(t)
	e.host_(Msg{Type: "start"})
	if errs := e.errors(e.host); len(errs) != 1 {
		t.Fatal("start without players accepted")
	}
}

func TestAnswerNeverInSnapshotBeforeSolve(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani")
	check := func(stage string) {
		for _, c := range []*Client{e.host, ps[0].c} {
			for _, f := range e.drain(c) {
				s := string(mustJSON(f))
				for _, a := range []string{"KUDA", "KAKI"} {
					if strings.Contains(s, a) {
						t.Fatalf("%s: answer %s leaked in %s", stage, a, s)
					}
				}
			}
		}
	}
	check("start")
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	check("answering")
	e.host_(Msg{Type: "skip"})
	check("skipped")
	e.host_(Msg{Type: "reveal", ClueID: "2-down"}) // ADA revealed; KUDA/KAKI letters not complete words
	check("other revealed")
}

func TestResumeKeepsScoreBellLockout(t *testing.T) {
	e := newEnv(t)
	ps := e.start("ani", "budi")
	e.room.Message(ps[0].c, Msg{Type: "set_bell", Bell: "bong"})
	e.host_(Msg{Type: "open_clue", ClueID: "1-across"})
	e.room.Message(ps[0].c, Msg{Type: "buzz"})
	e.room.Message(ps[0].c, Msg{Type: "answer", Text: "SAPI"})
	e.room.Leave(ps[0].c)
	e.sync()

	again := e.join("", nil, ps[0].you.Token)
	if again.you.PlayerID != ps[0].you.PlayerID || again.you.Token != ps[0].you.Token || again.you.Nickname != "ani" {
		t.Fatalf("resume %+v", again.you)
	}
	st := e.room.snapshotSync()
	p := findPlayer(st, again.you.PlayerID)
	if !p.Connected || p.Score != -5 || p.Bell != "bong" || len(st.Players) != 2 {
		t.Fatalf("player %+v", p)
	}
	e.room.Message(again.c, Msg{Type: "buzz"})
	if st := e.room.snapshotSync(); st.Active.State != "waiting" {
		t.Fatal("lockout lost after resume")
	}
}

func TestResumeReplacesOldConnection(t *testing.T) {
	e := newEnv(t)
	a := e.join("ani", nil, "")
	b := e.join("x", nil, a.you.Token)
	if b.you.PlayerID != a.you.PlayerID {
		t.Fatal("not resumed")
	}
	for range a.c.out { // old connection is closed after its last frames
	}
	if st := e.room.snapshotSync(); len(st.Players) != 1 || !st.Players[0].Connected {
		t.Fatalf("players %+v", st.Players)
	}
}

func TestNicknameRules(t *testing.T) {
	e := newEnv(t)
	e.join("Dewi", nil, "")
	for _, nick := range []string{"dewi", "  DEWI ", "", strings.Repeat("a", 21)} {
		c := NewClient()
		e.room.Join(c, Hello{Role: "player", Nickname: nick}, nil)
		frames := e.drain(c)
		if len(frames) != 1 || frames[0].typ() != "error" {
			t.Fatalf("nick %q: %v", nick, frames)
		}
		if _, open := <-c.out; open {
			t.Fatalf("nick %q: connection not closed", nick)
		}
	}
	// Logged-in user whose username collides with a guest is rejected too.
	c := NewClient()
	e.room.Join(c, Hello{Role: "player"}, &store.User{ID: 9, Username: "DEWI", Role: store.RolePlayer})
	if f := e.drain(c); len(f) != 1 || f[0].typ() != "error" {
		t.Fatal("username collision accepted")
	}
}

func TestBells(t *testing.T) {
	e := newEnv(t)
	a := e.join("ani", nil, "")
	b := e.join("budi", nil, "")
	st := e.room.snapshotSync()
	if findPlayer(st, a.you.PlayerID).Bell != "siuuu" || findPlayer(st, b.you.PlayerID).Bell != "fahhh" {
		t.Fatalf("defaults %+v", st.Players)
	}
	if !slices.Equal(st.Bells, Bells) {
		t.Fatal("bell list")
	}
	e.drain(b.c)
	e.room.Message(b.c, Msg{Type: "set_bell", Bell: "siuuu"})
	if errs := e.errors(b.c); len(errs) != 1 || errs[0] != "Bel sudah dipakai pemain lain" {
		t.Fatalf("errors %v", errs)
	}
	e.room.Message(a.c, Msg{Type: "set_bell", Bell: "day-in-life"})
	c := e.join("cici", nil, "")
	if findPlayer(e.room.snapshotSync(), c.you.PlayerID).Bell != "siuuu" {
		t.Fatal("freed bell not reused")
	}
}

func TestMaxPlayersAndKick(t *testing.T) {
	e := newEnv(t)
	var first joined
	for i := range MaxPlayers {
		j := e.join(fmt.Sprintf("p%d", i), nil, "")
		if i == 0 {
			first = j
		}
	}
	c := NewClient()
	e.room.Join(c, Hello{Role: "player", Nickname: "extra"}, nil)
	if f := e.drain(c); len(f) != 1 || f[0].typ() != "error" {
		t.Fatal("51st player accepted")
	}
	e.host_(Msg{Type: "kick", PlayerID: first.you.PlayerID})
	frames := e.drain(first.c)
	if frames[len(frames)-1].typ() != "kicked" {
		t.Fatalf("frames %v", frames)
	}
	if len(e.room.snapshotSync().Players) != MaxPlayers-1 {
		t.Fatal("kick did not remove")
	}
}

func TestHostRequiresAdmin(t *testing.T) {
	e := newEnv(t)
	c := NewClient()
	e.room.Join(c, Hello{Role: "host"}, &store.User{ID: 3, Username: "p", Role: store.RolePlayer})
	if f := e.drain(c); len(f) != 1 || f[0].typ() != "error" {
		t.Fatal("non-admin host accepted")
	}
	pc := e.join("ani", nil, "")
	e.room.Message(pc.c, Msg{Type: "start"})
	if errs := e.errors(pc.c); len(errs) != 1 {
		t.Fatal("player could start")
	}
}
