package dashboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
)

type CommandStore interface {
	CreateChannelCommand(storage.ChannelCommand) error
	QueryChannelCommands(storage.ChannelCommandQuery) ([]storage.ChannelCommand, error)
	QueryOperationAudits(storage.OperationAuditQuery) ([]storage.OperationAudit, error)
}
type CommandHandler struct {
	Store     CommandStore
	Instances InstanceStore
	names     *nameResolver
}

func (h CommandHandler) WithNameSource(source NameSource) CommandHandler {
	h.names = newNameResolver(source, time.Minute)
	return h
}

type channelCommandItem struct {
	ID           string         `json:"id"`
	InstanceID   string         `json:"instance_id"`
	InstanceName string         `json:"instance_name"`
	ChannelID    int64          `json:"channel_id"`
	Status       string         `json:"status"`
	Payload      map[string]any `json:"payload"`
	CreatedBy    string         `json:"created_by"`
	ErrorSummary string         `json:"error_summary,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

func (h CommandHandler) commandItem(v storage.ChannelCommand) channelCommandItem {
	p := map[string]any{}
	_ = json.Unmarshal([]byte(v.PayloadJSON), &p)
	name := v.InstanceID
	if h.names != nil {
		name = h.names.InstanceName(v.InstanceID)
	}
	return channelCommandItem{ID: v.ID, InstanceID: v.InstanceID, InstanceName: name, ChannelID: v.ChannelID, Status: v.Status, Payload: p, CreatedBy: v.CreatedBy, ErrorSummary: v.ErrorSummary, CreatedAt: v.CreatedAt}
}

func (h CommandHandler) Create(w http.ResponseWriter, r *http.Request) {
	channelID, e := strconv.ParseInt(r.PathValue("channelID"), 10, 64)
	if e != nil || channelID <= 0 {
		writeDashboardError(w, 400, "invalid_command")
		return
	}
	var q struct {
		InstanceID string `json:"instance_id"`
		Confirm    bool   `json:"confirm"`
		Status     *int   `json:"status"`
		Weight     *uint  `json:"weight"`
		Priority   *int64 `json:"priority"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		writeDashboardError(w, 400, "invalid_command")
		return
	}
	if !q.Confirm {
		writeDashboardError(w, 400, "confirm_required")
		return
	}
	if q.Status == nil && q.Weight == nil && q.Priority == nil {
		writeDashboardError(w, 400, "invalid_command")
		return
	}
	if _, ok, err := h.Instances.InstanceByID(q.InstanceID); err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	} else if !ok {
		writeDashboardError(w, 404, "instance_not_found")
		return
	}
	changes := map[string]any{}
	if q.Status != nil {
		changes["status"] = *q.Status
	}
	if q.Weight != nil {
		changes["weight"] = *q.Weight
	}
	if q.Priority != nil {
		changes["priority"] = *q.Priority
	}
	payload, _ := json.Marshal(changes)
	var b [16]byte
	if _, e = rand.Read(b[:]); e != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	now := time.Now().UTC()
	v := storage.ChannelCommand{ID: hex.EncodeToString(b[:]), InstanceID: q.InstanceID, ChannelID: channelID, CommandType: "channel.update", PayloadJSON: string(payload), Status: "pending", CreatedBy: ctauth.Actor(r), CreatedAt: now, UpdatedAt: now}
	if h.Store.CreateChannelCommand(v) != nil {
		writeDashboardError(w, 500, "create_failed")
		return
	}
	writeDashboardJSON(w, 201, h.commandItem(v))
}
func (h CommandHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, e := h.Store.QueryChannelCommands(storage.ChannelCommandQuery{InstanceID: q.Get("instance_id"), Status: q.Get("status"), Limit: limit, Offset: offset})
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	out := make([]channelCommandItem, 0, len(items))
	for _, v := range items {
		out = append(out, h.commandItem(v))
	}
	writeDashboardJSON(w, 200, map[string]any{"items": out})
}

type operationAuditItem struct {
	InstanceID    string    `json:"instance_id"`
	InstanceName  string    `json:"instance_name"`
	OperationType string    `json:"operation_type"`
	TargetType    string    `json:"target_type"`
	TargetID      string    `json:"target_id"`
	ActorID       string    `json:"actor_id"`
	AfterSummary  string    `json:"after_summary"`
	CreatedAt     time.Time `json:"created_at"`
}

func (h CommandHandler) Audits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, e := h.Store.QueryOperationAudits(storage.OperationAuditQuery{InstanceID: q.Get("instance_id"), Limit: limit, Offset: offset})
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	out := make([]operationAuditItem, 0, len(items))
	for _, v := range items {
		name := v.InstanceID
		if h.names != nil {
			name = h.names.InstanceName(v.InstanceID)
		}
		out = append(out, operationAuditItem{InstanceID: v.InstanceID, InstanceName: name, OperationType: v.OperationType, TargetType: v.TargetType, TargetID: v.TargetID, ActorID: v.ActorID, AfterSummary: v.AfterSummary, CreatedAt: v.CreatedAt})
	}
	writeDashboardJSON(w, 200, map[string]any{"items": out})
}
