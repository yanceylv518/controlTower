package tuning

import (
	"context"
	"time"
)

type ProbeRound struct {
	ID                string        `json:"id"`
	ChannelID         int64         `json:"channel_id"`
	Model             string        `json:"model"`
	Planned           int           `json:"planned"`
	Status            string        `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	Metrics           *ProbeMetrics `json:"metrics,omitempty"`
	ReportedAttempts  *int          `json:"reported_attempts,omitempty"`
	ReportedSuccesses *int          `json:"reported_successes,omitempty"`
}
type ProbeRoundPage struct {
	Items      []ProbeRound `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}
type ProbeRoundsReader interface {
	QueryProbeRounds(context.Context, ProbeRecordQuery) (ProbeRoundPage, error)
}
