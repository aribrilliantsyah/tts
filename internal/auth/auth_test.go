package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tts/internal/store"
)

func TestPasswordAndToken(t *testing.T) {
	long := strings.Repeat("a", 199)
	h, err := HashPassword(long + "b")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, long+"b") || CheckPassword(h, long+"c") {
		t.Fatal("check password beyond 72 bytes")
	}
	if a, b := NewToken(), NewToken(); len(a) != 64 || a == b {
		t.Fatalf("token %q %q", a, b)
	}
}

func TestSessionFlow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.UnixMilli(1_000_000)
	s := &Sessions{Store: st, Now: func() time.Time { return now }}
	u, _ := st.CreateUser(context.Background(), "admin", "x", store.RoleAdmin, 0)
	p, _ := st.CreateUser(context.Background(), "pemain", "x", store.RolePlayer, 0)

	login := func(id int64) *http.Cookie {
		rec := httptest.NewRecorder()
		if err := s.Login(rec, httptest.NewRequest("POST", "/", nil), id); err != nil {
			t.Fatal(err)
		}
		c := rec.Result().Cookies()[0]
		if c.Name != CookieName || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("cookie %+v", c)
		}
		return c
	}
	fail := func(w http.ResponseWriter, status int, msg string) { w.WriteHeader(status) }
	h := s.Require(true, fail, func(w http.ResponseWriter, r *http.Request) {
		if FromContext(r.Context()).ID != u.ID {
			t.Error("user not in context")
		}
	})
	call := func(c *http.Cookie) int {
		req := httptest.NewRequest("GET", "/", nil)
		if c != nil {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	if code := call(nil); code != 401 {
		t.Fatalf("anon %d", code)
	}
	if code := call(login(p.ID)); code != 403 {
		t.Fatalf("player %d", code)
	}
	ac := login(u.ID)
	if code := call(ac); code != 200 {
		t.Fatalf("admin %d", code)
	}
	now = now.Add(SessionTTL)
	if code := call(ac); code != 401 {
		t.Fatalf("expired %d", code)
	}
}
