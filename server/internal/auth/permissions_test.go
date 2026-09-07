package auth

import (
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRestrictedAdminEndpointMatrix(t *testing.T) {
	cases := []struct {
		permission, method, path string
		want                     bool
	}{
		{"monitor.customers", "GET", "metrics?dimension_type=instance_user", true},
		{"monitor.customers", "GET", "metric-history?dimension_type=instance_user_model", true},
		{"monitor.customers", "GET", "metrics?dimension_type=instance_channel", false},
		{"monitor.customers", "GET", "metrics?dimension_type=instance_model", false},
		{"monitor.customers", "GET", "metrics", false},
		{"monitor.customers", "GET", "agents", false},
		{"monitor.channels", "GET", "metrics?dimension_type=instance_channel", true},
		{"monitor.channels", "GET", "channel-snapshots", true},
		{"monitor.channels", "GET", "metrics?dimension_type=instance_model", false},
		{"monitor.models", "GET", "metrics?dimension_type=instance_model_user", true},
		{"monitor.models", "GET", "channel-snapshots", false},
		{"monitor.runtime", "GET", "server-metrics", true},
		{"monitor.runtime", "GET", "metrics?dimension_type=instance_user", false},
		{"data.users", "GET", "passthrough/users", true},
		{"data.users", "GET", "passthrough/logs", false},
		{"data.logs", "GET", "passthrough/logs", true},
		{"data.logs", "GET", "usage", false},
		{"data.usage", "GET", "usage", true},
		{"data.usage", "GET", "passthrough/users", false},
		{"billing.users", "GET", "billing/user-days", true},
		{"billing.users", "GET", "billing/upstream-channels", false},
		{"billing.channels", "GET", "billing/upstream-channels", true},
		{"billing.channels", "GET", "billing/user-days", false},
		{"billing.users", "POST", "billing/jobs", false},
		{"billing.tasks", "POST", "billing/jobs", true},
		{"monitor.read", "GET", "metrics?dimension_type=instance_user", true},
		{"monitor.read", "POST", "channels/7/commands", false},
		{"monitor.read", "POST", "alerts/action", false},
		{"monitor.read", "GET", "settings", false},
		{"monitor.read", "GET", "operation-audits", false},
		{"monitor.read", "GET", "billing/summary", false},
		{"data.read", "GET", "passthrough/logs", true},
		{"data.read", "POST", "balance-alert-users", false},
		{"accounts.manage", "GET", "passthrough/users", true},
		{"accounts.manage", "GET", "passthrough/logs", false},
		{"billing.manage", "POST", "billing/statements", true},
		{"billing.manage", "PUT", "billing/prices", false},
		{"models.manage", "PUT", "billing/prices", true},
		{"upstreams.manage", "POST", "billing/upstreams", true},
		{"discounts.manage", "PUT", "billing/discounts", true},
		{"tuning.manage", "POST", "channels/7/commands", true},
		{"alerts.manage", "POST", "alerts/cleanup", true},
		{"notifications.manage", "POST", "notification-deliveries/1/resend", true},
		{"instances.manage", "POST", "instances/1/rotate-token", true},
		{"audits.read", "GET", "operation-audits", true},
		{"settings.manage", "PUT", "settings", true},
		{"settings.manage", "GET", "new-endpoint", false},
	}
	for _, tc := range cases {
		t.Run(tc.permission+tc.method+tc.path, func(t *testing.T) {
			u := storage.User{Role: "admin", Permissions: []string{tc.permission}}
			if got := allowAdminRequest(u, httptest.NewRequest(tc.method, "/api/dashboard/"+tc.path, nil)); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestAccountsCannotEscalateOrModifyViewerRole(t *testing.T) {
	m, s := setup(t)
	root, _, _ := s.UserByUsername("admin")
	now := time.Now().UTC()
	create := func(name string, permissions []string) storage.User {
		u, err := m.CreateAccount(root.ID, AccountInput{Username: name, Password: "password1", Role: "admin", Permissions: permissions}, now)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	empty := create("empty", nil)
	if empty.Permissions == nil || HasPermission(empty, "accounts.manage") {
		t.Fatal("missing permissions must grant nothing")
	}
	limited := create("limited", []string{"accounts.manage", "monitor.read"})
	for _, permissions := range [][]string{{"*"}, {"settings.manage"}, {"unknown"}} {
		_, err := m.CreateAccount(limited.ID, AccountInput{Username: "forbidden", Password: "password1", Role: "admin", Permissions: permissions}, now)
		if err != ErrForbidden {
			t.Fatalf("escalation accepted: %v", err)
		}
	}
	if _, _, err := m.UpdateAccount(limited.ID, root.ID, AccountInput{Role: "admin", Enabled: false}, now); err != ErrForbidden {
		t.Fatalf("root modification: %v", err)
	}
	if _, err := m.ResetAccountPassword(limited.ID, root.ID, "password2", now); err != ErrForbidden {
		t.Fatalf("root reset: %v", err)
	}
	if _, _, err := m.UpdateAccount(root.ID, root.ID, AccountInput{Role: "admin"}, now); err != ErrInvalid {
		t.Fatal("self-change accepted")
	}
	viewer, err := m.CreateAccount(root.ID, AccountInput{Username: "viewer", Password: "password1", Role: "viewer", ScopeSite: "site-a", ScopeUserIDs: []int64{11}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.UpdateAccount(root.ID, viewer.ID, AccountInput{Role: "admin", Permissions: []string{"*"}}, now); err != ErrInvalid {
		t.Fatal("viewer role changed")
	}
	_, updated, err := m.UpdateAccount(root.ID, viewer.ID, AccountInput{Role: "viewer", ScopeSite: "site-a", ScopeUserIDs: []int64{12}, Enabled: true}, now)
	if err != nil || updated.ScopeUserIDs[0] != 12 {
		t.Fatalf("viewer edit broken: %v", err)
	}
	root.Enabled = false
	if err = s.UpdateUser(root); err == nil {
		t.Fatal("last full admin disabled")
	}
}

func TestPermissionChangesAndResetAffectExistingSessions(t *testing.T) {
	m, s := setup(t)
	root, _, _ := s.UserByUsername("admin")
	now := time.Now().UTC()
	u, err := m.CreateAccount(root.ID, AccountInput{Username: "operator", Password: "password1", Role: "admin", Permissions: []string{"monitor.read"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := m.Login(u.Username, "password1", now)
	if err != nil {
		t.Fatal(err)
	}
	mw := RequireSessionOrToken(m, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	check := func(want int) {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/dashboard/metrics?dimension_type=instance_user", nil)
		req.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
	}
	check(204)
	if _, _, err = m.UpdateAccount(root.ID, u.ID, AccountInput{Role: "admin", Enabled: true, Permissions: []string{}}, now); err != nil {
		t.Fatal(err)
	}
	check(403)
	if _, err = m.ResetAccountPassword(root.ID, u.ID, "password2", now); err != nil {
		t.Fatal(err)
	}
	check(401)
	if _, _, err = m.Login(u.Username, "password1", now); err == nil {
		t.Fatal("old password accepted")
	}
	_, session, err = m.Login(u.Username, "password2", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = m.UpdateAccount(root.ID, u.ID, AccountInput{Role: "admin", Enabled: false}, now); err != nil {
		t.Fatal(err)
	}
	check(401)
	if _, _, err = m.UpdateAccount(root.ID, u.ID, AccountInput{Role: "admin", Enabled: true, Permissions: []string{"monitor.read"}}, now); err != nil {
		t.Fatal(err)
	}
	check(401)
}

func TestAccountHandlerAuditUsesSessionIdentityAndNeverPassword(t *testing.T) {
	m, audit := setup(t)
	_, session, err := m.Login("admin", "password1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	h := Handlers{M: m, Audit: audit}
	r := httptest.NewRequest("POST", "/api/auth/users", strings.NewReader(`{"username":"operator","password":"private-password","role":"admin","display_name":"张三","permissions":["monitor.read"],"actor":"forged"}`))
	r.AddCookie(&http.Cookie{Name: "ct_session", Value: session.ID})
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	w := httptest.NewRecorder()
	h.Users(w, r)
	items, err := audit.QueryOperationAudits(storage.OperationAuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != 201 || len(items) != 1 {
		t.Fatalf("%d %s audits=%d", w.Code, w.Body.String(), len(items))
	}
	item := items[0]
	if item.ActorID != "admin" || strings.Contains(item.AfterSummary, "private-password") || strings.Contains(item.AfterSummary, "password_hash") {
		t.Fatalf("bad audit: %+v", item)
	}
	var summary map[string]any
	if json.Unmarshal([]byte(item.AfterSummary), &summary) != nil || summary["actor_user_id"] == nil {
		t.Fatal("missing actor ID")
	}
}

func TestLegacyBundlesExpandWithoutGrantingSiblingMenus(t *testing.T) {
	actor := storage.User{Role: "admin", Permissions: []string{"accounts.manage", "monitor.customers"}}
	if canGrant(actor, []string{"monitor.read"}) {
		t.Fatal("a single menu must not grant its legacy bundle")
	}
	legacy := storage.User{Role: "admin", Permissions: []string{"monitor.read", "data.read", "billing.manage"}}
	dto := userResponse(legacy)
	for _, key := range []string{"monitor.customers", "monitor.channels", "monitor.models", "monitor.runtime", "data.users", "data.logs", "billing.users", "billing.channels", "billing.tasks"} {
		if !HasPermission(legacy, key) {
			t.Fatalf("legacy permission lost: %s", key)
		}
	}
	for _, key := range dto.Permissions {
		if _, old := legacyPermissions[key]; old {
			t.Fatalf("editor received bundle %s", key)
		}
	}
	legacy.Permissions = dto.Permissions
	filtered := []string{}
	for _, key := range legacy.Permissions {
		if key != "monitor.channels" {
			filtered = append(filtered, key)
		}
	}
	legacy.Permissions = filtered
	if HasPermission(legacy, "monitor.channels") || !HasPermission(legacy, "monitor.customers") {
		t.Fatal("removing one child changed sibling permissions")
	}
}
