package archivecontract

import (
	"regexp"
	"time"
)

const CapabilityPipeline = "archive_four_tasks_v1"

func (s FoundationStatus) SupportsPipeline() bool {
	for _, c := range s.Capabilities {
		if c == CapabilityPipeline {
			return true
		}
	}
	return false
}

const CapabilityWorkflow = "full_history_workflow_v5"

// WorkflowStatus describes one persistent, whole-site archive operation.
// No user-selected date range participates in its identity.
type WorkflowStatus struct {
	Preparation   *WorkflowPreparationProgress `json:"preparation,omitempty"`
	Issues        []WorkflowIssue              `json:"issues,omitempty"`
	Phase         string                       `json:"phase"`
	Date          string                       `json:"date,omitempty"`
	FirstDate     string                       `json:"first_date,omitempty"`
	ImportedRows  uint64                       `json:"imported_rows,string"`
	CompletedDays uint64                       `json:"completed_days,string"`
	BlockedDays   uint64                       `json:"blocked_days,string"`
	ErrorCode     string                       `json:"error_code,omitempty"`
}

// WorkflowPreparationProgress records committed preparation work, including
// empty pages. Counters start when this version first observes the phase; they
// are not a historical total or an estimate of the work remaining.
type WorkflowPreparationProgress struct {
	Phase            string    `json:"phase"`
	Table            string    `json:"table,omitempty"`
	AfterID          int64     `json:"after_id,string"`
	ProcessedRows    uint64    `json:"processed_rows,string"`
	LastBatchRows    uint64    `json:"last_batch_rows,string"`
	CommittedBatches uint64    `json:"committed_batches,string"`
	RecordedSince    time.Time `json:"recorded_since"`
	LastCommittedAt  time.Time `json:"last_committed_at"`
}

var workflowMonthlyTable = regexp.MustCompile(`^logs_([0-9]{6}|undated)$`)

func (p WorkflowPreparationProgress) Validate() error {
	if p.AfterID < 0 || p.CommittedBatches == 0 || p.LastBatchRows > p.ProcessedRows || p.RecordedSince.IsZero() || p.LastCommittedAt.Before(p.RecordedSince) {
		return ErrConflict
	}
	switch p.Phase {
	case "reset_state", "reset_daily", "reset_monthly":
		table := map[string]string{"reset_state": "archive_log_state", "reset_daily": "log_daily_stats", "reset_monthly": "log_monthly_stats"}[p.Phase]
		if p.Table != table || p.AfterID != 0 {
			return ErrConflict
		}
	case "import_target":
		if p.Table != "" && !workflowMonthlyTable.MatchString(p.Table) || p.Table == "" && (p.AfterID != 0 || p.LastBatchRows != 0) {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	return nil
}

type WorkflowIssue struct {
	Date string `json:"date"`
	Code string `json:"code"`
}

func (s WorkflowStatus) Validate() error {
	if s.Preparation != nil && s.Preparation.Validate() != nil {
		return ErrConflict
	}
	if len(s.Issues) > 20 {
		return ErrConflict
	}
	for _, issue := range s.Issues {
		if _, _, err := DateBounds(issue.Date); err != nil || len(issue.Code) > 128 {
			return ErrConflict
		}
	}
	switch s.Phase {
	case "reset_state", "reset_daily", "reset_monthly", "import_target", "source_scan", "backfill", "verify", "seal", "live":
	default:
		return ErrConflict
	}
	for _, d := range []string{s.Date, s.FirstDate} {
		if d != "" {
			if _, _, err := DateBounds(d); err != nil {
				return err
			}
		}
	}
	if len(s.ErrorCode) > 128 {
		return ErrConflict
	}
	return nil
}

func (s FoundationStatus) SupportsWorkflow() bool {
	for _, capability := range s.Capabilities {
		if capability == CapabilityWorkflow {
			return true
		}
	}
	return false
}
