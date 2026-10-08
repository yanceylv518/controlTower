package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"controltower/server/internal/auditmeta"
	"controltower/server/internal/storage"
	mysql "github.com/go-sql-driver/mysql"
)

const maxPermissionPresets = 100
const maxPresetApplyAccounts = 50

var errPresetConflict = errors.New("permission_preset_conflict")
var errPresetMissing = errors.New("permission_preset_not_found")
var errPresetLimit = errors.New("permission_preset_limit")
var errPresetInvalid = errors.New("invalid_permission_preset")
var errPresetName = errors.New("permission_preset_name_exists")
var errPresetAccountsChanged = errors.New("permission_preset_accounts_changed")

type presetInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
	Version     int64    `json:"version"`
}
type presetApplyInput struct {
	Version             int64              `json:"version"`
	UserIDs             []int64            `json:"user_ids"`
	Mode                string             `json:"mode"`
	ExpectedPermissions map[int64][]string `json:"expected_permissions"`
}

func normalizePreset(actor storage.User, input presetInput) (storage.PermissionPreset, error) {
	p := storage.PermissionPreset{Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description), Permissions: ExpandPermissions(input.Permissions)}
	if p.Name == "" || utf8.RuneCountInString(p.Name) > 64 || utf8.RuneCountInString(p.Description) > 256 || len(input.Permissions) > 128 || len(p.Permissions) == 0 || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return p, errPresetInvalid
	}
	if slices.Contains(p.Permissions, "*") {
		return p, errPresetInvalid
	}
	if !canGrant(actor, p.Permissions) {
		return p, ErrForbidden
	}
	slices.Sort(p.Permissions)
	return p, nil
}

func lockedPresetActor(tx storage.PermissionPresetTransaction, id int64) (storage.User, []storage.User, error) {
	users, err := tx.Administrators()
	if err != nil {
		return storage.User{}, nil, err
	}
	for _, user := range users {
		if user.ID == id && user.Enabled && HasPermission(user, "accounts.manage") {
			return user, users, nil
		}
	}
	return storage.User{}, nil, ErrForbidden
}

func presetByID(items []storage.PermissionPreset, id, version int64) (storage.PermissionPreset, error) {
	for _, p := range items {
		if p.ID == id {
			if version != p.Version {
				return p, errPresetConflict
			}
			return p, nil
		}
	}
	return storage.PermissionPreset{}, errPresetMissing
}

func presetError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "permission_preset_failed"
	var duplicate *mysql.MySQLError
	switch {
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, errPresetInvalid), errors.Is(err, ErrInvalid):
		status, code = http.StatusBadRequest, "invalid_permission_preset"
	case errors.Is(err, errPresetConflict):
		status, code = http.StatusConflict, "permission_preset_conflict"
	case errors.Is(err, errPresetAccountsChanged):
		status, code = http.StatusConflict, "permission_preset_accounts_changed"
	case errors.Is(err, errPresetMissing):
		status, code = http.StatusNotFound, "permission_preset_not_found"
	case errors.Is(err, errPresetName), errors.As(err, &duplicate) && duplicate.Number == 1062:
		status, code = http.StatusConflict, "permission_preset_name_exists"
	case errors.Is(err, errPresetLimit):
		status, code = http.StatusConflict, "permission_preset_limit"
	}
	write(w, status, map[string]string{"error": code})
}

func (h Handlers) presetAccess(w http.ResponseWriter, r *http.Request) (storage.User, storage.PermissionPresetStore, bool) {
	actor, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return actor, nil, false
	}
	if !HasPermission(actor, "accounts.manage") {
		write(w, 403, map[string]string{"error": "forbidden"})
		return actor, nil, false
	}
	if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		write(w, 403, map[string]string{"error": "csrf"})
		return actor, nil, false
	}
	store, ok := h.M.store.(storage.PermissionPresetStore)
	if !ok {
		write(w, 503, map[string]string{"error": "permission_preset_unavailable"})
		return actor, nil, false
	}
	return actor, store, true
}

func insertPresetAudit(tx storage.PermissionPresetTransaction, r *http.Request, actor storage.User, operation, targetType, targetID string, before, after any) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	audit := storage.OperationAudit{ID: "preset-" + hex.EncodeToString(raw), OperationType: operation, TargetType: targetType, TargetID: targetID, ActorID: actor.Username, ActorType: "human", ActorRole: actor.Role, SourceComponent: "identity", TriggerType: "manual", AuthMethod: "session", BeforeSummary: string(b), AfterSummary: string(a), Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	auditmeta.Enrich(r, &audit)
	return tx.InsertAudit(audit)
}

func (h Handlers) PermissionPresets(w http.ResponseWriter, r *http.Request) {
	actor, store, ok := h.presetAccess(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if r.Method == http.MethodGet {
		items, err := store.ListPermissionPresets(ctx)
		if err != nil {
			presetError(w, err)
			return
		}
		visible := []storage.PermissionPreset{}
		for _, p := range items {
			if canGrant(actor, p.Permissions) && !slices.Contains(p.Permissions, "*") {
				visible = append(visible, p)
			}
		}
		write(w, 200, map[string]any{"items": visible, "max_presets": maxPermissionPresets, "max_apply_accounts": maxPresetApplyAccounts})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	var input presetInput
	if err := decodeAuthBody(w, r, &input, accountRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	var preset storage.PermissionPreset
	err := store.WithPermissionPresetTransaction(ctx, func(tx storage.PermissionPresetTransaction) error {
		actor, _, err := lockedPresetActor(tx, actor.ID)
		if err != nil {
			return err
		}
		preset, err = normalizePreset(actor, input)
		if err != nil {
			return err
		}
		items, err := tx.Presets()
		if err != nil {
			return err
		}
		if len(items) >= maxPermissionPresets {
			return errPresetLimit
		}
		for _, p := range items {
			if strings.EqualFold(p.Name, preset.Name) {
				return errPresetName
			}
		}
		preset.Version = 1
		preset.CreatedBy = actor.Username
		preset.UpdatedBy = actor.Username
		preset.CreatedAt = time.Now().UTC()
		preset.UpdatedAt = preset.CreatedAt
		if err := tx.SavePreset(&preset); err != nil {
			return err
		}
		return insertPresetAudit(tx, r, actor, "auth.permission_preset_create", "permission_preset", strconv.FormatInt(preset.ID, 10), map[string]any{}, preset)
	})
	if err != nil {
		presetError(w, err)
		return
	}
	auditmeta.MarkSemanticAudit(r)
	write(w, 201, preset)
}

func (h Handlers) PermissionPreset(w http.ResponseWriter, r *http.Request) {
	actor, store, ok := h.presetAccess(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "PUT, DELETE")
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		presetError(w, errPresetInvalid)
		return
	}
	var input presetInput
	if err := decodeAuthBody(w, r, &input, accountRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	if input.Version <= 0 {
		presetError(w, errPresetInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var after storage.PermissionPreset
	err = store.WithPermissionPresetTransaction(ctx, func(tx storage.PermissionPresetTransaction) error {
		actor, _, err := lockedPresetActor(tx, actor.ID)
		if err != nil {
			return err
		}
		items, err := tx.Presets()
		if err != nil {
			return err
		}
		before, err := presetByID(items, id, input.Version)
		if err != nil {
			return err
		}
		if !canGrant(actor, before.Permissions) {
			return ErrForbidden
		}
		if r.Method == http.MethodDelete {
			if err := tx.DeletePreset(id); err != nil {
				return err
			}
			return insertPresetAudit(tx, r, actor, "auth.permission_preset_delete", "permission_preset", strconv.FormatInt(id, 10), before, map[string]any{})
		}
		after, err = normalizePreset(actor, input)
		if err != nil {
			return err
		}
		for _, p := range items {
			if p.ID != id && strings.EqualFold(p.Name, after.Name) {
				return errPresetName
			}
		}
		after.ID = id
		after.Version = before.Version + 1
		after.CreatedAt = before.CreatedAt
		after.CreatedBy = before.CreatedBy
		after.UpdatedAt = time.Now().UTC()
		after.UpdatedBy = actor.Username
		if err := tx.SavePreset(&after); err != nil {
			return err
		}
		return insertPresetAudit(tx, r, actor, "auth.permission_preset_update", "permission_preset", strconv.FormatInt(id, 10), before, after)
	})
	if err != nil {
		presetError(w, err)
		return
	}
	auditmeta.MarkSemanticAudit(r)
	if r.Method == http.MethodDelete {
		write(w, 200, map[string]bool{"ok": true})
	} else {
		write(w, 200, after)
	}
}

func (h Handlers) ApplyPermissionPreset(w http.ResponseWriter, r *http.Request) {
	actor, store, ok := h.presetAccess(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		write(w, 405, map[string]string{"error": "method_not_allowed"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		presetError(w, errPresetInvalid)
		return
	}
	var input presetApplyInput
	if err := decodeAuthBody(w, r, &input, accountRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	if input.Version <= 0 || len(input.UserIDs) == 0 || len(input.UserIDs) > maxPresetApplyAccounts || len(input.ExpectedPermissions) != len(input.UserIDs) || (input.Mode != "replace" && input.Mode != "merge") {
		presetError(w, errPresetInvalid)
		return
	}
	ids := map[int64]bool{}
	for _, id := range input.UserIDs {
		if id <= 0 || ids[id] {
			presetError(w, errPresetInvalid)
			return
		}
		ids[id] = true
		if permissions, ok := input.ExpectedPermissions[id]; !ok || permissions == nil || len(permissions) > 128 {
			presetError(w, errPresetInvalid)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	changed := 0
	err = store.WithPermissionPresetTransaction(ctx, func(tx storage.PermissionPresetTransaction) error {
		actor, users, err := lockedPresetActor(tx, actor.ID)
		if err != nil {
			return err
		}
		items, err := tx.Presets()
		if err != nil {
			return err
		}
		preset, err := presetByID(items, id, input.Version)
		if err != nil {
			return err
		}
		if !canGrant(actor, preset.Permissions) || slices.Contains(preset.Permissions, "*") {
			return ErrForbidden
		}
		// 先校验所有目标，再进行写入；查看账号、当前账号及完整管理员不允许批量降权。
		found := 0
		for _, u := range users {
			if ids[u.ID] {
				found++
				if u.ID == actor.ID || storage.IsFullAdmin(u) || !canManage(actor, u) {
					return ErrForbidden
				}
				// 差异预览之后的权限变更必须重新确认，不能静默替换其他操作者的修改。
				expected := ExpandPermissions(input.ExpectedPermissions[u.ID])
				actual := ExpandPermissions(u.Permissions)
				slices.Sort(expected)
				slices.Sort(actual)
				if !slices.Equal(expected, actual) {
					return errPresetAccountsChanged
				}
			}
		}
		if found != len(ids) {
			return ErrForbidden
		}
		for _, before := range users {
			if !ids[before.ID] {
				continue
			}
			after := before
			permissions := slices.Clone(preset.Permissions)
			if input.Mode == "merge" {
				permissions = ExpandPermissions(append(slices.Clone(before.Permissions), permissions...))
			}
			if !canGrant(actor, permissions) {
				return ErrForbidden
			}
			slices.Sort(permissions)
			existing := ExpandPermissions(before.Permissions)
			slices.Sort(existing)
			if slices.Equal(existing, permissions) {
				continue
			}
			after.Permissions = permissions
			after.UpdatedAt = time.Now().UTC()
			if err := tx.SetPermissions(after.ID, permissions, after.UpdatedAt); err != nil {
				return err
			}
			summary := map[string]any{"actor_username": actor.Username, "account": userResponse(after), "permission_preset": preset, "apply_mode": input.Mode}
			if err := insertPresetAudit(tx, r, actor, "auth.permission_preset_apply", "ct_user", strconv.FormatInt(after.ID, 10), userResponse(before), summary); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	if err != nil {
		presetError(w, err)
		return
	}
	auditmeta.MarkSemanticAudit(r)
	write(w, 200, map[string]any{"ok": true, "changed": changed, "selected": len(ids)})
}
