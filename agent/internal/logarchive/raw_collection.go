package logarchive

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// The collection ledger describes physical rows, not their statistics. Missing
// entries are adopted from actual monthly rows under the dataset write fence;
// no existing raw row is counted as a newly inserted statistic.
func (w *Worker) writerRawStates(ctx context.Context, tx *sql.Tx, batch writerBatch, ids []any) (map[int64]writerState, error) {
	states, err := writerReadLedger(ctx, tx, ids, true, "archive_raw_state")
	if err != nil {
		return nil, err
	}
	var missing []any
	for _, id := range ids {
		if _, ok := states[id.(int64)]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return states, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^logs_([0-9]{6}|undated)$' ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var adopted [][]any
	for _, table := range tables {
		var engine string
		if err = tx.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&engine); err != nil {
			return nil, err
		}
		if !strings.EqualFold(engine, "InnoDB") {
			return nil, ErrFoundationSchema
		}
		rows, err = tx.QueryContext(ctx, "SELECT * FROM "+quote(table)+" WHERE id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(missing)), ",")+") ORDER BY id", missing...)
		if err != nil {
			return nil, err
		}
		var page writerBatch
		budget := w.readBudget()
		budget.MaxRows = len(missing) + 1
		if _, _, err = readWriterPage(ctx, rows, &page, budget, math.MaxInt64, 0, time.Now()); err != nil {
			return nil, err
		}
		if page.Limited {
			return nil, &scanError{code: "row_too_large"}
		}
		for _, row := range page.Rows {
			id, c, e := writerContribution(page.Columns, row)
			if e != nil {
				return nil, e
			}
			if "logs_"+c.month() != table {
				return nil, &scanError{code: "repair_unscoped_target", sourceID: id}
			}
			if _, exists := states[id]; exists {
				return nil, ErrWriterCheckpoint
			}
			hash, e := writerRawHash(page.Columns, row)
			if e != nil {
				return nil, e
			}
			states[id] = writerState{contribution: c, hash: hash[:]}
			raw, _ := json.Marshal(c)
			adopted = append(adopted, []any{id, string(raw), hash[:], nil})
		}
	}
	if err = insertRows(ctx, tx, "archive_raw_state", []string{"id", "contribution", "raw_row_hash", "last_batch_id"}, adopted); err != nil {
		return nil, err
	}
	return states, nil
}
