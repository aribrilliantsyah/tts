// Package crossword lays out words into a connected crossword grid and derives
// clue numbering. It has no knowledge of storage or transport.
package crossword

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
)

type Direction string

const (
	Across Direction = "across"
	Down   Direction = "down"
)

const (
	MaxWords    = 100
	MaxGridSize = 40
	MaxClueLen  = 300
	MinAnswer   = 2
)

type WordInput struct {
	Answer string `json:"answer"`
	Clue   string `json:"clue"`
}

type PlacedWord struct {
	Answer    string    `json:"answer"`
	Clue      string    `json:"clue"`
	Row       int       `json:"row"`
	Col       int       `json:"col"`
	Direction Direction `json:"direction"`
}

type Layout struct {
	Width    int          `json:"width"`
	Height   int          `json:"height"`
	Words    []PlacedWord `json:"words"`
	Unplaced []WordInput  `json:"unplaced"`
}

// Cell addresses one grid square. Key() is the "row,col" form used in JSON maps.
type Cell struct{ Row, Col int }

func (c Cell) Key() string { return fmt.Sprintf("%d,%d", c.Row, c.Col) }

type NumberedWord struct {
	PlacedWord
	Number int `json:"number"`
}

// ID is the stable clue identifier, e.g. "3-down".
func (w NumberedWord) ID() string { return fmt.Sprintf("%d-%s", w.Number, w.Direction) }

// Cells returns the grid squares the word occupies, in reading order.
func (w PlacedWord) Cells() []Cell {
	out := make([]Cell, len(w.Answer))
	for i := range out {
		if w.Direction == Down {
			out[i] = Cell{w.Row + i, w.Col}
		} else {
			out[i] = Cell{w.Row, w.Col + i}
		}
	}
	return out
}

type Grid struct {
	Width   int
	Height  int
	Letters map[Cell]byte
	Numbers map[Cell]int
	// Words sorted by number, then across before down.
	Words []NumberedWord
}

// Normalize uppercases and strips everything except A–Z.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Build validates placed words and derives letters, numbering and dimensions.
// Words are normalized (answer via Normalize, clue trimmed) before validation.
func Build(words []PlacedWord) (Grid, error) {
	if len(words) == 0 {
		return Grid{}, errors.New("kuis butuh minimal 1 kata")
	}
	if len(words) > MaxWords {
		return Grid{}, fmt.Errorf("maksimal %d kata", MaxWords)
	}
	g := Grid{Letters: map[Cell]byte{}, Numbers: map[Cell]int{}}
	norm := make([]PlacedWord, len(words))
	for i, w := range words {
		w.Answer = Normalize(w.Answer)
		w.Clue = strings.TrimSpace(w.Clue)
		switch {
		case len(w.Answer) < MinAnswer:
			return Grid{}, fmt.Errorf("kata #%d: jawaban minimal %d huruf", i+1, MinAnswer)
		case w.Clue == "":
			return Grid{}, fmt.Errorf("kata %q: petunjuk kosong", w.Answer)
		case len([]rune(w.Clue)) > MaxClueLen:
			return Grid{}, fmt.Errorf("kata %q: petunjuk terlalu panjang", w.Answer)
		case w.Row < 0 || w.Col < 0:
			return Grid{}, fmt.Errorf("kata %q: posisi tidak valid", w.Answer)
		case w.Direction != Across && w.Direction != Down:
			return Grid{}, fmt.Errorf("kata %q: arah tidak valid", w.Answer)
		}
		for j, c := range w.Cells() {
			if prev, ok := g.Letters[c]; ok && prev != w.Answer[j] {
				return Grid{}, fmt.Errorf("huruf bentrok di baris %d, kolom %d", c.Row+1, c.Col+1)
			}
			g.Letters[c] = w.Answer[j]
			g.Width = max(g.Width, c.Col+1)
			g.Height = max(g.Height, c.Row+1)
		}
		norm[i] = w
	}
	if g.Width > MaxGridSize || g.Height > MaxGridSize {
		return Grid{}, fmt.Errorf("grid maksimal %d×%d", MaxGridSize, MaxGridSize)
	}
	starts := make([]Cell, 0, len(norm))
	for _, w := range norm {
		c := Cell{w.Row, w.Col}
		if _, seen := g.Numbers[c]; !seen {
			g.Numbers[c] = 0
			starts = append(starts, c)
		}
	}
	sort.Slice(starts, func(i, j int) bool {
		if starts[i].Row != starts[j].Row {
			return starts[i].Row < starts[j].Row
		}
		return starts[i].Col < starts[j].Col
	})
	for i, c := range starts {
		g.Numbers[c] = i + 1
	}
	for _, w := range norm {
		g.Words = append(g.Words, NumberedWord{PlacedWord: w, Number: g.Numbers[Cell{w.Row, w.Col}]})
	}
	sort.SliceStable(g.Words, func(i, j int) bool {
		if g.Words[i].Number != g.Words[j].Number {
			return g.Words[i].Number < g.Words[j].Number
		}
		return g.Words[i].Direction == Across && g.Words[j].Direction == Down
	})
	return g, nil
}

const workSize = 60

type board map[Cell]byte

// fit returns the number of crossings for placing word at (row,col,dir), or -1 if illegal.
func (b board) fit(word string, row, col int, dir Direction, first bool) int {
	dr, dc := 0, 1
	if dir == Down {
		dr, dc = 1, 0
	}
	endR, endC := row+dr*(len(word)-1), col+dc*(len(word)-1)
	if row < 0 || col < 0 || endR >= workSize || endC >= workSize {
		return -1
	}
	if _, ok := b[Cell{row - dr, col - dc}]; ok {
		return -1
	}
	if _, ok := b[Cell{endR + dr, endC + dc}]; ok {
		return -1
	}
	crossings := 0
	for i := range len(word) {
		r, c := row+dr*i, col+dc*i
		if v, ok := b[Cell{r, c}]; ok {
			if v != word[i] {
				return -1
			}
			crossings++
			continue
		}
		// Perpendicular neighbours of a new letter must be empty, else unintended words form.
		if _, ok := b[Cell{r - dc, c - dr}]; ok {
			return -1
		}
		if _, ok := b[Cell{r + dc, c + dr}]; ok {
			return -1
		}
	}
	if crossings == len(word) || (!first && crossings == 0) {
		return -1
	}
	return crossings
}

func (b board) put(w PlacedWord) {
	for i, c := range w.Cells() {
		b[c] = w.Answer[i]
	}
}

func attempt(order []WordInput) Layout {
	b := board{}
	var placed []PlacedWord
	var unplaced []WordInput
	for _, in := range order {
		word := in.Answer
		if len(placed) == 0 {
			w := PlacedWord{Answer: word, Clue: in.Clue, Row: workSize / 2, Col: (workSize - len(word)) / 2, Direction: Across}
			b.put(w)
			placed = append(placed, w)
			continue
		}
		var best *PlacedWord
		bestScore := 0
		for _, p := range placed {
			dir := Down
			if p.Direction == Down {
				dir = Across
			}
			for i := range len(p.Answer) {
				for j := range len(word) {
					if p.Answer[i] != word[j] {
						continue
					}
					cross := p.Cells()[i]
					row, col := cross.Row, cross.Col-j
					if dir == Down {
						row, col = cross.Row-j, cross.Col
					}
					if s := b.fit(word, row, col, dir, false); s > bestScore {
						bestScore = s
						best = &PlacedWord{Answer: word, Clue: in.Clue, Row: row, Col: col, Direction: dir}
					}
				}
			}
		}
		if best == nil {
			unplaced = append(unplaced, in)
			continue
		}
		b.put(*best)
		placed = append(placed, *best)
	}
	return crop(placed, unplaced)
}

func crop(placed []PlacedWord, unplaced []WordInput) Layout {
	l := Layout{Words: []PlacedWord{}, Unplaced: unplaced}
	if l.Unplaced == nil {
		l.Unplaced = []WordInput{}
	}
	if len(placed) == 0 {
		return l
	}
	minR, minC, maxR, maxC := workSize, workSize, -1, -1
	for _, w := range placed {
		for _, c := range w.Cells() {
			minR, minC = min(minR, c.Row), min(minC, c.Col)
			maxR, maxC = max(maxR, c.Row), max(maxC, c.Col)
		}
	}
	for _, w := range placed {
		w.Row -= minR
		w.Col -= minC
		l.Words = append(l.Words, w)
	}
	l.Width, l.Height = maxC-minC+1, maxR-minR+1
	return l
}

// Generate lays out inputs into a connected crossword, keeping the best of many
// randomized attempts (most words placed, then smallest area). Inputs with an
// answer shorter than MinAnswer or an empty clue are dropped. rng may be nil.
func Generate(inputs []WordInput, rng *rand.Rand) Layout {
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	var words []WordInput
	for _, in := range inputs {
		a, c := Normalize(in.Answer), strings.TrimSpace(in.Clue)
		if len(a) >= MinAnswer && c != "" {
			words = append(words, WordInput{Answer: a, Clue: c})
		}
	}
	if len(words) == 0 {
		return Layout{Words: []PlacedWord{}, Unplaced: []WordInput{}}
	}
	var best Layout
	for t := range 200 {
		order := append([]WordInput(nil), words...)
		if t == 0 {
			sort.SliceStable(order, func(i, j int) bool { return len(order[i].Answer) > len(order[j].Answer) })
		} else {
			rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		}
		l := attempt(order)
		if t == 0 || len(l.Words) > len(best.Words) ||
			(len(l.Words) == len(best.Words) && l.Width*l.Height < best.Width*best.Height) {
			best = l
		}
		if len(best.Unplaced) == 0 && t >= 30 {
			break
		}
	}
	return best
}
