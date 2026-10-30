package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"tts/internal/live"
	"tts/internal/store"
)

const (
	helloTimeout = 5 * time.Second
	wsReadLimit  = 8 << 10
	pingEvery    = 20 * time.Second
	writeTimeout = 10 * time.Second
)

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	var in struct {
		QuizID int64 `json:"quiz_id"`
	}
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	q, found := s.anyQuiz(w, r, in.QuizID)
	if !found {
		return
	}
	settings, err := s.store.EffectiveSettings(r.Context(), q)
	if err != nil {
		serverError(w, r, err)
		return
	}
	room, err := s.hub.Create(live.Config{QuizID: q.ID, Title: q.Title, Words: q.Words, Settings: settings})
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Code string `json:"code"`
	}{room.Code()})
}

func (s *Server) room(w http.ResponseWriter, r *http.Request) (*live.Room, bool) {
	room, found := s.hub.Get(r.PathValue("code"))
	if !found {
		writeError(w, http.StatusNotFound, "Room tidak ditemukan")
	}
	return room, found
}

func (s *Server) roomInfo(w http.ResponseWriter, r *http.Request) {
	if room, found := s.room(w, r); found {
		writeJSON(w, http.StatusOK, room.Info())
	}
}

// joinURL is the player join link encoded in the room QR code.
func (s *Server) joinURL(code string) string {
	return s.urls()[0] + "/play.html#/join/" + code
}

func (s *Server) roomQR(w http.ResponseWriter, r *http.Request) {
	room, found := s.room(w, r)
	if !found {
		return
	}
	png, err := qrcode.Encode(s.joinURL(room.Code()), qrcode.Medium, 512)
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// liveSocket upgrades to WebSocket, waits for hello, then pumps messages between
// the connection and the room until either side ends.
func (s *Server) liveSocket(w http.ResponseWriter, r *http.Request) {
	room, found := s.room(w, r)
	if !found {
		return
	}
	u, loggedIn, err := s.sessions.User(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	var user *store.User
	if loggedIn {
		user = &u
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	defer conn.CloseNow()
	conn.SetReadLimit(wsReadLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	helloCtx, helloCancel := context.WithTimeout(ctx, helloTimeout)
	typ, data, err := conn.Read(helloCtx)
	helloCancel()
	if err != nil {
		conn.Close(websocket.StatusPolicyViolation, "hello timeout")
		return
	}
	var hello live.Hello
	if typ != websocket.MessageText || json.Unmarshal(data, &hello) != nil || hello.Type != "hello" {
		conn.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}

	client := live.NewClient()
	room.Join(client, hello, user)
	defer room.Leave(client)

	go s.readLoop(ctx, cancel, conn, room, client)

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case msg, open := <-client.Out():
			if !open {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case <-room.Done():
			conn.Close(websocket.StatusGoingAway, "room closed")
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) readLoop(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, room *live.Room, client *live.Client) {
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		recv := s.now() // stamp before any parsing: this is the server time the client's offset math relies on
		if reply, ok := pong(data, recv); ok {
			// Answered here, never through the room goroutine: a pong must not queue behind game events.
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, reply)
			wcancel()
			if err != nil {
				return
			}
			continue
		}
		var m live.Msg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		room.Message(client, m)
	}
}

// pong builds the reply to a {"type":"ping","t0":<number>} frame: t0 echoed verbatim plus the
// server's unix ms. ok is false when data is not a well-formed ping.
func pong(data []byte, now time.Time) (reply []byte, ok bool) {
	var in struct {
		Type string          `json:"type"`
		T0   json.RawMessage `json:"t0"`
	}
	if json.Unmarshal(data, &in) != nil || in.Type != "ping" {
		return nil, false
	}
	var n float64
	if json.Unmarshal(in.T0, &n) != nil {
		return nil, false
	}
	reply = append(reply, `{"type":"pong","t0":`...)
	reply = append(reply, in.T0...)
	reply = append(reply, `,"server":`...)
	reply = strconv.AppendInt(reply, now.UnixMilli(), 10)
	return append(reply, '}'), true
}
