package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type TuningStore interface{ tuning.Store }
type ChannelBaseValueStore interface {
	ListChannelBaseValues(string, string) ([]tuning.ChannelBaseValue, error)
	SaveChannelBaseValues(string, string, []tuning.ChannelBaseValue, time.Time) error
	SyncChannelBaseValues(string, []string) ([]tuning.ChannelBaseValue, error)
}
type ContinuousStateStore interface {
	ListContinuousStates(string) ([]tuning.ContinuousState, error)
}
type BasePrioritySyncStore interface {
	ChannelBaseValueStore
	ContinuousStateStore
	CreateContinuousWeightChange(tuning.Recommendation, string, time.Time) (string, error)
}
type TuningPreflightStore interface {
	CreateTuningPreflight(string, int64, string, time.Time) (storage.ChannelCommand, error)
	GetTuningPreflight(string, string) (storage.ChannelCommand, bool, error)
}

func tuningSiteID(r *http.Request) string {
	if id := strings.TrimSpace(r.URL.Query().Get("site_id")); id != "" {
		return id
	}
	// Keep instance_id as a temporary compatibility alias for rc20 clients.
	return strings.TrimSpace(r.URL.Query().Get("instance_id"))
}

func (h Handler) HandleTuningContinuousStates(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(ContinuousStateStore)
	if !ok {
		writeDashboardError(w, 501, "continuous_dispatch_not_supported")
		return
	}
	if r.URL.Query().Get("rates_only") == "1" {
		ratesStore, supported := h.tuningStore.(interface {
			QueryCurrentChannelRateSnapshot(string, time.Time) ([]tuning.ChannelMetric, time.Time, error)
		})
		if !supported {
			writeDashboardError(w, 503, "current_rates_unavailable")
			return
		}
		now := time.Now().UTC()
		rates, asOf, err := ratesStore.QueryCurrentChannelRateSnapshot(id, now)
		if err != nil {
			writeDashboardError(w, 503, "current_rates_unavailable")
			return
		}
		items := make([]map[string]any, 0, len(rates))
		for _, rate := range rates {
			items = append(items, map[string]any{"channel_id": rate.ChannelID, "rpm": rate.RequestCount, "tpm": rate.TPM})
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items, "as_of": asOf, "window_start": asOf.Add(-60 * time.Second), "window_seconds": 60, "delay_seconds": int64(now.Sub(asOf).Seconds())})
		return
	}
	items, err := store.ListContinuousStates(id)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}

type PolicyResponse struct {
	InstanceID string        `json:"instance_id"`
	SiteID     string        `json:"site_id"`
	Policy     tuning.Policy `json:"policy"`
	Mode       string        `json:"mode"`
	IsDefault  bool          `json:"isDefault"`
	UpdatedAt  *time.Time    `json:"updated_at,omitempty"`
	UpdatedBy  string        `json:"updated_by,omitempty"`
}

func (h Handler) HandleTuningBaseValues(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(ChannelBaseValueStore)
	if !ok {
		writeDashboardError(w, 501, "base_values_not_supported")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := store.ListChannelBaseValues(id, r.URL.Query().Get("model"))
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	case http.MethodPut:
		var req struct {
			Items []tuning.ChannelBaseValue `json:"items"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		seen := make(map[int64]struct{}, len(req.Items))
		for _, v := range req.Items {
			if v.ChannelID <= 0 || strings.TrimSpace(v.ModelName) == "" || v.BaseWeight < 0 || v.BasePriority < 0 || v.MaxRPM < 0 || v.MaxTPM < 0 {
				writeDashboardError(w, 400, "validation_failed")
				return
			}
			if _, duplicate := seen[v.ChannelID]; duplicate {
				writeDashboardError(w, 400, "duplicate_channel_id")
				return
			}
			seen[v.ChannelID] = struct{}{}
		}
		before, err := store.ListChannelBaseValues(id, "")
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		now := time.Now().UTC()
		actor := ctauth.Actor(r)
		if err = store.SaveChannelBaseValues(id, actor, req.Items, now); err != nil {
			writeDashboardError(w, 500, "save_failed")
			return
		}
		if syncStore, supported := h.tuningStore.(BasePrioritySyncStore); supported {
			if err = syncSavedBasePriorities(syncStore, id, actor, req.Items, before, now); err != nil {
				writeDashboardError(w, 500, "priority_sync_failed")
				return
			}
		}
		items, err := store.ListChannelBaseValues(id, "")
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"items": items})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}

func syncSavedBasePriorities(store BasePrioritySyncStore, siteID, actor string, saved, before []tuning.ChannelBaseValue, now time.Time) error {
	current := make(map[int64]tuning.ChannelBaseValue, len(before))
	for _, value := range before {
		current[value.ChannelID] = value
	}
	states, err := store.ListContinuousStates(siteID)
	if err != nil {
		return err
	}
	phases := make(map[int64]string, len(states))
	for _, state := range states {
		phases[state.ChannelID] = state.Phase
	}
	for _, value := range saved {
		phase := phases[value.ChannelID]
		if phase == "circuit" || phase == "probing" {
			continue
		}
		observed := current[value.ChannelID]
		if observed.ChannelID == 0 {
			observed = value
		}
		if value.BasePriority == observed.CurrentPriority {
			continue
		}
		currentPriority, proposedPriority := observed.CurrentPriority, value.BasePriority
		rec := tuning.Recommendation{
			ID: tuning.NewID(now, siteID, value.ChannelID, "base_priority_sync"), InstanceID: siteID,
			ChannelID: value.ChannelID, ChannelName: observed.ChannelName, CreatedAt: now, Rule: "base_priority_sync",
			Evidence:      map[string]any{"model": value.ModelName, "trigger": "base_value_saved"},
			CurrentWeight: observed.CurrentWeight, ProposedWeight: observed.CurrentWeight,
			CurrentPriority: &currentPriority, ProposedPriority: &proposedPriority, ModeAtCreation: "manual", Status: "recorded",
		}
		if _, err = store.CreateContinuousWeightChange(rec, actor, now); err != nil {
			return err
		}
	}
	return nil
}

func (h Handler) HandleTuningBaseValuesSync(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(ChannelBaseValueStore)
	if !ok {
		writeDashboardError(w, 501, "base_values_not_supported")
		return
	}
	var req struct {
		Models []string `json:"models"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeDashboardError(w, 400, "invalid_json")
		return
	}
	items, err := store.SyncChannelBaseValues(id, req.Models)
	if err != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}

func (h Handler) HandleTuningPreflight(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	store, ok := h.tuningStore.(TuningPreflightStore)
	if !ok {
		writeDashboardError(w, 501, "tuning_preflight_not_supported")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			ChannelID int64 `json:"channel_id"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.ChannelID <= 0 {
			writeDashboardError(w, 400, "invalid_channel_id")
			return
		}
		command, err := store.CreateTuningPreflight(id, req.ChannelID, ctauth.Actor(r), time.Now().UTC())
		if err != nil {
			writeDashboardError(w, 409, "tuning_preflight_unavailable")
			return
		}
		// Direct-control sites verify synchronously, so the command may
		// already be terminal here; return the error so the client can
		// settle without polling.
		writeDashboardJSON(w, 202, map[string]any{"command_id": command.ID, "status": command.Status, "error": command.ErrorSummary})
	case http.MethodGet:
		command, found, err := store.GetTuningPreflight(id, strings.TrimSpace(r.URL.Query().Get("command_id")))
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		if !found {
			writeDashboardError(w, 404, "preflight_not_found")
			return
		}
		writeDashboardJSON(w, 200, map[string]any{"command_id": command.ID, "status": command.Status, "error": command.ErrorSummary})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
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
	ActedBy          string         `json:"acted_by,omitempty"`
	ActedAt          *time.Time     `json:"acted_at,omitempty"`
}

func (h Handler) WithTuningStore(s TuningStore) Handler { h.tuningStore = s; return h }
func (h Handler) HandleTuningPolicy(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
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
			writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, SiteID: id, Policy: tuning.DefaultPolicy(), Mode: "observe", IsDefault: true})
			return
		}
		writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, SiteID: id, Policy: rec.Policy, Mode: rec.Mode, UpdatedAt: &rec.UpdatedAt, UpdatedBy: rec.UpdatedBy})
	case http.MethodPut:
		var req struct {
			Policy             tuning.Policy `json:"policy"`
			Mode               string        `json:"mode"`
			PreflightCommandID string        `json:"preflight_command_id"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			writeDashboardError(w, 400, "invalid_json")
			return
		}
		if req.Mode != "observe" && req.Mode != "confirm" && req.Mode != "auto" {
			writeDashboardError(w, 400, "mode_not_supported")
			return
		}
		if fields := req.Policy.Validate(); len(fields) > 0 {
			writeDashboardJSON(w, 400, map[string]any{"error": "validation_failed", "fields": fields})
			return
		}
		current, _, err := h.tuningStore.GetPolicy(id)
		if err != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		newlyAuto := false
		for model, nextMode := range req.Policy.DispatchModes {
			if nextMode == "auto" && current.Policy.DispatchModes[model] != "auto" {
				newlyAuto = true
				break
			}
		}
		if newlyAuto {
			preflightStore, supported := h.tuningStore.(TuningPreflightStore)
			command, found, preflightErr := storage.ChannelCommand{}, false, error(nil)
			if supported && req.PreflightCommandID != "" {
				command, found, preflightErr = preflightStore.GetTuningPreflight(id, req.PreflightCommandID)
			}
			if !supported || preflightErr != nil || !found || command.Status != "succeeded" || time.Since(command.UpdatedAt) > 5*time.Minute {
				writeDashboardJSON(w, 409, map[string]any{"error": "tuning_preflight_required", "detail": command.ErrorSummary})
				return
			}
		}
		now := time.Now().UTC()
		rec := tuning.PolicyRecord{InstanceID: id, Policy: req.Policy, Mode: req.Mode, UpdatedAt: now, UpdatedBy: ctauth.Actor(r)}
		if h.tuningStore.PutPolicy(rec) != nil {
			writeDashboardError(w, 500, "query_failed")
			return
		}
		writeDashboardJSON(w, 200, PolicyResponse{InstanceID: id, SiteID: id, Policy: req.Policy, Mode: req.Mode, UpdatedAt: &now, UpdatedBy: rec.UpdatedBy})
	default:
		writeDashboardError(w, 405, "method_not_allowed")
	}
}
func recommendationItem(r tuning.Recommendation) RecommendationItem {
	return RecommendationItem{r.ID, r.InstanceID, r.ChannelName, r.Rule, r.ChannelID, r.CreatedAt, r.Evidence, r.CurrentWeight, r.ProposedWeight, r.CurrentPriority, r.ProposedPriority, r.ModeAtCreation, r.Status, r.CommandID, r.Outcome, r.OutcomeAt, r.Hit, r.ActedBy, r.ActedAt}
}

func (h Handler) HandleTuningRecommendations(w http.ResponseWriter, r *http.Request) {
	id := tuningSiteID(r)
	if id == "" {
		writeDashboardError(w, 400, "site_id_required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	before, _ := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before"))
	rule := r.URL.Query().Get("rule")
	rows, err := h.tuningStore.ListRecommendations(tuning.RecommendationQuery{InstanceID: id, Rule: rule, Limit: limit, Before: before})
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
	id := tuningSiteID(r)
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
	writeDashboardJSON(w, 200, map[string]any{"total": x.Total, "by_rule": x.ByRule})
}
