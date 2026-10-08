package ingest

import (
	"context"
	"maps"
	"slices"
	"time"

	"controltower/server/internal/storage"
)

func (s *MemoryStore) ListPermissionPresets(ctx context.Context) ([]storage.PermissionPreset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return sortedPresets(s.permissionPresets), nil
}

func sortedPresets(values map[int64]storage.PermissionPreset) []storage.PermissionPreset {
	items := []storage.PermissionPreset{}
	for _, item := range values {
		item.Permissions = slices.Clone(item.Permissions)
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b storage.PermissionPreset) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return items
}

type permissionPresetTx struct {
	users   map[int64]storage.User
	presets map[int64]storage.PermissionPreset
	audits  map[string]storage.OperationAudit
	nextID  int64
}

func (s *MemoryStore) WithPermissionPresetTransaction(ctx context.Context, apply func(storage.PermissionPresetTransaction) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	tx := &permissionPresetTx{users: maps.Clone(s.users), presets: make(map[int64]storage.PermissionPreset), audits: maps.Clone(s.operationAudits), nextID: s.nextPermissionPresetID}
	for id, user := range tx.users {
		user.Permissions = slices.Clone(user.Permissions)
		user.ScopeUserIDs = slices.Clone(user.ScopeUserIDs)
		tx.users[id] = user
	}
	for _, p := range sortedPresets(s.permissionPresets) {
		tx.presets[p.ID] = p
	}
	if err := apply(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.users, s.permissionPresets, s.operationAudits = tx.users, tx.presets, tx.audits
	s.nextPermissionPresetID = tx.nextID
	return nil
}
func (t *permissionPresetTx) Administrators() ([]storage.User, error) {
	items := []storage.User{}
	for _, user := range t.users {
		if user.Role == "admin" {
			user.Permissions = slices.Clone(user.Permissions)
			items = append(items, user)
		}
	}
	slices.SortFunc(items, func(a, b storage.User) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return items, nil
}
func (t *permissionPresetTx) Presets() ([]storage.PermissionPreset, error) {
	return sortedPresets(t.presets), nil
}
func (t *permissionPresetTx) SavePreset(p *storage.PermissionPreset) error {
	if p.ID == 0 {
		p.ID = t.nextID + 1
		for id := range t.presets {
			if id >= p.ID {
				p.ID = id + 1
			}
		}
		t.nextID = p.ID
	}
	value := *p
	value.Permissions = slices.Clone(p.Permissions)
	t.presets[p.ID] = value
	return nil
}
func (t *permissionPresetTx) DeletePreset(id int64) error { delete(t.presets, id); return nil }
func (t *permissionPresetTx) SetPermissions(id int64, permissions []string, now time.Time) error {
	user := t.users[id]
	user.Permissions = slices.Clone(permissions)
	user.UpdatedAt = now
	t.users[id] = user
	return nil
}
func (t *permissionPresetTx) InsertAudit(audit storage.OperationAudit) error {
	if !storage.IsSupportedOperationAudit(audit.OperationType) {
		return storage.ErrUnsupportedOperationAudit
	}
	t.audits[audit.ID] = storage.NormalizeOperationAudit(audit)
	return nil
}
