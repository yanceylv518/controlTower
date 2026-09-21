package archivecontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"
)

// CanonicalManifest uses deterministic object-key order and preserves numeric
// lexemes; producers use decimal strings for large business integers.
func CanonicalManifest(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF {
		return nil, ErrConflict
	}
	return json.Marshal(v)
}
func ManifestHash(raw []byte) (string, error) {
	b, e := CanonicalManifest(raw)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type VerificationRun struct {
	RunID         string                `json:"run_id"`
	TaskID        string                `json:"task_id"`
	Attempt       int                   `json:"attempt"`
	State         string                `json:"state"`
	Phase         string                `json:"phase"`
	Method        string                `json:"method"`
	StartRevision uint64                `json:"start_revision,string"`
	FinalRevision *uint64               `json:"final_revision,string,omitempty"`
	IssueCount    uint64                `json:"issue_count,string"`
	Summary       json.RawMessage       `json:"summary"`
	Assurance     VerificationAssurance `json:"assurance"`
	StartedAt     time.Time             `json:"started_at"`
	CompletedAt   *time.Time            `json:"completed_at,omitempty"`
	ErrorCode     string                `json:"error_code,omitempty"`
}
type VerificationIssue struct {
	SourceID   int64  `json:"source_id,string"`
	Kind       string `json:"kind"`
	SourceHash string `json:"source_hash,omitempty"`
	TargetHash string `json:"target_hash,omitempty"`
}
type PublishedDayVersion struct {
	SealedDay
	IsCurrent        bool            `json:"is_current"`
	MutationRevision uint64          `json:"mutation_revision,string"`
	PublishRevision  uint64          `json:"publish_revision,string"`
	PublishedAt      time.Time       `json:"published_at"`
	Manifest         json.RawMessage `json:"manifest"`
}
type DayVerification struct {
	Identity
	Date           string                `json:"date"`
	DayState       string                `json:"day_state"`
	Runs           []VerificationRun     `json:"runs"`
	SelectedRunID  string                `json:"selected_run_id,omitempty"`
	Issues         []VerificationIssue   `json:"issues"`
	NextIssueID    *int64                `json:"next_issue_id,string,omitempty"`
	Versions       []PublishedDayVersion `json:"versions"`
	ArchiveBilling bool                  `json:"archive_billing"`
}
