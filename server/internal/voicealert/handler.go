package voicealert

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Handler must be mounted behind RequireSessionOrToken (admin-only route).
type Handler struct {
	Store  SettingsStore
	Caller Caller
	Runner *Runner
}

type SettingsStore interface {
	Config(context.Context, string) (Config, error)
	SaveConfig(context.Context, string, Config, string) error
	Calls(context.Context, string) ([]CallRecord, error)
	Customers(context.Context) (CustomerList, error)
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
	site := r.URL.Query().Get("site_id")
	if !validSite(site) {
		fail(400, "请选择有效站点")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method == http.MethodPut {
		c := DefaultConfig()
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			fail(400, "配置格式错误")
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			fail(400, "配置格式错误")
			return
		}
		if err := c.ValidateSite(site); err != nil {
			fail(400, err.Error())
			return
		}
		if err := c.Validate(); err != nil {
			fail(400, err.Error())
			return
		}
		if err := h.Store.SaveConfig(ctx, site, c, ctauth.Actor(r)); err != nil {
			fail(400, "保存失败，请检查站点是否存在、已启用以及数据库状态")
			return
		}
		if h.Runner != nil {
			h.Runner.Notify()
		}
	} else if r.Method != http.MethodGet {
		fail(405, "method_not_allowed")
		return
	}
	c, err := h.Store.Config(ctx, site)
	if err != nil {
		fail(500, "配置读取失败")
		return
	}
	c.Targets = nil
	c = c.WithDirectionRules()
	calls, err := h.Store.Calls(ctx, site)
	if err != nil {
		fail(500, "电话记录读取失败")
		return
	}
	statuses := []TargetStatus{}
	if h.Runner != nil {
		for _, status := range h.Runner.Status() {
			if status.Site == site {
				statuses = append(statuses, status)
			}
		}
	}
	list, directoryErr := h.Store.Customers(ctx)
	directoryError := ""
	if directoryErr != nil {
		directoryError = "客户列表暂时无法读取，请稍后刷新；已保存的接听范围保持不变"
		list = CustomerList{Customers: []Target{}, UnavailableSites: []string{}}
	}
	filtered := CustomerList{Customers: []Target{}, UnavailableSites: []string{}}
	for _, customer := range list.Customers {
		if customer.Site == site {
			filtered.Customers = append(filtered.Customers, customer)
		}
	}
	for _, unavailable := range list.UnavailableSites {
		if unavailable == site {
			filtered.UnavailableSites = append(filtered.UnavailableSites, unavailable)
		}
	}
	list = filtered
	_ = json.NewEncoder(w).Encode(map[string]any{"site_id": site, "site_scoped": true, "direction_rules": true, "config": c, "credentials_ready": h.Caller.Ready(), "worker_enabled": h.Runner != nil, "calls": calls, "targets": statuses, "customers": list.Customers, "unavailable_sites": list.UnavailableSites, "directory_error": directoryError})
}
