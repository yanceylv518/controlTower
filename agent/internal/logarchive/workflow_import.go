package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// Rebuild only derived legacy state. Raw monthly tables are never cleared,
// renamed or rewritten here. Cursor and each rebuilt page commit together.
func (w *Worker) importWorkflowPage(ctx context.Context, g af.WriterGrant, s *workflowState) error {
	phase := s.Phase
	var processed uint64
	var processedTable string
	var afterID int64
	reportOperation(ctx, "check_statistics_schema", "archive", "")
	if err := ensureMonthlyTables(ctx, w.target, nil); err != nil {
		return err
	}
	var batch writerBatch
	if s.Phase == "import_target" {
		if s.Table == "" {
			if err := w.nextWorkflowTable(ctx, s); err != nil {
				return err
			}
		}
		if s.Table != "" {
			processedTable, afterID = s.Table, s.AfterID
			if !writerMonthlyName.MatchString(s.Table) {
				return ErrFoundationSchema
			}
			var engine string
			reportOperation(ctx, "check_month_table", "archive", s.Table)
			if err := w.target.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?`, s.Table).Scan(&engine); err != nil {
				return err
			}
			if !strings.EqualFold(engine, "InnoDB") {
				return ErrFoundationSchema
			}
			reportOperation(ctx, "read_existing_logs", "archive", s.Table)
			rows, err := w.target.QueryContext(ctx, "SELECT * FROM "+quote(s.Table)+" WHERE id>? ORDER BY id LIMIT ?", s.AfterID, w.readBudget().MaxRows+1)
			if err != nil {
				return err
			}
			batch.AfterID = s.AfterID
			if _, _, err = readWriterPage(ctx, rows, &batch, w.readBudget(), math.MaxInt64, 0, time.Now()); err != nil {
				return err
			}
			if len(batch.Rows) == 0 {
				if err = w.nextWorkflowTable(ctx, s); err != nil {
					return err
				}
			}
		}
	}
	reportOperation(ctx, "begin_archive_batch", "archive", "archive_dataset_meta")
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, g.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(meta, g); err != nil {
		return err
	}
	switch s.Phase {
	case "reset_state", "reset_daily", "reset_monthly":
		table, next := "archive_log_state", "reset_daily"
		if s.Phase == "reset_daily" {
			table, next = "log_daily_stats", "reset_monthly"
		}
		if s.Phase == "reset_monthly" {
			table, next = "log_monthly_stats", "import_target"
		}
		reportOperation(ctx, "clear_legacy_statistics", "archive", table)
		result, e := tx.ExecContext(ctx, "DELETE FROM "+quote(table)+" LIMIT ?", w.readBudget().MaxRows)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			s.Phase = next
		}
		processed, processedTable = uint64(n), table
	case "import_target":
		daily, monthly := deltas{}, deltas{}
		var blockers uint64
		for _, row := range batch.Rows {
			reportOperation(ctx, "rebuild_contributions", "archive", "archive_log_state")
			id, c, e := writerContribution(batch.Columns, row)
			if e != nil {
				return e
			}
			if "logs_"+c.month() != s.Table {
				return &scanError{code: "repair_unscoped_target"}
			}
			hash, e := writerRawHash(batch.Columns, row)
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(c)
			// INSERT intentionally rejects duplicate source IDs across month tables.
			if _, e = tx.ExecContext(ctx, `INSERT INTO archive_log_state(id,contribution,raw_row_hash,last_batch_id) VALUES(?,?,?,NULL)`, id, string(raw), hash[:]); e != nil {
				return e
			}
			daily.add(c.Day, c, 1)
			monthly.add(c.month(), c, 1)
			if c.Blocking {
				blockers++
			}
			if c.Day != "undated" {
				reportOperation(ctx, "update_date_catalog", "archive", "archive_days")
				if _, e = tx.ExecContext(ctx, `INSERT INTO archive_days(log_date,mutation_revision,state,catalog_revision,updated_at) VALUES(?,1,'dirty',?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE mutation_revision=mutation_revision+1,catalog_revision=VALUES(catalog_revision),updated_at=VALUES(updated_at)`, c.Day, meta.revision+1); e != nil {
					return e
				}
			}
		}
		if len(batch.Rows) > 0 {
			reportOperation(ctx, "compare_imported_logs", "archive", s.Table)
			if err = verifyRows(ctx, tx, s.Table, batch.Columns, batch.Rows); err != nil {
				return err
			}
			reportOperation(ctx, "rebuild_daily_statistics", "archive", "log_daily_stats")
			if err = writeDeltas(ctx, tx, "log_daily_stats", daily); err != nil {
				return err
			}
			reportOperation(ctx, "rebuild_monthly_statistics", "archive", "log_monthly_stats")
			if err = writeDeltas(ctx, tx, "log_monthly_stats", monthly); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET catalog_revision=catalog_revision+1,unscoped_blocking_issues=unscoped_blocking_issues+?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, blockers); err != nil {
				return err
			}
			s.AfterID = batch.AfterID
			s.ImportedRows += uint64(len(batch.Rows))
			afterID = batch.AfterID
			processed = uint64(len(batch.Rows))
		} else if s.Table == "" {
			if _, err = tx.ExecContext(ctx, `INSERT INTO archive_checkpoints(stream_key,stream_type,after_id,cursor_version,updated_at) VALUES('incremental','incremental',0,1,UTC_TIMESTAMP(6))`); err != nil {
				return err
			}
			s.Phase = "live"
		}
	}
	recordPreparationProgress(s, phase, processedTable, afterID, processed, time.Now().UTC())
	reportOperation(ctx, "save_workflow", "archive", "archive_workflow")
	if err = saveWorkflow(ctx, tx, *s); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, g); err != nil {
		return err
	}
	reportOperation(ctx, "commit_archive_batch", "archive", "")
	return tx.Commit()
}

func recordPreparationProgress(s *workflowState, phase, table string, afterID int64, rows uint64, now time.Time) {
	p := af.WorkflowPreparationProgress{Phase: phase, RecordedSince: now}
	if s.Preparation != nil && s.Preparation.Phase == phase {
		p = *s.Preparation
	}
	p.Table, p.AfterID = table, afterID
	p.ProcessedRows += rows
	p.LastBatchRows = rows
	p.CommittedBatches++
	p.LastCommittedAt = now
	s.Preparation = &p
}

func (w *Worker) nextWorkflowTable(ctx context.Context, s *workflowState) error {
	reportOperation(ctx, "select_month_table", "archive", "")
	var name string
	err := w.target.QueryRowContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^logs_([0-9]{6}|undated)$' AND TABLE_NAME>? ORDER BY TABLE_NAME LIMIT 1`, s.Table).Scan(&name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	s.Table = name
	s.AfterID = 0
	return nil
}
