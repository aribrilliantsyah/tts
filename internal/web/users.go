package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"unicode/utf8"

	"tts/internal/auth"
	"tts/internal/store"
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]{3,32}$`)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userBody struct {
	User *store.User `json:"user"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	if !usernameRE.MatchString(in.Username) {
		writeError(w, http.StatusBadRequest, "Username 3–32 karakter: huruf, angka, _ . -")
		return
	}
	if n := utf8.RuneCountInString(in.Password); n < 6 || n > 200 {
		writeError(w, http.StatusBadRequest, "Password 6–200 karakter")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		serverError(w, r, err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), in.Username, hash, store.RolePlayer, s.now().UnixMilli())
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "Username sudah dipakai")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := s.sessions.Login(w, r, u.ID); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userBody{&u})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	u, hash, err := s.store.UserByUsername(r.Context(), in.Username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		serverError(w, r, err)
		return
	}
	if err != nil || !auth.CheckPassword(hash, in.Password) {
		writeError(w, http.StatusUnauthorized, "Username atau password salah")
		return
	}
	if err := s.sessions.Login(w, r, u.ID); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, userBody{&u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Logout(w, r); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, found, err := s.sessions.User(r)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, userBody{nil})
		return
	}
	writeJSON(w, http.StatusOK, userBody{&u})
}

func (s *Server) setAvatar(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Avatar json.RawMessage `json:"avatar"`
	}
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	v, valid := store.ValidAvatar(in.Avatar)
	if !valid {
		writeError(w, http.StatusBadRequest, "Avatar tidak valid")
		return
	}
	if err := s.store.SetAvatar(r.Context(), auth.FromContext(r.Context()).ID, v); err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListUsers(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Users []store.UserRow `json:"users"`
	}{list})
}

func (s *Server) patchUser(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Pengguna tidak ditemukan")
	if !valid {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if !decode(w, r, defaultBodyLimit, &in) {
		return
	}
	if in.Role != store.RoleAdmin && in.Role != store.RolePlayer {
		writeError(w, http.StatusBadRequest, "Peran tidak valid")
		return
	}
	if id == auth.FromContext(r.Context()).ID && in.Role != store.RoleAdmin {
		writeError(w, http.StatusBadRequest, "Tidak bisa menurunkan peran sendiri")
		return
	}
	s.userResult(w, r, s.store.SetRole(r.Context(), id, in.Role))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, valid := pathID(w, r, "Pengguna tidak ditemukan")
	if !valid {
		return
	}
	if id == auth.FromContext(r.Context()).ID {
		writeError(w, http.StatusBadRequest, "Tidak bisa menghapus akun sendiri")
		return
	}
	s.userResult(w, r, s.store.DeleteUser(r.Context(), id))
}

func (s *Server) userResult(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "Pengguna tidak ditemukan")
	case err != nil:
		serverError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, ok)
	}
}
