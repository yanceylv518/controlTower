package dashboard

import (
	"context"
	"controltower/internal/channelcontrol"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type InstanceStore interface {
	ListInstances() ([]storage.Instance, error)
	InstanceByID(string) (storage.Instance, bool, error)
	CreateInstance(storage.Instance) error
	UpdateInstance(string, string, string, bool, time.Time) error
	CreateInstanceToken(storage.InstanceToken) error
	ExpireInstanceTokens(string, time.Time, time.Time) error
}
type InstanceHandler struct {
	Store          InstanceStore
	Runtime        RuntimeStore
	Pepper         string
	Settings       *settings.Provider
	ReadonlyConfig ReadonlyConfigStore
	ControlConfig  ControlConfigStore
	// ControlCheck validates a direct control config with a read-only new-api
	// call before it is saved; tests may replace it.
	ControlCheck func(ctx context.Context, apiURL, accessToken string, adminUserID int64) error
	SecretKey    string
}
type ReadonlyConfigStore interface {
	ReadonlyDSNForSite(string) (string, error)
	UpdateReadonlyDSNForSite(string, string, time.Time) error
}
type ControlConfigStore interface {
	ControlConfigForSite(string) (storage.SiteControlConfig, error)
	UpdateControlConfigForSite(string, string, string, int64, time.Time) error
}
type InstanceItem struct {
	InstanceID             string          `json:"instance_id"`
	SiteID                 string          `json:"site_id"`
	Name                   string          `json:"name"`
	Enabled                bool            `json:"enabled"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	Agents                 []InstanceAgent `json:"agents"`
	LogsReadonlyConfigured bool            `json:"logs_readonly_configured"`
	ControlConfigured      bool            `json:"control_configured"`
	ControlAPIURL          string          `json:"control_api_url"`
	ControlAdminUserID     int64           `json:"control_admin_user_id"`
}
type InstanceAgent struct {
	ID              string    `json:"id"`
	Version         string    `json:"version"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	BacklogEstimate int64     `json:"backlog_estimate"`
	Online          bool      `json:"online"`
}

func (i InstanceHandler) item(v storage.Instance) (InstanceItem, error) {
	out := InstanceItem{InstanceID: v.ID, SiteID: siteOf(v), Name: v.Name, Enabled: v.Enabled, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Agents: []InstanceAgent{}}
	if i.ReadonlyConfig != nil {
		encrypted, err := i.ReadonlyConfig.ReadonlyDSNForSite(siteOf(v))
		if err != nil {
			return out, err
		}
		out.LogsReadonlyConfigured = encrypted != ""
	}
	if i.ControlConfig != nil {
		cfg, err := i.ControlConfig.ControlConfigForSite(siteOf(v))
		if err != nil {
			return out, err
		}
		out.ControlConfigured = cfg.EncryptedToken != ""
		out.ControlAPIURL = cfg.APIURL
		out.ControlAdminUserID = cfg.AdminUserID
	}
	if i.Runtime != nil {
		agents, e := i.Runtime.QueryAgents(storage.AgentQuery{InstanceID: v.ID, Limit: storage.MaxRuntimeQueryLimit})
		if e != nil {
			return out, e
		}
		now := time.Now()
		offlineSeconds := 120
		if i.Settings != nil {
			if current, e := i.Settings.Current(); e == nil {
				offlineSeconds = current.OfflineSeconds
			}
		}
		for _, a := range agents {
			out.Agents = append(out.Agents, InstanceAgent{ID: a.ID, Version: a.Version, LastSeenAt: a.LastSeenAt, BacklogEstimate: a.BacklogEstimate, Online: now.Sub(a.LastSeenAt) <= time.Duration(offlineSeconds)*time.Second})
		}
	}
	return out, nil
}

var instanceIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
var siteIDPattern = regexp.MustCompile(`^[a-z0-9_-]{0,64}$`)

func tokenHash(p, t string) string {
	x := sha256.Sum256([]byte(p + t))
	return hex.EncodeToString(x[:])
}
func newToken() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func (i InstanceHandler) List(w http.ResponseWriter, r *http.Request) {
	v, e := i.Store.ListInstances()
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	items := make([]InstanceItem, 0, len(v))
	current, scoped := ctauth.CurrentUser(r)
	for _, instance := range v {
		if scoped && current.Role == "viewer" && siteOf(instance) != current.ScopeSite {
			continue
		}
		item, e := i.item(instance)
		if e != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		if scoped && current.Role == "viewer" {
			item.Agents = []InstanceAgent{}
			item.ControlAPIURL = ""
			item.ControlAdminUserID = 0
			item.ControlConfigured = false
		}
		items = append(items, item)
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}
func (i InstanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID     string `json:"instance_id"`
		SiteID string `json:"site_id"`
		Name   string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil || !instanceIDPattern.MatchString(q.ID) {
		writeDashboardError(w, 400, "invalid_instance_id")
		return
	}
	if !siteIDPattern.MatchString(q.SiteID) {
		writeDashboardError(w, 400, "invalid_site_id")
		return
	}
	if _, ok, _ := i.Store.InstanceByID(q.ID); ok {
		writeDashboardError(w, 409, "instance_exists")
		return
	}
	n := time.Now().UTC()
	v := storage.Instance{ID: q.ID, SiteID: q.SiteID, Name: q.Name, Enabled: true, CreatedAt: n, UpdatedAt: n}
	if i.Store.CreateInstance(v) != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	t, e := newToken()
	if e != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	if i.Store.CreateInstanceToken(storage.InstanceToken{InstanceID: q.ID, TokenHash: tokenHash(i.Pepper, t), CreatedAt: n}) != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 201, map[string]string{"instance_id": q.ID, "site_id": siteOf(v), "name": q.Name, "token": t})
}
func (i InstanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	v, ok, e := i.Store.InstanceByID(id)
	if e != nil || !ok {
		writeDashboardError(w, 404, "instance_not_found")
		return
	}
	var q struct {
		SiteID             *string `json:"site_id"`
		Name               *string `json:"name"`
		Enabled            *bool   `json:"enabled"`
		LogsReadonlyDSN    *string `json:"logs_readonly_dsn"`
		ControlAPIURL      *string `json:"control_api_url"`
		ControlAPIToken    *string `json:"control_api_token"`
		ControlAdminUserID *int64  `json:"control_admin_user_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&q)
	if q.SiteID != nil {
		if !siteIDPattern.MatchString(*q.SiteID) {
			writeDashboardError(w, 400, "invalid_site_id")
			return
		}
		v.SiteID = *q.SiteID
	}
	if q.Name != nil {
		v.Name = *q.Name
	}
	if q.Enabled != nil {
		v.Enabled = *q.Enabled
	}
	var encryptedReadonlyDSN string
	if q.LogsReadonlyDSN != nil {
		if i.ReadonlyConfig == nil {
			writeDashboardError(w, 501, "readonly_passthrough_unavailable")
			return
		}
		if len(*q.LogsReadonlyDSN) > 512 {
			writeDashboardError(w, 400, "invalid_logs_readonly_dsn")
			return
		}
		if *q.LogsReadonlyDSN != "" {
			if err := validateReadonlyDSN(r.Context(), *q.LogsReadonlyDSN); err != nil {
				writeDashboardError(w, 400, "logs_readonly_connection_test_failed")
				return
			}
			var err error
			encryptedReadonlyDSN, err = encryptSecret(i.SecretKey, *q.LogsReadonlyDSN)
			if err != nil {
				writeDashboardError(w, 503, "secret_key_not_configured")
				return
			}
		}
	}
	var encryptedControlToken, controlURL string
	var controlAdminUserID int64
	if q.ControlAPIURL != nil {
		if i.ControlConfig == nil {
			writeDashboardError(w, 501, "control_config_unavailable")
			return
		}
		controlURL = strings.TrimSpace(*q.ControlAPIURL)
		if controlURL != "" {
			if !strings.HasPrefix(controlURL, "http://") && !strings.HasPrefix(controlURL, "https://") || len(controlURL) > 255 {
				writeDashboardError(w, 400, "invalid_control_api_url")
				return
			}
			token := ""
			if q.ControlAPIToken != nil {
				token = strings.TrimSpace(*q.ControlAPIToken)
			}
			if token == "" || len(token) > 512 {
				writeDashboardError(w, 400, "invalid_control_api_token")
				return
			}
			if q.ControlAdminUserID == nil || *q.ControlAdminUserID <= 0 {
				writeDashboardError(w, 400, "invalid_control_admin_user_id")
				return
			}
			controlAdminUserID = *q.ControlAdminUserID
			check := i.ControlCheck
			if check == nil {
				check = defaultControlCheck
			}
			checkCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err := check(checkCtx, controlURL, token, controlAdminUserID)
			cancel()
			if err != nil {
				writeDashboardError(w, 400, "control_connection_test_failed")
				return
			}
			encryptedControlToken, err = encryptSecret(i.SecretKey, token)
			if err != nil {
				writeDashboardError(w, 503, "secret_key_not_configured")
				return
			}
		}
	}
	v.UpdatedAt = time.Now().UTC()
	if i.Store.UpdateInstance(id, v.SiteID, v.Name, v.Enabled, v.UpdatedAt) != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	if q.ControlAPIURL != nil {
		if err := i.ControlConfig.UpdateControlConfigForSite(siteOf(v), controlURL, encryptedControlToken, controlAdminUserID, v.UpdatedAt); err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
	}
	if q.LogsReadonlyDSN != nil {
		if err := i.ReadonlyConfig.UpdateReadonlyDSNForSite(siteOf(v), encryptedReadonlyDSN, v.UpdatedAt); err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		persisted, err := i.ReadonlyConfig.ReadonlyDSNForSite(siteOf(v))
		if err != nil || persisted != encryptedReadonlyDSN {
			writeDashboardError(w, 500, "logs_readonly_config_not_persisted")
			return
		}
	}
	item, e := i.item(v)
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, item)
}

func defaultControlCheck(ctx context.Context, apiURL, accessToken string, adminUserID int64) error {
	return channelcontrol.New(apiURL, accessToken, adminUserID, nil).Check(ctx)
}

func validateReadonlyDSN(parent context.Context, dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	configureReadonlyDB(db)
	ctx, cancel := context.WithTimeout(parent, readonlyQueryTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	for _, query := range []string{
		"SELECT id,user_id,created_at,type,username,model_name,channel_id,token_name,prompt_tokens,completion_tokens,quota,use_time,request_id,content FROM logs LIMIT 0",
		"SELECT id,username,display_name,quota,used_quota,status,created_at,last_login_at FROM users LIMIT 0",
	} {
		rows, queryErr := db.QueryContext(ctx, query)
		if queryErr != nil {
			return queryErr
		}
		_ = rows.Close()
	}
	return nil
}
func (i InstanceHandler) Rotate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	instance, ok, err := i.Store.InstanceByID(id)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	if !ok {
		writeDashboardError(w, 404, "instance_not_found")
		return
	}
	if !instance.Enabled {
		writeDashboardError(w, 409, "instance_disabled")
		return
	}
	n := time.Now().UTC()
	g := n.Add(24 * time.Hour)
	if i.Store.ExpireInstanceTokens(id, g, n) != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	t, e := newToken()
	if e != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	if i.Store.CreateInstanceToken(storage.InstanceToken{InstanceID: id, TokenHash: tokenHash(i.Pepper, t), CreatedAt: n}) != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"token": t, "grace_until": g})
}
