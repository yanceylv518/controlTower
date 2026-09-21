package archivecontract

const CapabilityWorkflow = "full_history_workflow_v5"

// WorkflowStatus describes one persistent, whole-site archive operation.
// No user-selected date range participates in its identity.
type WorkflowStatus struct {
	Issues        []WorkflowIssue `json:"issues,omitempty"`
	Phase         string          `json:"phase"`
	Date          string          `json:"date,omitempty"`
	FirstDate     string          `json:"first_date,omitempty"`
	ImportedRows  uint64          `json:"imported_rows,string"`
	CompletedDays uint64          `json:"completed_days,string"`
	BlockedDays   uint64          `json:"blocked_days,string"`
	ErrorCode     string          `json:"error_code,omitempty"`
}

type WorkflowIssue struct {
	Date string `json:"date"`
	Code string `json:"code"`
}

func (s WorkflowStatus) Validate() error {
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
