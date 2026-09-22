package logarchive

import (
	"bytes"
	"context"
	af "controltower/internal/archivecontract"
	ap "controltower/internal/archivepipeline"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"time"
)

// Idempotent per-row deltas let old rc129 imports be adopted without resetting
// their counters. This path never modifies original logs or the source cursor.
func organizeContributions(ctx context.Context, tx *sql.Tx, current map[int64]writerState) error {
	ids := make([]any, 0, len(current))
	for id := range current {
		ids = append(ids, id)
	}
	old, err := writerReadStates(ctx, tx, ids, true)
	if err != nil {
		return err
	}
	daily, monthly := deltas{}, deltas{}
	var ledger [][]any
	for id, next := range current {
		if prev, ok := old[id]; ok {
			left, _ := json.Marshal(prev.contribution)
			right, _ := json.Marshal(next.contribution)
			if bytes.Equal(prev.hash, next.hash) && bytes.Equal(left, right) {
				continue
			}
			daily.add(prev.Day, prev.contribution, -1)
			monthly.add(prev.month(), prev.contribution, -1)
		}
		daily.add(next.Day, next.contribution, 1)
		monthly.add(next.month(), next.contribution, 1)
		raw, _ := json.Marshal(next.contribution)
		ledger = append(ledger, []any{id, string(raw), next.hash, nil})
	}
	if err = insertRows(ctx, tx, "archive_log_state", []string{"id", "contribution", "raw_row_hash", "last_batch_id"}, ledger); err != nil {
		return err
	}
	if err = writeDeltas(ctx, tx, "log_daily_stats", daily); err != nil {
		return err
	}
	return writeDeltas(ctx, tx, "log_monthly_stats", monthly)
}

func (w *Worker) organizePipelinePage(ctx context.Context, g af.WriterGrant, work ap.Work) error {
	table := "logs_" + strings.ReplaceAll(work.Date[:7], "-", "")
	var exists int
	if err := w.target.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, table).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		if err := ensureMonthlyTables(ctx, w.target, []string{strings.TrimPrefix(table, "logs_")}); err != nil {
			return err
		}
	}
	return w.pipelineTransaction(ctx, g, func(tx *sql.Tx, p *pipelineCheckpoint) error {
		active, ok := p.State.Active[ap.Organization]
		if !ok || active != work {
			return ap.ErrConflict
		}
		d := p.State.Days[work.Date]
		if d.Revision != work.Revision {
			delete(p.State.Active, ap.Organization)
			p.AfterID = 0
			p.AfterCreated = 0
			return nil
		}
		from, to, err := af.DateBounds(work.Date)
		if err != nil {
			return err
		}
		table := "logs_" + strings.ReplaceAll(work.Date[:7], "-", "")
		reportOperation(ctx, "read_existing_logs", "archive", table)
		var index string
		if err = tx.QueryRowContext(ctx, `SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='created_at' AND SEQ_IN_INDEX=1 AND SUB_PART IS NULL LIMIT 1`, table).Scan(&index); err != nil {
			return &scanError{code: "archive_count_index_missing", cause: err}
		}
		rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quote(table)+" FORCE INDEX ("+quote(index)+") WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?", from, to, p.AfterCreated, p.AfterCreated, p.AfterID, w.readBudget().MaxRows+1)
		if err != nil {
			return err
		}
		batch := writerBatch{Scan: &af.BackfillTask{}, AfterID: p.AfterID, AfterCreated: p.AfterCreated}
		_, exhausted, err := readWriterPage(ctx, rows, &batch, w.readBudget(), math.MaxInt64, 0, time.Now())
		if err != nil {
			return err
		}
		current := map[int64]writerState{}
		for _, row := range batch.Rows {
			id, c, e := writerContribution(batch.Columns, row)
			if e != nil {
				return e
			}
			if c.Blocking {
				return &scanError{code: "invalid_source_row", sourceID: id}
			}
			hash, e := writerRawHash(batch.Columns, row)
			if e != nil {
				return e
			}
			current[id] = writerState{contribution: c, hash: hash[:]}
		}
		reportOperation(ctx, "rebuild_contributions", "archive", "archive_log_state")
		if err = organizeContributions(ctx, tx, current); err != nil {
			return err
		}
		p.AfterID, p.AfterCreated = batch.AfterID, batch.AfterCreated
		if !exhausted {
			return nil
		}
		// Cross-day collection corrections may move a row out of this table.
		// Reconcile their old contribution too, even though the raw scan no longer
		// encounters that source ID. The queue is committed with raw writes.
		pending, err := tx.QueryContext(ctx, `SELECT source_id FROM archive_pending_statistics WHERE log_date=? ORDER BY source_id LIMIT ?`, work.Date, w.readBudget().MaxRows)
		if err != nil {
			return err
		}
		var ids []any
		for pending.Next() {
			var id int64
			if err = pending.Scan(&id); err != nil {
				pending.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = pending.Err()
		pending.Close()
		if err != nil {
			return err
		}
		if len(ids) > 0 {
			latest, e := writerReadLedger(ctx, tx, ids, true, "archive_raw_state")
			if e != nil {
				return e
			}
			if len(latest) != len(ids) {
				return ErrWriterCheckpoint
			}
			if e = organizeContributions(ctx, tx, latest); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "DELETE FROM archive_pending_statistics WHERE source_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", ids...); e != nil {
				return e
			}
			return nil
		}
		if err = p.State.Finish(work, "", ""); err != nil {
			return err
		}
		p.AfterID = 0
		p.AfterCreated = 0
		return nil
	})
}
