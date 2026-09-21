package archivecontract

import "strings"

const CapabilityReconcile = "daily_verify_v4"
const CapabilitySeal = "day_seal_v4"

// VerificationAssurance is an explicit site operator assertion, not a property
// inferred from matching scans. Rows before StableBeforeUnix must remain
// immutable and retained until ValidUntilUnix, including historical corrections.
type VerificationAssurance struct {
	StableBeforeUnix int64  `json:"stable_before_unix,string"`
	ValidUntilUnix   int64  `json:"valid_until_unix,string"`
	Evidence         string `json:"evidence"`
}

func (a VerificationAssurance) Validate() error {
	if a.StableBeforeUnix <= 0 || a.ValidUntilUnix <= a.StableBeforeUnix || strings.TrimSpace(a.Evidence) == "" || len(a.Evidence) > 2048 {
		return ErrConflict
	}
	return nil
}
func (a VerificationAssurance) Covers(date string, now int64) bool {
	_, end, err := DateBounds(date)
	return err == nil && a.Validate() == nil && a.StableBeforeUnix >= end && now >= end && now < a.ValidUntilUnix
}

type ReconcileTask struct {
	Identity
	TaskID    string                `json:"task_id"`
	Date      string                `json:"date"`
	Attempt   int                   `json:"attempt"`
	Policy    CoveragePolicy        `json:"policy"`
	Assurance VerificationAssurance `json:"assurance"`
}

func (t ReconcileTask) Validate() error {
	if t.Identity.Validate() != nil || t.Policy.Validate() != nil || t.Assurance.Validate() != nil || t.Attempt < 1 || t.Attempt > 2147483647 {
		return ErrConflict
	}
	if _, err := IDBytes(t.TaskID); err != nil {
		return err
	}
	_, end, err := DateBounds(t.Date)
	if err != nil || t.Assurance.StableBeforeUnix < end {
		return ErrConflict
	}
	return nil
}

type ReconcileStatus struct {
	TaskID          string `json:"task_id"`
	RunID           string `json:"run_id,omitempty"`
	Date            string `json:"date"`
	Attempt         int    `json:"attempt"`
	WriterEpoch     uint64 `json:"writer_epoch,string"`
	ProgressVersion uint64 `json:"progress_version,string"`
	StartRevision   uint64 `json:"start_revision,string"`
	FinalRevision   uint64 `json:"final_revision,string"`
	SourceRows      uint64 `json:"source_rows,string"`
	TargetRows      uint64 `json:"target_rows,string"`
	IssueCount      uint64 `json:"issue_count,string"`
	ReadBytes       uint64 `json:"read_bytes,string"`
	ElapsedMillis   uint64 `json:"elapsed_millis,string"`
	CatalogRevision uint64 `json:"catalog_revision,string"`
	State           string `json:"state"`
	Phase           string `json:"phase"`
	Method          string `json:"method"`
	SourceDigest    string `json:"source_digest,omitempty"`
	TargetDigest    string `json:"target_digest,omitempty"`
	ErrorCode       string `json:"error_code,omitempty"`
	RetryAfterUnix  int64  `json:"retry_after_unix,string"`
}

func (s ReconcileStatus) Validate() error {
	if _, err := IDBytes(s.TaskID); err != nil {
		return err
	}
	if _, _, err := DateBounds(s.Date); err != nil {
		return err
	}
	if s.Attempt < 1 || s.Attempt > 2147483647 || s.WriterEpoch == 0 || s.RetryAfterUnix < 0 || !ValidVerifyError(s.ErrorCode) {
		return ErrConflict
	}
	if s.RunID != "" {
		if _, err := IDBytes(s.RunID); err != nil {
			return err
		}
	}
	if s.Method != "stable_window_paged" && s.Method != "copied_source_paged" {
		return ErrConflict
	}
	if s.Method == "copied_source_paged" && s.Phase != "target_second" && s.Phase != "completed" {
		return ErrConflict
	}
	switch s.Phase {
	case "source_first", "target_first", "source_second", "target_second", "completed":
	default:
		return ErrConflict
	}
	switch s.State {
	case "running":
		if s.Phase == "completed" {
			return ErrConflict
		}
	case "matched", "mismatched":
		if s.RunID == "" || s.Phase != "completed" || s.ProgressVersion == 0 {
			return ErrConflict
		}
	case "blocked", "retry_wait":
		if s.ErrorCode == "" {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	for _, h := range []string{s.SourceDigest, s.TargetDigest} {
		if h != "" && !hashPattern.MatchString(h) {
			return ErrConflict
		}
	}
	if s.State == "matched" && (s.ErrorCode != "" || s.SourceDigest == "" || s.SourceDigest != s.TargetDigest || s.SourceRows != s.TargetRows || s.IssueCount != 0 || s.StartRevision != s.FinalRevision) {
		return ErrConflict
	}
	return nil
}

type SealTask struct {
	Identity
	TaskID  string         `json:"task_id"`
	Dates   []string       `json:"dates"`
	Attempt int            `json:"attempt"`
	Policy  CoveragePolicy `json:"policy"`
}

func (t SealTask) Validate() error {
	if t.Identity.Validate() != nil || t.Policy.Validate() != nil || t.Attempt < 1 || t.Attempt > 2147483647 || len(t.Dates) < 1 || len(t.Dates) > 31 {
		return ErrConflict
	}
	if _, err := IDBytes(t.TaskID); err != nil {
		return err
	}
	for i, d := range t.Dates {
		if _, _, err := DateBounds(d); err != nil {
			return err
		}
		if i > 0 && d <= t.Dates[i-1] {
			return ErrConflict
		}
	}
	return nil
}

type SealedDay struct {
	Date         string `json:"date"`
	VersionID    string `json:"version_id"`
	ManifestHash string `json:"manifest_hash"`
	VersionNo    uint64 `json:"version_no,string"`
}
type SealStatus struct {
	TaskID          string      `json:"task_id"`
	BuildID         string      `json:"build_id,omitempty"`
	Attempt         int         `json:"attempt"`
	WriterEpoch     uint64      `json:"writer_epoch,string"`
	ProgressVersion uint64      `json:"progress_version,string"`
	CatalogRevision uint64      `json:"catalog_revision,string"`
	State           string      `json:"state"`
	ErrorCode       string      `json:"error_code,omitempty"`
	RetryAfterUnix  int64       `json:"retry_after_unix,string"`
	Versions        []SealedDay `json:"versions,omitempty"`
}

func (s SealStatus) Validate() error {
	if _, err := IDBytes(s.TaskID); err != nil {
		return err
	}
	if s.Attempt < 1 || s.Attempt > 2147483647 || s.WriterEpoch == 0 || s.RetryAfterUnix < 0 || !ValidVerifyError(s.ErrorCode) {
		return ErrConflict
	}
	if s.BuildID != "" {
		if _, err := IDBytes(s.BuildID); err != nil {
			return err
		}
	}
	switch s.State {
	case "running":
	case "succeeded":
		if s.BuildID == "" || s.ErrorCode != "" || s.CatalogRevision == 0 || s.ProgressVersion == 0 || len(s.Versions) == 0 {
			return ErrConflict
		}
	case "blocked", "retry_wait":
		if s.ErrorCode == "" {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	if len(s.Versions) > 31 {
		return ErrConflict
	}
	for i, d := range s.Versions {
		if _, _, err := DateBounds(d.Date); err != nil {
			return err
		}
		if _, err := IDBytes(d.VersionID); err != nil {
			return err
		}
		if !hashPattern.MatchString(d.ManifestHash) || d.VersionNo == 0 || (i > 0 && d.Date <= s.Versions[i-1].Date) {
			return ErrConflict
		}
	}
	if s.State != "succeeded" && len(s.Versions) != 0 {
		return ErrConflict
	}
	return nil
}
func ValidVerifyError(code string) bool {
	if ValidScanError(code) {
		return true
	}
	switch code {
	case "verification_expired", "verification_required", "verification_mismatch", "target_drift", "target_extra", "global_blocked", "cohort_incomplete", "fact_invalid", "build_conflict", "build_failed", "assurance_required", "reconcile_failed", "source_changed":
		return true
	}
	return false
}
func (s FoundationStatus) SupportsReconcile() bool {
	for _, c := range s.Capabilities {
		if c == CapabilityReconcile {
			return true
		}
	}
	return false
}
func (s FoundationStatus) SupportsSeal() bool {
	for _, c := range s.Capabilities {
		if c == CapabilitySeal {
			return true
		}
	}
	return false
}
