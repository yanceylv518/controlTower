package logarchive

import (
	"context"
	"regexp"
	"strings"
)

var foundationColumnPattern = regexp.MustCompile(`^([a-z][a-z0-9_]*) ([A-Z]+(?:\([0-9,]+\))?(?: UNSIGNED)?)(?: |,)`)
var foundationIntegerWidth = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint)\([0-9]+\)`)
var foundationIndexPattern = regexp.MustCompile(`(?m)^\s*(PRIMARY KEY|UNIQUE KEY [a-z0-9_]+|KEY [a-z0-9_]+) \(([a-z0-9_, ]+)\)`)

type foundationColumn struct{ name, kind, nullable, charset, collation, extra, defaultValue string }

// Check the resulting structure as well as the migration receipts. An existing
// table with a conflicting definition must not be adopted by IF NOT EXISTS.
func inspectFoundationStructure(ctx context.Context, db foundationQuery) error {
	migrations, err := loadFoundationMigrations()
	if err != nil {
		return err
	}
	type tableDefinition struct {
		table, statement string
		columns          []foundationColumn
	}
	var definitions []tableDefinition
	for _, migration := range migrations {
		if table, column, ok := foundationAddedColumn(migration.sql); ok {
			found := false
			for i := range definitions {
				if definitions[i].table == table {
					definitions[i].columns = append(definitions[i].columns, column)
					found = true
					break
				}
			}
			if !found {
				return ErrFoundationSchema
			}
			continue
		}
		table, columns := foundationExpectedColumns(migration.sql)
		definitions = append(definitions, tableDefinition{table, migration.sql, columns})
	}
	for _, definition := range definitions {
		table, columns := definition.table, definition.columns
		if table == "" || len(columns) == 0 {
			return ErrFoundationSchema
		}
		var engine string
		if db.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&engine) != nil || !strings.EqualFold(engine, "InnoDB") {
			return ErrFoundationSchema
		}
		rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COALESCE(CHARACTER_SET_NAME,''),COALESCE(COLLATION_NAME,''),EXTRA,COALESCE(COLUMN_DEFAULT,'') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION`, table)
		if err != nil {
			return ErrFoundationSchema
		}
		index := 0
		for rows.Next() {
			var got foundationColumn
			if rows.Scan(&got.name, &got.kind, &got.nullable, &got.charset, &got.collation, &got.extra, &got.defaultValue) != nil {
				rows.Close()
				return ErrFoundationSchema
			}
			got.kind = foundationIntegerWidth.ReplaceAllString(got.kind, "$1")
			if index >= len(columns) || got != columns[index] {
				rows.Close()
				return ErrFoundationSchema
			}
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || index != len(columns) {
			return ErrFoundationSchema
		}
		if err := inspectFoundationIndexes(ctx, db, table, definition.statement); err != nil {
			return err
		}
	}
	// This constraint is part of the foundation's day/version binding contract.
	rows, err := db.QueryContext(ctx, `SELECT COLUMN_NAME,REFERENCED_TABLE_NAME,REFERENCED_COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE CONSTRAINT_SCHEMA=DATABASE() AND REFERENCED_TABLE_SCHEMA=DATABASE() AND TABLE_NAME='archive_days' AND CONSTRAINT_NAME='fk_archive_current_day_version' ORDER BY ORDINAL_POSITION`)
	if err != nil {
		return ErrFoundationSchema
	}
	defer rows.Close()
	expected := [][3]string{{"log_date", "archive_day_versions", "log_date"}, {"current_version_id", "archive_day_versions", "day_version_id"}}
	index := 0
	for rows.Next() {
		var got [3]string
		if rows.Scan(&got[0], &got[1], &got[2]) != nil || index >= len(expected) || got != expected[index] {
			return ErrFoundationSchema
		}
		index++
	}
	if rows.Err() != nil || index != len(expected) {
		return ErrFoundationSchema
	}
	rows.Close()
	var onDelete, onUpdate string
	if err := db.QueryRowContext(ctx, `SELECT DELETE_RULE,UPDATE_RULE FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE() AND TABLE_NAME='archive_days' AND CONSTRAINT_NAME='fk_archive_current_day_version'`).Scan(&onDelete, &onUpdate); err != nil || onDelete != "RESTRICT" || onUpdate != "RESTRICT" {
		return ErrFoundationSchema
	}
	return nil
}

func foundationAddedColumn(statement string) (string, foundationColumn, bool) {
	// Keep supported resumable transformations deliberately narrow. Future
	// migrations must supply their own validation for different DDL shapes.
	parts := strings.Fields(statement)
	if len(parts) != 8 || parts[0] != "ALTER" || parts[1] != "TABLE" || parts[3] != "ADD" || parts[4] != "COLUMN" || parts[7] != "NULL" {
		return "", foundationColumn{}, false
	}
	ledger := parts[2] == "archive_log_state" && ((parts[5] == "raw_row_hash" && parts[6] == "BINARY(32)") || (parts[5] == "last_batch_id" && parts[6] == "BINARY(16)"))
	cohort := parts[2] == "archive_batch_receipts" && parts[5] == "cohort_dates_json" && parts[6] == "JSON"
	if !ledger && !cohort {
		return "", foundationColumn{}, false
	}
	return parts[2], foundationColumn{name: parts[5], kind: strings.ToLower(parts[6]), nullable: "YES"}, true
}

func inspectFoundationIndexes(ctx context.Context, db foundationQuery, table, statement string) error {
	type definition struct {
		nonUnique int
		columns   string
	}
	expected := make(map[string]definition)
	for _, match := range foundationIndexPattern.FindAllStringSubmatch(statement, -1) {
		name, nonUnique := "PRIMARY", 0
		if match[1] != "PRIMARY KEY" {
			parts := strings.Fields(match[1])
			name = parts[len(parts)-1]
			if parts[0] != "UNIQUE" {
				nonUnique = 1
			}
		}
		expected[name] = definition{nonUnique, strings.ReplaceAll(match[2], " ", "")}
	}
	rows, err := db.QueryContext(ctx, `SELECT INDEX_NAME,NON_UNIQUE,COLUMN_NAME,COALESCE(SUB_PART,0) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY INDEX_NAME,SEQ_IN_INDEX`, table)
	if err != nil {
		return ErrFoundationSchema
	}
	defer rows.Close()
	actual := make(map[string]definition)
	for rows.Next() {
		var name, column string
		var nonUnique, prefixLength int
		if rows.Scan(&name, &nonUnique, &column, &prefixLength) != nil {
			return ErrFoundationSchema
		}
		if _, required := expected[name]; required && prefixLength != 0 {
			return ErrFoundationSchema
		}
		prior := actual[name]
		if prior.columns != "" {
			prior.columns += ","
		}
		prior.columns += column
		prior.nonUnique = nonUnique
		actual[name] = prior
	}
	if rows.Err() != nil {
		return ErrFoundationSchema
	}
	for name, want := range expected {
		if actual[name] != want {
			return ErrFoundationSchema
		}
	}
	// Extra nonunique indexes do not change correctness; an extra unique index
	// may merge or reject unrelated identities and is never silently accepted.
	for name, got := range actual {
		if _, ok := expected[name]; !ok && got.nonUnique == 0 {
			return ErrFoundationSchema
		}
	}
	return nil
}

func foundationExpectedColumns(statement string) (string, []foundationColumn) {
	lines := strings.Split(statement, "\n")
	first := strings.Fields(lines[0])
	if len(first) < 6 {
		return "", nil
	}
	table := first[5]
	var columns []foundationColumn
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		match := foundationColumnPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		column := foundationColumn{name: match[1], kind: strings.ToLower(match[2]), nullable: "YES"}
		if strings.Contains(line, "NOT NULL") {
			column.nullable = "NO"
		}
		if strings.HasPrefix(column.kind, "varchar") || strings.HasPrefix(column.kind, "char(") {
			column.charset, column.collation = "utf8mb4", "utf8mb4_bin"
			if strings.Contains(line, "CHARACTER SET ascii") {
				column.charset, column.collation = "ascii", "ascii_bin"
			}
		}
		if strings.Contains(line, "DEFAULT 0") {
			column.defaultValue = "0"
		}
		columns = append(columns, column)
	}
	return table, columns
}
