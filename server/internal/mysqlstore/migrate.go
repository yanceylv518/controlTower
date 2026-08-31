package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

const mysqlDuplicateKeyNameError = 1061
const mysqlDuplicateColumnError = 1060

const migrationLockTimeoutSeconds = 1800
const migrationLockName = "control_tower_schema_migrations"

const createSchemaMigrationsSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(255) NOT NULL,
  applied_at DATETIME(6) NOT NULL,
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`

type migrationExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ApplySQL(ctx context.Context, db migrationExecutor, sqlText string) error {
	for _, statement := range splitSQLStatements(sqlText) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			// Duplicate-column/key errors are only expected from idempotent
			// re-runs of ALTER/CREATE INDEX statements. A CREATE TABLE must
			// never fail silently: swallowing its error leaves the table
			// missing and every later statement on it failing confusingly
			// (this exact failure shipped once; see the M1 stage report).
			if ignorableMigrationError(err) && !isCreateTableStatement(statement) {
				continue
			}
			return err
		}
	}
	return nil
}

func isCreateTableStatement(statement string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "CREATE TABLE")
}

func ApplyDir(ctx context.Context, db *sql.DB, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)

	// Use one dedicated connection because MySQL advisory locks are scoped to
	// the connection that acquired them. This prevents two Server replicas from
	// applying the same newly discovered migration concurrently.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var lockAcquired int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, ?)`, migrationLockName, migrationLockTimeoutSeconds).Scan(&lockAcquired); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if lockAcquired != 1 {
		return errors.New("acquire migration lock: lock not acquired")
	}
	defer func() {
		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelRelease()
		var released sql.NullInt64
		_ = conn.QueryRowContext(releaseCtx, `SELECT RELEASE_LOCK(?)`, migrationLockName).Scan(&released)
	}()

	if _, err = conn.ExecContext(ctx, createSchemaMigrationsSQL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	applied := make(map[string]struct{}, len(files))
	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}
	for rows.Next() {
		var version string
		if err = rows.Scan(&version); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = struct{}{}
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close applied migrations: %w", err)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("list applied migrations: %w", err)
	}

	for _, path := range files {
		version := filepath.Base(path)
		if _, ok := applied[version]; ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = ApplySQL(ctx, conn, string(data)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err = conn.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,UTC_TIMESTAMP(6))`, version); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

func splitSQLStatements(sqlText string) []string {
	parts := strings.Split(sqlText, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		statement := strings.TrimSpace(part)
		if statement == "" {
			continue
		}
		statements = append(statements, statement)
	}
	return statements
}

func ignorableMigrationError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == mysqlDuplicateKeyNameError || mysqlErr.Number == mysqlDuplicateColumnError
}
