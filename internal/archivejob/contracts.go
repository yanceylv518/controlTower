// Package archivejob is the new archive protocol. It has no four-task state.
package archivejob

import "time"

const Protocol = 1

type Settings struct {
	Collection bool   `json:"collection"`
	History    bool   `json:"history"`
	RetryToken string `json:"retry_token,omitempty"`
}

type Progress struct {
	Step      string    `json:"step"`
	Date      string    `json:"date,omitempty"`
	Table     string    `json:"table,omitempty"`
	AfterID   int64     `json:"after_id,string"`
	Rows      uint64    `json:"rows,string"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
}

type Day struct {
	Date     string `json:"date"`
	State    string `json:"state"`
	Revision uint64 `json:"revision,string"`
	Version  string `json:"version,omitempty"`
	Rows     string `json:"rows"`
	Step     string `json:"step,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Status struct {
	CountsError     string    `json:"counts_error,omitempty"`
	CountsDate      string    `json:"counts_date,omitempty"`
	Latest          *Position `json:"latest,omitempty"`
	Protocol        int       `json:"protocol"`
	Collection      Progress  `json:"collection"`
	History         Progress  `json:"history"`
	FirstDate       string    `json:"first_date,omitempty"`
	FirstDateSource string    `json:"first_date_source,omitempty"`
	Frontier        string    `json:"frontier,omitempty"`
	Cutoff          string    `json:"cutoff,omitempty"`
	Days            []Day     `json:"days"`
	NextDay         string    `json:"next_day,omitempty"`
}

type Position struct {
	ID         int64     `json:"id,string"`
	Table      string    `json:"table"`
	LogTime    time.Time `json:"log_time"`
	ObservedAt time.Time `json:"observed_at"`
}

func (s Status) Valid() bool {
	if s.Protocol != Protocol || len(s.Days) > 100 || s.Collection.AfterID < 0 || s.History.AfterID < 0 {
		return false
	}
	for _, date := range []string{s.FirstDate, s.Frontier, s.Cutoff, s.NextDay, s.History.Date, s.CountsDate} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return false
			}
		}
	}
	for _, d := range s.Days {
		if _, err := time.Parse("2006-01-02", d.Date); err != nil {
			return false
		}
		switch d.State {
		case "collecting", "pending", "processing", "sealed", "failed":
		default:
			return false
		}
		if len(d.Error) > 256 {
			return false
		}
	}
	return len(s.Collection.Error) <= 256 && len(s.History.Error) <= 256 && len(s.CountsError) <= 256
}
