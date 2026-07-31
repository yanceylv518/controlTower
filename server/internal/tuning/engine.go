package tuning

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"sort"
	"strings"
	"time"
)

type Store interface {
	GetPolicy(string) (PolicyRecord, bool, error)
	PutPolicy(PolicyRecord) error
	ListEnabledInstances() ([]string, error)
	QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error)
	QueryP95Buckets(string, int64, time.Time, time.Time, int64) ([]float64, error)
	LatestChannels(string) ([]Channel, error)
	InsertRecommendation(Recommendation) error
	HasRecentRecommendation(string, int64, string, time.Time) (bool, error)
	CountActionRecommendations(string, int64, time.Time) (int, error)
	LastActionRecommendationAt(string, int64) (time.Time, bool, error)
	ListDispatchStates(string) ([]DispatchState, error)
	PutDispatchState(DispatchState) error
	DeleteDispatchState(string, int64) error
	PendingOutcomes(time.Time, int) ([]Recommendation, error)
	UpdateOutcome(string, map[string]any, time.Time, *bool) error
	ListRecommendations(RecommendationQuery) ([]Recommendation, error)
	RecommendationReport(RecommendationQuery) (Report, error)
}
type channelState struct {
	errorWindows, latencyWindows, trialHealthyWindows int
}
type baselineEntry struct {
	value     float64
	ok        bool
	expiresAt time.Time
}
type Engine struct {
	store     Store
	states    map[string]*channelState
	last      map[string]time.Time
	baselines map[string]baselineEntry
}

func NewEngine(s Store) *Engine {
	return &Engine{store: s, states: map[string]*channelState{}, last: map[string]time.Time{}, baselines: map[string]baselineEntry{}}
}
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			e.Tick(now.UTC())
		}
	}
}
func (e *Engine) Tick(now time.Time) {
	ids, err := e.store.ListEnabledInstances()
	if err != nil {
		log.Printf("tuning list instances failed: %v", err)
		return
	}
	for _, id := range ids {
		p, ok, err := e.store.GetPolicy(id)
		if err != nil {
			continue
		}
		if !ok {
			p = PolicyRecord{InstanceID: id, Policy: DefaultPolicy(), Mode: "observe"}
		}
		if now.Sub(e.last[id]) >= time.Duration(p.Policy.Scheduling.WindowMinutes)*time.Minute {
			n, c := e.evaluate(id, p, now)
			e.last[id] = now
			log.Printf("tuning duty evaluation instance=%s active_channels=%d recommendations=%d mode=%s", id, c, n, p.Mode)
		}
	}
	if expirer, ok := e.store.(interface {
		ExpirePendingRecommendations(time.Time) (int64, error)
	}); ok {
		_, _ = expirer.ExpirePendingRecommendations(now.Add(-60 * time.Minute))
	}
	e.fillOutcomes(now)
}
func (e *Engine) evaluate(id string, pr PolicyRecord, now time.Time) (int, int) {
	metrics, err := e.store.QueryMetrics(id, now.Add(-time.Duration(pr.Policy.Scheduling.WindowMinutes)*time.Minute), now)
	if err != nil {
		return 0, 0
	}
	channels, err := e.store.LatestChannels(id)
	if err != nil {
		return 0, 0
	}
	dispatchRows, err := e.store.ListDispatchStates(id)
	if err != nil {
		return 0, 0
	}
	mm := map[int64]ChannelMetric{}
	for _, m := range metrics {
		mm[m.ChannelID] = m
	}
	dispatch := map[int64]DispatchState{}
	for _, state := range dispatchRows {
		dispatch[state.ChannelID] = state
	}
	mode := normalizedMode(pr.Mode)
	made := e.recordMixedChannels(id, channels, mode, now)
	evaluated := 0
	for model, ladder := range buildLadders(channels) {
		available := make([]Channel, 0, len(ladder))
		for _, ch := range ladder {
			state, demoted := dispatch[ch.ID]
			if !demoted || state.NextTrialAt == nil {
				available = append(available, ch)
			}
		}
		if len(available) == 0 {
			trialDue := false
			for _, ch := range ladder {
				state, ok := dispatch[ch.ID]
				if ok && state.NextTrialAt != nil && !now.Before(*state.NextTrialAt) {
					trialDue = true
					break
				}
			}
			if !trialDue {
				made += e.recordInfo(id, ladder[0], "ladder_exhausted", model, mode, now, map[string]any{"model": model})
			}
			continue
		}
		top := available[0].Priority
		for _, ch := range available {
			if ch.Priority != top {
				break
			}
			m, ok := mm[ch.ID]
			if !ok {
				continue
			}
			evaluated++
			made += e.evaluateActive(id, model, ch, ladder, m, pr.Policy, mode, dispatch, now)
		}
		made += e.evaluateDynamicWeights(id, model, available, mm, pr.Policy, mode, dispatch, now)
	}
	made += e.scheduleTrials(id, channels, dispatch, pr.Policy, mode, now)
	return made, evaluated
}

func (e *Engine) evaluateDynamicWeights(id, model string, channels []Channel, metrics map[int64]ChannelMetric, p Policy, mode string, dispatch map[int64]DispatchState, now time.Time) int {
	d := p.DynamicWeighting
	if !d.Enabled || len(channels) < 2 {
		return 0
	}
	top := channels[0].Priority
	eligible := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		m, ok := metrics[ch.ID]
		_, degraded := dispatch[ch.ID]
		if ch.Priority == top && ok && !degraded && m.RequestCount >= p.Scheduling.MinSamples {
			eligible = append(eligible, ch)
		}
	}
	if len(eligible) < 2 {
		return 0
	}
	var total float64
	var baseTTFT, baseError, baseCache, baseOTPS float64
	for _, ch := range eligible {
		m := metrics[ch.ID]
		w := float64(m.RequestCount)
		total += w
		baseTTFT += positiveLatency(m) * w
		baseError += m.ErrorRate() * w
		baseCache += m.CacheHitRate * w
		baseOTPS += m.OTPS * w
	}
	if total == 0 {
		return 0
	}
	baseTTFT, baseError, baseCache, baseOTPS = baseTTFT/total, baseError/total, baseCache/total, baseOTPS/total
	made := 0
	for _, ch := range eligible {
		m := metrics[ch.ID]
		ttftFactor := ratioFactor(baseTTFT, positiveLatency(m), d.TTFTInfluence)
		errorFactor := ratioFactor(1-m.ErrorRate(), 1-baseError, d.ErrorInfluence)
		cacheFactor := 1.0
		if m.CacheHitRate > baseCache {
			cacheFactor = math.Pow(1+(m.CacheHitRate-baseCache), d.CacheInfluence)
		}
		otpsFactor := ratioFactor(m.OTPS, baseOTPS, d.OTPSInfluence)
		raw := clamp(ttftFactor*errorFactor*cacheFactor*otpsFactor, d.MinMultiplier, d.MaxMultiplier)
		smoothed := 1 + d.SmoothingAlpha*(raw-1)
		protected := clamp(smoothed, 1-d.MaxDecreasePerRound, 1+d.MaxIncreasePerRound)
		proposed := max(int64(math.Round(float64(ch.Weight)*protected)), 1)
		if proposed == ch.Weight {
			continue
		}
		recent, err := e.store.HasRecentRecommendation(id, ch.ID, "rebalance", now.Add(-time.Duration(p.Scheduling.WindowMinutes)*time.Minute))
		if err != nil || recent {
			continue
		}
		ev := map[string]any{
			"model": model, "samples": m.RequestCount,
			"ttft_p95": positiveLatency(m), "baseline_ttft_p95": baseTTFT,
			"error_rate": m.ErrorRate(), "baseline_error_rate": baseError,
			"cache_hit_rate": m.CacheHitRate, "baseline_cache_hit_rate": baseCache,
			"otps": m.OTPS, "baseline_otps": baseOTPS,
			"ttft_factor": ttftFactor, "error_factor": errorFactor,
			"cache_factor": cacheFactor, "otps_factor": otpsFactor,
			"raw_multiplier": raw, "protected_multiplier": protected,
			"observe_only": true,
			"window_start": now.Add(-time.Duration(p.Scheduling.WindowMinutes) * time.Minute), "window_end": now,
		}
		currentPriority := ch.Priority
		if e.store.InsertRecommendation(Recommendation{
			ID: NewID(now, id, ch.ID, "rebalance"), InstanceID: id, ChannelID: ch.ID,
			ChannelName: ch.Name, CreatedAt: now, Rule: "rebalance", Evidence: ev,
			CurrentWeight: ch.Weight, ProposedWeight: proposed,
			CurrentPriority: &currentPriority, ProposedPriority: &currentPriority,
			ModeAtCreation: mode, Status: "recorded",
		}) == nil {
			made++
		}
	}
	return made
}

func positiveLatency(m ChannelMetric) float64 {
	if m.TTFTP95 > 0 {
		return m.TTFTP95
	}
	return m.P95
}

func ratioFactor(numerator, denominator, influence float64) float64 {
	if influence == 0 || numerator <= 0 || denominator <= 0 {
		return 1
	}
	return math.Pow(numerator/denominator, influence)
}

func clamp(value, low, high float64) float64 {
	return math.Max(low, math.Min(high, value))
}

func buildLadders(channels []Channel) map[string][]Channel {
	out := map[string][]Channel{}
	for _, ch := range channels {
		if !enabled(ch.Status) || len(ch.Models) != 1 {
			continue
		}
		model := strings.TrimSpace(ch.Models[0])
		if model != "" {
			out[model] = append(out[model], ch)
		}
	}
	for model := range out {
		sort.SliceStable(out[model], func(i, j int) bool {
			if out[model][i].Priority == out[model][j].Priority {
				return out[model][i].ID < out[model][j].ID
			}
			return out[model][i].Priority > out[model][j].Priority
		})
	}
	return out
}

func enabled(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "enabled", "enable", "active", "normal", "1":
		return true
	default:
		return false
	}
}

func (e *Engine) evaluateActive(id, model string, ch Channel, ladder []Channel, m ChannelMetric, p Policy, mode string, dispatch map[int64]DispatchState, now time.Time) int {
	scheduling := p.Scheduling
	criteria := criteriaFor(p, model)
	key := id + ":" + fmtInt(ch.ID)
	local := e.states[key]
	if local == nil {
		local = &channelState{}
		e.states[key] = local
	}
	if m.RequestCount < scheduling.MinSamples {
		local.errorWindows, local.latencyWindows = 0, 0
		return 0
	}
	rate := m.ErrorRate()
	severe := rate >= criteria.SevereThreshold
	if rate >= criteria.ErrorRateThreshold {
		local.errorWindows++
	} else {
		local.errorWindows = 0
	}
	baseline, baselineOK := e.baseline(id, ch.ID, scheduling.MinSamples, now)
	latencyBad := baselineOK && m.P95 >= criteria.LatencyFloorSeconds && m.P95 >= baseline*criteria.LatencyMultiplier
	if latencyBad {
		local.latencyWindows++
	} else {
		local.latencyWindows = 0
	}
	trigger := ""
	if severe {
		trigger = "severe"
	} else if local.errorWindows >= criteria.SustainedWindows {
		trigger = "degrade"
	} else if local.latencyWindows >= criteria.SustainedWindows {
		trigger = "latency_degrade"
	}
	if trigger == "" {
		if state, trialing := dispatch[ch.ID]; trialing && state.NextTrialAt == nil {
			local.trialHealthyWindows++
			if local.trialHealthyWindows >= scheduling.TrialWindows {
				_ = e.store.DeleteDispatchState(id, ch.ID)
				delete(dispatch, ch.ID)
				local.trialHealthyWindows = 0
			}
		}
		return 0
	}
	local.trialHealthyWindows = 0
	if !e.actionAllowed(id, ch.ID, p, now) {
		return 0
	}
	backup, exhausted := findBackup(ladder, ch.ID, dispatch)
	if backup == nil {
		rule := "no_backup"
		if exhausted {
			rule = "ladder_exhausted"
		}
		return e.recordInfo(id, ch, rule, model, mode, now, map[string]any{
			"trigger": trigger, "error_rate": rate, "error_rate_total": m.TotalErrorRate(), "user_error_count": m.UserErrorCount, "p95": m.P95,
		})
	}
	current := ch.Priority
	proposed := ladder[len(ladder)-1].Priority - 1
	ev := map[string]any{
		"trigger": trigger, "model": model, "error_rate": rate, "error_rate_total": m.TotalErrorRate(), "user_error_count": m.UserErrorCount,
		"criteria_name":        criteria.Name,
		"error_rate_threshold": criteria.ErrorRateThreshold, "severe_threshold": criteria.SevereThreshold,
		"min_samples": scheduling.MinSamples, "samples": m.RequestCount,
		"p95": m.P95, "baseline_p95": baseline,
		"latency_multiplier": criteria.LatencyMultiplier, "latency_floor_seconds": criteria.LatencyFloorSeconds,
		"backup_channel_id": backup.ID, "backup_channel_name": backup.Name,
		"window_start": now.Add(-time.Duration(scheduling.WindowMinutes) * time.Minute), "window_end": now,
	}
	rec := Recommendation{
		ID: NewID(now, id, ch.ID, "demote"), InstanceID: id, ChannelID: ch.ID,
		ChannelName: ch.Name, CreatedAt: now, Rule: "demote", Evidence: ev,
		CurrentWeight: ch.Weight, ProposedWeight: ch.Weight,
		CurrentPriority: &current, ProposedPriority: &proposed,
		ModeAtCreation: mode, Status: actionStatus(mode),
	}
	if e.store.InsertRecommendation(rec) != nil {
		return 0
	}
	attempts := 0
	if old, ok := dispatch[ch.ID]; ok {
		attempts = old.TrialAttempts + 1
	}
	next := now.Add(trialDelay(p, attempts))
	state := DispatchState{
		InstanceID: id, ChannelID: ch.ID, ModelName: model,
		OriginalPriority: current, DemotedAt: now, TrialAttempts: attempts,
		NextTrialAt: &next, UpdatedAt: now,
	}
	if mode == "observe" {
		if e.store.PutDispatchState(state) == nil {
			dispatch[ch.ID] = state
		}
	}
	local.errorWindows, local.latencyWindows = 0, 0
	return 1
}

func (e *Engine) scheduleTrials(id string, channels []Channel, states map[int64]DispatchState, p Policy, mode string, now time.Time) int {
	scheduling := p.Scheduling
	byID := map[int64]Channel{}
	for _, ch := range channels {
		byID[ch.ID] = ch
	}
	made := 0
	for channelID, state := range states {
		if state.NextTrialAt == nil || now.Before(*state.NextTrialAt) {
			continue
		}
		ch, ok := byID[channelID]
		if !ok || !enabled(ch.Status) || !e.actionAllowed(id, channelID, p, now) {
			continue
		}
		current, proposed := ch.Priority, state.OriginalPriority
		criteria := criteriaFor(p, state.ModelName)
		rec := Recommendation{
			ID: NewID(now, id, ch.ID, "trial"), InstanceID: id, ChannelID: ch.ID,
			ChannelName: ch.Name, CreatedAt: now, Rule: "trial",
			Evidence: map[string]any{
				"model": state.ModelName, "hypothetical": true,
				"criteria_name":  criteria.Name,
				"trial_attempts": state.TrialAttempts, "demoted_at": state.DemotedAt,
				"min_samples": scheduling.MinSamples, "error_rate_threshold": criteria.ErrorRateThreshold,
				"latency_multiplier": criteria.LatencyMultiplier, "latency_floor_seconds": criteria.LatencyFloorSeconds,
			},
			CurrentWeight: ch.Weight, ProposedWeight: ch.Weight,
			CurrentPriority: &current, ProposedPriority: &proposed,
			ModeAtCreation: mode, Status: actionStatus(mode),
		}
		if e.store.InsertRecommendation(rec) != nil {
			continue
		}
		if mode == "observe" {
			state.NextTrialAt = nil
			state.UpdatedAt = now
			_ = e.store.PutDispatchState(state)
			states[channelID] = state
		}
		made++
	}
	return made
}

func (e *Engine) recordMixedChannels(id string, channels []Channel, mode string, now time.Time) int {
	made := 0
	for _, ch := range channels {
		if enabled(ch.Status) && len(ch.Models) > 1 {
			made += e.recordInfo(id, ch, "mixed_channel", "", mode, now, map[string]any{"models": ch.Models})
		}
	}
	return made
}

func (e *Engine) recordInfo(id string, ch Channel, rule, model, mode string, now time.Time, ev map[string]any) int {
	exists, err := e.store.HasRecentRecommendation(id, ch.ID, rule, now.Add(-24*time.Hour))
	if err != nil || exists {
		return 0
	}
	if model != "" {
		ev["model"] = model
	}
	current := ch.Priority
	if e.store.InsertRecommendation(Recommendation{
		ID: NewID(now, id, ch.ID, rule), InstanceID: id, ChannelID: ch.ID,
		ChannelName: ch.Name, CreatedAt: now, Rule: rule, Evidence: ev,
		CurrentWeight: ch.Weight, ProposedWeight: ch.Weight,
		CurrentPriority: &current, ProposedPriority: &current,
		ModeAtCreation: mode, Status: "recorded",
	}) != nil {
		return 0
	}
	return 1
}

func normalizedMode(mode string) string {
	if mode == "confirm" {
		return "confirm"
	}
	return "observe"
}

func actionStatus(mode string) string {
	if mode == "confirm" {
		return "pending"
	}
	return "recorded"
}

func (e *Engine) actionAllowed(id string, channelID int64, p Policy, now time.Time) bool {
	// One open decision per channel: while a pending action recommendation
	// awaits confirm, re-evaluations must not stack duplicates (the window is
	// longer than the cooldown, so every pass would add one) or burn the
	// daily budget on repeats of the same decision.
	if checker, ok := e.store.(interface {
		HasPendingActionRecommendation(string, int64) (bool, error)
	}); ok {
		if pending, err := checker.HasPendingActionRecommendation(id, channelID); err != nil || pending {
			return false
		}
	}
	last, ok, err := e.store.LastActionRecommendationAt(id, channelID)
	if err != nil || (ok && now.Sub(last) < time.Duration(p.Scheduling.CooldownMinutes)*time.Minute) {
		return false
	}
	count, err := e.store.CountActionRecommendations(id, channelID, now.Add(-24*time.Hour))
	return err == nil && count < p.Scheduling.DailyActionLimit
}

func findBackup(ladder []Channel, activeID int64, dispatch map[int64]DispatchState) (*Channel, bool) {
	if len(ladder) <= 1 {
		return nil, false
	}
	activePriority := priorityOf(ladder, activeID)
	available := false
	for i := range ladder {
		if ladder[i].ID == activeID {
			continue
		}
		if _, demoted := dispatch[ladder[i].ID]; demoted {
			continue
		}
		available = true
		if ladder[i].Priority < activePriority {
			return &ladder[i], false
		}
	}
	return nil, !available
}

func priorityOf(ladder []Channel, id int64) int64 {
	for _, ch := range ladder {
		if ch.ID == id {
			return ch.Priority
		}
	}
	return math.MinInt64
}

func trialDelay(p Policy, attempts int) time.Duration {
	minutes := float64(p.Scheduling.TrialInitialMinutes) * math.Pow(p.Scheduling.TrialBackoffFactor, float64(attempts))
	if minutes > float64(p.Scheduling.TrialMaxMinutes) {
		minutes = float64(p.Scheduling.TrialMaxMinutes)
	}
	return time.Duration(minutes * float64(time.Minute))
}

func (e *Engine) baseline(id string, channelID, minSamples int64, now time.Time) (float64, bool) {
	key := id + ":" + fmtInt(channelID)
	if cached, ok := e.baselines[key]; ok && now.Before(cached.expiresAt) {
		return cached.value, cached.ok
	}
	values, err := e.store.QueryP95Buckets(id, channelID, now.Add(-24*time.Hour), now, minSamples)
	if err != nil || len(values) < 8 {
		e.baselines[key] = baselineEntry{expiresAt: now.Add(10 * time.Minute)}
		return 0, false
	}
	sort.Float64s(values)
	mid := len(values) / 2
	value := values[mid]
	if len(values)%2 == 0 {
		value = (values[mid-1] + values[mid]) / 2
	}
	e.baselines[key] = baselineEntry{value: value, ok: true, expiresAt: now.Add(10 * time.Minute)}
	return value, true
}

func (e *Engine) fillOutcomes(now time.Time) {
	defaultPolicy := DefaultPolicy()
	defaultCriteria := criteriaFor(defaultPolicy, "")
	items, err := e.store.PendingOutcomes(now.Add(-30*time.Minute), 100)
	if err != nil {
		return
	}
	for _, r := range items {
		if r.Rule != "demote" && r.Rule != "trial" {
			_ = e.store.UpdateOutcome(r.ID, map[string]any{"informational": true}, now, nil)
			continue
		}
		ms, err := e.store.QueryMetrics(r.InstanceID, r.CreatedAt, r.CreatedAt.Add(30*time.Minute))
		if err != nil {
			continue
		}
		var m ChannelMetric
		for _, x := range ms {
			if x.ChannelID == r.ChannelID {
				m = x
			}
		}
		errorThreshold := evidenceNumber(r.Evidence, "error_rate_threshold", defaultCriteria.ErrorRateThreshold)
		latencyMultiplier := evidenceNumber(r.Evidence, "latency_multiplier", defaultCriteria.LatencyMultiplier)
		latencyFloor := evidenceNumber(r.Evidence, "latency_floor_seconds", defaultCriteria.LatencyFloorSeconds)
		baseline := evidenceNumber(r.Evidence, "baseline_p95", 0)
		minSamples := int64(evidenceNumber(r.Evidence, "min_samples", float64(defaultPolicy.Scheduling.MinSamples)))
		out := map[string]any{
			"error_rate": m.ErrorRate(), "error_rate_total": m.TotalErrorRate(), "user_error_count": m.UserErrorCount, "samples": m.RequestCount, "p95": m.P95,
			"error_rate_threshold": errorThreshold, "baseline_p95": baseline,
		}
		var hit *bool
		if m.RequestCount < minSamples {
			out["insufficient_samples"] = true
		} else {
			bad := m.ErrorRate() >= errorThreshold ||
				(baseline > 0 && m.P95 >= latencyFloor && m.P95 >= baseline*latencyMultiplier)
			v := bad
			if r.Rule == "trial" {
				v = !bad
			}
			hit = &v
		}
		_ = e.store.UpdateOutcome(r.ID, out, now, hit)
	}
}

func evidenceNumber(evidence map[string]any, key string, fallback float64) float64 {
	value, ok := evidence[key]
	if !ok {
		return fallback
	}
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case json.Number:
		if parsed, err := number.Float64(); err == nil {
			return parsed
		}
	}
	return fallback
}
func fmtInt(v int64) string {
	const d = "0123456789"
	if v == 0 {
		return "0"
	}
	b := make([]byte, 0, 20)
	for v > 0 {
		b = append([]byte{d[v%10]}, b...)
		v /= 10
	}
	return string(b)
}
