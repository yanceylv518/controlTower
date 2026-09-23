package archivecontract

import (
	"regexp"
	"time"
)

// RawPosition observes stored raw logs, independently of the collection checkpoint.
// An empty Table means all monthly tables were inspected and contain no rows.
type RawPosition struct {
	Table      string     `json:"table"`
	ID         int64      `json:"id,string"`
	LogTime    *time.Time `json:"log_time,omitempty"`
	ObservedAt time.Time  `json:"observed_at"`
}

func (p RawPosition) Valid() bool {
	if p.ObservedAt.IsZero() {
		return false
	}
	if p.Table == "" {
		return p.ID == 0 && p.LogTime == nil
	}
	if !regexp.MustCompile(`^logs_[0-9]{6}$`).MatchString(p.Table) {
		return false
	}
	_, err := time.Parse("200601", p.Table[5:])
	return err == nil && p.ID >= 0 && p.LogTime != nil
}
