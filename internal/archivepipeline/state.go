// Package archivepipeline defines the next archive scheduler's durable state
// transitions. It is not enabled by the legacy full-history runner. Callers must
// persist each transition in the same fenced transaction as its associated data
// changes; a heartbeat or an in-memory worker result is never a commit receipt.
package archivepipeline

import (
	af "controltower/internal/archivecontract"
	"errors"
	"math"
	"sort"
	"time"
)

type Task string

const (
	Capability        = "archive_four_tasks_v1"
	Migration    Task = "migration"
	Organization Task = "organization"
	Verification Task = "verification"
	Collection   Task = "collection"
)

type Status struct {
	CollectionDone bool              `json:"collection_done"`
	CollectionDate string            `json:"collection_date,omitempty"`
	MigrationDone  bool              `json:"migration_done"`
	Cutoff         string            `json:"cutoff"`
	Active         map[Task]Work     `json:"active"`
	Errors         map[Task]string   `json:"errors,omitempty"`
	Settings       Settings          `json:"settings"`
	Progress       map[Task]Progress `json:"progress,omitempty"`
}

type Progress struct {
	Operation  *af.Operation  `json:"operation,omitempty"`
	Diagnostic *af.Diagnostic `json:"diagnostic,omitempty"`
	AfterID    int64          `json:"after_id,string"`
	Rows       uint64         `json:"rows,string"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func (s Status) Validate() error {
	if !s.Settings.Validate() || (s.CollectionDate != "" && !validDate(s.CollectionDate)) || !validDate(s.Cutoff) || len(s.Active) > 2 || len(s.Errors) > 4 || len(s.Progress) > 4 {
		return ErrConflict
	}
	for task, w := range s.Active {
		if !validTask(task) || w.Task != task || w.Token == 0 || (w.Date != "" && !validDate(w.Date)) {
			return ErrConflict
		}
	}
	if _, ok := s.Active[Verification]; ok && len(s.Active) != 1 {
		return ErrConflict
	}
	for task, code := range s.Errors {
		if !validTask(task) || len(code) > 128 {
			return ErrConflict
		}
	}
	for task, p := range s.Progress {
		if !validTask(task) || p.AfterID < 0 || p.UpdatedAt.IsZero() || p.Operation != nil && p.Operation.Validate() != nil || p.Diagnostic != nil && p.Diagnostic.Validate() != nil {
			return ErrConflict
		}
	}
	return nil
}
func validTask(t Task) bool {
	return t == Migration || t == Organization || t == Verification || t == Collection
}

var (
	ErrConflict = errors.New("archive_pipeline_conflict")
	ErrNotReady = errors.New("archive_pipeline_not_ready")
	ErrStale    = errors.New("archive_pipeline_revision_changed")
)

// Settings are independent task switches. A pause prevents new work, but does
// not revoke a transaction already in flight. Date ordering is always oldest
// first for organization/verification, regardless of collection's own cursor.
type Settings struct {
	RetryToken            string `json:"retry_token,omitempty"`
	Migration             bool   `json:"migration"`
	Organization          bool   `json:"organization"`
	Verification          bool   `json:"verification"`
	Collection            bool   `json:"collection"`
	CollectionFrom        string `json:"collection_from,omitempty"`
	CollectionThrough     string `json:"collection_through,omitempty"`
	CollectionNewestFirst bool   `json:"collection_newest_first,omitempty"`
}

func (s Settings) Validate() bool {
	if s.RetryToken != "" {
		if _, err := af.IDBytes(s.RetryToken); err != nil {
			return false
		}
	}
	if s.CollectionFrom == "" && s.CollectionThrough == "" {
		return !s.CollectionNewestFirst
	}
	return validDate(s.CollectionFrom) && validDate(s.CollectionThrough) && s.CollectionFrom <= s.CollectionThrough
}
func (s Settings) SameRange(other Settings) bool {
	return s.CollectionFrom == other.CollectionFrom && s.CollectionThrough == other.CollectionThrough && s.CollectionNewestFirst == other.CollectionNewestFirst
}
func (s Settings) enabled(task Task) bool {
	switch task {
	case Migration:
		return s.Migration
	case Organization:
		return s.Organization
	case Verification:
		return s.Verification
	case Collection:
		return s.Collection
	}
	return false
}

// Work is a fenced reservation, not proof that SQL is currently executing.
// Tokens remain unique across restarts. Never release a reservation just because
// a wall-clock timeout elapsed: first fence the old database writer.
type Work struct {
	Token    uint64 `json:"token,string"`
	Task     Task   `json:"task"`
	Date     string `json:"date,omitempty"`
	Revision uint64 `json:"revision,string"`
}

type Result struct {
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	Diagnostic  *af.Diagnostic `json:"diagnostic,omitempty"`
	Round       uint64         `json:"round,string"`
	Revision    uint64         `json:"revision,string"`
	Code        string         `json:"code,omitempty"`
	SealVersion string         `json:"seal_version,omitempty"`
}

type Day struct {
	Revision uint64 `json:"revision,string"`
	// Collected is a round boundary, not a promise that no late rows can arrive.
	Collected         bool    `json:"collected"`
	OrganizedRevision uint64  `json:"organized_revision,string"`
	Result            *Result `json:"result,omitempty"`
	// Keep the immutable last published version even when Result is invalidated.
	LastSeal *Result `json:"last_seal,omitempty"`
}

type State struct {
	Version       int      `json:"version"`
	Settings      Settings `json:"settings"`
	SchemaReady   bool     `json:"schema_ready"`
	MigrationDone bool     `json:"migration_done"`
	Round         uint64   `json:"round,string"`
	Cutoff        string   `json:"cutoff,omitempty"`
	// Frontier is the greatest date with an observed, committed raw log row.
	// Table existence and empty next-month tables must never advance it.
	Frontier  string          `json:"frontier,omitempty"`
	NextToken uint64          `json:"next_token,string"`
	Days      map[string]*Day `json:"days"`
	Active    map[Task]Work   `json:"active"`
}

func New(settings Settings) State {
	return State{Version: 1, Settings: settings, Days: map[string]*Day{}, Active: map[Task]Work{}}
}

func validDate(date string) bool {
	d, err := time.Parse("2006-01-02", date)
	return err == nil && d.Format("2006-01-02") == date && date >= "1970-01-01" && date <= "9998-12-31"
}

// BeginRound fixes yesterday in the site's time zone. Midnight during a long
// run cannot silently extend its scope. A new round retries unsuccessful dates.
func (s *State) BeginRound(now time.Time, location *time.Location) error {
	if location == nil || now.IsZero() || len(s.Active) != 0 || s.Round == math.MaxUint64 {
		return ErrConflict
	}
	cutoff := now.In(location).AddDate(0, 0, -1).Format("2006-01-02")
	if !validDate(cutoff) {
		return ErrConflict
	}
	s.Round++
	s.Cutoff = cutoff
	return nil
}

// ObserveCommittedRows must run in the raw-write transaction. changed contains
// both old and new dates for a moved row. Identical retries have changed=false.
// Already-stored legacy rows may be observed without inventing a source cursor.
func (s *State) ObserveCommittedRows(observed []string, changed map[string]bool) error {
	if s.Days == nil {
		return ErrConflict
	}
	for _, date := range observed {
		if !validDate(date) {
			return ErrConflict
		}
	}
	for date, mutation := range changed {
		if !validDate(date) {
			return ErrConflict
		}
		if d := s.Days[date]; mutation && d != nil && d.Revision == math.MaxUint64 {
			return ErrConflict
		}
	}
	for _, date := range observed {
		if date > s.Frontier {
			s.Frontier = date
		}
		if s.Days[date] == nil {
			s.Days[date] = &Day{Revision: 1}
		}
	}
	for date, mutation := range changed {
		if !mutation {
			continue
		}
		d := s.Days[date]
		if d == nil {
			d = &Day{}
			s.Days[date] = d
		}
		d.Revision++
		// Invalidate immediately, not when a later verification happens to fail.
		d.OrganizedRevision = 0
		d.Result = nil
	}
	for date, d := range s.Days {
		d.Collected = date < s.Frontier
	}
	return nil
}

// NextDate returns the earliest eligible date not finished for this revision
// in this round. A recorded date-specific failure allows the next date to run.
// Waiting for collection is not a failure and must leave collection runnable.
func (s *State) NextDate() string {
	var dates []string
	for date, d := range s.Days {
		if date > s.Cutoff || !d.Collected {
			continue
		}
		if r := d.Result; r != nil && r.Revision == d.Revision && (r.SealVersion != "" || r.Round == s.Round) {
			continue
		}
		dates = append(dates, date)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return ""
	}
	return dates[0]
}

// Reserve enforces exclusion even when separate goroutines ask concurrently
// through the transactional store. Verification reserves the source slot for
// its entire date operation, including target-only verification and publication.
func (s *State) Reserve(task Task) (Work, error) {
	if !s.Settings.enabled(task) || !s.SchemaReady {
		return Work{}, ErrNotReady
	}
	if _, ok := s.Active[task]; ok {
		return Work{}, ErrConflict
	}
	if s.NextToken == math.MaxUint64 || s.Active == nil {
		return Work{}, ErrConflict
	}
	w := Work{Task: task}
	switch task {
	case Migration:
		if s.MigrationDone {
			return Work{}, ErrNotReady
		}
	case Collection:
		if _, ok := s.Active[Verification]; ok {
			return Work{}, ErrNotReady
		}
		// Give a ready verifier the next source turn after a collection batch.
		date := s.NextDate()
		if date != "" && s.MigrationDone && s.Settings.Verification && s.Days[date].OrganizedRevision == s.Days[date].Revision {
			return Work{}, ErrNotReady
		}
	case Organization, Verification:
		if !s.MigrationDone || s.Round == 0 {
			return Work{}, ErrNotReady
		}
		if _, ok := s.Active[Organization]; ok {
			return Work{}, ErrNotReady
		}
		if _, ok := s.Active[Verification]; ok {
			return Work{}, ErrNotReady
		}
		w.Date = s.NextDate()
		if w.Date == "" {
			return Work{}, ErrNotReady
		}
		d := s.Days[w.Date]
		w.Revision = d.Revision
		if task == Organization && d.OrganizedRevision == d.Revision {
			return Work{}, ErrNotReady
		}
		if task == Verification {
			if d.OrganizedRevision != d.Revision {
				return Work{}, ErrNotReady
			}
			if _, ok := s.Active[Collection]; ok {
				return Work{}, ErrNotReady
			}
		}
	default:
		return Work{}, ErrConflict
	}
	s.NextToken++
	w.Token = s.NextToken
	s.Active[task] = w
	return w, nil
}

func (s *State) owns(work Work) bool {
	current, ok := s.Active[work.Task]
	return ok && current == work && work.Token != 0
}

// Finish is committed atomically with the task's checkpoint and, for a seal,
// publication. A revision mismatch returns ErrStale without changing anything;
// the transaction must roll back. Release afterwards to requeue fresh work.
func (s *State) Finish(work Work, code, sealVersion string) error {
	if !s.owns(work) || len(code) > 128 || len(sealVersion) > 128 || (code != "" && sealVersion != "") {
		return ErrConflict
	}
	if work.Task != Verification && sealVersion != "" {
		return ErrConflict
	}
	now := time.Now().UTC()
	if work.Date != "" {
		d := s.Days[work.Date]
		if d == nil || d.Revision != work.Revision {
			return ErrStale
		}
		if code != "" {
			d.Result = &Result{CompletedAt: &now, Round: s.Round, Revision: d.Revision, Code: code}
		} else if work.Task == Organization {
			d.OrganizedRevision = d.Revision
		} else {
			if sealVersion == "" {
				return ErrConflict
			}
			d.Result = &Result{CompletedAt: &now, Round: s.Round, Revision: d.Revision, SealVersion: sealVersion}
			last := *d.Result
			d.LastSeal = &last
		}
	} else if code != "" {
		// Dataset-wide failures are not a reason to finish or skip dates.
		return ErrConflict
	} else if work.Task == Migration {
		s.MigrationDone = true
	}
	delete(s.Active, work.Task)
	return nil
}

// Release is for a completed/rolled-back batch, pause, or infrastructure error.
// It does not mark a date attempted and must not run while its SQL can commit.
func (s *State) Release(work Work) error {
	if !s.owns(work) {
		return ErrConflict
	}
	delete(s.Active, work.Task)
	return nil
}

// RecoverAfterFence is only legal AFTER the dataset writer epoch has advanced
// and old writers are unable to commit. Progress and migration completion stay.
func (s *State) RecoverAfterFence() { s.Active = map[Task]Work{} }

func (s *State) DayState(date string) string {
	d := s.Days[date]
	if d == nil {
		return "unknown"
	}
	if !d.Collected {
		return "collecting"
	}
	if !s.MigrationDone {
		return "waiting_migration"
	}
	for _, task := range []Task{Organization, Verification} {
		if w, ok := s.Active[task]; ok && w.Date == date && w.Revision == d.Revision {
			return string(task)
		}
	}
	if r := d.Result; r != nil && r.Revision == d.Revision {
		if r.SealVersion != "" {
			return "sealed"
		}
		return "blocked"
	}
	if d.OrganizedRevision == d.Revision {
		return "organized"
	}
	if d.LastSeal != nil {
		return "changed"
	}
	return "collected"
}
