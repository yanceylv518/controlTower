package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
)

const mysqlDuplicateKeyNameError = 1061
const mysqlDuplicateColumnError = 1060

func ApplySQL(ctx context.Context, db *sql.DB, sqlText string) error {
	for _, statement := range splitSQLStatements(sqlText) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			if ignorableMigrationError(err) {
				continue
			}
			return err
		}
	}
	return nil
}

func ApplyDir(ctx context.Context, db *sql.DB, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = ApplySQL(ctx, db, string(data)); err != nil {
			return err
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
