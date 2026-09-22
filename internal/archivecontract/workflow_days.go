package archivecontract

import (
	"regexp"
	"time"
)

const WorkflowDayPageSize = 100

// These are observed derived counts, not a certificate of source completeness.
type WorkflowDayCounts struct {
	LogRows     string `json:"log_rows"`
	RequestRows string `json:"request_rows"`
	ErrorRows   string `json:"error_rows"`
}

type WorkflowDay struct {
	Diagnostic *Diagnostic        `json:"diagnostic,omitempty"`
	Raw        *RawDayCount       `json:"raw,omitempty"`
	Date       string             `json:"date"`
	State      string             `json:"state"`
	Counts     *WorkflowDayCounts `json:"counts,omitempty"`
	ErrorCode  string             `json:"error_code,omitempty"`
	UpdatedAt  *time.Time         `json:"updated_at,omitempty"`
	ObservedAt time.Time          `json:"observed_at"`
}

// RawDayCount is independent of derived contribution/statistics rebuilding.
// Rows is nil until a bounded exact count succeeds; an error may accompany an
// older retained snapshot. ObservedAt is the count's time, not the heartbeat.
type RawDayCount struct {
	Rows       *string    `json:"rows,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	ErrorCode  string     `json:"error_code,omitempty"`
}

// A bounded, rotating snapshot of the archive's small per-day tables. It does
// not query source logs or change the archive cursor. Raw counts are supplied
// separately by a bounded, cached monthly index scan.
type WorkflowDayPage struct {
	TaskID     string        `json:"task_id"`
	Days       []WorkflowDay `json:"days,omitempty"`
	NextAfter  string        `json:"next_after,omitempty"`
	ObservedAt time.Time     `json:"observed_at"`
}

var workflowCount = regexp.MustCompile(`^[0-9]{1,38}$`)

func (p WorkflowDayPage) Validate() error {
	if _, err := IDBytes(p.TaskID); err != nil || p.ObservedAt.IsZero() || len(p.Days) > WorkflowDayPageSize {
		return ErrConflict
	}
	if p.NextAfter != "" {
		if _, _, err := DateBounds(p.NextAfter); err != nil {
			return err
		}
	}
	previous := ""
	for _, d := range p.Days {
		if d.Diagnostic != nil && d.Diagnostic.Validate() != nil {
			return ErrConflict
		}
		if d.Raw != nil && (len(d.Raw.ErrorCode) > 128 || (d.Raw.Rows == nil) != (d.Raw.ObservedAt == nil) || (d.Raw.Rows != nil && (!workflowCount.MatchString(*d.Raw.Rows) || d.Raw.ObservedAt.IsZero() || d.Raw.ObservedAt.After(p.ObservedAt)))) {
			return ErrConflict
		}
		if _, _, err := DateBounds(d.Date); err != nil || d.Date <= previous || !d.ObservedAt.Equal(p.ObservedAt) || len(d.ErrorCode) > 128 {
			return ErrConflict
		}
		switch d.State {
		case "collecting", "waiting_migration", "organization", "verification", "organized", "changed", "collected", "unknown", "preparing", "pending", "rebuilding", "backfill", "verify", "seal", "sealed", "blocked":
		default:
			return ErrConflict
		}
		if d.Counts != nil && (!workflowCount.MatchString(d.Counts.LogRows) || !workflowCount.MatchString(d.Counts.RequestRows) || !workflowCount.MatchString(d.Counts.ErrorRows)) {
			return ErrConflict
		}
		previous = d.Date
	}
	if p.NextAfter != "" && (len(p.Days) == 0 || p.NextAfter != previous) {
		return ErrConflict
	}
	return nil
}
