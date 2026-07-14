package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/tuning"
)

type TuningStore interface{ tuning.Store }
type PolicyResponse struct {
	InstanceID string        `json:"instance_id"`
	Policy     tuning.Policy `json:"policy"`
	Mode       string        `json:"mode"`
	IsDefault  bool          `json:"isDefault"`
	UpdatedAt  *time.Time    `json:"updated_at,omitempty"`
	UpdatedBy  string        `json:"updated_by,omitempty"`
}
type RecommendationItem struct {
	ID               string         `json:"id"`
	InstanceID       string         `json:"instance_id"`
	ChannelName      string         `json:"channel_name"`
	Rule             string         `json:"rule"`
	ChannelID        int64          `json:"channel_id"`
	CreatedAt        time.Time      `json:"created_at"`
	Evidence         map[string]any `json:"evidence"`
	CurrentWeight    int64          `json:"current_weight"`
	ProposedWeight   int64          `json:"proposed_weight"`
	CurrentPriority  *int64         `json:"current_priority"`
	ProposedPriority *int64         `json:"proposed_priority"`
	ModeAtCreation   string         `json:"mode_at_creation"`
	Status           string         `json:"status"`
	CommandID        *string        `json:"command_id"`
	Outcome          map[string]any `json:"outcome"`
	OutcomeAt        *time.Time     `json:"outcome_at"`
	Hit              *bool          `json:"hit"`
}

func (h Handler) WithTuningStore(s TuningStore) Handler { h.tuningStore = s; return h }
func (h Handler) HandleTuningPolicy(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("instance_id")
	if id == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		rec, ok, err := h.tuningStore.GetPolicy(id)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		if !ok {
			writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, Policy: tuning.DefaultPolicy(), Mode: "observe", IsDefault: true})
			return
		}
		writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, Policy: rec.Policy, Mode: rec.Mode, UpdatedAt: &rec.UpdatedAt, UpdatedBy: rec.UpdatedBy})
	case http.MethodPut:
		var req struct {
			Policy tuning.Policy `json:"policy"`
			Mode   string        `json:"mode"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		if req.Mode != "observe" {
			writeDashboardError(w, 400, "mode_not_supported")
			return
		}
		if fields := req.Policy.Validate(); len(fields) > 0 {
			writeDashboardJSON(w, 400, map[string]any{"error": "validation_failed", "fields": fields})
			return
		}
		now := time.Now().UTC()
		rec := tuning.PolicyRecord{InstanceID: id, Policy: req.Policy, Mode: req.Mode, UpdatedAt: now, UpdatedBy: ctauth.Actor(r)}
		if h.tuningStore.PutPolicy(rec) != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, Policy: req.Policy, Mode: req.Mode, UpdatedAt: &now, UpdatedBy: rec.UpdatedBy})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
func recommendationItem(r tuning.Recommendation) RecommendationItem {
	return RecommendationItem{r.ID, r.InstanceID, r.ChannelName, r.Rule, r.ChannelID, r.CreatedAt, r.Evidence, r.CurrentWeight, r.ProposedWeight, r.CurrentPriority, r.ProposedPriority, r.ModeAtCreation, r.Status, r.CommandID, r.Outcome, r.OutcomeAt, r.Hit}
}
func (h Handler) HandleTuningRecommendations(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("instance_id")
	if id == "" {
		writeDashboardError(w, 400, "instance_id_required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before"))
	rows, err := h.tuningStore.ListRecommendations(tuning.RecommendationQuery{InstanceID: id, Limit: limit, Before: before})
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	items := make([]RecommendationItem, 0, len(rows))
	for _, x := range rows {
		items = append(items, recommendationItem(x))
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}
func (h Handler) HandleTuningReport(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("instance_id")
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if id == "" || (days != 7 && days != 30) {
		writeDashboardError(w, 400, "invalid_query")
		return
	}
	x, err := h.tuningStore.RecommendationReport(tuning.RecommendationQuery{InstanceID: id, Days: days})
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	rate := float64(0)
	if x.Judged > 0 {
		rate = float64(x.Hits) / float64(x.Judged)
	}
	writeDashboardJSON(w, 200, map[string]any{"total": x.Total, "by_rule": x.ByRule, "filled": x.Filled, "judged": x.Judged, "hits": x.Hits, "hit_rate": rate, "autoCriteria": "观察期命中率持续 ≥85% 且无最小可用集险情，才建议切 auto"})
}
