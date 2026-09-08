package dashboard

import (
	"context"
	cl "controltower/internal/containerlog"
	"controltower/server/internal/auth"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

type ContainerLogStore interface {
	ContainerLogTargets(context.Context) ([]cl.Target, error)
	CreateContainerLog(context.Context, cl.Task) error
	ListContainerLogs(context.Context, int64) ([]cl.Task, error)
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
		reply(map[string]any{"items": v}, e)
		return
	}
	if r.Method == http.MethodGet {
		if id := r.PathValue("id"); id != "" {
			v, e := h.Store.GetContainerLog(r.Context(), id, u.ID)
			if e != nil {
				http.Error(w, "not found or expired", 404)
				return
			}
			reply(v, nil)
			return
		}
		v, e := h.Store.ListContainerLogs(r.Context(), u.ID)
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
	if dec.Decode(&input) != nil || input.Query.Validate(time.Now().UTC()) != nil || input.InstanceID == "" || len(input.InstanceID) > 128 || !cl.ValidName(input.AgentID) {
		http.Error(w, "invalid query", 400)
		return
	}
	b := make([]byte, 16)
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
