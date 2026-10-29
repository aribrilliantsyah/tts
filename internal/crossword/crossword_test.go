package crossword

import (
	"math/rand/v2"
	"testing"
)

var zoo = []WordInput{
	{"Harimau", "Raja hutan yang belang"},
	{"KUDA", "Ditunggangi joki"},
	{"gajah", "Belalainya panjang"},
	{"RUSA", "Tanduknya bercabang"},
	{"ULAR", "Melata tanpa kaki"},
}

func TestGeneratePlacesConnectedValidGrid(t *testing.T) {
	l := Generate(zoo, rand.New(rand.NewPCG(1, 2)))
	if len(l.Words) != len(zoo) || len(l.Unplaced) != 0 {
		t.Fatalf("placed %d, unplaced %v", len(l.Words), l.Unplaced)
	}
	g, err := Build(l.Words)
	if err != nil {
		t.Fatalf("generated layout fails Build: %v", err)
	}
	if g.Width != l.Width || g.Height != l.Height {
		t.Fatalf("dims: layout %dx%d, grid %dx%d", l.Width, l.Height, g.Width, g.Height)
	}
	// Connectivity: flood fill from one letter reaches all letters.
	var start Cell
	for c := range g.Letters {
		start = c
		break
	}
	seen := map[Cell]bool{start: true}
	stack := []Cell{start}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, n := range []Cell{{c.Row + 1, c.Col}, {c.Row - 1, c.Col}, {c.Row, c.Col + 1}, {c.Row, c.Col - 1}} {
			if _, ok := g.Letters[n]; ok && !seen[n] {
				seen[n] = true
				stack = append(stack, n)
			}
		}
	}
	if len(seen) != len(g.Letters) {
		t.Fatalf("grid not connected: reached %d of %d cells", len(seen), len(g.Letters))
	}
}

func TestGenerateReportsUnplaceable(t *testing.T) {
	l := Generate([]WordInput{{"ABC", "x"}, {"XYZ", "no shared letter"}}, rand.New(rand.NewPCG(1, 1)))
	if len(l.Words) != 1 || len(l.Unplaced) != 1 {
		t.Fatalf("want 1 placed + 1 unplaced, got %d/%d", len(l.Words), len(l.Unplaced))
	}
}

func TestBuildNumbersReadingOrderAndSharedStarts(t *testing.T) {
	g, err := Build([]PlacedWord{
		{Answer: "CAT", Clue: "a", Row: 0, Col: 0, Direction: Across},
		{Answer: "CAR", Clue: "b", Row: 0, Col: 0, Direction: Down},
		{Answer: "TO", Clue: "c", Row: 0, Col: 2, Direction: Down},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, w := range g.Words {
		got = append(got, w.ID())
	}
	want := []string{"1-across", "1-down", "2-down"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids %v, want %v", got, want)
		}
	}
}

func TestBuildRejectsConflictsAndBadInput(t *testing.T) {
	cases := map[string][]PlacedWord{
		"conflict":  {{Answer: "CAT", Clue: "a", Direction: Across}, {Answer: "DOG", Clue: "b", Direction: Down}},
		"short":     {{Answer: "A", Clue: "a", Direction: Across}},
		"no clue":   {{Answer: "CAT", Clue: "  ", Direction: Across}},
		"bad dir":   {{Answer: "CAT", Clue: "a", Direction: "diag"}},
		"negative":  {{Answer: "CAT", Clue: "a", Row: -1, Direction: Across}},
		"too wide":  {{Answer: "ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMNO", Clue: "a", Direction: Across}},
		"empty set": {},
	}
	for name, words := range cases {
		if _, err := Build(words); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
