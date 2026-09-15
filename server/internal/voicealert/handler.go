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
	Store  Store
	Caller Caller
	Runner *Runner
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
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
		if err := c.Validate(); err != nil {
			fail(400, err.Error())
			return
		}
		if err := h.Store.SaveConfig(ctx, c, ctauth.Actor(r)); err != nil {
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
	c, err := h.Store.Config(ctx)
	if err != nil {
		fail(500, "配置读取失败")
		return
	}
	calls, err := h.Store.Calls(ctx)
	if err != nil {
		fail(500, "电话记录读取失败")
		return
	}
	statuses := []TargetStatus{}
	if h.Runner != nil {
		statuses = h.Runner.Status()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"config": c, "credentials_ready": h.Caller.Ready(), "worker_enabled": h.Runner != nil, "calls": calls, "targets": statuses})
}
