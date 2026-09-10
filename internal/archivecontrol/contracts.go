package archivecontrol

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var ErrConflict = errors.New("archive_config_conflict")

type Config struct {
	ReconcileID     string `json:"reconcile_id,omitempty"`
	ReconcileDate   string `json:"reconcile_date,omitempty"`
	Version         int64  `json:"version"`
	AgentID         string `json:"agent_id"`
	InstanceID      string `json:"instance_id"`
	Running         bool   `json:"running"`
	BatchSize       int    `json:"batch_size"`
	IntervalSeconds int    `json:"interval_seconds"`
	DelaySeconds    int    `json:"delay_seconds"`
}

func Default() Config { return Config{BatchSize: 500, IntervalSeconds: 30, DelaySeconds: 300} }
func (c Config) Validate() bool {
	if c.ReconcileID != "" || c.ReconcileDate != "" {
		d, err := time.ParseInLocation("2006-01-02", c.ReconcileDate, time.FixedZone("Beijing", 28800))
		if err != nil || len(c.ReconcileID) != 32 || !d.AddDate(0, 0, 1).Before(time.Now().Add(-time.Duration(c.DelaySeconds)*time.Second)) {
			return false
		}
	}
	return c.Version >= 0 && len(c.InstanceID) > 0 && len(c.InstanceID) <= 64 && len(c.AgentID) > 0 && len(c.AgentID) <= 64 && c.BatchSize >= 1 && c.BatchSize <= 5000 && c.IntervalSeconds >= 2 && c.IntervalSeconds <= 3600 && c.DelaySeconds >= 60 && c.DelaySeconds <= 86400
}

type Reconciliation struct {
	ID         string     `json:"id"`
	Date       string     `json:"date"`
	State      string     `json:"state"`
	SourceRows int64      `json:"source_rows"`
	TargetRows int64      `json:"target_rows"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type Status struct {
	SupportsDailyCheck bool            `json:"supports_daily_check,omitempty"`
	Reconciliation     *Reconciliation `json:"reconciliation,omitempty"`
	Days               []Day           `json:"days,omitempty"`
	SiteID             string          `json:"site_id,omitempty"`
	AgentID            string          `json:"agent_id"`
	Session            string          `json:"session"`
	Configured         bool            `json:"configured"`
	AppliedVersion     int64           `json:"applied_version"`
	State              string          `json:"state"`
	LastID             int64           `json:"last_id,string"`
	LastSuccess        *time.Time      `json:"last_success,omitempty"`
	VerifiedAt         *time.Time      `json:"verified_at,omitempty"`
	VerifiedRows       int             `json:"verified_rows"`
	LastBatchRows      int             `json:"last_batch_rows"`
	Error              string          `json:"error"`
}

func (s Status) Validate() bool {
	if v := s.Reconciliation; v != nil {
		if len(v.ID) != 32 || len(v.Date) != 10 || v.SourceRows < 0 || v.TargetRows < 0 || len(v.Error) > 256 {
			return false
		}
		switch v.State {
		case "running", "matched", "mismatched", "failed":
		default:
			return false
		}
	}
	if len(s.Days) > 5000 {
		return false
	}
	seen := map[string]bool{}
	for _, d := range s.Days {
		if !d.Validate() || seen[d.Date] {
			return false
		}
		seen[d.Date] = true
	}
	if s.AgentID == "" || len(s.AgentID) > 64 || len(s.Session) != 32 || s.AppliedVersion < 0 || s.LastID < 0 || len(s.Error) > 256 || s.VerifiedRows < 0 || s.LastBatchRows < 0 {
		return false
	}
	switch s.State {
	case "paused", "running", "error", "unconfigured", "waiting":
		return true
	}
	return false
}

type Item struct {
	Days       []Day      `json:"days"`
	SiteID     string     `json:"site_id"`
	Targets    []Target   `json:"targets"`
	InstanceID string     `json:"instance_id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Config     Config     `json:"config"`
	Status     Status     `json:"status"`
	SeenAt     *time.Time `json:"seen_at,omitempty"`
}
type Target struct {
	InstanceID string    `json:"instance_id"`
	Name       string    `json:"name"`
	AgentID    string    `json:"agent_id"`
	Configured bool      `json:"configured"`
	SeenAt     time.Time `json:"seen_at"`
}
type Response struct {
	StatusAccepted bool   `json:"status_accepted"`
	SiteID         string `json:"site_id"`
	Config         Config `json:"config"`
	Granted        bool   `json:"granted"`
	LeaseSeconds   int    `json:"lease_seconds"`
}
type Store interface {
	ListLogArchiveDays(context.Context, string, string) ([]Day, error)
	ListLogArchives(context.Context, string) ([]Item, error)
	UpdateLogArchive(context.Context, string, Config, string) error
	PollLogArchive(context.Context, string, Status) (Response, error)
}

type Day struct {
	Date         string    `json:"date"`
	ArchivedRows string    `json:"archived_rows"`
	RequestRows  string    `json:"request_rows"`
	ErrorRows    string    `json:"error_rows"`
	LastID       int64     `json:"last_id,string"`
	VerifiedAt   time.Time `json:"verified_at"`
}

var decimalCount = regexp.MustCompile(`^[0-9]{1,38}$`)

func (d Day) Validate() bool {
	if d.Date != "undated" {
		if _, err := time.Parse("2006-01-02", d.Date); err != nil {
			return false
		}
	}
	return d.LastID >= 0 && !d.VerifiedAt.IsZero() && decimalCount.MatchString(d.ArchivedRows) && decimalCount.MatchString(d.RequestRows) && decimalCount.MatchString(d.ErrorRows)
}
