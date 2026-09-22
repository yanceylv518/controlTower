package logarchive

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"controltower/internal/archivecontract"
)

var (
	ErrWriterLease      = errors.New("archive writer authorization expired or superseded")
	ErrWriterCheckpoint = errors.New("archive target checkpoint changed")
	ErrWriterReceipt    = errors.New("archive batch receipt conflicts with payload")
	ErrWriterFrozen     = errors.New("archive date is frozen")
	ErrWriterLegacyData = errors.New("archive contains legacy data without an authoritative checkpoint; explicit import is required")
)

const writerStream = "incremental"

type BatchResult struct {
	BatchID         string
	AfterID         int64
	Rows            int
	CatalogRevision uint64
	Replayed        bool
	CommittedAt     time.Time
	Metrics         archivecontract.ArchiveMetrics
	More            bool
}

type writerMeta struct {
	epoch, revision uint64
	session         []byte
	active          bool
	unscoped        uint64
}

func readWriterMeta(ctx context.Context, db foundationQuery, identity archivecontract.Identity, lock bool) (writerMeta, error) {
	var meta writerMeta
	query := `SELECT singleton_id,site_id,dataset_id,source_generation_id,format_version,writer_epoch,writer_session,COALESCE(writer_lease_until>UTC_TIMESTAMP(6),0),catalog_revision,unscoped_blocking_issues FROM archive_dataset_meta ORDER BY singleton_id LIMIT 2`
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return meta, ErrFoundationNotPrepared
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var singleton, format int
		var site string
		var dataset, generation []byte
		if rows.Scan(&singleton, &site, &dataset, &generation, &format, &meta.epoch, &meta.session, &meta.active, &meta.revision, &meta.unscoped) != nil {
			return meta, ErrFoundationSchema
		}
		count++
		if count != 1 || singleton != 1 || site != identity.SiteID || hex.EncodeToString(dataset) != identity.DatasetID || hex.EncodeToString(generation) != identity.SourceGenerationID {
			return meta, ErrFoundationIdentity
		}
		if format != archivecontract.FormatVersion {
			return meta, ErrFoundationVersion
		}
	}
	if rows.Err() != nil {
		return meta, ErrFoundationSchema
	}
	if count != 1 {
		return meta, ErrFoundationNotPrepared
	}
	return meta, nil
}

func requireWriter(meta writerMeta, grant archivecontract.WriterGrant) error {
	session, _ := archivecontract.IDBytes(grant.Session)
	if meta.epoch != grant.WriterEpoch || !bytes.Equal(meta.session, session) || !meta.active {
		return ErrWriterLease
	}
	return nil
}

// AcquireWriter persists the CT-assigned fencing epoch. remaining is measured
// by the caller's monotonic request-start deadline, never from a cached grant.
// Database waits consume that budget too; target time supplies the lease clock.
func (w *Worker) AcquireWriter(ctx context.Context, grant archivecontract.WriterGrant, remaining time.Duration) error {
	return w.acquireWriter(ctx, grant, remaining, false)
}

func (w *Worker) acquireWriter(ctx context.Context, grant archivecontract.WriterGrant, remaining time.Duration, workflow bool) error {
	deadline := time.Now().Add(remaining)
	if err := grant.Validate(); err != nil {
		return err
	}
	if remaining <= 0 {
		return ErrWriterLease
	}
	if _, err := w.inspectFoundationCached(ctx, grant.Identity); err != nil {
		return err
	}
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return &scanError{code: "writer_transaction_failed", cause: err}
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return err
	}
	session, _ := archivecontract.IDBytes(grant.Session)
	if grant.WriterEpoch < meta.epoch || (grant.WriterEpoch == meta.epoch && !bytes.Equal(meta.session, session)) {
		return ErrWriterLease
	}
	if workflow {
		if err := initializeWorkflow(ctx, tx); err != nil {
			return err
		}
	} else if err := requireWriterBaseline(ctx, tx); err != nil {
		return err
	}
	// Only a transaction holding the dataset fence and proving an empty
	// baseline may create the initial cursor. A date scan can then run first.
	if !workflow {
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_checkpoints(stream_key,stream_type,after_id,cursor_version,updated_at) VALUES('incremental','incremental',0,1,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE stream_key=VALUES(stream_key)`); err != nil {
			return ErrWriterCheckpoint
		}
	}
	remaining = time.Until(deadline)
	if remaining > 90*time.Second {
		remaining = 90 * time.Second
	}
	if advertised := time.Duration(grant.LeaseSeconds) * time.Second; remaining > advertised {
		remaining = advertised
	}
	if remaining <= 0 {
		return ErrWriterLease
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET writer_epoch=?,writer_session=?,writer_lease_until=TIMESTAMPADD(MICROSECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, grant.WriterEpoch, session, remaining.Microseconds()); err != nil {
		return &scanError{code: "writer_authorization_failed", cause: err}
	}
	// A delayed response cannot extend authority beyond the entry budget.
	if time.Until(deadline) <= 0 {
		return ErrWriterLease
	}
	if err := tx.Commit(); err != nil {
		return &scanError{code: "writer_commit_uncertain", cause: err}
	}
	return nil
}

var writerMonthlyName = regexp.MustCompile(`^logs_(?:[0-9]{6}|undated)$`)

// A lost local cache is harmless; a missing authoritative target checkpoint
// next to existing data requires explicit recovery/import, not guessed progress.
func requireWriterBaseline(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_checkpoints WHERE stream_key=?", writerStream).Scan(&count); err != nil {
		return ErrWriterCheckpoint
	}
	if count == 1 {
		var templateRows int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM logs LIMIT 1) legacy_template`).Scan(&templateRows); err != nil {
			return ErrFoundationSchema
		}
		if templateRows != 0 {
			return ErrWriterLegacyData
		}
		var kind string
		var version int
		var after int64
		var receipt []byte
		if err := tx.QueryRowContext(ctx, `SELECT stream_type,cursor_version,after_id,last_batch_id FROM archive_checkpoints WHERE stream_key='incremental'`).Scan(&kind, &version, &after, &receipt); err != nil || kind != "incremental" || version != 1 || after < 0 || (after > 0 && len(receipt) != 16) {
			return ErrWriterCheckpoint
		}
		if after == 0 && len(receipt) == 0 {
			var committed int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM archive_batch_receipts WHERE stream_key='incremental' LIMIT 1) prior`).Scan(&committed); err != nil || committed != 0 {
				return ErrWriterCheckpoint
			}
		}
		return nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() ORDER BY TABLE_NAME")
	if err != nil {
		return ErrFoundationSchema
	}
	var tables []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			rows.Close()
			return ErrFoundationSchema
		}
		if name == "logs" || name == "archive_log_state" || name == "log_daily_stats" || name == "log_monthly_stats" || name == "archive_batch_receipts" || name == "archive_days" || name == "archive_day_versions" || name == "archive_scan_tasks" || name == "archive_ingest_issues" || writerMonthlyName.MatchString(name) {
			tables = append(tables, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return ErrFoundationSchema
	}
	for _, table := range tables {
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM "+quote(table)+" LIMIT 1) AS archive_existing").Scan(&count); err != nil {
			return ErrFoundationSchema
		}
		if count != 0 {
			return ErrWriterLegacyData
		}
	}
	return nil
}

// ReleaseWriter needs no source connection. A stale executor cannot clear its
// successor's authorization, and retaining the epoch/session prevents ABA reuse.
func (w *Worker) ReleaseWriter(ctx context.Context, grant archivecontract.WriterGrant) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("archive writer release transaction failed")
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return err
	}
	session, _ := archivecontract.IDBytes(grant.Session)
	if meta.epoch != grant.WriterEpoch || !bytes.Equal(meta.session, session) {
		return ErrWriterLease
	}
	if _, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET writer_lease_until=NULL,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`); err != nil {
		return errors.New("archive writer release failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("archive writer release commit uncertain")
	}
	return nil
}

type writerCursor struct {
	AfterID         int64  `json:"after_id"`
	CatalogRevision uint64 `json:"catalog_revision,string"`
	AfterCreated    int64  `json:"after_created_unix,omitempty"`
	Completed       bool   `json:"completed,omitempty"`
}

// ProgressV2 reads only authoritative target state. Local checkpoint files and
// MAX(source/target id) never initialize or advance the versioned cursor.
func (w *Worker) ProgressV2(ctx context.Context, identity archivecontract.Identity) (BatchResult, error) {
	if err := identity.Validate(); err != nil {
		return BatchResult{}, err
	}
	tx, err := w.target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return BatchResult{}, errors.New("archive progress read failed")
	}
	defer tx.Rollback()
	meta, err := readWriterMeta(ctx, tx, identity, false)
	if err != nil {
		return BatchResult{}, err
	}
	result := BatchResult{CatalogRevision: meta.revision}
	var batchID []byte
	var streamType string
	var cursorVersion int
	err = tx.QueryRowContext(ctx, `SELECT after_id,last_batch_id,stream_type,cursor_version FROM archive_checkpoints WHERE stream_key=?`, writerStream).Scan(&result.AfterID, &batchID, &streamType, &cursorVersion)
	if errors.Is(err, sql.ErrNoRows) {
		if err := requireWriterBaseline(ctx, tx); err != nil {
			return BatchResult{}, err
		}
		return result, nil
	}
	if err != nil || result.AfterID < 0 || streamType != "incremental" || cursorVersion != 1 {
		return BatchResult{}, ErrWriterCheckpoint
	}
	if result.AfterID == 0 && len(batchID) == 0 {
		if err := requireWriterBaseline(ctx, tx); err != nil {
			return BatchResult{}, err
		}
		return result, nil
	}
	if len(batchID) != 16 {
		return BatchResult{}, ErrWriterCheckpoint
	}
	result.BatchID = hex.EncodeToString(batchID)
	var rawTime, rawCursor []byte
	if err := tx.QueryRowContext(ctx, `SELECT row_count,committed_at,cursor_after_json FROM archive_batch_receipts WHERE batch_id=? AND stream_key=?`, batchID, writerStream).Scan(&result.Rows, &rawTime, &rawCursor); err != nil {
		return BatchResult{}, ErrWriterReceipt
	}
	var cursor writerCursor
	if json.Unmarshal(rawCursor, &cursor) != nil || cursor.AfterID != result.AfterID || cursor.CatalogRevision > meta.revision || result.Rows < 1 {
		return BatchResult{}, ErrWriterReceipt
	}
	result.CommittedAt, err = time.Parse("2006-01-02 15:04:05.999999", string(rawTime))
	if err != nil {
		return BatchResult{}, ErrWriterReceipt
	}
	return result, nil
}

// PassV2 copies one bounded, delay-safe source prefix and commits its progress
// with the rows. P3 adds independent backfill streams; no source writes occur.
func (w *Worker) PassV2(ctx context.Context, grant archivecontract.WriterGrant) (result BatchResult, err error) {
	return w.passV2(ctx, grant, "")
}

func (w *Worker) passV2(ctx context.Context, grant archivecontract.WriterGrant, workflowID string) (result BatchResult, err error) {
	return w.passV2Mode(ctx, grant, workflowID, false)
}
func (w *Worker) passV2Mode(ctx context.Context, grant archivecontract.WriterGrant, workflowID string, rawOnly bool) (result BatchResult, err error) {
	started := time.Now()
	budget := w.readBudget()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(budget.MaxDurationMillis)*time.Millisecond)
	defer cancel()
	result.Metrics.Stream = "incremental"
	defer func() {
		result.Metrics.Stream = "incremental"
		result.Metrics.ElapsedMillis = uint64(time.Since(started).Milliseconds())
		if err != nil {
			result.Metrics.ErrorCode = scanFailureCode(err)
			w.invalidateFoundationCache()
			issueCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = w.recordIncrementalIssue(issueCtx, grant, err)
		}
	}()
	if err = grant.Validate(); err != nil {
		return result, err
	}
	if _, err = w.inspectFoundationCached(ctx, grant.Identity); err != nil {
		return result, err
	}
	meta, err := readWriterMeta(ctx, w.target, grant.Identity, false)
	if err != nil {
		return result, err
	}
	if err = requireWriter(meta, grant); err != nil {
		return result, err
	}
	progress, err := w.ProgressV2(ctx, grant.Identity)
	if err != nil {
		return result, err
	}
	var sourceNow int64
	if w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&sourceNow) != nil {
		return result, &scanError{code: "source_clock_unavailable"}
	}
	delay := w.delay
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	rows, e := w.source.QueryContext(ctx, "SELECT * FROM logs WHERE id > ? ORDER BY id ASC LIMIT ?", progress.AfterID, budget.MaxRows+1)
	if e != nil {
		return result, &scanError{code: "source_query_failed"}
	}
	batch := writerBatch{BeforeID: progress.AfterID, AfterID: progress.AfterID, WorkflowID: workflowID, RawOnly: rawOnly}
	readBytes, _, e := readWriterPage(ctx, rows, &batch, budget, sourceNow, int64(delay/time.Second), started)
	if e != nil {
		return result, e
	}
	if len(batch.Rows) == 0 {
		progress.Rows = 0
		progress.Metrics = archivecontract.ArchiveMetrics{Stream: "incremental", ReadBytes: readBytes}
		return progress, nil
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return result, errors.New("archive batch identity failed")
	}
	batch.ID = hex.EncodeToString(id[:])
	lastCreated := batch.AfterCreated
	batch.AfterCreated = 0
	result, err = w.commitWriterBatch(ctx, grant, batch, writerFaultHooks{})
	result.Metrics = archivecontract.ArchiveMetrics{Stream: "incremental", ReadBytes: readBytes}
	if err == nil {
		result.Metrics.WrittenBytes = readBytes
		if lastCreated > 0 {
			lag := sourceNow - lastCreated
			if lag < 0 {
				lag = 0
			}
			result.Metrics.LagSeconds = &lag
		}
		result.More = batch.Limited
	}
	return result, err
}

func (w *Worker) recordIncrementalIssue(ctx context.Context, grant archivecontract.WriterGrant, cause error) error {
	var se *scanError
	if !errors.As(cause, &se) || se.sourceID == 0 {
		return nil
	}
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
	if err = countOversizedIssue(ctx, tx, &meta, cause); err != nil {
		return err
	}
	if err = w.writeIngestIssue(ctx, tx, writerStream, "", cause); err != nil {
		return err
	}
	if err = writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return err
	}
	return tx.Commit()
}

func writerReceipt(ctx context.Context, db foundationQuery, batch writerBatch, hash [32]byte) (BatchResult, bool, error) {
	id, _ := archivecontract.IDBytes(batch.ID)
	var gotHash, afterJSON, beforeJSON, rawTime []byte
	var stream string
	result := BatchResult{BatchID: batch.ID, Replayed: true}
	err := db.QueryRowContext(ctx, `SELECT stream_key,payload_hash,cursor_before_json,cursor_after_json,row_count,committed_at FROM archive_batch_receipts WHERE batch_id=?`, id).Scan(&stream, &gotHash, &beforeJSON, &afterJSON, &result.Rows, &rawTime)
	if errors.Is(err, sql.ErrNoRows) {
		return BatchResult{}, false, nil
	}
	if err != nil {
		return BatchResult{}, false, errors.New("archive batch receipt lookup failed")
	}
	var before, after writerCursor
	if stream != batch.stream() || !bytes.Equal(gotHash, hash[:]) || json.Unmarshal(beforeJSON, &before) != nil || json.Unmarshal(afterJSON, &after) != nil || before.AfterID != batch.BeforeID || after.AfterID != batch.AfterID || result.Rows != len(batch.Rows) || (batch.Scan != nil && (before.AfterCreated != batch.BeforeCreated || after.AfterCreated != batch.AfterCreated || after.Completed != batch.Completed)) {
		return BatchResult{}, false, ErrWriterReceipt
	}
	result.AfterID, result.CatalogRevision = after.AfterID, after.CatalogRevision
	result.CommittedAt, err = time.Parse("2006-01-02 15:04:05.999999", string(rawTime))
	if err != nil {
		return BatchResult{}, false, ErrWriterReceipt
	}
	return result, true, nil
}
