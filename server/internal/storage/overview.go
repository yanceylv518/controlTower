package storage

import "time"

type OverviewCoverage struct {
	InstanceID  string    `json:"instance_id"`
	BucketTime  time.Time `json:"bucket_time"`
	ConfirmedAt time.Time `json:"confirmed_at"`
}
type OverviewInstance struct {
	ID         string         `json:"instance_id"`
	Name       string         `json:"name"`
	Enabled    bool           `json:"enabled"`
	LastSeenAt *time.Time     `json:"last_seen_at"`
	Health     []HealthCheck  `json:"-"`
	Docker     []DockerStatus `json:"-"`
}
type OverviewAlertSnapshot struct {
	Items       []Alert
	Total       int
	Unconfirmed int
	EvaluatedAt *time.Time
}
