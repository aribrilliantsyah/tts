package mdimport

import (
	"strings"
	"testing"
)

const good = `---
Judul: Kebun Binatang   # nama kuis
deskripsi: 5 hewan bersembunyi di grid
poin_benar: 10
penalti_salah: 5
batas_waktu: 10
---

# Hewan
- HARIMAU: Raja hutan yang belang
* kuda: Ditunggangi joki di pacuan
- Gajah: Hewan berbelalai: besar
`

func TestParseValid(t *testing.T) {
	r := Parse("zoo.md", good)
	if !r.OK || len(r.Errors) != 0 {
		t.Fatalf("errors: %+v", r.Errors)
	}
	q := r.Quiz
	if q.Title != "Kebun Binatang" || q.Description != "5 hewan bersembunyi di grid" {
		t.Fatalf("front matter: %+v", q)
	}
	if *q.PointsCorrect != 10 || *q.PenaltyWrong != 5 || *q.AnswerSeconds != 10 {
		t.Fatal("numbers")
	}
	if got := len(q.Layout.Words) + len(q.Layout.Unplaced); got != 3 {
		t.Fatalf("words %d", got)
	}
	for _, w := range q.Layout.Words {
		if w.Answer == "GAJAH" && w.Clue != "Hewan berbelalai: besar" {
			t.Fatalf("split on first colon: %q", w.Clue)
		}
	}
}

func TestParseOptionalNumbersNil(t *testing.T) {
	r := Parse("a.md", "---\njudul: X\n---\n- AB: cd\r\n- BA: dc\n")
	if !r.OK {
		t.Fatalf("%+v", r.Errors)
	}
	if r.Quiz.PointsCorrect != nil || r.Quiz.PenaltyWrong != nil || r.Quiz.AnswerSeconds != nil {
		t.Fatal("overrides should be nil")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		line      int
		msg       string
		wantDraft bool
	}{
		{"no front matter", "- AB: x\n", 1, "front matter tidak ditemukan", false},
		{"unclosed front matter", "---\njudul: X\n- AB: x\n", 1, "tidak ditutup", false},
		{"missing title", "---\ndeskripsi: x\n---\n- AB: x\n", 1, "judul wajib", false},
		{"bad front matter line", "---\njudul: X\nasal\n---\n- AB: x\n", 3, "kunci: nilai", true},
		{"unknown key", "---\njudul: X\nwaktu: 5\n---\n- AB: x\n", 3, "kunci tidak dikenal", true},
		{"empty title", "---\njudul:   # kosong\n---\n- AB: x\n", 2, "judul wajib", false},
		{"points not number", "---\njudul: X\npoin_benar: sepuluh\n---\n- AB: x\n", 3, "harus berupa angka", true},
		{"points out of range", "---\njudul: X\npoin_benar: 0\n---\n- AB: x\n", 3, "1–100", true},
		{"penalty out of range", "---\njudul: X\npenalti_salah: 101\n---\n- AB: x\n", 3, "0–100", true},
		{"seconds out of range", "---\njudul: X\nbatas_waktu: 4\n---\n- AB: x\n", 3, "5–60", true},
		{"duplicate key", "---\njudul: X\nJUDUL: Y\n---\n- AB: x\n", 3, "sudah ada di baris 2", true},
		{"title too long", "---\njudul: " + strings.Repeat("a", 121) + "\n---\n- AB: x\n", 2, "judul maksimal", false},
		{"unrecognized line", "---\njudul: X\n---\n- AB: x\nhalo\n", 5, "baris tidak dikenali", true},
		{"word missing colon", "---\njudul: X\n---\n- AB: x\n- CD petunjuk\n", 5, "format kata", true},
		{"answer too short", "---\njudul: X\n---\n- AB: x\n- A1: satu\n", 5, "minimal 2 huruf", true},
		{"empty clue", "---\njudul: X\n---\n- AB: x\n- CD:   \n", 5, "petunjuk kosong", true},
		{"clue too long", "---\njudul: X\n---\n- AB: x\n- CD: " + strings.Repeat("p", 301) + "\n", 5, "petunjuk maksimal 300", true},
		{"duplicate answer", "---\njudul: X\n---\n- AB: x\n- a-b: y\n", 5, "duplikat (pertama di baris 4)", true},
		{"no words", "---\njudul: X\n---\n", 4, "belum ada kata", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse("f.md", tc.content)
			if r.OK {
				t.Fatal("want not ok")
			}
			found := false
			for _, e := range r.Errors {
				if e.Line == tc.line && strings.Contains(e.Message, tc.msg) {
					found = true
				}
			}
			if !found {
				t.Fatalf("want line %d %q, got %+v", tc.line, tc.msg, r.Errors)
			}
			if (r.Quiz != nil) != tc.wantDraft {
				t.Fatalf("draft present=%v want %v", r.Quiz != nil, tc.wantDraft)
			}
		})
	}
}

func TestParseUnplacedNotError(t *testing.T) {
	r := Parse("u.md", "---\njudul: X\n---\n- ABC: x\n- XYZ: y\n")
	if !r.OK {
		t.Fatalf("%+v", r.Errors)
	}
	if len(r.Quiz.Layout.Unplaced) != 1 {
		t.Fatalf("want 1 unplaced, got %+v", r.Quiz.Layout)
	}
}
