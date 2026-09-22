package logarchive

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
)

// verifyRows reads only the just-written IDs from the target transaction.
// It streams comparison against the existing source batch, not a second source query.
func verifyRows(ctx context.Context, tx *sql.Tx, table string, columns []string, batch [][]any) error {
	reportOperation(ctx, "compare_imported_logs", "archive", table)
	idIndex := -1
	names := make([]string, len(columns))
	for i, name := range columns {
		names[i] = quote(name)
		if name == "id" {
			idIndex = i
		}
	}
	if idIndex < 0 {
		return errors.New("archive verification requires id")
	}
	expected := make(map[string][]any, len(batch))
	ids := make([]any, 0, len(batch))
	for _, row := range batch {
		id, ok := row[idIndex].(string)
		if !ok {
			return errors.New("archive verification invalid source id")
		}
		if _, exists := expected[id]; exists {
			return errors.New("archive verification duplicate source id")
		}
		expected[id] = row
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	q := "SELECT " + strings.Join(names, ",") + " FROM " + quote(table) + " WHERE `id` IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	rows, err := tx.QueryContext(ctx, q, ids...)
	if err != nil {
		return &scanError{code: "verification_read_failed", cause: err}
	}
	defer rows.Close()
	for rows.Next() {
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return &scanError{code: "verification_read_failed", cause: err}
		}
		id := string(raw[idIndex])
		want, ok := expected[id]
		if !ok {
			return &scanError{code: "verification_unexpected_row"}
		}
		for i, got := range raw {
			if want[i] == nil && got == nil {
				continue
			}
			s, ok := want[i].(string)
			if !ok || got == nil || string(got) != s {
				// Never include values (which may contain secrets) in errors.
				rowID, _ := strconv.ParseInt(id, 10, 64)
				return &scanError{code: "verification_field_mismatch", sourceID: rowID}
			}
		}
		delete(expected, id)
	}
	if rows.Err() != nil {
		return &scanError{code: "verification_read_failed", cause: rows.Err()}
	}
	if len(expected) > 0 {
		return &scanError{code: "verification_missing_rows"}
	}
	return nil
}
