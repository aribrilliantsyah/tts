package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	RoleAdmin  = "admin"
	RolePlayer = "player"
)

type User struct {
	ID       int64           `json:"id"`
	Username string          `json:"username"`
	Role     string          `json:"role"`
	Avatar   json.RawMessage `json:"avatar"`
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// UserRow is a user as listed in the admin users page.
type UserRow struct {
	User
	CreatedAt int64 `json:"created_at"`
	Solved    int   `json:"solved"`
}

// CreateUser inserts a user; ErrConflict when the username is taken.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string, now int64) (User, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users(username, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		username, passwordHash, role, now)
	if isUnique(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("user id: %w", err)
	}
	return User{ID: id, Username: username, Role: role, Avatar: json.RawMessage(`{}`)}, nil
}

// UserByUsername returns the user and its password hash (case-insensitive lookup).
func (s *Store) UserByUsername(ctx context.Context, username string) (User, string, error) {
	var u User
	var hash, avatar string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, role, avatar, password_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &u.Role, &avatar, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, "", ErrNotFound
	}
	if err != nil {
		return User{}, "", fmt.Errorf("user by name: %w", err)
	}
	u.Avatar = json.RawMessage(avatar)
	return u, hash, nil
}

func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin'`).Scan(&n); err != nil {
		return false, fmt.Errorf("count admins: %w", err)
	}
	return n > 0, nil
}

func (s *Store) SetAvatar(ctx context.Context, userID int64, avatar json.RawMessage) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET avatar = ? WHERE id = ?`, string(avatar), userID); err != nil {
		return fmt.Errorf("set avatar: %w", err)
	}
	return nil
}

func (s *Store) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, u.role, u.avatar, u.created_at,
		       (SELECT count(*) FROM attempts a WHERE a.user_id = u.id AND a.completed_at IS NOT NULL)
		FROM users u ORDER BY u.created_at, u.id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	out := []UserRow{}
	for rows.Next() {
		var r UserRow
		var avatar string
		if err := rows.Scan(&r.ID, &r.Username, &r.Role, &avatar, &r.CreatedAt, &r.Solved); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		r.Avatar = json.RawMessage(avatar)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetRole(ctx context.Context, userID int64, role string) error {
	return s.execOne(ctx, "set role", `UPDATE users SET role = ? WHERE id = ?`, role, userID)
}

func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	return s.execOne(ctx, "delete user", `DELETE FROM users WHERE id = ?`, userID)
}

// execOne runs a statement that must affect exactly one row, else ErrNotFound.
func (s *Store) execOne(ctx context.Context, what, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateSession(ctx context.Context, token string, userID, expiresAt int64) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionUser returns the user owning an unexpired session token.
func (s *Store) SessionUser(ctx context.Context, token string, now int64) (User, error) {
	var u User
	var avatar string
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.role, u.avatar FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, token, now).Scan(&u.ID, &u.Username, &u.Role, &avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("session user: %w", err)
	}
	u.Avatar = json.RawMessage(avatar)
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
