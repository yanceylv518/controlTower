package billing

import (
	"context"
	"errors"
	"time"
)

type AutomaticTarget struct {
	ProgressUntil time.Time `json:"-"`
	BatchID       string    `json:"batch_id,omitempty"`
	Overwrite     bool      `json:"overwrite,omitempty"`
	InstanceID    string    `json:"instance_id"`
	Kind          string    `json:"kind"`
	SubjectID     int64     `json:"subject_id"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to,omitempty"`
}
type AutomationStore interface {
	PutBillingAutomaticTarget(context.Context, AutomaticTarget) error
	ListBillingAutomaticTargets(context.Context) ([]AutomaticTarget, error)
	MissingBillingDays(context.Context, AutomaticTarget, time.Time) ([]time.Time, error)
}

func CompleteDayBoundary(now time.Time) time.Time {
	v := now.In(BusinessLocation)
	return time.Date(v.Year(), v.Month(), v.Day(), 0, 0, 0, 0, BusinessLocation)
}

var ErrDailyBillsIncomplete = errors.New("daily bills incomplete")
var ErrStatementNoData = errors.New("no consumption in billing period")

var ErrDailyCurrencyMismatch = errors.New("daily bill currencies or exchange rates differ")

type GenerationDay struct {
	Day       string     `json:"day"`
	Status    string     `json:"status"`
	JobID     string     `json:"job_id,omitempty"`
	Processed int64      `json:"processed"`
	Error     string     `json:"error,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}
type GenerationProgress struct {
	SubjectID   int64           `json:"subject_id"`
	Outcome     string          `json:"outcome"`
	TotalDays   int             `json:"total_days"`
	Checked     int             `json:"checked"`
	Complete    int             `json:"complete"`
	Empty       int             `json:"empty"`
	Pending     int             `json:"pending"`
	Running     int             `json:"running"`
	Failed      int             `json:"failed"`
	Processed   int64           `json:"processed"`
	Error       string          `json:"error,omitempty"`
	LastAttempt *time.Time      `json:"last_attempt,omitempty"`
	Days        []GenerationDay `json:"days"`
	Monthly     *GenerationDay  `json:"monthly,omitempty"`
}

var ErrGenerationInProgress = errors.New("billing generation in progress")

type GenerationState struct {
	Busy    bool              `json:"busy"`
	Targets []AutomaticTarget `json:"targets"`
	Jobs    []Job             `json:"jobs"`
}

var ErrGenerationCancelled = errors.New("billing generation cancelled")

type GenerationTask struct {
	ID             string               `json:"id"`
	InstanceID     string               `json:"instance_id"`
	Kind           string               `json:"kind"`
	From           time.Time            `json:"from"`
	To             time.Time            `json:"to"`
	WorkUntil      time.Time            `json:"work_until"`
	Source         string               `json:"source"`
	Overwrite      bool                 `json:"overwrite"`
	SubjectIDs     []int64              `json:"subject_ids"`
	CreatedAt      time.Time            `json:"created_at"`
	Outcome        string               `json:"outcome"`
	Percentage     int                  `json:"percentage"`
	CompletedUsers int                  `json:"completed_users"`
	FailedUsers    int                  `json:"failed_users"`
	CompleteDays   int                  `json:"complete_days"`
	EmptyDays      int                  `json:"empty_days"`
	Items          []GenerationProgress `json:"items,omitempty"`
}
