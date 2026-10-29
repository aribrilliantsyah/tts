package live

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"

	"tts/internal/crossword"
)

var ErrNoCode = errors.New("no free room code")

// Hub maps 6-digit codes to open rooms.
type Hub struct {
	clock   Clock
	persist Persister

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewHub(clock Clock, persist Persister) *Hub {
	return &Hub{clock: clock, persist: persist, rooms: map[string]*Room{}}
}

// Create opens a room for the quiz. The words are validated with crossword.Build.
func (h *Hub) Create(cfg Config) (*Room, error) {
	g, err := crossword.Build(cfg.Words)
	if err != nil {
		return nil, fmt.Errorf("build quiz grid: %w", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for range 1000 {
		code := fmt.Sprintf("%06d", rand.IntN(1_000_000))
		if _, taken := h.rooms[code]; taken {
			continue
		}
		r := newRoom(code, cfg, g, h.clock, h.persist, h)
		h.rooms[code] = r
		return r, nil
	}
	return nil, ErrNoCode
}

// Get returns the open room with the code.
func (h *Hub) Get(code string) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[code]
	return r, ok
}

func (h *Hub) remove(r *Room) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.rooms[r.code] == r {
		delete(h.rooms, r.code)
	}
}

// Close shuts down every room.
func (h *Hub) Close() {
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()
	for _, r := range rooms {
		r.Close()
	}
}
