// Package web serves the JSON API, the live WebSocket and the embedded frontend.
package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"tts/internal/auth"
	"tts/internal/live"
	"tts/internal/store"
)

type Config struct {
	Store  *store.Store
	Hub    *live.Hub
	Static fs.FS // frontend files (web.FS)
	Port   int
	Now    func() time.Time
}

type Server struct {
	store    *store.Store
	hub      *live.Hub
	static   fs.FS
	port     int
	now      func() time.Time
	sessions *auth.Sessions
}

// New builds the HTTP handler.
func New(cfg Config) http.Handler {
	s := &Server{store: cfg.Store, hub: cfg.Hub, static: cfg.Static, port: cfg.Port, now: cfg.Now,
		sessions: &auth.Sessions{Store: cfg.Store, Now: cfg.Now}}
	mux := http.NewServeMux()
	user := func(h http.HandlerFunc) http.HandlerFunc { return s.sessions.Require(false, writeError, h) }
	admin := func(h http.HandlerFunc) http.HandlerFunc { return s.sessions.Require(true, writeError, h) }

	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("PUT /api/me/avatar", user(s.setAvatar))
	mux.HandleFunc("GET /api/server-info", s.serverInfo)

	mux.HandleFunc("GET /api/quizzes", user(s.soloList))
	mux.HandleFunc("GET /api/quizzes/{id}", user(s.soloGet))
	mux.HandleFunc("PUT /api/quizzes/{id}/progress", user(s.soloProgress))
	mux.HandleFunc("POST /api/quizzes/{id}/check", user(s.soloCheck))
	mux.HandleFunc("GET /api/quizzes/{id}/leaderboard", user(s.soloLeaderboard))

	mux.HandleFunc("GET /api/admin/settings", admin(s.getSettings))
	mux.HandleFunc("PUT /api/admin/settings", admin(s.putSettings))
	mux.HandleFunc("POST /api/admin/generate", admin(s.generate))
	mux.HandleFunc("POST /api/admin/import", admin(s.importMD))
	mux.HandleFunc("GET /api/admin/quizzes", admin(s.adminQuizzes))
	mux.HandleFunc("POST /api/admin/quizzes", admin(s.createQuiz))
	mux.HandleFunc("GET /api/admin/quizzes/{id}", admin(s.adminQuiz))
	mux.HandleFunc("PUT /api/admin/quizzes/{id}", admin(s.updateQuiz))
	mux.HandleFunc("DELETE /api/admin/quizzes/{id}", admin(s.deleteQuiz))
	mux.HandleFunc("GET /api/admin/quizzes/{id}/results", admin(s.quizResults))
	mux.HandleFunc("GET /api/admin/users", admin(s.users))
	mux.HandleFunc("PATCH /api/admin/users/{id}", admin(s.patchUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", admin(s.deleteUser))
	mux.HandleFunc("GET /api/admin/live-games", admin(s.liveGames))

	mux.HandleFunc("POST /api/live/rooms", admin(s.createRoom))
	mux.HandleFunc("GET /api/live/rooms/{code}", s.roomInfo)
	mux.HandleFunc("GET /api/live/rooms/{code}/qr.png", s.roomQR)
	mux.HandleFunc("GET /ws/live/{code}", s.liveSocket)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Tidak ditemukan")
	})
	mux.HandleFunc("/", s.serveStatic)
	return mux
}

// --- JSON helpers ---

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Debug("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

type okBody struct {
	OK bool `json:"ok"`
}

var ok = okBody{OK: true}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "Terjadi kesalahan di server")
}

const defaultBodyLimit = 1 << 20

// decode reads a JSON body into v; it writes the error response and returns false on failure.
func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Body harus berupa JSON (application/json)")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			writeError(w, http.StatusRequestEntityTooLarge, "Data terlalu besar")
		} else {
			writeError(w, http.StatusBadRequest, "JSON tidak valid")
		}
		return false
	}
	return true
}

// pathID parses the {id} path value; it writes 404 and returns false when invalid.
func pathID(w http.ResponseWriter, r *http.Request, notFound string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, notFound)
		return 0, false
	}
	return id, true
}

// --- server info ---

// virtualIface matches container, VM and VPN interfaces players on the venue Wi-Fi cannot reach.
var virtualIface = []string{"docker", "br-", "veth", "virbr", "vbox", "vmnet", "tailscale", "zt", "tun", "tap", "wg", "lxc", "cni", "flannel"}

// LANURLs lists http://<ip>:<port> for every non-loopback IPv4 address of an up,
// physical-looking interface, most likely venue LAN first (192.168/16, 10/8, 172.16/12, others).
func LANURLs(port int) []string {
	type cand struct {
		url  string
		rank int
	}
	var found []cand
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			slices.ContainsFunc(virtualIface, func(p string) bool { return strings.HasPrefix(ifc.Name, p) }) {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, isNet := a.(*net.IPNet)
			if !isNet {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			rank := 3
			switch {
			case ip4[0] == 192 && ip4[1] == 168:
				rank = 0
			case ip4[0] == 10:
				rank = 1
			case ip4[0] == 172 && ip4[1]&0xf0 == 16:
				rank = 2
			}
			found = append(found, cand{"http://" + net.JoinHostPort(ip4.String(), strconv.Itoa(port)), rank})
		}
	}
	slices.SortStableFunc(found, func(a, b cand) int { return a.rank - b.rank })
	out := make([]string, len(found))
	for i, c := range found {
		out[i] = c.url
	}
	return out
}

// urls returns the LAN URLs, or the localhost URL when there is no LAN address.
func (s *Server) urls() []string {
	if u := LANURLs(s.port); len(u) > 0 {
		return u
	}
	return []string{"http://localhost:" + strconv.Itoa(s.port)}
}

func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		URLs []string `json:"urls"`
	}{s.urls()})
}

// --- static files ---

var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".ttf":         "font/ttf",
	".mp3":         "audio/mpeg",
	".ogg":         "audio/ogg",
	".wav":         "audio/wav",
	".txt":         "text/plain; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ext := path.Ext(name)
	ct, known := contentTypes[ext]
	if !known {
		ct = mime.TypeByExtension(ext)
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ext == ".html" {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}
