package archivecontrol

import (
	af "controltower/internal/archivecontract"
	"time"
)

// Backfill progress is an operational report, never verification or billing evidence.
type BackfillRequest struct {
	RequestID string `json:"request_id"`
	Date      string `json:"date"`
}

type BackfillTaskItem struct {
	af.BackfillTask
	State         string             `json:"state"`
	ErrorCode     string             `json:"error_code,omitempty"`
	Progress      *af.BackfillStatus `json:"progress,omitempty"`
	RequestedBy   string             `json:"requested_by"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	FinishedAt    *time.Time         `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time         `json:"next_attempt_at,omitempty"`
}

type ArchiveCoveragePolicy struct {
	af.CoveragePolicy
	ConfirmedBy string     `json:"confirmed_by,omitempty"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

type ArchiveCoverageDay struct {
	Date        string            `json:"date"`
	State       string            `json:"state"`
	BlockReason string            `json:"block_reason,omitempty"`
	Task        *BackfillTaskItem `json:"task,omitempty"`
}

type ArchiveCoverageMonth struct {
	af.Identity
	Month          string                `json:"month"`
	Policy         ArchiveCoveragePolicy `json:"policy"`
	Days           []ArchiveCoverageDay  `json:"days"`
	ArchiveBilling bool                  `json:"archive_billing"`
	DayVersions    bool                  `json:"day_versions"`
}
