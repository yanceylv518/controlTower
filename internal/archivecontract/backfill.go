package archivecontract

import (
	"strings"
	"time"
)

const CapabilityBackfill = "date_backfill_v3"

// ScanBudget applies to one source read and its target commit. The byte limits
// count decoded raw values, not compressed network payloads.
type ScanBudget struct {
	MaxRows           int    `json:"max_rows"`
	MaxBytes          uint64 `json:"max_bytes,string"`
	MaxRowBytes       uint64 `json:"max_row_bytes,string"`
	MaxDurationMillis int    `json:"max_duration_millis"`
}

func DefaultScanBudget() ScanBudget {
	return ScanBudget{MaxRows: 500, MaxBytes: 4 << 20, MaxRowBytes: 4 << 20, MaxDurationMillis: 30000}
}

func (b ScanBudget) Validate() error {
	if b.MaxRows < 1 || b.MaxRows > 5000 || b.MaxBytes < 1024 || b.MaxBytes > 64<<20 || b.MaxRowBytes < 1024 || b.MaxRowBytes > b.MaxBytes || b.MaxDurationMillis < 1000 || b.MaxDurationMillis > 30000 {
		return ErrConflict
	}
	return nil
}

// CoveragePolicy is an explicit operational declaration. Scanning never turns
// it into verification evidence or proves that a day can be billed.
type CoveragePolicy struct {
	Revision           uint64     `json:"revision,string"`
	CoverageFrom       string     `json:"coverage_from,omitempty"`
	SourceRetainedFrom string     `json:"source_retained_from,omitempty"`
	Evidence           string     `json:"evidence,omitempty"`
	RecentDays         int        `json:"recent_days"`
	Budget             ScanBudget `json:"budget"`
}

func DefaultCoveragePolicy() CoveragePolicy {
	return CoveragePolicy{RecentDays: 7, Budget: DefaultScanBudget()}
}

func DateBounds(date string) (int64, int64, error) {
	d, err := time.ParseInLocation("2006-01-02", date, time.FixedZone("Asia/Shanghai", 8*3600))
	if err != nil || d.Year() < 1970 || d.Format("2006-01-02") != date {
		return 0, 0, ErrConflict
	}
	return d.Unix(), d.AddDate(0, 0, 1).Unix(), nil
}

func (p CoveragePolicy) Validate() error {
	if p.RecentDays < 0 || p.RecentDays > 31 || p.Budget.Validate() != nil || len(p.Evidence) > 2048 {
		return ErrConflict
	}
	for _, date := range []string{p.CoverageFrom, p.SourceRetainedFrom} {
		if date != "" {
			if _, _, err := DateBounds(date); err != nil || strings.TrimSpace(p.Evidence) == "" {
				return ErrConflict
			}
		}
	}
	return nil
}

type BackfillTask struct {
	Identity
	TaskID  string         `json:"task_id"`
	Date    string         `json:"date"`
	Type    string         `json:"type"`
	Attempt int            `json:"attempt"`
	Policy  CoveragePolicy `json:"policy"`
}

func (t BackfillTask) Validate() error {
	if t.Identity.Validate() != nil || t.Policy.Validate() != nil || t.Attempt < 1 || t.Attempt > 2147483647 {
		return ErrConflict
	}
	if _, err := IDBytes(t.TaskID); err != nil {
		return err
	}
	if _, _, err := DateBounds(t.Date); err != nil {
		return err
	}
	if t.Type != "date_backfill" && t.Type != "recent_backfill" {
		return ErrUnsupported
	}
	return nil
}

type BackfillStatus struct {
	TaskID           string `json:"task_id"`
	Date             string `json:"date"`
	Attempt          int    `json:"attempt"`
	WriterEpoch      uint64 `json:"writer_epoch,string"`
	State            string `json:"state"`
	AfterCreatedUnix int64  `json:"after_created_unix,string"`
	AfterID          int64  `json:"after_id,string"`
	ScannedRows      uint64 `json:"scanned_rows,string"`
	ReadBytes        uint64 `json:"read_bytes,string"`
	WrittenBytes     uint64 `json:"written_bytes,string"`
	ElapsedMillis    uint64 `json:"elapsed_millis,string"`
	BatchID          string `json:"batch_id,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
	EmptyCandidate   bool   `json:"empty_candidate"`
	SourceNowUnix    int64  `json:"source_now_unix,string"`
	RetryAfterUnix   int64  `json:"retry_after_unix,string"`
	CatalogRevision  uint64 `json:"catalog_revision,string"`
}

func (s BackfillStatus) Validate() error {
	if _, err := IDBytes(s.TaskID); err != nil {
		return err
	}
	from, to, err := DateBounds(s.Date)
	if err != nil || s.Attempt < 1 || s.Attempt > 2147483647 || s.WriterEpoch == 0 || s.AfterID < 0 || s.AfterCreatedUnix < 0 || s.SourceNowUnix < 0 || s.RetryAfterUnix < 0 {
		return ErrConflict
	}
	if s.AfterCreatedUnix != 0 && (s.AfterCreatedUnix < from || s.AfterCreatedUnix >= to) {
		return ErrConflict
	}
	if s.AfterID != 0 && s.AfterCreatedUnix == 0 {
		return ErrConflict
	}
	if (s.ScannedRows == 0 && s.AfterID != 0) || (s.ScannedRows > 0 && (s.AfterID == 0 || s.AfterCreatedUnix == 0)) {
		return ErrConflict
	}
	if s.BatchID != "" {
		if _, err := IDBytes(s.BatchID); err != nil {
			return err
		}
	}
	switch s.State {
	case "running", "succeeded", "blocked", "retry_wait":
	default:
		return ErrConflict
	}
	if !ValidScanError(s.ErrorCode) || (s.EmptyCandidate && (s.State != "succeeded" || s.ScannedRows != 0)) || (s.State == "succeeded" && (s.ErrorCode != "" || s.BatchID == "")) {
		return ErrConflict
	}
	if (s.State == "blocked" || s.State == "retry_wait") && s.ErrorCode == "" {
		return ErrConflict
	}
	if s.State == "succeeded" && s.SourceNowUnix < to {
		return ErrConflict
	}
	return nil
}

// Fixed codes keep SQL errors and source data out of control-plane status.
func ValidScanError(code string) bool {
	switch code {
	case "", "source_index_missing", "source_cleared", "source_history_unknown", "source_gap", "source_drift", "source_unavailable", "source_query_failed", "source_clock_unavailable", "invalid_date", "date_not_ready", "row_too_large", "repair_unscoped_target", "invalid_timestamp", "unknown_charged_type", "invalid_source_row", "target_unavailable", "checkpoint_conflict", "receipt_conflict", "writer_lease", "date_frozen", "budget_exhausted", "schema_changed", "scan_failed":
		return true
	}
	return false
}

type ArchiveMetrics struct {
	Stream        string `json:"stream"`
	ReadBytes     uint64 `json:"read_bytes,string"`
	WrittenBytes  uint64 `json:"written_bytes,string"`
	ElapsedMillis uint64 `json:"elapsed_millis,string"`
	LagSeconds    *int64 `json:"lag_seconds,string,omitempty"`
	ErrorCode     string `json:"error_code,omitempty"`
}

func (m ArchiveMetrics) Validate() error {
	if m.Stream != "incremental" && m.Stream != "date_backfill" && m.Stream != "recent_backfill" {
		return ErrConflict
	}
	if !ValidScanError(m.ErrorCode) || (m.LagSeconds != nil && *m.LagSeconds < 0) {
		return ErrConflict
	}
	return nil
}
