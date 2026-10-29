package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"tts/internal/auth"
	"tts/internal/crossword"
	"tts/internal/live"
	"tts/internal/mdimport"
	"tts/internal/store"
)

const (
	maxImportFiles = 20
	maxImportFile  = 64 << 10
)

// PublicPuzzle is a quiz as shown to players: no answers.
type PublicPuzzle struct {
	ID          int64             `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Cells       []live.PublicCell `json:"cells"`
	Clues       []live.PublicClue `json:"clues"`
}

// --- solo mode ---

// publishedQuiz loads a published quiz and its grid, writing 404 otherwise.
func (s *Server) publishedQuiz(w http.ResponseWriter, r *http.Request) (store.Quiz, crossword.Grid, bool) {
	id, valid := pathID(w, r, "Kuis tidak ditemukan")
	if !valid {
		return store.Quiz{}, crossword.Grid{}, false
	}
	q, err := s.store.Quiz(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !q.Published) {
		writeError(w, http.StatusNotFound, "Kuis tidak ditemukan")
		return store.Quiz{}, crossword.Grid{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.Quiz{}, crossword.Grid{}, false
	}
	g, err := crossword.Build(q.Words)
	if err != nil {
		serverError(w, r, err)
		return store.Quiz{}, crossword.Grid{}, false
	}
	return q, g, true
}

func (s *Server) soloList(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.SoloQuizzes(r.Context(), auth.FromContext(r.Context()).ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Quizzes []store.SoloQuizRow `json:"quizzes"`
	}{list})
}

func solution(g crossword.Grid) map[string]string {
	out := make(map[string]string, len(g.Letters))
	for c, l := range g.Letters {
		out[c.Key()] = string(l)
	}
	return out
}

func (s *Server) soloGet(w http.ResponseWriter, r *http.Request) {
	q, g, found := s.publishedQuiz(w, r)
	if !found {
		return
	}
	a, err := s.store.StartAttempt(r.Context(), auth.FromContext(r.Context()).ID, q.ID, s.now().UnixMilli())
	if err != nil {
		serverError(w, r, err)
		return
	}
	var sol map[string]string
	if a.CompletedAt != nil {
		sol = solution(g)
	}
	writeJSON(w, http.StatusOK, struct {
		Puzzle   PublicPuzzle      `json:"puzzle"`
		Attempt  store.Attempt     `json:"attempt"`
		Solution map[string]string `json:"solution"`
	}{PublicPuzzle{ID: q.ID, Title: q.Title, Description: q.Description, Width: g.Width, Height: g.Height,
		Cells: live.PublicCells(g), Clues: live.PublicClues(g)}, a, sol})
}

type cellsBody struct {
	Cells map[string]string `json:"cells"`
}

// cleanCells keeps only keys that are grid cells and values that are one letter (uppercased).
func cleanCells(g crossword.Grid, in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		rs, cs, found := strings.Cut(k, ",")
		row, err1 := strconv.Atoi(rs)
		col, err2 := strconv.Atoi(cs)
		if !found || err1 != nil || err2 != nil {
			continue
		}
		cell := crossword.Cell{Row: row, Col: col}
		if _, inGrid := g.Letters[cell]; !inGrid {
			continue
		}
		if l := crossword.Normalize(v); len(l) == 1 && utf8.RuneCountInString(v) == 1 {
			out[cell.Key()] = l
		}
	}
	return out
}

func (s *Server) soloProgress(w http.ResponseWriter, r *http.Request) {
	q, g, found := s.publishedQuiz(w, r)
	if !found {
		return
	}
	var in cellsBody
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	if err := s.store.SaveProgress(r.Context(), auth.FromContext(r.Context()).ID, q.ID, cleanCells(g, in.Cells), s.now().UnixMilli()); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}

type checkResult struct {
	Completed   bool     `json:"completed"`
	Wrong       []string `json:"wrong"`
	Empty       int      `json:"empty"`
	CompletedAt *int64   `json:"completed_at"`
	DurationMS  *int64   `json:"duration_ms"`
}

func (s *Server) soloCheck(w http.ResponseWriter, r *http.Request) {
	q, g, found := s.publishedQuiz(w, r)
	if !found {
		return
	}
	var in cellsBody
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	cells := cleanCells(g, in.Cells)
	res := checkResult{Wrong: []string{}}
	for c, want := range g.Letters {
		switch got, filled := cells[c.Key()]; {
		case !filled:
			res.Empty++
		case got[0] != want:
			res.Wrong = append(res.Wrong, c.Key())
		}
	}
	userID := auth.FromContext(r.Context()).ID
	now := s.now().UnixMilli()
	prev, err := s.store.StartAttempt(r.Context(), userID, q.ID, now)
	if err != nil {
		serverError(w, r, err)
		return
	}
	a := prev
	if prev.CompletedAt == nil {
		if a, err = s.store.RecordCheck(r.Context(), userID, q.ID, cells, res.Empty == 0 && len(res.Wrong) == 0, now); err != nil {
			serverError(w, r, err)
			return
		}
	}
	if a.CompletedAt != nil {
		res = checkResult{Completed: true, Wrong: []string{}, CompletedAt: a.CompletedAt, DurationMS: new(*a.CompletedAt - a.StartedAt)}
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) soloLeaderboard(w http.ResponseWriter, r *http.Request) {
	q, _, found := s.publishedQuiz(w, r)
	if !found {
		return
	}
	entries, err := s.store.Leaderboard(r.Context(), q.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Title   string                   `json:"title"`
		Entries []store.LeaderboardEntry `json:"entries"`
	}{q.Title, entries})
}

// --- admin: settings, generate, import ---

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Settings(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SetSettings(r.Context(), in); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}

func (s *Server) generate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Words []crossword.WordInput `json:"words"`
	}
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	if len(in.Words) == 0 {
		writeError(w, http.StatusBadRequest, "Masukkan minimal 1 kata")
		return
	}
	if len(in.Words) > crossword.MaxWords {
		writeError(w, http.StatusBadRequest, "Maksimal "+strconv.Itoa(crossword.MaxWords)+" kata")
		return
	}
	writeJSON(w, http.StatusOK, crossword.Generate(in.Words, nil))
}

func (s *Server) importMD(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Files []struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if !decode(w, r, (maxImportFiles+1)*maxImportFile*2, &in) {
		return
	}
	if len(in.Files) == 0 {
		writeError(w, http.StatusBadRequest, "Pilih minimal 1 file")
		return
	}
	if len(in.Files) > maxImportFiles {
		writeError(w, http.StatusBadRequest, "Maksimal "+strconv.Itoa(maxImportFiles)+" file sekaligus")
		return
	}
	for _, f := range in.Files {
		if len(f.Content) > maxImportFile {
			writeError(w, http.StatusRequestEntityTooLarge, "File "+f.Name+" lebih dari 64 KB")
			return
		}
	}
	results := make([]mdimport.Result, len(in.Files))
	for i, f := range in.Files {
		results[i] = mdimport.Parse(f.Name, f.Content)
	}
	writeJSON(w, http.StatusOK, struct {
		Results []mdimport.Result `json:"results"`
	}{results})
}

// --- admin: quizzes ---

type quizInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Published   bool   `json:"published"`
	store.Overrides
	Layout struct {
		Words []crossword.PlacedWord `json:"words"`
	} `json:"layout"`
}

// QuizFull is the admin view of a quiz, answers included.
type QuizFull struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Published   bool   `json:"published"`
	store.Overrides
	Layout    quizLayout             `json:"layout"`
	Width     int                    `json:"width"`
	Height    int                    `json:"height"`
	Words     []crossword.PlacedWord `json:"words"`
	CreatedAt int64                  `json:"created_at"`
	UpdatedAt int64                  `json:"updated_at"`
}

type quizLayout struct {
	Words []crossword.PlacedWord `json:"words"`
}

// readQuiz decodes and validates a QuizInput, writing 400 on failure.
func (s *Server) readQuiz(w http.ResponseWriter, r *http.Request) (store.Quiz, bool) {
	var in quizInput
	if !decode(w, r, defaultBodyLimit, &in) {
		return store.Quiz{}, false
	}
	q := store.Quiz{Title: strings.TrimSpace(in.Title), Description: strings.TrimSpace(in.Description),
		Published: in.Published, Overrides: in.Overrides}
	bad := func(msg string) (store.Quiz, bool) {
		writeError(w, http.StatusBadRequest, msg)
		return store.Quiz{}, false
	}
	if n := utf8.RuneCountInString(q.Title); n < 1 || n > mdimport.MaxTitle {
		return bad("Judul 1–120 karakter")
	}
	if utf8.RuneCountInString(q.Description) > mdimport.MaxDescription {
		return bad("Deskripsi maksimal 1000 karakter")
	}
	if err := q.Overrides.Validate(); err != nil {
		return bad(err.Error())
	}
	g, err := crossword.Build(in.Layout.Words)
	if err != nil {
		return bad(err.Error())
	}
	q.Width, q.Height = g.Width, g.Height
	q.Words = make([]crossword.PlacedWord, len(g.Words))
	for i, nw := range g.Words {
		q.Words[i] = nw.PlacedWord
	}
	q.UpdatedAt = s.now().UnixMilli()
	return q, true
}

func (s *Server) adminQuizzes(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.AdminQuizzes(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Quizzes []store.AdminQuizRow `json:"quizzes"`
	}{list})
}

func (s *Server) createQuiz(w http.ResponseWriter, r *http.Request) {
	q, valid := s.readQuiz(w, r)
	if !valid {
		return
	}
	q.CreatedAt = q.UpdatedAt
	q.CreatedBy = new(auth.FromContext(r.Context()).ID)
	id, err := s.store.CreateQuiz(r.Context(), q)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		ID int64 `json:"id"`
	}{id})
}

// anyQuiz loads a quiz (published or not), writing 404 when missing.
func (s *Server) anyQuiz(w http.ResponseWriter, r *http.Request, id int64) (store.Quiz, bool) {
	q, err := s.store.Quiz(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Kuis tidak ditemukan")
		return store.Quiz{}, false
	}
	if err != nil {
		serverError(w, r, err)
		return store.Quiz{}, false
	}
	return q, true
}

func (s *Server) adminQuiz(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Kuis tidak ditemukan")
	if !valid {
		return
	}
	q, found := s.anyQuiz(w, r, id)
	if !found {
		return
	}
	writeJSON(w, http.StatusOK, QuizFull{ID: q.ID, Title: q.Title, Description: q.Description, Published: q.Published,
		Overrides: q.Overrides, Layout: quizLayout{q.Words}, Width: q.Width, Height: q.Height, Words: q.Words,
		CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt})
}

func (s *Server) updateQuiz(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Kuis tidak ditemukan")
	if !valid {
		return
	}
	q, valid := s.readQuiz(w, r)
	if !valid {
		return
	}
	q.ID = id
	reset, err := s.store.UpdateQuiz(r.Context(), q)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Kuis tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		OK            bool `json:"ok"`
		ResetAttempts bool `json:"resetAttempts"`
	}{true, reset})
}

func (s *Server) deleteQuiz(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Kuis tidak ditemukan")
	if !valid {
		return
	}
	err := s.store.DeleteQuiz(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Kuis tidak ditemukan")
	case err != nil:
		serverError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, ok)
	}
}

func (s *Server) quizResults(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Kuis tidak ditemukan")
	if !valid {
		return
	}
	q, found := s.anyQuiz(w, r, id)
	if !found {
		return
	}
	g, err := crossword.Build(q.Words)
	if err != nil {
		serverError(w, r, err)
		return
	}
	results, err := s.store.SoloResults(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Title      string             `json:"title"`
		TotalCells int                `json:"totalCells"`
		Results    []store.SoloResult `json:"results"`
	}{q.Title, len(g.Letters), results})
}

func (s *Server) liveGames(w http.ResponseWriter, r *http.Request) {
	games, err := s.store.LiveGames(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Games []store.LiveGame `json:"games"`
	}{games})
}
