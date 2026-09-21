package archivecontrol

import (
	"time"

	af "controltower/internal/archivecontract"
)

// These records describe an operational queue. Their status does not authorize
// billing; only the Server's independent archive reader synchronizes evidence.
type ReconcileRequest struct {
	RequestID string                   `json:"request_id"`
	Date      string                   `json:"date"`
	Assurance af.VerificationAssurance `json:"assurance"`
}

type SealRequest struct {
	RequestID string   `json:"request_id"`
	Dates     []string `json:"dates"`
}

type ArchiveTaskMetadata struct {
	State         string     `json:"state"`
	ErrorCode     string     `json:"error_code,omitempty"`
	RequestedBy   string     `json:"requested_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
}

type ReconcileTaskItem struct {
	af.ReconcileTask
	ArchiveTaskMetadata
	Progress *af.ReconcileStatus `json:"progress,omitempty"`
}

type SealTaskItem struct {
	af.SealTask
	ArchiveTaskMetadata
	Progress *af.SealStatus `json:"progress,omitempty"`
}
