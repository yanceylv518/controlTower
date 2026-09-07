package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s Store) UserByUsername(name string) (storage.User, bool, error) {
	var u storage.User
	err := scanUser(s.db.QueryRowContext(context.Background(), "SELECT id,username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions FROM users WHERE username=?", name), &u)
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
	err := scanUser(s.db.QueryRow("SELECT id,username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions FROM users WHERE id=?", id), &u)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	return u, true, nil
}
func (s Store) CreateUser(u storage.User) error {
	scope, _ := json.Marshal(u.ScopeUserIDs)
	permissions, _ := json.Marshal(u.Permissions)
	r, e := s.db.Exec("INSERT INTO users(username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions) VALUES(?,?,?,?,?,?,?,?,?,?)", u.Username, u.PasswordHash, u.Role, u.ScopeSite, scope, u.Enabled, u.CreatedAt, u.UpdatedAt, u.DisplayName, permissions)
	if e == nil && u.ID == 0 {
		_, _ = r.LastInsertId()
	}
	return e
}

type rowScanner interface{ Scan(...any) error }

func scanUser(row rowScanner, u *storage.User) error {
	var scope, permissions []byte
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.ScopeSite, &scope, &u.Enabled, &u.CreatedAt, &u.UpdatedAt, &u.DisplayName, &permissions); err != nil {
		return err
	}
	if len(scope) > 0 {
		_ = json.Unmarshal(scope, &u.ScopeUserIDs)
	}
	if len(permissions) > 0 {
		if err := json.Unmarshal(permissions, &u.Permissions); err != nil {
			return err
		}
	}
	return nil
}
func (s Store) ListUsers() ([]storage.User, error) {
	rows, err := s.db.Query("SELECT id,username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []storage.User{}
	for rows.Next() {
		var u storage.User
		if err = scanUser(rows, &u); err != nil {
			return nil, err
		}
		items = append(items, u)
	}
	return items, rows.Err()
}
func (s Store) UpdateUser(u storage.User) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock all administrator rows in a stable order so concurrent changes cannot remove the last full admin.
	rows, err := tx.Query("SELECT id,username,password_hash,role,scope_site,scope_user_ids,enabled,created_at,updated_at,display_name,permissions FROM users WHERE role='admin' ORDER BY id FOR UPDATE")
	if err != nil {
		return err
	}
	fullCount, replacingFull := 0, false
	for rows.Next() {
		var item storage.User
		if err = scanUser(rows, &item); err != nil {
			rows.Close()
			return err
		}
		if item.Enabled && storage.IsFullAdmin(item) {
			fullCount++
			if item.ID == u.ID {
				replacingFull = true
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if replacingFull && fullCount == 1 && (!u.Enabled || !storage.IsFullAdmin(u)) {
		return errors.New("last_full_admin")
	}
	scope, _ := json.Marshal(u.ScopeUserIDs)
	permissions, _ := json.Marshal(u.Permissions)
	_, err = tx.Exec("UPDATE users SET role=?,scope_site=?,scope_user_ids=?,enabled=?,updated_at=?,display_name=?,permissions=? WHERE id=?", u.Role, u.ScopeSite, scope, u.Enabled, u.UpdatedAt, u.DisplayName, permissions, u.ID)
	if err != nil {
		return err
	}
	// Disabled accounts must not regain their old sessions when re-enabled.
	if !u.Enabled {
		if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", u.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s Store) ResetUserPassword(id int64, h string, now time.Time) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE users SET password_hash=?,updated_at=? WHERE id=?", h, now, id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) UpdateUserPassword(id int64, h string, now time.Time) error {
	return s.ResetUserPassword(id, h, now)
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
