package storage

import "time"

type SystemSetting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
}

type SystemSettingStore interface {
	ListSystemSettings() ([]SystemSetting, error)
	ReplaceSystemSettings(values map[string]string, actor string, now time.Time) error
}
