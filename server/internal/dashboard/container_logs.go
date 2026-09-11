package dashboard

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ContainerLogStore interface {
	ContainerLogTargets(context.Context) ([]cl.Target, error)
	CreateContainerLog(context.Context, cl.Task) error
	ListContainerLogs(context.Context, int64) ([]cl.Task, error)
	ListContainerLogHistory(context.Context, cl.HistoryFilter) (cl.HistoryPage, error)
	GetContainerLog(context.Context, string, int64) (cl.Task, error)
	PollContainerLogs(context.Context, string, cl.Poll) (*cl.Task, error)
}
type ContainerLogHandler struct{ Store ContainerLogStore }

func (h ContainerLogHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	u, ok := auth.CurrentUser(r)
	if !ok || !auth.HasPermission(u, "logs.query") {
		http.Error(w, "forbidden", 403)
		return
	}
	reply := func(v any, err error) {
		if err != nil {
			http.Error(w, "query unavailable", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.URL.Path == "/api/dashboard/container-log-targets" {
		v, e := h.Store.ContainerLogTargets(r.Context())
		reply(map[string]any{"items": v, "supports_query_batch": true, "supports_history_pagination": true}, e)
		return
	}
	if r.Method == http.MethodGet {
		// Only full administrators may inspect other requesters' history.
		historyActor := u.ID
		if storage.IsFullAdmin(u) {
			historyActor = 0
		}
		if id := r.PathValue("id"); id != "" {
			v, e := h.Store.GetContainerLog(r.Context(), id, historyActor)
			if e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					http.Error(w, "not found or expired", 404)
				} else {
					log.Printf("container log task read failed task_id=%q actor_id=%d: %v", id, u.ID, e)
					http.Error(w, "task read failed; retry later", 500)
				}
				return
			}
			reply(v, nil)
			return
		}
		if r.URL.Query().Get("paged") == "1" {
			params := r.URL.Query()
			f := cl.HistoryFilter{ActorID: historyActor, Site: strings.TrimSpace(params.Get("site")), Actor: strings.TrimSpace(params.Get("actor")), RequestID: strings.TrimSpace(params.Get("request_id")), Page: 1, PageSize: 20}
			if f.Actor != "" && !storage.IsFullAdmin(u) {
				http.Error(w, "forbidden", 403)
				return
			}
			valid := len(f.Site) <= 128 && len(f.Actor) <= 128 && len(f.RequestID) <= 128
			for key, target := range map[string]*int{"page": &f.Page, "page_size": &f.PageSize} {
				if value := params.Get(key); value != "" {
					n, err := strconv.Atoi(value)
					if err != nil {
						valid = false
					}
					*target = n
				}
			}
			for key, target := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
				if value := params.Get(key); value != "" {
					t, err := time.Parse(time.RFC3339, value)
					if err != nil {
						valid = false
					}
					*target = t
				}
			}
			if !valid || f.Page < 1 || f.Page > 1000000 || f.PageSize < 1 || f.PageSize > 100 || (!f.From.IsZero() && !f.To.IsZero() && f.To.Before(f.From)) {
				http.Error(w, "invalid history filter", 400)
				return
			}
			v, e := h.Store.ListContainerLogHistory(r.Context(), f)
			v.CanFilterActor = storage.IsFullAdmin(u)
			reply(v, e)
			return
		}
		v, e := h.Store.ListContainerLogs(r.Context(), historyActor)
		reply(map[string]any{"items": v}, e)
		return
	}
	var input struct {
		InstanceID string   `json:"instance_id"`
		AgentID    string   `json:"agent_id"`
		Query      cl.Query `json:"query"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		http.Error(w, "invalid query", 400)
		return
	}
	if input.Query.Cursor != "" {
		http.Error(w, "cursor is internal to Agent", 400)
		return
	}

	if input.Query.Validate(time.Now().UTC()) != nil || input.InstanceID == "" || len(input.InstanceID) > 128 || !cl.ValidName(input.AgentID) {
		http.Error(w, "invalid query", 400)
		return
	}
	b := make([]byte, 16)
	if cache, ok := h.Store.(interface {
		FindReusableContainerLog(context.Context, string, string, int64, cl.Query) (cl.Task, error)
	}); ok {
		cached, err := cache.FindReusableContainerLog(r.Context(), input.InstanceID, input.AgentID, u.ID, input.Query)
		if err == nil {
			w.Header().Set("X-CT-Log-Reused", "true")
			reply(cached, nil)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("container log cache read failed actor_id=%d: %v", u.ID, err)
			http.Error(w, "task read failed; retry later", 500)
			return
		}
	}
	if _, e := rand.Read(b); e != nil {
		http.Error(w, "unavailable", 500)
		return
	}
	t := cl.Task{ID: hex.EncodeToString(b), InstanceID: input.InstanceID, AgentID: input.AgentID, ActorID: u.ID, Actor: u.Username, ActorName: u.DisplayName, Query: input.Query, CreatedAt: time.Now().UTC(), Result: cl.Result{Status: "pending", Lines: []string{}}}
	if err := h.Store.CreateContainerLog(r.Context(), t); err != nil {
		http.Error(w, "目标离线、容器不可用或查询队列已满", 409)
		return
	}
	w.WriteHeader(202)
	reply(t, nil)
}
