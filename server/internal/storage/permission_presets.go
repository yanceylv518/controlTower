package storage

import (
	"context"
	"time"
)

type PermissionPreset struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Permissions    []string  `json:"permissions"`
	Version        int64     `json:"version"`
	CreatedBy      string    `json:"created_by"`
	UpdatedBy      string    `json:"updated_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	BoundAccounts  int       `json:"bound_accounts"`
	SyncedAccounts int       `json:"synced_accounts,omitempty"`
}

// PermissionPresetTransaction 锁定管理员及预设后执行检查，权限变更和审计一同提交或回滚。
type PermissionPresetTransaction interface {
	Administrators() ([]User, error)
	Presets() ([]PermissionPreset, error)
	SavePreset(*PermissionPreset) error
	DeletePreset(int64) error
	SetPermissions(int64, []string, time.Time) error
	SaveAccount(*User) error
	InsertAudit(OperationAudit) error
}

type PermissionPresetStore interface {
	ListPermissionPresets(context.Context) ([]PermissionPreset, error)
	WithPermissionPresetTransaction(context.Context, func(PermissionPresetTransaction) error) error
}
