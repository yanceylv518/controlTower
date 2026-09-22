package logarchive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
)

func scanStream(t af.BackfillTask) string { return (writerBatch{Scan: &t}).stream() }

func (w *Worker) scanStatus(ctx context.Context, grant af.WriterGrant, t af.BackfillTask) (af.BackfillStatus, error) {
	s := af.BackfillStatus{TaskID: t.TaskID, Date: t.Date, Attempt: t.Attempt, WriterEpoch: grant.WriterEpoch, State: "running"}
	id, _ := af.IDBytes(t.TaskID)
	var date, kind string
	var policy []byte
	var errorCode sql.NullString
	var afterID, afterCreated sql.NullInt64
	var batch []byte
	err := w.target.QueryRowContext(ctx, `SELECT DATE_FORMAT(t.log_date,'%Y-%m-%d'),t.task_type,t.policy_json,t.state,t.scanned_rows,t.read_bytes,t.written_bytes,t.elapsed_millis,t.error_code,COALESCE(FLOOR(UNIX_TIMESTAMP(t.retry_after)),0),t.empty_candidate,t.source_now_unix,c.after_id,c.after_created_unix,c.last_batch_id,m.catalog_revision FROM archive_scan_tasks t CROSS JOIN archive_dataset_meta m LEFT JOIN archive_checkpoints c ON c.stream_key=? WHERE t.task_id=? AND t.attempt=? AND m.singleton_id=1`, scanStream(t), id, t.Attempt).Scan(&date, &kind, &policy, &s.State, &s.ScannedRows, &s.ReadBytes, &s.WrittenBytes, &s.ElapsedMillis, &errorCode, &s.RetryAfterUnix, &s.EmptyCandidate, &s.SourceNowUnix, &afterID, &afterCreated, &batch, &s.CatalogRevision)
	if err != nil {
		return s, err
	}
	var saved af.CoveragePolicy
	if json.Unmarshal(policy, &saved) != nil || saved != t.Policy || date != t.Date || kind != t.Type {
		return af.BackfillStatus{}, ErrWriterCheckpoint
	}
	s.ErrorCode = errorCode.String
	s.AfterID = afterID.Int64
	s.AfterCreatedUnix = afterCreated.Int64
	s.BatchID = hex.EncodeToString(batch)
	if (s.ScannedRows > 0 && (!afterID.Valid || len(batch) != 16)) || (s.State == "succeeded" && len(batch) != 16) || s.Validate() != nil {
		return s, ErrWriterCheckpoint
	}
	if len(batch) > 0 {
		var cursorRaw []byte
		var count uint64
		var stream string
		var cursorVersion int
		var boundTask []byte
		var from, to int64
		if err := w.target.QueryRowContext(ctx, `SELECT r.cursor_after_json,r.row_count,c.stream_type,c.cursor_version,c.task_id,c.from_unix,c.to_unix FROM archive_checkpoints c JOIN archive_batch_receipts r ON r.batch_id=c.last_batch_id AND r.stream_key=c.stream_key WHERE c.stream_key=?`, scanStream(t)).Scan(&cursorRaw, &count, &stream, &cursorVersion, &boundTask, &from, &to); err != nil {
			return af.BackfillStatus{}, ErrWriterReceipt
		}
		var cursor writerCursor
		expectedFrom, expectedTo, _ := af.DateBounds(t.Date)
		if json.Unmarshal(cursorRaw, &cursor) != nil || cursor.AfterID != s.AfterID || cursor.AfterCreated != s.AfterCreatedUnix || cursor.CatalogRevision > s.CatalogRevision || count > s.ScannedRows || stream != t.Type || cursorVersion != 1 || hex.EncodeToString(boundTask) != t.TaskID || from != expectedFrom || to != expectedTo || (s.State == "succeeded" && !cursor.Completed) {
			return af.BackfillStatus{}, ErrWriterCheckpoint
		}
	} else if afterID.Valid {
		return af.BackfillStatus{}, ErrWriterCheckpoint
	}
	return s, nil
}

func (w *Worker) initializeScan(ctx context.Context, grant af.WriterGrant, t af.BackfillTask) error {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return err
	}
	if err = requireWriter(meta, grant); err != nil {
		return err
	}
	if err = requireWriterBaseline(ctx, tx); err != nil {
		return err
	}
	id, _ := af.IDBytes(t.TaskID)
	policy, _ := json.Marshal(t.Policy)
	if _, err = tx.ExecContext(ctx, `INSERT INTO archive_scan_tasks(task_id,attempt,log_date,task_type,policy_json,state,writer_epoch,started_at,updated_at) VALUES(?,?,?,?,?,'running',?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE task_id=VALUES(task_id)`, id, t.Attempt, t.Date, t.Type, string(policy), grant.WriterEpoch); err != nil {
		return err
	}
	// A task creates a date entry, never verification evidence.
	r, err := tx.ExecContext(ctx, `INSERT IGNORE INTO archive_days(log_date,state,updated_at) VALUES(?,'collecting',UTC_TIMESTAMP(6))`, t.Date)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n > 0 {
		if err = bumpScanCatalog(ctx, tx, &meta, t.Date, ""); err != nil {
			return err
		}
	}
	if err = writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return err
	}
	return tx.Commit()
}

func bumpScanCatalog(ctx context.Context, tx *sql.Tx, meta *writerMeta, date, state string) error {
	if meta.revision == math.MaxUint64 {
		return errors.New("archive catalog revision exhausted")
	}
	meta.revision++
	if _, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, meta.revision); err != nil {
		return err
	}
	if state == "" {
		_, err := tx.ExecContext(ctx, `UPDATE archive_days SET catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, meta.revision, date)
		return err
	}
	// Existing published versions remain immutable and dirty until P4 validates.
	_, err := tx.ExecContext(ctx, `UPDATE archive_days SET state=IF(current_version_id IS NULL,?,'dirty'),catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, state, meta.revision, date)
	return err
}

func (w *Worker) writeScanCommit(ctx context.Context, tx *sql.Tx, grant af.WriterGrant, b writerBatch, meta *writerMeta, byteCount uint64) error {
	t := b.Scan
	id, _ := af.IDBytes(t.TaskID)
	var scanned uint64
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT scanned_rows,state FROM archive_scan_tasks WHERE task_id=? AND attempt=? FOR UPDATE`, id, t.Attempt).Scan(&scanned, &state); err != nil {
		return err
	}
	if state == "succeeded" || state == "blocked" {
		return ErrWriterCheckpoint
	}
	state = "running"
	dayState := ""
	code := ""
	empty := false
	if b.Completed {
		state = "succeeded"
		dayState = "pending_verify"
		empty = scanned+uint64(len(b.Rows)) == 0
		if empty {
			dayState = "empty_candidate"
		}
		if t.Policy.SourceRetainedFrom == "" && !b.RawOnly {
			state = "blocked"
			code = "source_history_unknown"
			dayState = "unknown"
			empty = false
		}
		// Empty pages must respect date freezes just like pages with rows.
		var frozen bool
		if err := tx.QueryRowContext(ctx, `SELECT freeze_task_id IS NOT NULL OR freeze_epoch IS NOT NULL OR freeze_revision IS NOT NULL OR freeze_until IS NOT NULL FROM archive_days WHERE log_date=? FOR UPDATE`, t.Date).Scan(&frozen); err != nil {
			return err
		}
		if frozen {
			return ErrWriterFrozen
		}
		if err := bumpScanCatalog(ctx, tx, meta, t.Date, dayState); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE archive_scan_tasks SET state=?,scanned_rows=scanned_rows+?,read_bytes=read_bytes+?,written_bytes=written_bytes+?,elapsed_millis=elapsed_millis+?,error_code=NULLIF(?,''),retry_after=NULL,empty_candidate=?,source_now_unix=?,writer_epoch=?,completed_at=IF(?,UTC_TIMESTAMP(6),NULL),updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND attempt=?`, state, len(b.Rows), byteCount, byteCount, b.ElapsedMillis, code, empty, b.SourceNow, grant.WriterEpoch, b.Completed, id, t.Attempt)
	return err
}

// ScanDateV3 advances exactly one bounded page. Source history declarations
// constrain coverage; a completed scan is never a verified or sealed version.
func (w *Worker) ScanDateV3(ctx context.Context, grant af.WriterGrant, t af.BackfillTask) (result af.BackfillStatus, err error) {
	return w.scanDate(ctx, grant, t, false)
}

// allowOpen is exclusive to the chronological workflow; open days never complete.
func (w *Worker) scanDate(ctx context.Context, grant af.WriterGrant, t af.BackfillTask, allowOpen bool) (result af.BackfillStatus, err error) {
	return w.scanDateMode(ctx, grant, t, allowOpen, false)
}
func (w *Worker) scanDateMode(ctx context.Context, grant af.WriterGrant, t af.BackfillTask, allowOpen, rawOnly bool) (result af.BackfillStatus, err error) {
	if grant.Validate() != nil || t.Validate() != nil || !grant.Identity.Equal(t.Identity) {
		return result, af.ErrConflict
	}
	started := time.Now()
	budget := w.effectiveScanBudget(t.Policy.Budget)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	if _, err = w.inspectFoundationCached(ctx, grant.Identity); err != nil {
		return result, err
	}
	if err = w.initializeScan(ctx, grant, t); err != nil {
		return result, err
	}
	result, err = w.scanStatus(ctx, grant, t)
	if err != nil {
		return result, err
	}
	if result.State == "succeeded" || result.State == "blocked" {
		return result, nil
	}
	if result.RetryAfterUnix > time.Now().Unix() {
		return result, nil
	}
	defer func() {
		if err != nil {
			w.invalidateFoundationCache()
			failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if status, e := w.recordScanFailure(failureCtx, grant, t, err); e == nil {
				result = status
			}
		}
	}()
	var sourceNow int64
	reportOperation(ctx, "read_source_clock", "source", "")
	if e := w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&sourceNow); e != nil {
		return result, &scanError{code: "source_clock_unavailable", cause: e}
	}
	from, to, _ := af.DateBounds(t.Date)
	delay := w.delay
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	open := to > sourceNow-int64(delay/time.Second)
	if open {
		if !allowOpen {
			return result, &scanError{code: "date_not_ready"}
		}
		to = sourceNow - int64(delay/time.Second)
		if to <= from {
			return result, nil
		}
	}
	if t.Policy.SourceRetainedFrom != "" && t.Date < t.Policy.SourceRetainedFrom {
		return result, &scanError{code: "source_cleared"}
	}
	if err = w.requireScanIndex(ctx); err != nil {
		return result, err
	}
	batch := writerBatch{RawOnly: rawOnly, Capture: allowOpen && !rawOnly, Scan: &t, BeforeID: result.AfterID, AfterID: result.AfterID, BeforeCreated: result.AfterCreatedUnix, AfterCreated: result.AfterCreatedUnix, SourceNow: sourceNow}
	b := budget
	reportOperation(ctx, "read_source_logs", "source", "logs")
	rows, e := w.source.QueryContext(ctx, `SELECT * FROM logs WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`, from, to, batch.BeforeCreated, batch.BeforeCreated, batch.BeforeID, b.MaxRows+1)
	if e != nil {
		return result, &scanError{code: "source_query_failed", cause: e}
	}
	_, batch.Completed, err = readWriterPage(ctx, rows, &batch, b, sourceNow, int64(delay/time.Second), started)
	if err != nil {
		return result, err
	}
	if open {
		batch.Completed = false
		if len(batch.Rows) == 0 {
			return result, nil
		}
	}
	if len(batch.Rows) == 0 && !batch.Completed {
		return result, &scanError{code: "budget_exhausted"}
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return result, err
	}
	batch.ID = hex.EncodeToString(id[:])
	batch.ElapsedMillis = uint64(time.Since(started).Milliseconds())
	code := "write_archive_logs"
	if rawOnly {
		code = "write_raw_logs"
	}
	reportOperation(ctx, code, "archive", "")
	if _, err = w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{}); err != nil {
		return result, err
	}
	return w.scanStatus(ctx, grant, t)
}

func (w *Worker) requireScanIndex(ctx context.Context) error {
	reportOperation(ctx, "check_source_index", "source", "logs")
	var engine string
	if err := w.source.QueryRowContext(ctx, `SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs'`).Scan(&engine); err != nil {
		return &scanError{code: "source_index_check_failed", cause: err}
	}
	rows, err := w.source.QueryContext(ctx, `SELECT INDEX_NAME,COLUMN_NAME,COALESCE(SUB_PART,0),IS_VISIBLE FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' ORDER BY INDEX_NAME,SEQ_IN_INDEX`)
	if err != nil {
		// MySQL 5.7 has no invisible indexes or IS_VISIBLE column.
		rows, err = w.source.QueryContext(ctx, `SELECT INDEX_NAME,COLUMN_NAME,COALESCE(SUB_PART,0),'YES' FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' ORDER BY INDEX_NAME,SEQ_IN_INDEX`)
		if err != nil {
			return &scanError{code: "source_index_check_failed", cause: err}
		}
	}
	defer rows.Close()
	indexes := map[string][]string{}
	invalid := map[string]bool{}
	for rows.Next() {
		var name, column, visible string
		var prefix int
		if err := rows.Scan(&name, &column, &prefix, &visible); err != nil {
			return &scanError{code: "source_index_check_failed", cause: err}
		}
		indexes[name] = append(indexes[name], column)
		if prefix != 0 || visible != "YES" {
			invalid[name] = true
		}
	}
	if rows.Err() != nil {
		return &scanError{code: "source_index_check_failed", cause: rows.Err()}
	}
	primary := indexes["PRIMARY"]
	implicit := strings.EqualFold(engine, "InnoDB") && len(primary) == 1 && primary[0] == "id"
	for name, columns := range indexes {
		if !invalid[name] && len(columns) > 0 && columns[0] == "created_at" && ((len(columns) >= 2 && columns[1] == "id") || (len(columns) == 1 && implicit)) {
			return nil
		}
	}
	return &scanError{code: "source_index_missing"}
}

func (w *Worker) recordScanFailure(ctx context.Context, grant af.WriterGrant, t af.BackfillTask, cause error) (af.BackfillStatus, error) {
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return af.BackfillStatus{}, err
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return af.BackfillStatus{}, err
	}
	if err = requireWriter(meta, grant); err != nil {
		return af.BackfillStatus{}, err
	}
	code := scanFailureCode(cause)
	state := "retry_wait"
	dayState := "collecting"
	switch code {
	case "source_index_missing", "source_cleared", "row_too_large", "repair_unscoped_target", "invalid_timestamp", "invalid_source_row", "schema_changed", "checkpoint_conflict", "receipt_conflict":
		state = "blocked"
		dayState = "blocked"
	}
	if code == "source_cleared" {
		dayState = "unknown"
	}
	id, _ := af.IDBytes(t.TaskID)
	var persistedState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM archive_scan_tasks WHERE task_id=? AND attempt=? FOR UPDATE`, id, t.Attempt).Scan(&persistedState); err != nil {
		return af.BackfillStatus{}, err
	}
	if persistedState == "succeeded" || persistedState == "blocked" {
		_ = tx.Rollback()
		return w.scanStatus(ctx, grant, t)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE archive_scan_tasks SET state=?,failures=failures+1,error_code=?,retry_after=IF(?='retry_wait',TIMESTAMPADD(SECOND,LEAST(900,15*POW(2,LEAST(failures,6))),UTC_TIMESTAMP(6)),NULL),writer_epoch=?,updated_at=UTC_TIMESTAMP(6) WHERE task_id=? AND attempt=?`, state, code, state, grant.WriterEpoch, id, t.Attempt); err != nil {
		return af.BackfillStatus{}, err
	}
	if err = countOversizedIssue(ctx, tx, &meta, cause); err != nil {
		return af.BackfillStatus{}, err
	}
	if err = w.writeIngestIssue(ctx, tx, scanStream(t), t.Date, cause); err != nil {
		return af.BackfillStatus{}, err
	}
	if err = bumpScanCatalog(ctx, tx, &meta, t.Date, dayState); err != nil {
		return af.BackfillStatus{}, err
	}
	if err = writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return af.BackfillStatus{}, err
	}
	if err = tx.Commit(); err != nil {
		return af.BackfillStatus{}, err
	}
	return w.scanStatus(ctx, grant, t)
}

func (w *Worker) writeIngestIssue(ctx context.Context, tx *sql.Tx, stream, date string, cause error) error {
	var se *scanError
	if !errors.As(cause, &se) || se.sourceID == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO archive_ingest_issues(stream_key,source_id,log_date,error_code,row_bytes,first_seen_at,last_seen_at) VALUES(?,?,NULLIF(?,''),?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE error_code=VALUES(error_code),row_bytes=VALUES(row_bytes),resolved_at=NULL,last_seen_at=VALUES(last_seen_at)`, stream, se.sourceID, date, se.code, se.bytes)
	return err
}

// Oversized source rows and unscoped target damage cannot be assigned
// trustworthy coverage before archival or an explicit successful repair.
// Count each unresolved source ID once across all scan streams, so another
// completed date never hides this blocker. Normal row commit resolves it.
func countOversizedIssue(ctx context.Context, tx *sql.Tx, meta *writerMeta, cause error) error {
	var se *scanError
	if !errors.As(cause, &se) || (se.code != "row_too_large" && se.code != "repair_unscoped_target") {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_ingest_issues WHERE source_id=? AND error_code IN ('row_too_large','repair_unscoped_target') AND resolved_at IS NULL`, se.sourceID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if meta.unscoped == math.MaxUint64 || meta.revision == math.MaxUint64 {
		return errors.New("archive blocker count exhausted")
	}
	meta.unscoped++
	meta.revision++
	_, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET unscoped_blocking_issues=?,catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, meta.unscoped, meta.revision)
	return err
}
