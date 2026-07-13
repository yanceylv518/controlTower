package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"database/sql"
	"errors"
	"time"
)

func (s Store) UserByUsername(name string) (storage.User, bool, error) {
	var u storage.User
	err := s.db.QueryRowContext(context.Background(), "SELECT id,username,password_hash,role,created_at,updated_at FROM users WHERE username=?", name).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, false, nil
		}
		return u, false, err
	}
	return u, true, nil
}
func (s Store) UserByID(id int64) (storage.User, bool, error) {
	var u storage.User
	err := s.db.QueryRow("SELECT id,username,password_hash,role,created_at,updated_at FROM users WHERE id=?", id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	return u, true, nil
}
func (s Store) CreateUser(u storage.User) error {
	r, e := s.db.Exec("INSERT INTO users(username,password_hash,role,created_at,updated_at) VALUES(?,?,?,?,?)", u.Username, u.PasswordHash, u.Role, u.CreatedAt, u.UpdatedAt)
	if e == nil && u.ID == 0 {
		_, _ = r.LastInsertId()
	}
	return e
}
func (s Store) UpdateUserPassword(id int64, h string, now time.Time) error {
	_, e := s.db.Exec("UPDATE users SET password_hash=?,updated_at=? WHERE id=?", h, now, id)
	return e
}
func (s Store) CountUsers() (int, error) {
	var n int
	e := s.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, e
}
func (s Store) CreateSession(v storage.Session) error {
	_, e := s.db.Exec("INSERT INTO sessions(id,user_id,expires_at,created_at) VALUES(?,?,?,?)", v.ID, v.UserID, v.ExpiresAt, v.CreatedAt)
	return e
}
func (s Store) SessionByID(id string) (storage.Session, bool, error) {
	var v storage.Session
	e := s.db.QueryRow("SELECT id,user_id,expires_at,created_at FROM sessions WHERE id=?", id).Scan(&v.ID, &v.UserID, &v.ExpiresAt, &v.CreatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return v, false, e
	}
	return v, true, nil
}
func (s Store) DeleteSession(id string) error {
	_, e := s.db.Exec("DELETE FROM sessions WHERE id=?", id)
	return e
}
func (s Store) DeleteExpiredSessions(now time.Time) (int, error) {
	r, e := s.db.Exec("DELETE FROM sessions WHERE expires_at<=?", now)
	if e != nil {
		return 0, e
	}
	n, e := r.RowsAffected()
	return int(n), e
}
