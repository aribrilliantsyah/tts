// Package auth hashes passwords and manages cookie sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"tts/internal/store"
)

const (
	CookieName = "tts_session"
	SessionTTL = 7 * 24 * time.Hour
)

// bcryptInput maps any password to ≤72 bytes: bcrypt rejects longer input, and
// passwords may be up to 200 characters. SHA-256 keeps all of them significant.
func bcryptInput(pw string) []byte {
	sum := sha256.Sum256([]byte(pw))
	b := make([]byte, base64.RawStdEncoding.EncodedLen(len(sum)))
	base64.RawStdEncoding.Encode(b, sum[:])
	return b
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword(bcryptInput(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), bcryptInput(pw)) == nil
}

// NewToken returns 32 random bytes, hex-encoded.
func NewToken() string {
	var b [32]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Sessions issues and resolves session cookies backed by the store.
type Sessions struct {
	Store *store.Store
	Now   func() time.Time
}

// Login creates a session for userID and sets the cookie.
func (s *Sessions) Login(w http.ResponseWriter, r *http.Request, userID int64) error {
	tok := NewToken()
	exp := s.Now().Add(SessionTTL)
	if err := s.Store.CreateSession(r.Context(), tok, userID, exp.UnixMilli()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", Expires: exp, MaxAge: int(SessionTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Logout deletes the request's session (if any) and clears the cookie.
func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.Store.DeleteSession(r.Context(), c.Value); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	return nil
}

// User returns the logged-in user, or ok=false when there is no valid session.
func (s *Sessions) User(r *http.Request) (store.User, bool, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.User{}, false, nil
	}
	u, err := s.Store.SessionUser(r.Context(), c.Value, s.Now().UnixMilli())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, false, nil
	}
	if err != nil {
		return store.User{}, false, err
	}
	return u, true, nil
}

type ctxKey struct{}

// FromContext returns the user stored by RequireUser/RequireAdmin.
func FromContext(ctx context.Context) store.User {
	u, _ := ctx.Value(ctxKey{}).(store.User)
	return u
}

// ErrorFunc writes an error response; the web layer supplies its JSON writer.
type ErrorFunc func(w http.ResponseWriter, status int, msg string)

// Require wraps h so it runs only for a logged-in user (and an admin when admin is true).
// The user is available via FromContext.
func (s *Sessions) Require(admin bool, fail ErrorFunc, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := s.User(r)
		switch {
		case err != nil:
			fail(w, http.StatusInternalServerError, "Kesalahan server")
		case !ok:
			fail(w, http.StatusUnauthorized, "Silakan masuk dulu")
		case admin && !u.IsAdmin():
			fail(w, http.StatusForbidden, "Khusus admin")
		default:
			h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, u)))
		}
	}
}
