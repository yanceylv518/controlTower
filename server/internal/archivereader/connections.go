package archivereader

import (
	"context"
	"controltower/server/internal/secrets"
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"net"
	"strconv"
	"strings"
	"time"
)

var ErrConnectionConflict = errors.New("archive_connection_conflict")
var ErrConnectionMissing = errors.New("archive_connection_not_configured")

type Connection struct {
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Database          string `json:"database"`
	Username          string `json:"username"`
	TLS               bool   `json:"tls"`
	SourceHash        string `json:"source_hash"`
	Version           uint64 `json:"version"`
	TestedAt          string `json:"tested_at"`
	PasswordSet       bool   `json:"password_set"`
	EncryptedPassword string `json:"-"`
}
type ConnectionStore interface {
	ArchiveSiteExists(context.Context, string) (bool, error)
	LoadArchiveConnection(context.Context, string) (Connection, error)
	SaveArchiveConnection(context.Context, string, Connection, string) error
}

func (c Connection) Validate() error {
	if c.Host == "" || len(c.Host) > 253 || strings.ContainsAny(c.Host, " /\\\r\n\t") || c.Port < 1 || c.Port > 65535 || c.Database == "" || len(c.Database) > 64 || strings.ContainsAny(c.Database, "/\\\x00") || c.Username == "" || len(c.Username) > 128 {
		return ErrQuery
	}
	return nil
}
func (r Reader) connectionConfig(c Connection) (*mysql.Config, error) {
	if c.Validate() != nil {
		return nil, ErrQuery
	}
	password, err := secrets.Decrypt(r.SecretKey, c.EncryptedPassword)
	if err != nil {
		return nil, ErrUnavailable
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.User = c.Username
	cfg.Passwd = password
	cfg.DBName = c.Database
	if c.TLS {
		cfg.TLSConfig = "true"
	}
	return cfg, nil
}
func (r Reader) openJob(ctx context.Context, site string) (*sql.DB, string, error) {
	if r.Connections != nil {
		c, err := r.Connections.LoadArchiveConnection(ctx, site)
		if err == nil {
			if !hash64.MatchString(c.SourceHash) {
				return nil, "", ErrIdentity
			}
			cfg, err := r.connectionConfig(c)
			if err != nil {
				return nil, "", err
			}
			db, err := openConfig(ctx, cfg, permittedJobGrant)
			return db, c.SourceHash, err
		}
		if !errors.Is(err, ErrConnectionMissing) {
			return nil, "", ErrUnavailable
		}
	}
	return r.openJobFile(ctx, site)
}

// ProbeConnection performs only bounded SELECTs. It never writes to the target.
func (r Reader) ProbeConnection(ctx context.Context, c Connection) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cfg, err := r.connectionConfig(c)
	if err != nil {
		return "", err
	}
	db, err := openConfig(ctx, cfg, permittedJobGrant)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var hash string
	var version int
	if db.QueryRowContext(ctx, `SELECT source_hash,schema_version FROM log_archive_meta WHERE singleton_id=1`).Scan(&hash, &version) != nil {
		return "", ErrUnavailable
	}
	if version != 1 || !hash64.MatchString(hash) || (c.SourceHash != "" && c.SourceHash != hash) {
		return "", ErrIdentity
	}
	for _, q := range []string{
		`SELECT log_date,state,version_id,revision,raw_rows,step,error_code FROM log_archive_days LIMIT 0`,
		`SELECT version_id,log_date,revision FROM log_archive_day_versions LIMIT 0`,
		`SELECT version_id,group_hash,log_date,dimensions,amounts FROM log_archive_daily_stats LIMIT 0`,
	} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return "", ErrUnavailable
		}
		rows.Close()
	}
	return hash, nil
}
