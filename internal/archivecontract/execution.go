package archivecontract

import (
	"regexp"
	"time"
)

// Operation is an observed worker action, not inferred from a workflow cursor.
// It is transient and must never serve as evidence of a committed batch.
type Operation struct {
	Phase      string     `json:"phase"`
	Code       string     `json:"code"`
	Database   string     `json:"database"`
	Table      string     `json:"table,omitempty"`
	Date       string     `json:"date,omitempty"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Diagnostic deliberately excludes SQL, driver messages, DSNs and row payloads.
type Diagnostic struct {
	Code        string     `json:"code"`
	Operation   *Operation `json:"operation,omitempty"`
	MySQLNumber uint16     `json:"mysql_number,omitempty"`
	SQLState    string     `json:"sql_state,omitempty"`
	RowID       int64      `json:"row_id,string,omitempty"`
	RowBytes    uint64     `json:"row_bytes,string,omitempty"`
	OccurredAt  time.Time  `json:"occurred_at"`
	RetryAt     *time.Time `json:"retry_at,omitempty"`
}

var executionIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
var executionSQLState = regexp.MustCompile(`^[A-Z0-9]{5}$`)

func (o Operation) Validate() error {
	if !executionIdentifier.MatchString(o.Phase) || !executionIdentifier.MatchString(o.Code) || (o.Table != "" && !executionIdentifier.MatchString(o.Table)) || o.StartedAt.IsZero() {
		return ErrConflict
	}
	if o.Database != "source" && o.Database != "archive" && o.Database != "both" && o.Database != "control" {
		return ErrConflict
	}
	if o.Date != "" {
		if _, _, err := DateBounds(o.Date); err != nil {
			return err
		}
	}
	if o.State != "executing" && o.State != "completed" && o.State != "failed" {
		return ErrConflict
	}
	if o.State == "executing" && o.FinishedAt != nil || o.State != "executing" && o.FinishedAt == nil {
		return ErrConflict
	}
	if o.FinishedAt != nil && o.FinishedAt.Before(o.StartedAt) {
		return ErrConflict
	}
	return nil
}

func (d Diagnostic) Validate() error {
	if !executionIdentifier.MatchString(d.Code) || d.OccurredAt.IsZero() || d.RowID < 0 || d.Operation != nil && d.Operation.Validate() != nil || d.SQLState != "" && !executionSQLState.MatchString(d.SQLState) {
		return ErrConflict
	}
	if d.RetryAt != nil && d.RetryAt.Before(d.OccurredAt) {
		return ErrConflict
	}
	return nil
}
