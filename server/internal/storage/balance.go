package storage

import "time"

// UserQuotaUsage is a compact rollup over the existing user-dimension metrics.
// Quota is stored in new-api quota units, not display currency.
type UserQuotaUsage struct {
	InstanceID   string
	DimensionKey string
	RequestCount int64
	Quota        int64
	FirstBucket  time.Time
}

type BalanceAlertUserSetting struct {
	InstanceID string    `json:"instance_id"`
	UserID     int64     `json:"user_id"`
	Enabled    bool      `json:"enabled"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}
