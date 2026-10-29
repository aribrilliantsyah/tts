// Package mdimport parses the markdown quiz format (front matter + word list)
// into a quiz draft with per-line errors, then lays it out.
package mdimport

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"tts/internal/crossword"
	"tts/internal/store"
)

const (
	MaxTitle       = 120
	MaxDescription = 1000
)

// Error is a problem on one 1-based line. File-level problems (missing title,
// no words) point at the opening "---" or the last line respectively.
type Error struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Draft is a parsed, laid-out quiz that has not been saved.
type Draft struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	store.Overrides
	Layout crossword.Layout `json:"layout"`
}

type Result struct {
	Name   string  `json:"name"`
	OK     bool    `json:"ok"`
	Errors []Error `json:"errors"`
	Quiz   *Draft  `json:"quiz,omitempty"`
}

type parser struct {
	errs []Error
}

func (p *parser) fail(line int, format string, args ...any) {
	p.errs = append(p.errs, Error{Line: line, Message: fmt.Sprintf(format, args...)})
}

// stripComment removes an inline "# comment" (a '#' at the start or after whitespace).
func stripComment(v string) string {
	for i, r := range v {
		if r == '#' && (i == 0 || unicode.IsSpace(rune(v[i-1]))) {
			return strings.TrimSpace(v[:i])
		}
	}
	return strings.TrimSpace(v)
}

// Parse parses one file and, when a title and at least one word were found,
// generates its layout. OK is true only when there are no errors.
func Parse(name, content string) Result {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, "\r")
	}
	p := &parser{}
	d := Draft{}

	// Front matter: first non-blank line must be "---".
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	open := i + 1
	body := 0
	if i == len(lines) || strings.TrimSpace(lines[i]) != "---" {
		p.fail(min(open, len(lines)), "front matter tidak ditemukan: file harus diawali baris ---")
		body = i
	} else {
		closed := false
		seen := map[string]int{}
		for i++; i < len(lines); i++ {
			n, raw := i+1, strings.TrimSpace(lines[i])
			if raw == "---" {
				closed = true
				i++
				break
			}
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			key, val, ok := strings.Cut(raw, ":")
			if !ok {
				p.fail(n, "baris front matter harus berformat kunci: nilai")
				continue
			}
			key = strings.ToLower(strings.TrimSpace(key))
			val = stripComment(val)
			if prev, dup := seen[key]; dup {
				p.fail(n, "kunci %q sudah ada di baris %d", key, prev)
				continue
			}
			seen[key] = n
			switch key {
			case "judul":
				if val == "" {
					p.fail(n, "judul wajib diisi")
					continue
				}
				if len([]rune(val)) > MaxTitle {
					p.fail(n, "judul maksimal %d karakter", MaxTitle)
					continue
				}
				d.Title = val
			case "deskripsi":
				if len([]rune(val)) > MaxDescription {
					p.fail(n, "deskripsi maksimal %d karakter", MaxDescription)
					continue
				}
				d.Description = val
			case "poin_benar":
				d.PointsCorrect = p.number(n, key, val, store.MinPoints, store.MaxPoints)
			case "penalti_salah":
				d.PenaltyWrong = p.number(n, key, val, store.MinPenalty, store.MaxPenalty)
			case "batas_waktu":
				d.AnswerSeconds = p.number(n, key, val, store.MinSeconds, store.MaxSeconds)
			default:
				p.fail(n, "kunci tidak dikenal: %q", key)
			}
		}
		if !closed {
			p.fail(open, "front matter tidak ditutup dengan ---")
		} else if _, ok := seen["judul"]; !ok {
			p.fail(open, "judul wajib diisi")
		}
		body = i
	}

	var words []crossword.WordInput
	firstLine := map[string]int{}
	for i := body; i < len(lines); i++ {
		n, raw := i+1, strings.TrimSpace(lines[i])
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		item, ok := strings.CutPrefix(raw, "- ")
		if !ok {
			item, ok = strings.CutPrefix(raw, "* ")
		}
		if !ok {
			p.fail(n, "baris tidak dikenali")
			continue
		}
		ans, clue, ok := strings.Cut(item, ":")
		if !ok {
			p.fail(n, "format kata harus - JAWABAN: petunjuk")
			continue
		}
		answer, clue := crossword.Normalize(ans), strings.TrimSpace(clue)
		switch {
		case len(answer) < crossword.MinAnswer:
			p.fail(n, "jawaban minimal %d huruf", crossword.MinAnswer)
		case clue == "":
			p.fail(n, "petunjuk kosong")
		case len([]rune(clue)) > crossword.MaxClueLen:
			p.fail(n, "petunjuk maksimal %d karakter", crossword.MaxClueLen)
		case firstLine[answer] != 0:
			p.fail(n, "jawaban %s duplikat (pertama di baris %d)", answer, firstLine[answer])
		case len(words) == crossword.MaxWords:
			p.fail(n, "maksimal %d kata", crossword.MaxWords)
		default:
			firstLine[answer] = n
			words = append(words, crossword.WordInput{Answer: answer, Clue: clue})
		}
	}
	if len(words) == 0 {
		p.fail(len(lines), "belum ada kata (format: - JAWABAN: petunjuk)")
	}

	res := Result{Name: name, OK: len(p.errs) == 0, Errors: p.errs}
	if res.Errors == nil {
		res.Errors = []Error{}
	}
	if d.Title != "" && len(words) > 0 {
		d.Layout = crossword.Generate(words, nil)
		res.Quiz = &d
	}
	return res
}

func (p *parser) number(line int, key, val string, lo, hi int) *int {
	v, err := strconv.Atoi(val)
	if err != nil {
		p.fail(line, "%s harus berupa angka", key)
		return nil
	}
	if v < lo || v > hi {
		p.fail(line, "%s harus %d–%d", key, lo, hi)
		return nil
	}
	return new(v)
}
