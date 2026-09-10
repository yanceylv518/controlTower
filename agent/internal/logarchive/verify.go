package logarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// verifyRows reads only the just-written IDs from the target transaction.
// It streams comparison against the existing source batch, not a second source query.
func verifyRows(ctx context.Context, tx *sql.Tx, table string, columns []string, batch [][]any) error {
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
		return errors.New("archive verification target query failed; checkpoint unchanged")
	}
	defer rows.Close()
	for rows.Next() {
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if rows.Scan(dest...) != nil {
			return errors.New("archive verification target scan failed")
		}
		id := string(raw[idIndex])
		want, ok := expected[id]
		if !ok {
			return errors.New("archive verification unexpected or duplicate target id")
		}
		for i, got := range raw {
			if want[i] == nil && got == nil {
				continue
			}
			s, ok := want[i].(string)
			if !ok || got == nil || string(got) != s {
				// Never include values (which may contain secrets) in errors.
				return fmt.Errorf("archive verification field mismatch at column %d; checkpoint unchanged", i+1)
			}
		}
		delete(expected, id)
	}
	if rows.Err() != nil {
		return errors.New("archive verification target read failed")
	}
	if len(expected) > 0 {
		return errors.New("archive verification missing target rows; checkpoint unchanged")
	}
	return nil
}
