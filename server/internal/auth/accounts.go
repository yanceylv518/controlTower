package auth

import (
	"context"
	"controltower/server/internal/auditmeta"
	"controltower/server/internal/storage"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrForbidden = errors.New("forbidden")

type AccountInput struct {
	Username                   string   `json:"username"`
	Password                   string   `json:"password"`
	Role                       string   `json:"role"`
	ScopeSite                  string   `json:"scope_site"`
	ScopeUserIDs               []int64  `json:"scope_user_ids"`
	Enabled                    bool     `json:"enabled"`
	DisplayName                string   `json:"display_name"`
	Permissions                []string `json:"permissions"`
	PermissionPresetID         *int64   `json:"permission_preset_id"`
	PermissionPresetVersion    int64    `json:"permission_preset_version"`
	ExpectedPermissions        []string `json:"expected_permissions"`
	ExpectedPermissionPresetID *int64   `json:"expected_permission_preset_id"`
}

func (m *Manager) accountActor(id int64) (storage.User, error) {
	u, ok, err := m.store.UserByID(id)
	if err != nil {
		return u, err
	}
	if !ok || !u.Enabled || !HasPermission(u, "accounts.manage") {
		return u, ErrForbidden
	}
	return u, nil
}

func applyAccountInput(actor storage.User, target *storage.User, q AccountInput) error {
	if q.Role != target.Role || (q.Role != "admin" && q.Role != "viewer") {
		return ErrInvalid
	}
	if q.Role == "viewer" {
		if len(q.Permissions) > 0 || (q.PermissionPresetID != nil && *q.PermissionPresetID != 0) {
			return ErrForbidden
		}
		if strings.TrimSpace(q.ScopeSite) == "" || len(q.ScopeUserIDs) == 0 {
			return ErrInvalid
		}
		target.Permissions = []string{}
		target.ScopeSite, target.ScopeUserIDs = strings.TrimSpace(q.ScopeSite), q.ScopeUserIDs
		return nil
	}
	if utf8.RuneCountInString(q.DisplayName) > 64 || !canGrant(actor, q.Permissions) {
		return ErrForbidden
	}
	target.DisplayName = strings.TrimSpace(q.DisplayName)
	target.Permissions = ExpandPermissions(q.Permissions) // Missing/null never grants legacy full access.
	target.ScopeSite, target.ScopeUserIDs = "", nil
	return nil
}

func (m *Manager) CreateAccount(actorID int64, q AccountInput, now time.Time) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actor, err := m.accountActor(actorID)
	if err != nil {
		return storage.User{}, err
	}
	name := strings.TrimSpace(q.Username)
	if name == "" || utf8.RuneCountInString(name) > 64 || len(q.Password) < 8 || len(q.Password) > 1024 {
		return storage.User{}, ErrInvalid
	}
	if _, exists, e := m.store.UserByUsername(name); e != nil {
		return storage.User{}, e
	} else if exists {
		return storage.User{}, ErrInvalid
	}
	u := storage.User{Username: name, Role: q.Role, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err = applyAccountInput(actor, &u, q); err != nil {
		return u, err
	}
	u.PasswordHash, err = HashPassword(q.Password)
	if err != nil {
		return u, err
	}
	if _, supported := m.store.(storage.PermissionPresetStore); supported && q.Role == "admin" {
		_, u, err = m.saveAdministrator(actorID, u, q, now)
	} else if q.PermissionPresetID != nil && *q.PermissionPresetID != 0 {
		err = ErrForbidden
	} else {
		err = m.store.CreateUser(u)
	}
	if err != nil {
		return u, err
	}
	u, _, err = m.store.UserByUsername(name)
	return u, err
}

func (m *Manager) UpdateAccount(actorID, targetID int64, q AccountInput, now time.Time) (storage.User, storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actor, err := m.accountActor(actorID)
	if err != nil {
		return storage.User{}, storage.User{}, err
	}
	before, ok, err := m.store.UserByID(targetID)
	if err != nil {
		return before, before, err
	}
	if !ok || targetID == actorID {
		return before, before, ErrInvalid
	}
	if !canManage(actor, before) {
		return before, before, ErrForbidden
	}
	after := before
	if err = applyAccountInput(actor, &after, q); err != nil {
		return before, before, err
	}
	after.Enabled, after.UpdatedAt = q.Enabled, now
	if _, supported := m.store.(storage.PermissionPresetStore); supported && before.Role == "admin" {
		return m.saveAdministrator(actorID, after, q, now)
	}
	if q.PermissionPresetID != nil && *q.PermissionPresetID != 0 {
		return before, before, ErrForbidden
	}
	err = m.store.UpdateUser(after)
	return before, after, err
}

func (m *Manager) saveAdministrator(actorID int64, draft storage.User, input AccountInput, now time.Time) (storage.User, storage.User, error) {
	store := m.store.(storage.PermissionPresetStore)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var before, after storage.User
	err := store.WithPermissionPresetTransaction(ctx, func(tx storage.PermissionPresetTransaction) error {
		actor, users, err := lockedPresetActor(tx, actorID)
		if err != nil {
			return err
		}
		after = draft
		if draft.ID != 0 {
			found := false
			for _, user := range users {
				if user.ID == draft.ID {
					before, after, found = user, user, true
				}
			}
			if !found || draft.ID == actorID || !canManage(actor, before) {
				return ErrForbidden
			}
			actualPermissions := before.Permissions
			if storage.IsFullAdmin(before) {
				actualPermissions = []string{"*"}
			}
			if (before.PermissionPresetID != 0 && input.ExpectedPermissions == nil) || (input.ExpectedPermissions != nil && !samePresetPermissions(input.ExpectedPermissions, actualPermissions)) || (input.ExpectedPermissionPresetID != nil && *input.ExpectedPermissionPresetID != before.PermissionPresetID) {
				return errPresetAccountsChanged
			}
		}
		if err := applyAccountInput(actor, &after, input); err != nil {
			return err
		}
		if draft.ID != 0 {
			after.Enabled = input.Enabled
		}
		after.UpdatedAt = now
		if input.PermissionPresetID != nil {
			id := *input.PermissionPresetID
			if id < 0 || input.PermissionPresetVersion < 0 || (id != 0 && storage.IsFullAdmin(after)) {
				return errPresetInvalid
			}
			if id != 0 {
				presets, err := tx.Presets()
				if err != nil {
					return err
				}
				var preset storage.PermissionPreset
				for _, item := range presets {
					if item.ID == id {
						preset = item
					}
				}
				if preset.ID == 0 {
					return errPresetMissing
				}
				if input.PermissionPresetVersion == 0 && id != before.PermissionPresetID {
					return errPresetInvalid
				}
				if input.PermissionPresetVersion != 0 && input.PermissionPresetVersion != preset.Version {
					return errPresetConflict
				}
				if !canGrant(actor, preset.Permissions) {
					return ErrForbidden
				}
			}
			after.PermissionPresetID = id
		}
		if storage.IsFullAdmin(after) {
			after.PermissionPresetID = 0
		}
		if before.Enabled && storage.IsFullAdmin(before) && (!after.Enabled || !storage.IsFullAdmin(after)) {
			fullCount := 0
			for _, user := range users {
				if user.Enabled && storage.IsFullAdmin(user) {
					fullCount++
				}
			}
			if fullCount <= 1 {
				return errors.New("last_full_admin")
			}
		}
		return tx.SaveAccount(&after)
	})
	return before, after, err
}

func (m *Manager) ResetAccountPassword(actorID, targetID int64, password string, now time.Time) (storage.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actor, err := m.accountActor(actorID)
	if err != nil {
		return storage.User{}, err
	}
	target, ok, err := m.store.UserByID(targetID)
	if err != nil {
		return target, err
	}
	if !ok || targetID == actorID || target.Role != "admin" || len(password) < 8 || len(password) > 1024 {
		return target, ErrInvalid
	}
	if !canManage(actor, target) {
		return target, ErrForbidden
	}
	hash, err := HashPassword(password)
	if err != nil {
		return target, err
	}
	resetter, ok := m.store.(interface {
		ResetUserPassword(int64, string, time.Time) error
	})
	if !ok {
		return target, errors.New("password_reset_unavailable")
	}
	err = resetter.ResetUserPassword(targetID, hash, now)
	if err == nil {
		delete(m.attempts, target.Username)
	}
	return target, err
}

func accountError(w http.ResponseWriter, err error) {
	if errors.Is(err, errPresetConflict) || errors.Is(err, errPresetMissing) || errors.Is(err, errPresetInvalid) || errors.Is(err, errPresetAccountsChanged) {
		presetError(w, err)
		return
	}
	status, code := http.StatusInternalServerError, "account_update_failed"
	if errors.Is(err, ErrForbidden) {
		status, code = http.StatusForbidden, "forbidden"
	}
	if errors.Is(err, ErrInvalid) {
		status, code = http.StatusBadRequest, "invalid_user"
	}
	if err.Error() == "last_full_admin" {
		status, code = http.StatusConflict, "last_full_admin"
	}
	write(w, status, map[string]string{"error": code})
}

func (h Handlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		write(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	u, _, ok := h.current(r)
	if !ok {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	if !HasPermission(u, "accounts.manage") || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		write(w, 403, map[string]string{"error": "forbidden"})
		return
	}
	var q struct {
		Password string `json:"password"`
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		write(w, 400, map[string]string{"error": "invalid_user"})
		return
	}
	if err := decodeAuthBody(w, r, &q, authRequestBodyLimit); err != nil {
		writeAuthDecodeError(w, err)
		return
	}
	target, err := h.M.ResetAccountPassword(u.ID, id, q.Password, time.Now().UTC())
	if err != nil {
		accountError(w, err)
		return
	}
	h.accountAudit(r, u, target, target, "auth.password_reset")
	write(w, 200, map[string]bool{"ok": true})
}

func (h Handlers) accountAudit(r *http.Request, actor, before, after storage.User, operation string) {
	if h.Audit == nil {
		return
	}
	snapshot := func(u storage.User) string { b, _ := json.Marshal(userResponse(u)); return string(b) }
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return
	}
	meta, _ := json.Marshal(map[string]any{"actor_user_id": actor.ID, "actor_username": actor.Username, "actor_name": actor.DisplayName, "ip": clientIP(r), "account": userResponse(after)})
	audit := storage.OperationAudit{ID: "account-" + hex.EncodeToString(b), OperationType: operation, TargetType: "ct_user", TargetID: strconv.FormatInt(after.ID, 10), ActorID: actor.Username, BeforeSummary: snapshot(before), AfterSummary: string(meta), Status: "succeeded", CreatedAt: time.Now().UTC()}
	auditmeta.Enrich(r, &audit)
	if err := h.Audit.InsertOperationAudit(audit); err == nil {
		auditmeta.MarkSemanticAudit(r)
	}
}

func (h Handlers) sessionAudit(r *http.Request, actor storage.User, operation string) error {
	if h.Audit == nil {
		return nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	summary, _ := json.Marshal(map[string]any{"username": actor.Username, "role": actor.Role})
	audit := storage.OperationAudit{ID: "auth-" + hex.EncodeToString(b), InstanceID: actor.ScopeSite, OperationType: operation, TargetType: "ct_user", TargetID: strconv.FormatInt(actor.ID, 10), ActorID: actor.Username, ActorType: "human", ActorRole: actor.Role, AuthMethod: "session", SourceComponent: "identity", AfterSummary: string(summary), Status: "succeeded", CreatedAt: time.Now().UTC()}
	auditmeta.Enrich(r, &audit)
	if err := h.Audit.InsertOperationAudit(audit); err != nil {
		return err
	}
	auditmeta.MarkSemanticAudit(r)
	return nil
}

func (h Handlers) passwordAudit(r *http.Request, actor storage.User) {
	if h.Audit == nil {
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return
	}
	before, _ := json.Marshal(map[string]bool{"password_updated": false})
	after, _ := json.Marshal(map[string]bool{"password_updated": true})
	now := time.Now().UTC()
	audit := storage.OperationAudit{ID: "account-" + hex.EncodeToString(b), OperationType: "auth.password_change", TargetType: "ct_user", TargetID: strconv.FormatInt(actor.ID, 10), ActorID: actor.Username, BeforeSummary: string(before), AfterSummary: string(after), Status: "succeeded", CreatedAt: now, UpdatedAt: now}
	auditmeta.Enrich(r, &audit)
	if err := h.Audit.InsertOperationAudit(audit); err == nil {
		auditmeta.MarkSemanticAudit(r)
	}
}
