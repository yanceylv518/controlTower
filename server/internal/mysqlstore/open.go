package mysqlstore

import (
	"database/sql"
	"time"

	"github.com/go-sql-driver/mysql"
)

func Open(dsn string) (*sql.DB, error) {
	normalizedDSN, err := withDatabaseTimeoutDefaults(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", normalizedDSN)
	if err != nil {
		return nil, err
	}
	return db, nil
}

func withDatabaseTimeoutDefaults(dsn string) (string, error) {
	normalized, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", err
	}
	// Background runners have no request context to cancel them. Driver-level
	// deadlines prevent one broken CT database connection from blocking the
	// single tuning loop forever. Explicit DSN values always take precedence.
	if normalized.Timeout == 0 {
		normalized.Timeout = 5 * time.Second
	}
	if normalized.ReadTimeout == 0 {
		normalized.ReadTimeout = 30 * time.Second
	}
	if normalized.WriteTimeout == 0 {
		normalized.WriteTimeout = 30 * time.Second
	}
	return normalized.FormatDSN(), nil
}
