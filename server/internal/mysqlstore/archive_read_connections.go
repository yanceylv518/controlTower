package mysqlstore

import (
	"context"
	"controltower/server/internal/archivereader"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/go-sql-driver/mysql"
)

func (s Store) ArchiveSiteExists(ctx context.Context, site string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE deleted=0 AND COALESCE(NULLIF(site_id,''),id)=?`, site).Scan(&n)
	return n > 0, err
}

func (s Store) LoadArchiveConnection(ctx context.Context, site string) (archivereader.Connection, error) {
	var c archivereader.Connection
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT config_json,encrypted_password,version FROM archive_read_connections WHERE site_id=?`, site).Scan(&raw, &c.EncryptedPassword, &c.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return c, archivereader.ErrConnectionMissing
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	c.PasswordSet = c.EncryptedPassword != ""
	return c, nil
}
func (s Store) SaveArchiveConnection(ctx context.Context, site string, c archivereader.Connection, actor string) error {
	previous := c.Version
	c.Version++
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if previous == 0 {
		_, err = s.db.ExecContext(ctx, `INSERT INTO archive_read_connections VALUES(?,?,?,1,?,UTC_TIMESTAMP(6))`, site, string(raw), c.EncryptedPassword, actor)
		var e *mysql.MySQLError
		if errors.As(err, &e) && e.Number == 1062 {
			return archivereader.ErrConnectionConflict
		}
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE archive_read_connections SET config_json=?,encrypted_password=?,version=version+1,updated_by=?,updated_at=UTC_TIMESTAMP(6) WHERE site_id=? AND version=?`, string(raw), c.EncryptedPassword, actor, site, previous)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return archivereader.ErrConnectionConflict
	}
	return nil
}
