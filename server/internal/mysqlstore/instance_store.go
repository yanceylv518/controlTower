package mysqlstore

import (
	"controltower/server/internal/storage"
	"database/sql"
	"errors"
	"time"
)

func (s Store) ListInstances() ([]storage.Instance, error) {
	r, e := s.db.Query("SELECT id,name,env,region,base_url,enabled,created_at,updated_at FROM instances ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer r.Close()
	var o []storage.Instance
	for r.Next() {
		var v storage.Instance
		if e = r.Scan(&v.ID, &v.Name, &v.Env, &v.Region, &v.BaseURL, &v.Enabled, &v.CreatedAt, &v.UpdatedAt); e != nil {
			return nil, e
		}
		o = append(o, v)
	}
	return o, r.Err()
}
func (s Store) InstanceByID(id string) (storage.Instance, bool, error) {
	var v storage.Instance
	e := s.db.QueryRow("SELECT id,name,env,region,base_url,enabled,created_at,updated_at FROM instances WHERE id=?", id).Scan(&v.ID, &v.Name, &v.Env, &v.Region, &v.BaseURL, &v.Enabled, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, e == nil, e
}
func (s Store) CreateInstance(v storage.Instance) error {
	_, e := s.db.Exec("INSERT INTO instances(id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)", v.ID, v.Name, v.Env, v.Region, v.BaseURL, v.Enabled, v.CreatedAt, v.UpdatedAt)
	return e
}
func (s Store) UpdateInstance(id, n string, en bool, now time.Time) error {
	_, e := s.db.Exec("UPDATE instances SET name=?,enabled=?,updated_at=? WHERE id=?", n, en, now, id)
	return e
}
func (s Store) CreateInstanceToken(v storage.InstanceToken) error {
	_, e := s.db.Exec("INSERT INTO instance_tokens(instance_id,token_hash,created_at,expires_at) VALUES(?,?,?,?)", v.InstanceID, v.TokenHash, v.CreatedAt, v.ExpiresAt)
	return e
}
func (s Store) InstanceIDByTokenHash(h string, n time.Time) (string, bool, error) {
	var id string
	e := s.db.QueryRow(`SELECT t.instance_id FROM instance_tokens t JOIN instances i ON i.id=t.instance_id WHERE t.token_hash=? AND i.enabled=1 AND (t.expires_at IS NULL OR t.expires_at>?)`, h, n).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, e == nil, e
}
func (s Store) ExpireInstanceTokens(id string, g, n time.Time) error {
	_, e := s.db.Exec("UPDATE instance_tokens SET expires_at=? WHERE instance_id=? AND expires_at IS NULL", g, id)
	return e
}
func (s Store) DeleteExpiredInstanceTokens(n time.Time) (int, error) {
	r, e := s.db.Exec("DELETE FROM instance_tokens WHERE expires_at IS NOT NULL AND expires_at<=?", n)
	if e != nil {
		return 0, e
	}
	x, e := r.RowsAffected()
	return int(x), e
}
