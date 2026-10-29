// Package live runs buzzer rooms. Each room is owned by one goroutine that
// executes every input (client messages, joins, leaves, timer fires) in order.
package live

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"tts/internal/auth"
	"tts/internal/crossword"
	"tts/internal/store"
)

const (
	MaxPlayers  = 50
	MaxNickname = 20
	FinishedTTL = 30 * time.Minute
	IdleTTL     = 2 * time.Hour
)

// Clock abstracts time so tests can drive timers without sleeping.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

type Timer interface{ Stop() bool }

type realClock struct{}

func (realClock) Now() time.Time                            { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// RealClock is the wall clock.
var RealClock Clock = realClock{}

// Persister saves finished games.
type Persister interface {
	SaveLiveGame(ctx context.Context, g store.LiveGame) (int64, error)
}

// Client is one connection's outbound queue. The room closes Out when the
// connection must end (kicked, rejected, replaced, room closed, or too slow).
type Client struct {
	out    chan []byte
	closed bool // owned by the room goroutine
}

func NewClient() *Client { return &Client{out: make(chan []byte, 64)} }

func (c *Client) Out() <-chan []byte { return c.out }

// Config describes the quiz a room plays.
type Config struct {
	QuizID   int64
	Title    string
	Words    []crossword.PlacedWord
	Settings store.Settings
}

// Info is the public summary of a room.
type Info struct {
	Code      string `json:"code"`
	QuizTitle string `json:"quiz_title"`
	Phase     string `json:"phase"`
}

type player struct {
	id, token, nickname string
	avatar              json.RawMessage
	bell                string
	userID              *int64
	score               int
	correct, wrong      int
	correctMS           int64
	conn                *Client // nil while disconnected
}

type clue struct {
	pub      PublicClue
	answer   string
	cells    []crossword.Cell
	status   string
	solvedBy string
}

func (c *clue) done() bool { return c.status == StatusSolved || c.status == StatusRevealed }

type activeClue struct {
	clue      *clue
	answering *player
	buzzAt    time.Time
	deadline  time.Time
	timer     Timer
	locked    []string
}

func (a *activeClue) isLocked(id string) bool { return slices.Contains(a.locked, id) }

type Room struct {
	code    string
	cfg     Config
	clock   Clock
	persist Persister
	hub     *Hub
	in      chan func()
	done    chan struct{}
	phaseV  atomic.Pointer[string]

	// Everything below is owned by the run goroutine.
	stopped      bool
	phase        string
	settings     store.Settings
	cells        []PublicCell
	width        int
	height       int
	clues        []*clue
	byID         map[string]*clue
	players      []*player
	clients      map[*Client]*player // nil value = host
	nextID       int
	active       *activeClue
	gen          int
	last         *Outcome
	podium       []PodiumEntry
	startedAt    time.Time
	lastActivity time.Time
	idleTimer    Timer
	finishTimer  Timer
}

func newRoom(code string, cfg Config, g crossword.Grid, clock Clock, persist Persister, hub *Hub) *Room {
	r := &Room{
		code: code, cfg: cfg, clock: clock, persist: persist, hub: hub,
		in: make(chan func()), done: make(chan struct{}),
		phase: PhaseLobby, settings: cfg.Settings, cells: PublicCells(g), width: g.Width, height: g.Height,
		byID: map[string]*clue{}, clients: map[*Client]*player{},
		lastActivity: clock.Now(),
	}
	r.phaseV.Store(new(PhaseLobby))
	pub := PublicClues(g)
	for i, w := range g.Words {
		c := &clue{pub: pub[i], answer: w.Answer, cells: w.Cells(), status: StatusOpen}
		r.clues = append(r.clues, c)
		r.byID[c.pub.ID] = c
	}
	r.idleTimer = clock.AfterFunc(IdleTTL, func() { r.post(r.checkIdle) })
	go r.run()
	return r
}

func (r *Room) run() {
	defer close(r.done)
	for f := range r.in {
		f()
		if r.stopped {
			return
		}
	}
}

// post queues f on the room goroutine; it is dropped once the room is closed.
func (r *Room) post(f func()) {
	select {
	case r.in <- f:
	case <-r.done:
	}
}

func (r *Room) Code() string { return r.code }

// Done is closed when the room has shut down.
func (r *Room) Done() <-chan struct{} { return r.done }

func (r *Room) Info() Info {
	return Info{Code: r.code, QuizTitle: r.cfg.Title, Phase: *r.phaseV.Load()}
}

// Join processes a hello. user is the logged-in account, or nil for guests.
func (r *Room) Join(c *Client, h Hello, user *store.User) {
	r.post(func() { r.join(c, h, user) })
}

// Message handles a client message after a successful join.
func (r *Room) Message(c *Client, m Msg) { r.post(func() { r.message(c, m) }) }

// Leave reports that the client's connection has ended.
func (r *Room) Leave(c *Client) { r.post(func() { r.leave(c) }) }

// Close shuts the room down and waits for it to stop.
func (r *Room) Close() {
	r.post(r.shutdown)
	<-r.done
}

func (r *Room) touch() { r.lastActivity = r.clock.Now() }

func (r *Room) setPhase(p string) {
	r.phase = p
	r.phaseV.Store(new(p))
}

func (r *Room) checkIdle() {
	idle := r.clock.Now().Sub(r.lastActivity)
	if idle >= IdleTTL {
		r.shutdown()
		return
	}
	r.idleTimer = r.clock.AfterFunc(IdleTTL-idle, func() { r.post(r.checkIdle) })
}

func (r *Room) shutdown() {
	r.stopTimer()
	r.idleTimer.Stop()
	if r.finishTimer != nil {
		r.finishTimer.Stop()
	}
	for c := range r.clients {
		r.closeClient(c)
	}
	r.hub.remove(r)
	r.stopped = true
}

// --- output ---

func (r *Room) closeClient(c *Client) {
	if !c.closed {
		c.closed = true
		close(c.out)
	}
}

func (r *Room) sendRaw(c *Client, b []byte) {
	if c.closed {
		return
	}
	select {
	case c.out <- b:
	default:
		// Too slow: end the connection; its Leave arrives as a normal input.
		r.closeClient(c)
	}
}

func (r *Room) send(c *Client, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("live: encode message", "err", err)
		return
	}
	r.sendRaw(c, b)
}

func (r *Room) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Error("live: encode message", "err", err)
		return
	}
	for c := range r.clients {
		r.sendRaw(c, b)
	}
}

func (r *Room) broadcastState() { r.broadcast(stateMsg{Type: "state", State: r.snapshot()}) }

func (r *Room) sendError(c *Client, msg string) { r.send(c, errorMsg{Type: "error", Message: msg}) }

// snapshot builds the RoomState. Answers appear only for solved/revealed clues.
func (r *Room) snapshot() *RoomState {
	s := &RoomState{
		Code:  r.code,
		Phase: r.phase,
		Quiz: StateQuiz{Title: r.cfg.Title, Width: r.width, Height: r.height,
			Cells: r.cells, Clues: make([]StateClue, len(r.clues))},
		Letters:     map[string]string{},
		Settings:    r.settings,
		Players:     make([]StatePlayer, len(r.players)),
		Bells:       Bells,
		LastOutcome: r.last,
		Podium:      r.podium,
		ServerNow:   r.clock.Now().UnixMilli(),
	}
	for i, c := range r.clues {
		sc := StateClue{PublicClue: c.pub, Status: c.status}
		if c.done() {
			sc.Answer = new(c.answer)
			for j, cell := range c.cells {
				s.Letters[cell.Key()] = c.answer[j : j+1]
			}
		}
		if c.solvedBy != "" {
			sc.SolvedBy = new(c.solvedBy)
		}
		s.Quiz.Clues[i] = sc
	}
	for i, p := range r.players {
		s.Players[i] = StatePlayer{ID: p.id, Nickname: p.nickname, Avatar: p.avatar, Bell: p.bell, Score: p.score,
			Correct: p.correct, Wrong: p.wrong, Connected: p.conn != nil, IsGuest: p.userID == nil}
	}
	if a := r.active; a != nil {
		sa := &StateActive{ClueID: a.clue.pub.ID, State: "waiting", LockedOut: slices.Clone(a.locked)}
		if sa.LockedOut == nil {
			sa.LockedOut = []string{}
		}
		if a.answering != nil {
			sa.State = "answering"
			sa.AnsweringPlayer = new(a.answering.id)
			sa.Deadline = new(a.deadline.UnixMilli())
		}
		s.Active = sa
	}
	return s
}

// --- joins and leaves ---

func (r *Room) join(c *Client, h Hello, u *store.User) {
	if _, dup := r.clients[c]; dup || c.closed {
		return
	}
	r.touch()
	reject := func(msg string) {
		r.sendError(c, msg)
		r.closeClient(c)
	}
	switch h.Role {
	case "host":
		if u == nil || !u.IsAdmin() {
			reject("Khusus admin")
			return
		}
		r.clients[c] = nil
		r.send(c, stateMsg{Type: "state", State: r.snapshot()})
	case "player":
		p, msg := r.admit(h, u)
		if p == nil {
			reject(msg)
			return
		}
		if old := p.conn; old != nil {
			delete(r.clients, old)
			r.sendError(old, "Tersambung dari perangkat lain")
			r.closeClient(old)
		}
		p.conn = c
		r.clients[c] = p
		r.send(c, welcomeMsg{Type: "welcome", You: You{PlayerID: p.id, Token: p.token, Nickname: p.nickname}})
		r.broadcastState()
	default:
		reject("Peran tidak dikenal")
	}
}

// admit resumes an existing player (by token, or by account for logged-in users)
// or creates a new one. On failure it returns nil and the error message.
func (r *Room) admit(h Hello, u *store.User) (*player, string) {
	for _, p := range r.players {
		sameAccount := u != nil && p.userID != nil && *p.userID == u.ID
		if sameAccount || (h.Token != "" && p.token == h.Token && p.userID == nil && u == nil) {
			return p, ""
		}
	}
	if r.phase == PhaseFinished {
		return nil, "Permainan sudah selesai"
	}
	var nick string
	var userID *int64
	if u != nil {
		nick, userID = u.Username, new(u.ID)
	} else {
		nick = strings.TrimSpace(h.Nickname)
		if n := utf8.RuneCountInString(nick); n < 1 || n > MaxNickname {
			return nil, fmt.Sprintf("Nama harus 1–%d karakter", MaxNickname)
		}
	}
	for _, p := range r.players {
		if strings.EqualFold(p.nickname, nick) {
			return nil, "Nama sudah dipakai di room ini"
		}
	}
	if len(r.players) >= MaxPlayers {
		return nil, fmt.Sprintf("Room penuh (maksimal %d pemain)", MaxPlayers)
	}
	avatar, ok := store.ValidAvatar(h.Avatar)
	if !ok {
		avatar = json.RawMessage(`{}`)
		if u != nil {
			avatar = u.Avatar
		}
	}
	r.nextID++
	p := &player{id: fmt.Sprintf("p%d", r.nextID), token: auth.NewToken(), nickname: nick, avatar: avatar,
		bell: r.defaultBell(), userID: userID}
	r.players = append(r.players, p)
	return p, ""
}

// defaultBell is the first unused bell in list order; with more players than
// bells, the least-used one.
func (r *Room) defaultBell() string {
	use := map[string]int{}
	for _, p := range r.players {
		use[p.bell]++
	}
	best := Bells[0]
	for _, b := range Bells {
		if use[b] < use[best] {
			best = b
		}
		if use[b] == 0 {
			return b
		}
	}
	return best
}

func (r *Room) leave(c *Client) {
	p, ok := r.clients[c]
	r.closeClient(c)
	if !ok {
		return
	}
	r.touch()
	delete(r.clients, c)
	if p == nil {
		return
	}
	p.conn = nil
	if a := r.active; a != nil && r.phase == PhasePlaying {
		if a.answering == p {
			r.wrong(p)
			r.settle()
			return
		}
		if a.answering == nil && len(a.locked) > 0 && !r.anyEligible() {
			r.exhaust()
			r.settle()
			return
		}
	}
	r.broadcastState()
}

// --- messages ---

func (r *Room) message(c *Client, m Msg) {
	p, ok := r.clients[c]
	if !ok {
		return
	}
	r.touch()
	var msg string
	if p == nil {
		msg = r.hostMsg(m)
	} else {
		msg = r.playerMsg(p, m)
	}
	if msg != "" {
		r.sendError(c, msg)
	}
}

func (r *Room) hostMsg(m Msg) string {
	switch m.Type {
	case "settings":
		if r.phase != PhaseLobby {
			return "Pengaturan terkunci setelah permainan dimulai"
		}
		s := store.Settings{PointsCorrect: m.PointsCorrect, PenaltyWrong: m.PenaltyWrong, AnswerSeconds: m.AnswerSeconds}
		if err := s.Validate(); err != nil {
			return err.Error()
		}
		r.settings = s
	case "kick":
		if r.phase != PhaseLobby {
			return "Pemain hanya bisa dikeluarkan di lobby"
		}
		i := slices.IndexFunc(r.players, func(p *player) bool { return p.id == m.PlayerID })
		if i < 0 {
			return "Pemain tidak ditemukan"
		}
		p := r.players[i]
		r.players = slices.Delete(r.players, i, i+1)
		if p.conn != nil {
			delete(r.clients, p.conn)
			r.send(p.conn, struct {
				Type string `json:"type"`
			}{"kicked"})
			r.closeClient(p.conn)
		}
	case "start":
		if r.phase != PhaseLobby {
			return "Permainan sudah dimulai"
		}
		if len(r.players) == 0 {
			return "Belum ada pemain"
		}
		r.setPhase(PhasePlaying)
		r.startedAt = r.clock.Now()
	case "open_clue":
		if r.phase != PhasePlaying {
			return "Permainan belum berjalan"
		}
		if r.active != nil {
			return "Masih ada soal yang aktif"
		}
		c, ok := r.byID[m.ClueID]
		if !ok {
			return "Soal tidak ditemukan"
		}
		if c.done() {
			return "Soal sudah selesai"
		}
		r.active = &activeClue{clue: c}
		r.last = nil
	case "skip":
		if r.active == nil {
			return "Tidak ada soal yang aktif"
		}
		c := r.active.clue
		r.stopTimer()
		r.active = nil
		c.status = StatusSkipped
		r.last = &Outcome{ClueID: c.pub.ID, Kind: OutcomeSkipped}
	case "reveal":
		if r.phase != PhasePlaying {
			return "Permainan belum berjalan"
		}
		c, ok := r.byID[m.ClueID]
		if !ok {
			return "Soal tidak ditemukan"
		}
		if c.done() {
			return "Soal sudah selesai"
		}
		if r.active != nil && r.active.clue == c {
			r.stopTimer()
			r.active = nil
		}
		c.status = StatusRevealed
		r.settle()
		return ""
	case "end":
		if r.phase != PhasePlaying {
			return "Permainan belum berjalan"
		}
		r.finish()
		return ""
	case "set_bell", "set_avatar", "buzz", "answer":
		return "Hanya untuk pemain"
	default:
		return "Pesan tidak dikenal"
	}
	r.broadcastState()
	return ""
}

func (r *Room) playerMsg(p *player, m Msg) string {
	switch m.Type {
	case "set_bell":
		if r.phase == PhaseFinished {
			return "Permainan sudah selesai"
		}
		if !slices.Contains(Bells, m.Bell) {
			return "Bel tidak dikenal"
		}
		if m.Bell == p.bell {
			return ""
		}
		if slices.ContainsFunc(r.players, func(o *player) bool { return o.bell == m.Bell }) {
			return "Bel sudah dipakai pemain lain"
		}
		p.bell = m.Bell
	case "set_avatar":
		if r.phase != PhaseLobby {
			return "Avatar hanya bisa diganti di lobby"
		}
		v, ok := store.ValidAvatar(m.Avatar)
		if !ok {
			return "Avatar tidak valid"
		}
		p.avatar = v
	case "buzz":
		a := r.active
		if r.phase != PhasePlaying || a == nil || a.answering != nil || p.conn == nil || a.isLocked(p.id) {
			return "" // late or ineligible buzzes are ignored
		}
		now := r.clock.Now()
		d := time.Duration(r.settings.AnswerSeconds) * time.Second
		r.gen++
		gen := r.gen
		a.answering, a.buzzAt, a.deadline = p, now, now.Add(d)
		a.timer = r.clock.AfterFunc(d, func() { r.post(func() { r.timeout(gen) }) })
		r.broadcast(buzzMsg{Type: "buzz", PlayerID: p.id, Bell: p.bell})
	case "answer":
		a := r.active
		if r.phase != PhasePlaying || a == nil || a.answering != p {
			return "Bukan giliranmu menjawab"
		}
		if crossword.Normalize(m.Text) == a.clue.answer {
			r.correct(p)
		} else {
			r.wrong(p)
		}
		r.settle()
		return ""
	case "settings", "kick", "start", "open_clue", "skip", "reveal", "end":
		return "Khusus host"
	default:
		return "Pesan tidak dikenal"
	}
	r.broadcastState()
	return ""
}

// --- round resolution ---

func (r *Room) stopTimer() {
	if a := r.active; a != nil && a.timer != nil {
		a.timer.Stop()
		a.timer = nil
	}
}

func (r *Room) timeout(gen int) {
	if a := r.active; a != nil && a.answering != nil && gen == r.gen {
		r.touch()
		r.wrong(a.answering)
		r.settle()
	}
}

func (r *Room) anyEligible() bool {
	return slices.ContainsFunc(r.players, func(p *player) bool { return p.conn != nil && !r.active.isLocked(p.id) })
}

func (r *Room) correct(p *player) {
	a := r.active
	r.stopTimer()
	pts := r.settings.PointsCorrect
	p.score += pts
	p.correct++
	p.correctMS += r.clock.Now().Sub(a.buzzAt).Milliseconds()
	a.clue.status, a.clue.solvedBy = StatusSolved, p.id
	r.last = &Outcome{ClueID: a.clue.pub.ID, Kind: OutcomeSolved, PlayerID: new(p.id), Answer: new(a.clue.answer)}
	r.active = nil
	r.broadcast(resultMsg{Type: "result", PlayerID: p.id, Correct: true, ClueID: a.clue.pub.ID, Delta: pts})
}

// wrong penalises p, locks them out and reopens the buzzer or exhausts the clue.
func (r *Room) wrong(p *player) {
	a := r.active
	r.stopTimer()
	pen := r.settings.PenaltyWrong
	p.score -= pen
	p.wrong++
	a.locked = append(a.locked, p.id)
	a.answering = nil
	r.broadcast(resultMsg{Type: "result", PlayerID: p.id, Correct: false, ClueID: a.clue.pub.ID, Delta: -pen})
	if !r.anyEligible() {
		r.exhaust()
	}
}

func (r *Room) exhaust() {
	c := r.active.clue
	r.stopTimer()
	c.status = StatusRevealed
	r.last = &Outcome{ClueID: c.pub.ID, Kind: OutcomeExhausted, Answer: new(c.answer)}
	r.active = nil
}

// settle finishes the game when every clue is done, else broadcasts the state.
func (r *Room) settle() {
	if !slices.ContainsFunc(r.clues, func(c *clue) bool { return !c.done() }) {
		r.finish()
		return
	}
	r.broadcastState()
}

func (r *Room) finish() {
	r.stopTimer()
	r.active = nil
	for _, c := range r.clues {
		if !c.done() {
			c.status = StatusRevealed
		}
	}
	r.setPhase(PhaseFinished)
	ranked := slices.Clone(r.players)
	slices.SortStableFunc(ranked, func(a, b *player) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.correctMS, b.correctMS),
			cmp.Compare(strings.ToLower(a.nickname), strings.ToLower(b.nickname)))
	})
	r.podium = make([]PodiumEntry, len(ranked))
	game := store.LiveGame{QuizID: new(r.cfg.QuizID), QuizTitle: r.cfg.Title, StartedAt: r.startedAt.UnixMilli(),
		EndedAt: r.clock.Now().UnixMilli(), Settings: r.settings, Players: make([]store.LivePlayer, len(ranked))}
	for i, p := range ranked {
		r.podium[i] = PodiumEntry{PlayerID: p.id, Nickname: p.nickname, Avatar: p.avatar, Score: p.score, Rank: i + 1}
		game.Players[i] = store.LivePlayer{UserID: p.userID, Nickname: p.nickname, Avatar: p.avatar, Score: p.score,
			Correct: p.correct, Wrong: p.wrong, CorrectMS: p.correctMS, Rank: i + 1}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := r.persist.SaveLiveGame(ctx, game); err != nil {
		slog.Error("live: save finished game", "room", r.code, "err", err)
	}
	cancel()
	r.finishTimer = r.clock.AfterFunc(FinishedTTL, func() { r.post(r.shutdown) })
	r.broadcastState()
}
