package logarchive

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	af "controltower/internal/archivecontract"
)

type scanError struct {
	code     string
	sourceID int64
	bytes    uint64
}

func (e *scanError) Error() string { return "archive: " + e.code }

func (w *Worker) SetScanBudget(b af.ScanBudget) { w.budget = b }
func (w *Worker) readBudget() af.ScanBudget {
	b := w.budget
	if b.Validate() != nil {
		b = af.DefaultScanBudget()
	}
	if w.batchSize > 0 && w.batchSize < b.MaxRows {
		b.MaxRows = w.batchSize
	}
	return b
}

func (w *Worker) effectiveScanBudget(b af.ScanBudget) af.ScanBudget {
	current := w.readBudget()
	if current.MaxRows < b.MaxRows {
		b.MaxRows = current.MaxRows
	}
	if current.MaxBytes < b.MaxBytes {
		b.MaxBytes = current.MaxBytes
	}
	if current.MaxRowBytes < b.MaxRowBytes {
		b.MaxRowBytes = current.MaxRowBytes
	}
	if current.MaxDurationMillis < b.MaxDurationMillis {
		b.MaxDurationMillis = current.MaxDurationMillis
	}
	return b
}

func scanFailureCode(err error) string {
	var se *scanError
	if errors.As(err, &se) {
		return se.code
	}
	switch {
	case errors.Is(err, ErrWriterLease):
		return "writer_lease"
	case errors.Is(err, ErrWriterFrozen):
		return "date_frozen"
	case errors.Is(err, ErrWriterCheckpoint):
		return "checkpoint_conflict"
	case errors.Is(err, ErrWriterReceipt):
		return "receipt_conflict"
	case errors.Is(err, ErrFoundationSchema), errors.Is(err, ErrFoundationNotPrepared):
		return "schema_changed"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "budget_exhausted"
	default:
		return "scan_failed"
	}
}

// A page stops before consuming the next cursor when its decoded byte budget
// is full. An oversized single row blocks that exact cursor, never skips it.
func readWriterPage(ctx context.Context, rows *sql.Rows, batch *writerBatch, b af.ScanBudget, sourceNow, delay int64, started time.Time) (uint64, bool, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return 0, false, &scanError{code: "source_query_failed"}
	}
	batch.Columns = columns
	idIndex, createdIndex := -1, -1
	for i, c := range columns {
		if c == "id" {
			idIndex = i
		}
		if c == "created_at" {
			createdIndex = i
		}
	}
	if idIndex < 0 || createdIndex < 0 {
		return 0, false, &scanError{code: "invalid_source_row"}
	}
	var readBytes uint64
	exhausted := true
	for rows.Next() {
		if len(batch.Rows) >= b.MaxRows || time.Since(started) > time.Duration(b.MaxDurationMillis)*time.Millisecond*3/4 {
			batch.Limited = true
			exhausted = false
			break
		}
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(raw))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return readBytes, false, &scanError{code: "source_query_failed"}
		}
		id, err := strconv.ParseInt(string(raw[idIndex]), 10, 64)
		if err != nil || id <= 0 {
			return readBytes, false, &scanError{code: "invalid_source_row"}
		}
		var ts int64
		if raw[createdIndex] != nil {
			ts, err = strconv.ParseInt(string(raw[createdIndex]), 10, 64)
			if err != nil {
				return readBytes, false, &scanError{code: "invalid_timestamp", sourceID: id}
			}
		}
		if ts > sourceNow-delay {
			exhausted = false
			break
		}
		var size uint64
		for _, v := range raw {
			size += uint64(len(v))
		}
		if size > b.MaxRowBytes {
			return readBytes, false, &scanError{code: "row_too_large", sourceID: id, bytes: size}
		}
		if readBytes+size > b.MaxBytes {
			batch.Limited = true
			exhausted = false
			break
		}
		if batch.Scan == nil {
			if id <= batch.AfterID {
				return readBytes, false, ErrWriterCheckpoint
			}
		} else if ts < batch.AfterCreated || (ts == batch.AfterCreated && id <= batch.AfterID) {
			return readBytes, false, ErrWriterCheckpoint
		}
		values := make([]any, len(raw))
		for i, v := range raw {
			if v != nil {
				values[i] = string(v)
			}
		}
		batch.Rows = append(batch.Rows, values)
		batch.AfterID = id
		batch.AfterCreated = ts
		readBytes += size
	}
	if rows.Err() != nil {
		return readBytes, false, &scanError{code: "source_query_failed"}
	}
	return readBytes, exhausted, nil
}
