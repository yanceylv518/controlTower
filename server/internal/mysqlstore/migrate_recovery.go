package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
)

type recoveryColumn struct {
	name, kind, collation string
	defaultZero           bool
}

// Published SQL stays unchanged. Only these known single-statement migrations
// can recover a committed CREATE followed by a failed migration-ledger write.
func applyMigrationVersion(ctx context.Context, conn *sql.Conn, version, sqlText string) error {
	err := ApplySQL(ctx, conn, sqlText)
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1050 {
		return err
	}
	var table, primary string
	var columns []recoveryColumn
	switch version {
	case "123_billing_excluded_output_stats.sql":
		table, primary = "billing_excluded_output_stats", "job_id,model_name"
		columns = []recoveryColumn{{"job_id", "varchar(40)", "utf8mb4_unicode_ci", false}, {"model_name", "varchar(255)", "utf8mb4_bin", false}, {"request_count", "bigint", "", true}, {"total_amount", "decimal(36,12)", "", true}}
	case "133_billing_tier_statistics.sql":
		table, primary = "billing_tier_statistics", "job_id,bill_day,user_id"
		columns = []recoveryColumn{{"job_id", "varchar(40)", "utf8mb4_unicode_ci", false}, {"bill_day", "date", "", false}, {"user_id", "bigint", "", false}, {"statistics_json", "mediumtext", "utf8mb4_unicode_ci", false}}
	default:
		return err
	}
	if len(splitSQLStatements(sqlText)) != 1 || !strings.Contains(sqlText, "CREATE TABLE "+table+" (") {
		return err
	}
	if checkErr := validateRecoveredBillingTable(ctx, conn, table, primary, columns); checkErr != nil {
		return fmt.Errorf("cannot recover migration %s: %w", version, checkErr)
	}
	return nil
}

func validateRecoveredBillingTable(ctx context.Context, conn *sql.Conn, table, primary string, columns []recoveryColumn) error {
	var engine string
	if err := conn.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&engine); err != nil {
		return err
	}
	if engine != "InnoDB" {
		return fmt.Errorf("%s requires InnoDB", table)
	}
	for _, column := range columns {
		var kind, nullable, collation string
		var defaultValue sql.NullString
		err := conn.QueryRowContext(ctx, `SELECT COLUMN_TYPE,IS_NULLABLE,COALESCE(COLLATION_NAME,''),COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, column.name).Scan(&kind, &nullable, &collation, &defaultValue)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", table, column.name, err)
		}
		kind = strings.ReplaceAll(strings.ToLower(kind), "bigint(20)", "bigint")
		if kind != column.kind || nullable != "NO" || collation != column.collation {
			return fmt.Errorf("incompatible column %s.%s", table, column.name)
		}
		if column.defaultZero && (!defaultValue.Valid || strings.Trim(defaultValue.String, "0.") != "") {
			return fmt.Errorf("incompatible default %s.%s", table, column.name)
		}
	}
	var actualPrimary sql.NullString
	if err := conn.QueryRowContext(ctx, `SELECT GROUP_CONCAT(COLUMN_NAME ORDER BY ORDINAL_POSITION) FROM information_schema.KEY_COLUMN_USAGE WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY'`, table).Scan(&actualPrimary); err != nil {
		return err
	}
	if actualPrimary.String != primary {
		return fmt.Errorf("incompatible primary key on %s", table)
	}
	var foreignKeys int
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.KEY_COLUMN_USAGE k JOIN information_schema.REFERENTIAL_CONSTRAINTS r ON r.CONSTRAINT_SCHEMA=k.CONSTRAINT_SCHEMA AND r.TABLE_NAME=k.TABLE_NAME AND r.CONSTRAINT_NAME=k.CONSTRAINT_NAME WHERE k.TABLE_SCHEMA=DATABASE() AND k.TABLE_NAME=? AND k.COLUMN_NAME='job_id' AND k.REFERENCED_TABLE_SCHEMA=DATABASE() AND k.REFERENCED_TABLE_NAME='billing_jobs' AND k.REFERENCED_COLUMN_NAME='id' AND r.DELETE_RULE='CASCADE'`, table).Scan(&foreignKeys)
	if err != nil {
		return err
	}
	if foreignKeys != 1 {
		return fmt.Errorf("incompatible job foreign key on %s", table)
	}
	return nil
}
