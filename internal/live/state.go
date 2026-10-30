package live

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"

	"tts/internal/crossword"
	"tts/internal/store"
)

// Phases of a room.
const (
	PhaseLobby    = "lobby"
	PhasePlaying  = "playing"
	PhaseFinished = "finished"
)

// Clue statuses.
const (
	StatusOpen     = "open"
	StatusSkipped  = "skipped"
	StatusSolved   = "solved"
	StatusRevealed = "revealed"
)

// Bells is the built-in bell list, in fixed order.
var Bells = []string{"siuuu", "fahhh", "arghhh", "ack", "wow", "saiyan", "bong", "setrum", "kucing-nangis", "dexter", "crow-zero", "day-in-life"}

// Reactions is the fixed set of lobby reaction kinds, in display order. "bell" is a bell test:
// hosts play the sender's current bell. Reaction kinds are never persisted.
var Reactions = []string{"wave", "tawa", "api", "pesta", "degdegan", "bell"}

// Lobby reaction rate limits per player. Bell tests are also subject to the general reaction gap.
const (
	ReactGap = 600 * time.Millisecond
	BellGap  = 1500 * time.Millisecond
)

// PublicCell is one grid square without its letter.
type PublicCell struct {
	Row    int  `json:"row"`
	Col    int  `json:"col"`
	Number *int `json:"number"`
}

// PublicClue is a clue without its answer.
type PublicClue struct {
	ID        string              `json:"id"`
	Number    int                 `json:"number"`
	Direction crossword.Direction `json:"direction"`
	Row       int                 `json:"row"`
	Col       int                 `json:"col"`
	Length    int                 `json:"length"`
	Clue      string              `json:"clue"`
}

// PublicCells lists every lettered square in reading order with its number, if any.
func PublicCells(g crossword.Grid) []PublicCell {
	out := make([]PublicCell, 0, len(g.Letters))
	for c := range g.Letters {
		pc := PublicCell{Row: c.Row, Col: c.Col}
		if n, ok := g.Numbers[c]; ok {
			pc.Number = new(n)
		}
		out = append(out, pc)
	}
	slices.SortFunc(out, func(a, b PublicCell) int { return cmp.Or(cmp.Compare(a.Row, b.Row), cmp.Compare(a.Col, b.Col)) })
	return out
}

// PublicClues lists the grid's clues in numbering order, without answers.
func PublicClues(g crossword.Grid) []PublicClue {
	out := make([]PublicClue, len(g.Words))
	for i, w := range g.Words {
		out[i] = PublicClue{ID: w.ID(), Number: w.Number, Direction: w.Direction, Row: w.Row, Col: w.Col, Length: len(w.Answer), Clue: w.Clue}
	}
	return out
}

// RoomState is the full snapshot sent to every client after each change.
type RoomState struct {
	Code        string            `json:"code"`
	Phase       string            `json:"phase"`
	Quiz        StateQuiz         `json:"quiz"`
	Letters     map[string]string `json:"letters"`
	Settings    store.Settings    `json:"settings"`
	Players     []StatePlayer     `json:"players"`
	Bells       []string          `json:"bells"`
	Active      *StateActive      `json:"active"`
	LastOutcome *Outcome          `json:"last_outcome"`
	Podium      []PodiumEntry     `json:"podium"`
	ServerNow   int64             `json:"server_now"`
}

type StateQuiz struct {
	Title  string       `json:"title"`
	Width  int          `json:"width"`
	Height int          `json:"height"`
	Cells  []PublicCell `json:"cells"`
	Clues  []StateClue  `json:"clues"`
}

type StateClue struct {
	PublicClue
	Status   string  `json:"status"`
	Answer   *string `json:"answer"`
	SolvedBy *string `json:"solved_by"`
}

type StatePlayer struct {
	ID        string          `json:"id"`
	Nickname  string          `json:"nickname"`
	Avatar    json.RawMessage `json:"avatar"`
	Bell      string          `json:"bell"`
	Score     int             `json:"score"`
	Correct   int             `json:"correct"`
	Wrong     int             `json:"wrong"`
	Connected bool            `json:"connected"`
	IsGuest   bool            `json:"is_guest"`
}

type StateActive struct {
	ClueID          string   `json:"clue_id"`
	State           string   `json:"state"`
	AnsweringPlayer *string  `json:"answering_player"`
	Deadline        *int64   `json:"deadline"`
	LockedOut       []string `json:"locked_out"`
}

// Outcome kinds.
const (
	OutcomeSolved    = "solved"
	OutcomeExhausted = "exhausted"
	OutcomeSkipped   = "skipped"
)

type Outcome struct {
	ClueID   string  `json:"clue_id"`
	Kind     string  `json:"kind"`
	PlayerID *string `json:"player_id"`
	Answer   *string `json:"answer"`
}

type PodiumEntry struct {
	PlayerID string          `json:"player_id"`
	Nickname string          `json:"nickname"`
	Avatar   json.RawMessage `json:"avatar"`
	Score    int             `json:"score"`
	Rank     int             `json:"rank"`
}

// Hello is the first message a client sends.
type Hello struct {
	Type     string          `json:"type"`
	Role     string          `json:"role"`
	Nickname string          `json:"nickname"`
	Avatar   json.RawMessage `json:"avatar"`
	Token    string          `json:"token"`
}

// Msg is any later client message; fields are used per Type.
type Msg struct {
	Type          string          `json:"type"`
	Bell          string          `json:"bell"`
	Avatar        json.RawMessage `json:"avatar"`
	Text          string          `json:"text"`
	PointsCorrect int             `json:"points_correct"`
	PenaltyWrong  int             `json:"penalty_wrong"`
	AnswerSeconds int             `json:"answer_seconds"`
	PlayerID      string          `json:"player_id"`
	ClueID        string          `json:"clue_id"`
	Kind          string          `json:"kind"`
}

type stateMsg struct {
	Type  string     `json:"type"`
	State *RoomState `json:"state"`
}

type You struct {
	PlayerID string `json:"player_id"`
	Token    string `json:"token"`
	Nickname string `json:"nickname"`
}

type welcomeMsg struct {
	Type string `json:"type"`
	You  You    `json:"you"`
}

type errorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type buzzMsg struct {
	Type     string `json:"type"`
	PlayerID string `json:"player_id"`
	Bell     string `json:"bell"`
}

type resultMsg struct {
	Type     string `json:"type"`
	PlayerID string `json:"player_id"`
	Correct  bool   `json:"correct"`
	ClueID   string `json:"clue_id"`
	Delta    int    `json:"delta"`
}

type reactMsg struct {
	Type     string `json:"type"`
	PlayerID string `json:"player_id"`
	Kind     string `json:"kind"`
}
