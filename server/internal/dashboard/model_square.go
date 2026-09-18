package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SquareModel struct {
	Name                 string   `json:"model_name"`
	Description          string   `json:"description,omitempty"`
	Tags                 string   `json:"tags,omitempty"`
	VendorID             int      `json:"vendor_id,omitempty"`
	Owner                string   `json:"owner_by,omitempty"`
	QuotaType            int      `json:"quota_type"`
	ModelRatio           *float64 `json:"model_ratio"`
	ModelPrice           *float64 `json:"model_price"`
	CompletionRatio      *float64 `json:"completion_ratio"`
	CacheRatio           *float64 `json:"cache_ratio,omitempty"`
	CreateCacheRatio     *float64 `json:"create_cache_ratio,omitempty"`
	ImageRatio           *float64 `json:"image_ratio,omitempty"`
	AudioRatio           *float64 `json:"audio_ratio,omitempty"`
	AudioCompletionRatio *float64 `json:"audio_completion_ratio,omitempty"`
	Groups               []string `json:"enable_groups"`
	Endpoints            []string `json:"supported_endpoint_types"`
	BillingMode          string   `json:"billing_mode,omitempty"`
	BillingExpr          string   `json:"billing_expr,omitempty"`
}
type SquareVendor struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
type SquareResponse struct {
	Items        []SquareModel      `json:"items"`
	Vendors      []SquareVendor     `json:"vendors"`
	GroupRatios  map[string]float64 `json:"group_ratios"`
	UsableGroups map[string]string  `json:"usable_groups"`
	UpdatedAt    time.Time          `json:"updated_at"`
	ExpiresAt    time.Time          `json:"expires_at"`
	Stale        bool               `json:"stale"`
	Warning      string             `json:"warning,omitempty"`
	Source       string             `json:"source"`
}
type squareEntry struct {
	mu        sync.Mutex
	value     *SquareResponse
	attempted time.Time
	failed    bool
}
type ModelSquareCache struct {
	mu      sync.Mutex
	entries map[string]*squareEntry
	now     func() time.Time
}

func NewModelSquareCache() *ModelSquareCache {
	return &ModelSquareCache{entries: map[string]*squareEntry{}, now: time.Now}
}

func (c *ModelSquareCache) get(ctx context.Context, key string, force bool, fetch func(context.Context) (SquareResponse, error)) (SquareResponse, error) {
	c.mu.Lock()
	entry := c.entries[key]
	if entry == nil {
		if len(c.entries) >= 256 {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
		entry = &squareEntry{}
		c.entries[key] = entry
	}
	c.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	now := c.now().UTC()
	if !force && entry.value != nil && !entry.failed && now.Before(entry.value.ExpiresAt) {
		return *entry.value, nil
	}
	// Coalesce concurrent manual refreshes and back off failed sources.
	cooldown := 10 * time.Second
	if entry.failed {
		cooldown = time.Minute
	}
	if !entry.attempted.IsZero() && now.Sub(entry.attempted) < cooldown {
		if entry.value != nil {
			result := *entry.value
			result.Stale = entry.failed || !now.Before(result.ExpiresAt)
			if result.Stale {
				result.Warning = "NewAPI 暂不可用，显示上次成功缓存，稍后自动重试。"
			}
			return result, nil
		}
		return SquareResponse{}, errors.New("newapi_pricing_unavailable")
	}
	entry.attempted = now
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
	defer cancel()
	result, err := fetch(fetchCtx)
	if err != nil {
		entry.failed = true
		if entry.value != nil {
			result = *entry.value
			result.Stale = true
			result.Warning = "NewAPI 暂不可用，显示上次成功缓存，稍后自动重试。"
			return result, nil
		}
		return SquareResponse{}, err
	}
	result.UpdatedAt = c.now().UTC()
	result.ExpiresAt = result.UpdatedAt.Add(5 * time.Minute)
	result.Source = "newapi"
	entry.value = &result
	entry.failed = false
	return result, nil
}

type ModelSquareHandler struct {
	Config    ControlConfigStore
	SecretKey string
	Cache     *ModelSquareCache
	Client    *http.Client
}

func (h ModelSquareHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeDashboardError(w, 405, "newapi_models_readonly")
		return
	}
	if !billingAdminAllowed(r) {
		writeDashboardError(w, 403, "forbidden")
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("instance_id"))
	if site == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	cfg, err := h.Config.ControlConfigForSite(site)
	if err != nil {
		writeDashboardError(w, 500, "site_config_query_failed")
		return
	}
	base, err := url.Parse(strings.TrimRight(cfg.APIURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil {
		writeDashboardError(w, 422, "newapi_api_not_configured")
		return
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/pricing"
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	fingerprint := sha256.Sum256([]byte(site + "\x00" + cfg.APIURL + "\x00" + cfg.EncryptedToken + "\x00" + strconv.FormatInt(cfg.AdminUserID, 10)))
	result, err := h.Cache.get(r.Context(), hex.EncodeToString(fingerprint[:]), r.Method == http.MethodPost, func(ctx context.Context) (SquareResponse, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
		if err != nil {
			return SquareResponse{}, err
		}
		if cfg.EncryptedToken != "" {
			token, err := decryptSecret(h.SecretKey, cfg.EncryptedToken)
			if err != nil {
				return SquareResponse{}, err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("New-Api-User", strconv.FormatInt(cfg.AdminUserID, 10))
		}
		req.Header.Set("Accept", "application/json")
		client := h.Client
		if client == nil {
			client = &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
		response, err := client.Do(req)
		if err != nil {
			return SquareResponse{}, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return SquareResponse{}, fmt.Errorf("pricing status %d", response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
		if err != nil || len(body) > 8*1024*1024 {
			return SquareResponse{}, errors.New("invalid pricing body")
		}
		var payload struct {
			Success      bool               `json:"success"`
			Data         []SquareModel      `json:"data"`
			Vendors      []SquareVendor     `json:"vendors"`
			GroupRatios  map[string]float64 `json:"group_ratio"`
			UsableGroups map[string]string  `json:"usable_group"`
		}
		if json.Unmarshal(body, &payload) != nil || !payload.Success || payload.Data == nil || payload.GroupRatios == nil {
			return SquareResponse{}, errors.New("invalid pricing response")
		}
		for _, model := range payload.Data {
			if strings.TrimSpace(model.Name) == "" {
				return SquareResponse{}, errors.New("invalid model")
			}
		}
		return SquareResponse{Items: payload.Data, Vendors: payload.Vendors, GroupRatios: payload.GroupRatios, UsableGroups: payload.UsableGroups}, nil
	})
	if err != nil {
		writeDashboardError(w, 502, "newapi_pricing_unavailable")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/billing/group-ratios") {
		items := []map[string]string{}
		for group, ratio := range result.GroupRatios {
			items = append(items, map[string]string{"instance_id": site, "group_name": group, "ratio": strconv.FormatFloat(ratio, 'f', -1, 64)})
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items, "source": "newapi", "updated_at": result.UpdatedAt, "stale": result.Stale, "warning": result.Warning})
		return
	}
	writeDashboardJSON(w, 200, result)
}

// Retired mutation endpoints remain explicit, rather than silently maintaining a second catalog.
func NewAPIModelsReadonly(w http.ResponseWriter, r *http.Request) {
	writeDashboardError(w, 405, "newapi_models_readonly")
}
