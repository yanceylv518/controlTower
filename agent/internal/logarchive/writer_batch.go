package logarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"controltower/internal/archivecontract"
)

type writerBatch struct {
	RawOnly                     bool
	Capture                     bool
	WorkflowID                  string
	ID                          string
	BeforeID, AfterID           int64
	Columns                     []string
	Rows                        [][]any
	Scan                        *archivecontract.BackfillTask
	BeforeCreated, AfterCreated int64
	Completed                   bool
	SourceNow                   int64
	ElapsedMillis               uint64
	Limited                     bool
}

func (b writerBatch) stream() string {
	if b.Scan != nil {
		return "scan:" + b.Scan.TaskID + ":" + strconv.Itoa(b.Scan.Attempt)
	}
	return writerStream
}
func (b writerBatch) streamType() string {
	if b.Scan != nil {
		return b.Scan.Type
	}
	return "incremental"
}

// Fault hooks are private and used by integration tests, never configuration.
type writerFaultHooks struct {
	BeforeCommit func(*sql.Tx) error
	AfterCommit  func() error
}

func writerHashField(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

// Raw values can contain arbitrary binary bytes. JSON string normalization
// would collapse distinct invalid UTF-8 sequences and cannot serve as a hash.
func writerRawHash(columns []string, row []any) ([32]byte, error) {
	var result [32]byte
	if len(columns) == 0 || len(columns) != len(row) {
		return result, errors.New("invalid archive raw row shape")
	}
	h := sha256.New()
	writerHashField(h, []byte("archive-raw-row-v1"))
	seen := make(map[string]bool, len(columns))
	for i, name := range columns {
		if name == "" || seen[name] {
			return result, errors.New("invalid archive raw columns")
		}
		seen[name] = true
		writerHashField(h, []byte(name))
		if row[i] == nil {
			_, _ = h.Write([]byte{0})
			continue
		}
		value, ok := row[i].(string)
		if !ok {
			return result, errors.New("invalid archive raw value")
		}
		_, _ = h.Write([]byte{1})
		writerHashField(h, []byte(value))
	}
	copy(result[:], h.Sum(nil))
	return result, nil
}

func writerPayloadHash(identity archivecontract.Identity, batch writerBatch) ([32]byte, uint64, error) {
	var result [32]byte
	if _, err := archivecontract.IDBytes(batch.ID); err != nil {
		return result, 0, err
	}
	if batch.BeforeID < 0 || len(batch.Rows) > 5000 || (batch.Scan == nil && (batch.AfterID < batch.BeforeID || len(batch.Rows) == 0)) {
		return result, 0, ErrWriterCheckpoint
	}
	if batch.Scan != nil && (batch.Scan.Validate() != nil || !batch.Scan.Identity.Equal(identity) || (!batch.Completed && len(batch.Rows) == 0)) {
		return result, 0, ErrWriterCheckpoint
	}
	h := sha256.New()
	if batch.RawOnly {
		writerHashField(h, []byte("raw-collection-v1"))
	}
	if batch.Capture {
		if batch.Scan == nil {
			return result, 0, ErrWriterCheckpoint
		}
		writerHashField(h, []byte("copy-evidence-v1"))
	}
	for _, s := range []string{"archive-batch-v1", identity.SiteID, identity.DatasetID, identity.SourceGenerationID, batch.stream(), strconv.FormatInt(batch.BeforeID, 10), strconv.FormatInt(batch.AfterID, 10)} {
		writerHashField(h, []byte(s))
	}
	if batch.Scan != nil {
		parameters, _ := json.Marshal(batch.Scan)
		writerHashField(h, parameters)
		for _, s := range []string{strconv.FormatInt(batch.BeforeCreated, 10), strconv.FormatInt(batch.AfterCreated, 10), strconv.FormatBool(batch.Completed)} {
			writerHashField(h, []byte(s))
		}
	}
	var byteCount uint64
	if batch.WorkflowID != "" {
		if _, err := archivecontract.IDBytes(batch.WorkflowID); err != nil {
			return result, 0, err
		}
		writerHashField(h, []byte(batch.WorkflowID))
	}
	var prior int64
	priorCreated := batch.BeforeCreated
	if batch.Scan != nil {
		prior = batch.BeforeID
	}
	for _, row := range batch.Rows {
		rawHash, err := writerRawHash(batch.Columns, row)
		if err != nil {
			return result, 0, err
		}
		id, _, err := rowContribution(batch.Columns, row)
		if err != nil {
			return result, 0, err
		}
		if batch.Scan == nil && (id <= prior || id > batch.AfterID) {
			return result, 0, ErrWriterCheckpoint
		}
		if batch.Scan != nil {
			var created int64
			for i, c := range batch.Columns {
				if c == "created_at" {
					s, ok := row[i].(string)
					if !ok {
						return result, 0, ErrWriterCheckpoint
					}
					created, err = strconv.ParseInt(s, 10, 64)
					if err != nil {
						return result, 0, ErrWriterCheckpoint
					}
				}
			}
			from, to, _ := archivecontract.DateBounds(batch.Scan.Date)
			if created < from || created >= to || created < priorCreated || (created == priorCreated && id <= prior) {
				return result, 0, ErrWriterCheckpoint
			}
			priorCreated = created
		}
		prior = id
		writerHashField(h, rawHash[:])
		for _, v := range row {
			if s, ok := v.(string); ok {
				byteCount += uint64(len(s))
			}
		}
	}
	if len(batch.Rows) > 0 && prior != batch.AfterID {
		return result, 0, ErrWriterCheckpoint
	}
	if batch.Scan != nil && len(batch.Rows) > 0 && priorCreated != batch.AfterCreated {
		return result, 0, ErrWriterCheckpoint
	}
	copy(result[:], h.Sum(nil))
	return result, byteCount, nil
}

type writerState struct {
	contribution
	hash []byte
}

func writerReadStates(ctx context.Context, tx *sql.Tx, ids []any, lock bool) (map[int64]writerState, error) {
	return writerReadLedger(ctx, tx, ids, lock, "archive_log_state")
}
func writerReadLedger(ctx context.Context, tx *sql.Tx, ids []any, lock bool, table string) (map[int64]writerState, error) {
	if len(ids) == 0 {
		return map[int64]writerState{}, nil
	}
	query := "SELECT id,contribution,raw_row_hash FROM " + quote(table) + " WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") ORDER BY id"
	if lock {
		query += " FOR UPDATE"
	}
	rows, err := tx.QueryContext(ctx, query, ids...)
	if err != nil {
		return nil, errors.New("archive contribution lookup failed")
	}
	defer rows.Close()
	result := make(map[int64]writerState, len(ids))
	for rows.Next() {
		var id int64
		var raw []byte
		var state writerState
		if rows.Scan(&id, &raw, &state.hash) != nil || json.Unmarshal(raw, &state.contribution) != nil || !validContribution(state.contribution) || (len(state.hash) != 0 && len(state.hash) != 32) {
			return nil, errors.New("invalid archive contribution state")
		}
		result[id] = state
	}
	if rows.Err() != nil {
		return nil, errors.New("archive contribution read failed")
	}
	return result, nil
}

func writerContribution(columns []string, row []any) (int64, contribution, error) {
	id, c, err := rowContribution(columns, row)
	if err != nil {
		return 0, c, err
	}
	// Invalid/missing dates and unrecognized charged types block all billing
	// until P3/P4 can classify the anomaly. Counts are maintained per source ID.
	c.Blocking = c.Day == "undated"
	var typ, quota any
	for i, name := range columns {
		if name == "type" {
			typ = row[i]
		}
		if name == "quota" {
			quota = row[i]
		}
	}
	known := false
	if s, ok := typ.(string); ok {
		switch s {
		case "1", "2", "3", "4", "5", "6":
			known = true
		}
	}
	if !known && quota != nil && quota != "0" {
		c.Blocking = true
	}
	return id, c, nil
}

func writerGuardBeforeCommit(ctx context.Context, tx *sql.Tx, grant archivecontract.WriterGrant) error {
	meta, err := readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return err
	}
	return requireWriter(meta, grant)
}

func (w *Worker) commitWriterBatch(ctx context.Context, grant archivecontract.WriterGrant, batch writerBatch, hooks writerFaultHooks) (BatchResult, error) {
	if err := grant.Validate(); err != nil {
		return BatchResult{}, err
	}
	payloadHash, byteCount, err := writerPayloadHash(grant.Identity, batch)
	if err != nil {
		return BatchResult{}, err
	}
	meta, err := readWriterMeta(ctx, w.target, grant.Identity, false)
	if err != nil {
		return BatchResult{}, err
	}
	if err := requireWriter(meta, grant); err != nil {
		return BatchResult{}, err
	}
	current := make(map[int64]contribution, len(batch.Rows))
	hashes := make(map[int64][32]byte, len(batch.Rows))
	ids := make([]any, 0, len(batch.Rows))
	monthSet := map[string]bool{}
	for _, row := range batch.Rows {
		id, c, err := writerContribution(batch.Columns, row)
		if err != nil {
			return BatchResult{}, err
		}
		current[id] = c
		ids = append(ids, id)
		monthSet[c.month()] = true
		hashes[id], _ = writerRawHash(batch.Columns, row)
	}
	months := make([]string, 0, len(monthSet))
	for month := range monthSet {
		months = append(months, month)
	}
	sort.Strings(months)
	// MySQL DDL commits implicitly. It never belongs to the data transaction;
	// authorization is checked above and again after provisioning finishes.
	if err := ensureMonthlyTables(ctx, w.target, months); err != nil {
		return BatchResult{}, err
	}
	tx, err := w.target.BeginTx(ctx, nil)
	if err != nil {
		return BatchResult{}, errors.New("archive target transaction failed")
	}
	defer tx.Rollback()
	meta, err = readWriterMeta(ctx, tx, grant.Identity, true)
	if err != nil {
		return BatchResult{}, err
	}
	if err := requireWriter(meta, grant); err != nil {
		return BatchResult{}, err
	}
	if result, found, err := writerReceipt(ctx, tx, batch, payloadHash); err != nil || found {
		return result, err
	}
	var afterID int64
	var afterCreated sql.NullInt64
	var streamType string
	var cursorVersion int
	err = tx.QueryRowContext(ctx, `SELECT after_id,stream_type,cursor_version,after_created_unix FROM archive_checkpoints WHERE stream_key=? FOR UPDATE`, batch.stream()).Scan(&afterID, &streamType, &cursorVersion, &afterCreated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BatchResult{}, ErrWriterCheckpoint
	}
	if errors.Is(err, sql.ErrNoRows) {
		if err := requireWriterBaseline(ctx, tx); err != nil {
			return BatchResult{}, err
		}
		if batch.Scan != nil && (batch.BeforeID != 0 || batch.BeforeCreated != 0) {
			return BatchResult{}, ErrWriterCheckpoint
		}
	}
	if err == nil && (streamType != batch.streamType() || cursorVersion != 1 || (batch.Scan != nil && afterCreated.Int64 != batch.BeforeCreated)) {
		return BatchResult{}, ErrWriterCheckpoint
	}
	if afterID != batch.BeforeID {
		return BatchResult{}, ErrWriterCheckpoint
	}
	var old map[int64]writerState
	if batch.RawOnly {
		old, err = w.writerRawStates(ctx, tx, batch, ids)
	} else {
		old, err = writerReadStates(ctx, tx, ids, false)
	}
	if err != nil {
		return BatchResult{}, err
	}
	repairs, err := inspectRawRepairs(ctx, tx, batch, old, current, hashes)
	if err != nil {
		return BatchResult{}, err
	}
	daySet := map[string]bool{}
	cohortSet := map[string]bool{}
	changed := map[int64]bool{}
	for id, c := range current {
		rowDates := map[string]bool{}
		if c.Day != "undated" {
			daySet[c.Day] = true
			rowDates[c.Day] = true
		}
		if prev, ok := old[id]; ok {
			if prev.Day != "undated" {
				daySet[prev.Day] = true
				rowDates[prev.Day] = true
			}
			hash := hashes[id]
			changed[id] = !bytes.Equal(prev.hash, hash[:])
		} else {
			changed[id] = true
		}
		if repairs != nil {
			if repair, exists := repairs.repairs[id]; exists {
				changed[id] = true
				if repair.before.date != "" {
					daySet[repair.before.date] = true
					rowDates[repair.before.date] = true
				}
			}
		}
		// Only one source ID spanning dates forms a publication cohort.
		// A batch of unrelated new rows on many days must remain independent.
		if changed[id] && len(rowDates) > 1 {
			for date := range rowDates {
				cohortSet[date] = true
			}
		}
	}
	dates := make([]string, 0, len(daySet))
	for date := range daySet {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	// Meta -> dates (sorted) -> ID state is shared with future repair/seal writers.
	for _, date := range dates {
		if _, err := tx.ExecContext(ctx, `INSERT INTO archive_days(log_date,state,updated_at) VALUES(?,'collecting',UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE log_date=VALUES(log_date)`, date); err != nil {
			return BatchResult{}, errors.New("archive day initialization failed")
		}
		var frozen bool
		if err := tx.QueryRowContext(ctx, `SELECT freeze_task_id IS NOT NULL OR freeze_epoch IS NOT NULL OR freeze_revision IS NOT NULL OR freeze_until IS NOT NULL FROM archive_days WHERE log_date=? FOR UPDATE`, date).Scan(&frozen); err != nil {
			return BatchResult{}, errors.New("archive day lock failed")
		}
		if frozen {
			return BatchResult{}, ErrWriterFrozen
		}
	}
	locked, err := writerReadStates(ctx, tx, ids, true)
	if batch.RawOnly {
		locked = old
		err = nil
	} // dataset fence protects the raw ledger
	if err != nil {
		return BatchResult{}, err
	}
	if len(locked) != len(old) {
		return BatchResult{}, errors.New("archive contribution changed outside writer fence")
	}
	for id, prev := range old {
		now, ok := locked[id]
		left, _ := json.Marshal(prev.contribution)
		right, _ := json.Marshal(now.contribution)
		if !ok || !bytes.Equal(prev.hash, now.hash) || !bytes.Equal(left, right) {
			return BatchResult{}, errors.New("archive contribution changed outside writer fence")
		}
	}
	if err := repairs.lockAndCheck(ctx, tx, batch.Columns); err != nil {
		return BatchResult{}, err
	}
	if _, err := tx.ExecContext(ctx, "SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"); err != nil {
		return BatchResult{}, errors.New("archive target strict mode failed")
	}
	daily, monthly := deltas{}, deltas{}
	grouped := map[string][][]any{}
	ledger := [][]any{}
	mutatedDates := map[string]bool{}
	mutated := false
	unscopedDelta := int64(0)
	batchID, _ := archivecontract.IDBytes(batch.ID)
	for i, row := range batch.Rows {
		id := ids[i].(int64)
		next := current[id]
		var unscopedIssue int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_ingest_issues WHERE source_id=? AND error_code IN ('row_too_large','repair_unscoped_target') AND resolved_at IS NULL`, id).Scan(&unscopedIssue); err != nil {
			return BatchResult{}, err
		}
		if unscopedIssue > 0 {
			unscopedDelta--
			mutated = true
		}
		if _, err := tx.ExecContext(ctx, `UPDATE archive_ingest_issues SET resolved_at=UTC_TIMESTAMP(6) WHERE source_id=? AND resolved_at IS NULL`, id); err != nil {
			return BatchResult{}, err
		}
		if next.Blocking {
			if batch.RawOnly {
				return BatchResult{}, &scanError{code: "invalid_source_row", sourceID: id}
			}
			code := "unknown_charged_type"
			date := next.Day
			if date == "undated" {
				code = "invalid_timestamp"
				date = ""
			}
			if err := w.writeIngestIssue(ctx, tx, batch.stream(), date, &scanError{code: code, sourceID: id}); err != nil {
				return BatchResult{}, err
			}
		}
		if !changed[id] {
			continue
		}
		if repairs != nil {
			if repair, exists := repairs.repairs[id]; exists && repair.before.date != "" {
				mutatedDates[repair.before.date] = true
			}
		}
		mutated = true
		if batch.RawOnly {
			for _, date := range []string{next.Day, old[id].Day} {
				if date != "" && date != "undated" {
					if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO archive_pending_statistics(log_date,source_id) VALUES(?,?)`, date, id); err != nil {
						return BatchResult{}, err
					}
				}
			}
		}
		if prev, ok := old[id]; ok {
			daily.add(prev.Day, prev.contribution, -1)
			monthly.add(prev.month(), prev.contribution, -1)
			if prev.Day != "undated" {
				mutatedDates[prev.Day] = true
			}
			if prev.Blocking && len(prev.hash) == 32 {
				unscopedDelta--
			}
			if prev.month() != next.month() {
				// Hold the old table's metadata lock while checking its engine.
				// A manually changed historical table must not make DELETE escape
				// this transaction even though the new month's table is valid.
				var previousID int64
				if err := tx.QueryRowContext(ctx, "SELECT id FROM "+quote("logs_"+prev.month())+" WHERE id=? FOR UPDATE", id).Scan(&previousID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return BatchResult{}, errors.New("archive previous month lock failed")
				}
				var engine string
				if err := tx.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", "logs_"+prev.month()).Scan(&engine); err != nil || !strings.EqualFold(engine, "InnoDB") {
					return BatchResult{}, errors.New("archive previous month requires InnoDB")
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+quote("logs_"+prev.month())+" WHERE id=?", id); err != nil {
					return BatchResult{}, errors.New("archive previous month relocation failed")
				}
			}
		}
		daily.add(next.Day, next, 1)
		monthly.add(next.month(), next, 1)
		if next.Day != "undated" {
			mutatedDates[next.Day] = true
		}
		if next.Blocking {
			unscopedDelta++
		}
		grouped[next.month()] = append(grouped[next.month()], row)
		raw, _ := json.Marshal(next)
		hash := hashes[id]
		ledger = append(ledger, []any{id, string(raw), hash[:], batchID})
	}
	for _, month := range months {
		code := "write_archive_logs"
		if batch.RawOnly {
			code = "write_raw_logs"
		}
		reportOperation(ctx, code, "archive", "logs_"+month)
		if err := insertRows(ctx, tx, "logs_"+month, batch.Columns, grouped[month]); err != nil {
			return BatchResult{}, err
		}
	}
	ledgerTable := "archive_log_state"
	if batch.RawOnly {
		ledgerTable = "archive_raw_state"
		reportOperation(ctx, "save_raw_ledger", "archive", ledgerTable)
	} else {
		reportOperation(ctx, "rebuild_contributions", "archive", ledgerTable)
	}
	if err := insertRows(ctx, tx, ledgerTable, []string{"id", "contribution", "raw_row_hash", "last_batch_id"}, ledger); err != nil {
		return BatchResult{}, err
	}
	if !batch.RawOnly {
		if err := insertRows(ctx, tx, "archive_raw_state", []string{"id", "contribution", "raw_row_hash", "last_batch_id"}, ledger); err != nil {
			return BatchResult{}, err
		}
	}
	if !batch.RawOnly {
		reportOperation(ctx, "rebuild_daily_statistics", "archive", "log_daily_stats")
		if err := writeDeltas(ctx, tx, "log_daily_stats", daily); err != nil {
			return BatchResult{}, err
		}
		reportOperation(ctx, "rebuild_monthly_statistics", "archive", "log_monthly_stats")
		if err := writeDeltas(ctx, tx, "log_monthly_stats", monthly); err != nil {
			return BatchResult{}, err
		}
	} else {
		unscopedDelta = 0
	}
	// Verify every incoming row, including hash-identical rows, against the
	// target transaction. Out-of-band changes cannot silently pass a replay.
	verifyGroups := map[string][][]any{}
	for i, row := range batch.Rows {
		c := current[ids[i].(int64)]
		verifyGroups[c.month()] = append(verifyGroups[c.month()], row)
	}
	for _, month := range months {
		if err := verifyRows(ctx, tx, "logs_"+month, batch.Columns, verifyGroups[month]); err != nil {
			return BatchResult{}, err
		}
	}
	if err := repairs.writeAudit(ctx, tx, grant, batch); err != nil {
		return BatchResult{}, err
	}
	if mutated {
		if meta.revision == math.MaxUint64 {
			return BatchResult{}, errors.New("archive catalog revision exhausted")
		}
		meta.revision++
		if unscopedDelta < 0 && uint64(-unscopedDelta) > meta.unscoped {
			return BatchResult{}, errors.New("archive blocker count is inconsistent")
		}
		if unscopedDelta > 0 && uint64(unscopedDelta) > math.MaxUint64-meta.unscoped {
			return BatchResult{}, errors.New("archive blocker count exhausted")
		}
		if unscopedDelta < 0 {
			meta.unscoped -= uint64(-unscopedDelta)
		} else {
			meta.unscoped += uint64(unscopedDelta)
		}
		for _, date := range dates {
			if mutatedDates[date] {
				if _, err := tx.ExecContext(ctx, `UPDATE archive_days SET mutation_revision=mutation_revision+1,state='dirty',catalog_revision=?,updated_at=UTC_TIMESTAMP(6) WHERE log_date=?`, meta.revision, date); err != nil {
					return BatchResult{}, errors.New("archive day revision update failed")
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE archive_dataset_meta SET catalog_revision=?,unscoped_blocking_issues=?,updated_at=UTC_TIMESTAMP(6) WHERE singleton_id=1`, meta.revision, meta.unscoped); err != nil {
			return BatchResult{}, errors.New("archive catalog update failed")
		}
	}
	if err := syncPipelineMutation(ctx, tx, dates, mutatedDates, !batch.RawOnly, len(batch.Rows), batch.AfterID); err != nil {
		return BatchResult{}, err
	}
	if batch.Capture {
		if err := writeCopyEvidence(ctx, tx, batch); err != nil {
			return BatchResult{}, err
		}
	}
	if batch.Scan != nil {
		if err := w.writeScanCommit(ctx, tx, grant, batch, &meta, byteCount); err != nil {
			return BatchResult{}, err
		}
	}
	beforeJSON, _ := json.Marshal(writerCursor{AfterID: batch.BeforeID, AfterCreated: batch.BeforeCreated})
	afterJSON, _ := json.Marshal(writerCursor{AfterID: batch.AfterID, CatalogRevision: meta.revision, AfterCreated: batch.AfterCreated, Completed: batch.Completed})
	affectedDates := make([]string, 0, len(mutatedDates))
	for _, date := range dates {
		if mutatedDates[date] {
			affectedDates = append(affectedDates, date)
		}
	}
	affectedJSON, _ := json.Marshal(affectedDates)
	cohortDates := make([]string, 0, len(cohortSet))
	for _, date := range dates {
		if cohortSet[date] {
			cohortDates = append(cohortDates, date)
		}
	}
	cohortJSON, _ := json.Marshal(cohortDates)
	if _, err := tx.ExecContext(ctx, `INSERT INTO archive_batch_receipts(batch_id,stream_key,payload_hash,writer_epoch,cursor_before_json,cursor_after_json,row_count,byte_count,affected_dates_json,cohort_dates_json,committed_at) VALUES(?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, batchID, batch.stream(), payloadHash[:], grant.WriterEpoch, string(beforeJSON), string(afterJSON), len(batch.Rows), byteCount, string(affectedJSON), string(cohortJSON)); err != nil {
		return BatchResult{}, &scanError{code: "receipt_write_failed", cause: err}
	}
	var taskID any
	var from, to, created any
	if batch.Scan != nil {
		taskID, _ = archivecontract.IDBytes(batch.Scan.TaskID)
		from, to, _ = archivecontract.DateBounds(batch.Scan.Date)
		created = batch.AfterCreated
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO archive_checkpoints(stream_key,stream_type,task_id,from_unix,to_unix,after_created_unix,after_id,cursor_version,last_batch_id,updated_at) VALUES(?,?,?,?,?,?,?,1,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE after_created_unix=VALUES(after_created_unix),after_id=VALUES(after_id),last_batch_id=VALUES(last_batch_id),updated_at=VALUES(updated_at)`, batch.stream(), batch.streamType(), taskID, from, to, created, batch.AfterID, batchID); err != nil {
		return BatchResult{}, &scanError{code: "checkpoint_write_failed", cause: err}
	}
	result, found, err := writerReceipt(ctx, tx, batch, payloadHash)
	if err != nil || !found {
		return BatchResult{}, ErrWriterReceipt
	}
	result.Replayed = false
	if hooks.BeforeCommit != nil {
		if err := hooks.BeforeCommit(tx); err != nil {
			return BatchResult{}, err
		}
	}
	if err := writerGuardBeforeCommit(ctx, tx, grant); err != nil {
		return BatchResult{}, err
	}
	reportOperation(ctx, "commit_archive_batch", "archive", "")
	commitErr := tx.Commit()
	if commitErr == nil && hooks.AfterCommit != nil {
		commitErr = hooks.AfterCommit()
	}
	if commitErr == nil {
		return result, nil
	}
	// COMMIT errors are ambiguous. Consult the payload-bound target receipt;
	// never advance a local file or blindly replay statistics to guess success.
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if recovered, found, err := writerReceipt(recoveryCtx, w.target, batch, payloadHash); err == nil && found {
		return recovered, nil
	}
	return BatchResult{}, &scanError{code: "writer_commit_uncertain", cause: commitErr}
}
