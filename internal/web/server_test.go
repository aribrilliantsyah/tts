package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"tts/internal/auth"
	"tts/internal/live"
	"tts/internal/store"
)

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	store *store.Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("admin123")
	if _, err := st.CreateUser(context.Background(), "admin", hash, store.RoleAdmin, 1); err != nil {
		t.Fatal(err)
	}
	hub := live.NewHub(live.RealClock, st)
	static := fstest.MapFS{
		"index.html":   {Data: []byte("<!doctype html>index")},
		"live/play.js": {Data: []byte("export {}")},
		"theme.css":    {Data: []byte("body{}")},
	}
	srv := httptest.NewServer(New(Config{Store: st, Hub: hub, Static: static, Port: 3000, Now: time.Now}))
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
		st.Close()
	})
	return &harness{t: t, srv: srv, store: st}
}

func (h *harness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// do sends a JSON request and decodes the JSON response into out (if non-nil).
func (h *harness) do(c *http.Client, method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			h.t.Fatalf("%s %s: %v (%s)", method, path, err, data)
		}
	}
	return res.StatusCode
}

func (h *harness) raw(c *http.Client, method, path string) (int, http.Header, []byte) {
	req, _ := http.NewRequest(method, h.srv.URL+path, nil)
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, data
}

func (h *harness) admin() *http.Client {
	c := h.client()
	if code := h.do(c, "POST", "/api/login", credentials{"admin", "admin123"}, nil); code != 200 {
		h.t.Fatalf("admin login %d", code)
	}
	return c
}

var quizBody = map[string]any{
	"title": "Hewan", "description": "", "published": true,
	"points_correct": nil, "penalty_wrong": 3, "answer_seconds": nil,
	"layout": map[string]any{"words": []map[string]any{
		{"answer": "KUDA", "clue": "Ditunggangi", "row": 0, "col": 0, "direction": "across"},
		{"answer": "KAKI", "clue": "Untuk jalan", "row": 0, "col": 0, "direction": "down"},
	}},
}

func (h *harness) createQuiz(c *http.Client) int64 {
	var res struct{ ID int64 }
	if code := h.do(c, "POST", "/api/admin/quizzes", quizBody, &res); code != 201 {
		h.t.Fatalf("create quiz %d", code)
	}
	return res.ID
}

func TestJSONBodyRequired(t *testing.T) {
	h := newHarness(t)
	res, err := http.Post(h.srv.URL+"/api/login", "text/plain", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestAuthFlow(t *testing.T) {
	h := newHarness(t)
	c := h.client()
	var me userBody
	h.do(c, "GET", "/api/me", nil, &me)
	if me.User != nil {
		t.Fatal("anon has user")
	}
	if code := h.do(c, "POST", "/api/register", credentials{"ab", "rahasia"}, nil); code != 400 {
		t.Fatalf("short username %d", code)
	}
	if code := h.do(c, "POST", "/api/register", credentials{"dewi", "123"}, nil); code != 400 {
		t.Fatalf("short password %d", code)
	}
	if code := h.do(c, "POST", "/api/register", credentials{"dewi", "rahasia"}, &me); code != 200 || me.User.Role != "player" {
		t.Fatalf("register %d %+v", code, me.User)
	}
	if code := h.do(h.client(), "POST", "/api/register", credentials{"DEWI", "rahasia"}, nil); code != 409 {
		t.Fatalf("dup %d", code)
	}
	var e errorBody
	if code := h.do(h.client(), "POST", "/api/login", credentials{"dewi", "salah!"}, &e); code != 401 || e.Error != "Username atau password salah" {
		t.Fatalf("bad login %d %q", code, e.Error)
	}
	if code := h.do(c, "PUT", "/api/me/avatar", map[string]any{"avatar": map[string]string{"hair": "x"}}, nil); code != 200 {
		t.Fatalf("avatar %d", code)
	}
	if code := h.do(c, "PUT", "/api/me/avatar", map[string]any{"avatar": []int{1}}, nil); code != 400 {
		t.Fatalf("bad avatar %d", code)
	}
	h.do(c, "GET", "/api/me", nil, &me)
	if string(me.User.Avatar) != `{"hair":"x"}` {
		t.Fatalf("avatar %s", me.User.Avatar)
	}
	if code := h.do(c, "GET", "/api/admin/settings", nil, nil); code != 403 {
		t.Fatalf("player admin %d", code)
	}
	h.do(c, "POST", "/api/logout", nil, nil)
	h.do(c, "GET", "/api/me", nil, &me)
	if me.User != nil {
		t.Fatal("still logged in")
	}
	if code := h.do(c, "GET", "/api/quizzes", nil, nil); code != 401 {
		t.Fatalf("anon quizzes %d", code)
	}
}

func TestAdminAndSolo(t *testing.T) {
	h := newHarness(t)
	a := h.admin()
	var settings store.Settings
	h.do(a, "GET", "/api/admin/settings", nil, &settings)
	if settings != (store.Settings{PointsCorrect: 10, PenaltyWrong: 5, AnswerSeconds: 10}) {
		t.Fatalf("settings %+v", settings)
	}
	if code := h.do(a, "PUT", "/api/admin/settings", store.Settings{PointsCorrect: 10, PenaltyWrong: 5, AnswerSeconds: 61}, nil); code != 400 {
		t.Fatalf("range %d", code)
	}
	var layout struct {
		Words    []map[string]any
		Unplaced []any
	}
	h.do(a, "POST", "/api/admin/generate", map[string]any{"words": []map[string]string{{"answer": "kuda", "clue": "a"}, {"answer": "kaki", "clue": "b"}}}, &layout)
	if len(layout.Words) != 2 {
		t.Fatalf("generate %+v", layout)
	}
	id := h.createQuiz(a)

	var full QuizFull
	h.do(a, "GET", "/api/admin/quizzes/"+itoa(id), nil, &full)
	if *full.PenaltyWrong != 3 || full.PointsCorrect != nil || len(full.Words) != 2 || full.Width != 4 {
		t.Fatalf("full %+v", full)
	}

	p := h.client()
	h.do(p, "POST", "/api/register", credentials{"budi", "rahasia"}, nil)
	code, _, body := h.raw(p, "GET", "/api/quizzes/"+itoa(id))
	if code != 200 || strings.Contains(string(body), "KUDA") || strings.Contains(string(body), "KAKI") {
		t.Fatalf("puzzle leaks answers or failed: %d %s", code, body)
	}
	var chk checkResult
	h.do(p, "POST", "/api/quizzes/"+itoa(id)+"/check", cellsBody{map[string]string{"0,0": "K", "0,1": "X", "9,9": "Z"}}, &chk)
	if chk.Completed || len(chk.Wrong) != 1 || chk.Wrong[0] != "0,1" || chk.Empty != 5 {
		t.Fatalf("check %+v", chk)
	}
	all := map[string]string{"0,0": "k", "0,1": "U", "0,2": "D", "0,3": "A", "1,0": "A", "2,0": "K", "3,0": "I"}
	h.do(p, "POST", "/api/quizzes/"+itoa(id)+"/check", cellsBody{all}, &chk)
	if !chk.Completed || chk.DurationMS == nil {
		t.Fatalf("complete %+v", chk)
	}
	var got struct {
		Solution map[string]string
		Attempt  store.Attempt
	}
	h.do(p, "GET", "/api/quizzes/"+itoa(id), nil, &got)
	if got.Solution["0,3"] != "A" || got.Attempt.Checks != 2 {
		t.Fatalf("solution %+v", got)
	}
	var lb struct{ Entries []store.LeaderboardEntry }
	h.do(p, "GET", "/api/quizzes/"+itoa(id)+"/leaderboard", nil, &lb)
	if len(lb.Entries) != 1 || lb.Entries[0].Username != "budi" {
		t.Fatalf("leaderboard %+v", lb)
	}

	// Clue-only edit keeps attempts; unpublish hides from solo.
	edit := map[string]any{}
	for k, v := range quizBody {
		edit[k] = v
	}
	edit["published"] = false
	var upd struct{ ResetAttempts bool }
	h.do(a, "PUT", "/api/admin/quizzes/"+itoa(id), edit, &upd)
	if upd.ResetAttempts {
		t.Fatal("reset on non-placement edit")
	}
	if code := h.do(p, "GET", "/api/quizzes/"+itoa(id), nil, nil); code != 404 {
		t.Fatalf("unpublished visible %d", code)
	}

	var me userBody
	h.do(a, "GET", "/api/me", nil, &me)
	if code := h.do(a, "PATCH", "/api/admin/users/"+itoa(me.User.ID), map[string]string{"role": "player"}, nil); code != 400 {
		t.Fatalf("self demote %d", code)
	}
	if code := h.do(a, "DELETE", "/api/admin/users/"+itoa(me.User.ID), nil, nil); code != 400 {
		t.Fatalf("self delete %d", code)
	}
}

func TestImportLimits(t *testing.T) {
	h := newHarness(t)
	a := h.admin()
	var res struct{ Results []struct{ OK bool } }
	md := "---\njudul: X\n---\n- KUDA: a\n- KAKI: b\n"
	if code := h.do(a, "POST", "/api/admin/import", map[string]any{"files": []map[string]string{{"name": "a.md", "content": md}}}, &res); code != 200 || !res.Results[0].OK {
		t.Fatalf("import %d %+v", code, res)
	}
	files := make([]map[string]string, 21)
	for i := range files {
		files[i] = map[string]string{"name": "a.md", "content": md}
	}
	if code := h.do(a, "POST", "/api/admin/import", map[string]any{"files": files}, nil); code != 400 {
		t.Fatalf("21 files %d", code)
	}
	big := map[string]any{"files": []map[string]string{{"name": "big.md", "content": strings.Repeat("x", 65<<10)}}}
	if code := h.do(a, "POST", "/api/admin/import", big, nil); code != 413 {
		t.Fatalf("big file %d", code)
	}
}

func TestStatic(t *testing.T) {
	h := newHarness(t)
	c := h.client()
	for _, tc := range []struct{ path, ct string }{
		{"/", "text/html; charset=utf-8"},
		{"/live/play.js", "text/javascript; charset=utf-8"},
		{"/theme.css", "text/css; charset=utf-8"},
	} {
		code, hdr, _ := h.raw(c, "GET", tc.path)
		if code != 200 || hdr.Get("Content-Type") != tc.ct {
			t.Fatalf("%s: %d %s", tc.path, code, hdr.Get("Content-Type"))
		}
		if strings.HasPrefix(tc.ct, "text/html") != strings.Contains(hdr.Get("Cache-Control"), "no-cache") {
			t.Fatalf("%s cache %q", tc.path, hdr.Get("Cache-Control"))
		}
	}
	if code, _, _ := h.raw(c, "GET", "/nope.js"); code != 404 {
		t.Fatalf("missing %d", code)
	}
	var e errorBody
	if code := h.do(c, "GET", "/api/nope", nil, &e); code != 404 || e.Error == "" {
		t.Fatalf("api 404 %d", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestLiveRoomAndSocket(t *testing.T) {
	h := newHarness(t)
	a := h.admin()
	id := h.createQuiz(a)
	var room struct{ Code string }
	if code := h.do(a, "POST", "/api/live/rooms", map[string]int64{"quiz_id": id}, &room); code != 201 || len(room.Code) != 6 {
		t.Fatalf("room %d %+v", code, room)
	}
	var info live.Info
	h.do(h.client(), "GET", "/api/live/rooms/"+room.Code, nil, &info)
	if info.Phase != "lobby" || info.QuizTitle != "Hewan" {
		t.Fatalf("info %+v", info)
	}
	var e errorBody
	if code := h.do(h.client(), "GET", "/api/live/rooms/000000x", nil, &e); code != 404 || e.Error != "Room tidak ditemukan" {
		t.Fatalf("missing room %d %q", code, e.Error)
	}
	code, hdr, png := h.raw(h.client(), "GET", "/api/live/rooms/"+room.Code+"/qr.png")
	if code != 200 || hdr.Get("Content-Type") != "image/png" || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("qr %d %s", code, hdr.Get("Content-Type"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/ws/live/" + room.Code
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","role":"player","nickname":"dewi","avatar":{}}`))
	var types []string
	for len(types) < 2 {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "KUDA") {
			t.Fatalf("answer leaked: %s", data)
		}
		var m struct{ Type string }
		json.Unmarshal(data, &m)
		types = append(types, m.Type)
	}
	if types[0] != "welcome" || types[1] != "state" {
		t.Fatalf("frames %v", types)
	}

	// Host hello without admin cookie is rejected.
	host, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	host.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","role":"host"}`))
	_, data, err := host.Read(ctx)
	if err != nil || !strings.Contains(string(data), `"error"`) {
		t.Fatalf("host frame %s %v", data, err)
	}
	if _, _, err := host.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("host not closed: %v", err)
	}
}
