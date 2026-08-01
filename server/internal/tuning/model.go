package tuning

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrRecommendationNotFound   = errors.New("recommendation_not_found")
	ErrRecommendationNotPending = errors.New("recommendation_not_pending")
	ErrNoTargetInstance         = errors.New("no_target_instance")
)

type SchedulingParams struct {
	WindowMinutes         int     `json:"window_minutes"`
	MinSamples            int64   `json:"min_samples"`
	SparseMinSamples      int64   `json:"sparse_min_samples"`
	SparseLookbackMinutes int     `json:"sparse_lookback_minutes"`
	TrialInitialMinutes   int     `json:"trial_initial_minutes"`
	TrialBackoffFactor    float64 `json:"trial_backoff_factor"`
	TrialMaxMinutes       int     `json:"trial_max_minutes"`
	TrialWindows          int     `json:"trial_windows"`
	CooldownMinutes       int     `json:"cooldown_minutes"`
	DailyActionLimit      int     `json:"daily_action_limit"`
}

type DynamicWeightingParams struct {
	Mode                string  `json:"mode"`
	TTFTInfluence       float64 `json:"ttft_influence"`
	ErrorInfluence      float64 `json:"error_influence"`
	CacheInfluence      float64 `json:"cache_influence"`
	OTPSInfluence       float64 `json:"otps_influence"`
	MinMultiplier       float64 `json:"min_multiplier"`
	MaxMultiplier       float64 `json:"max_multiplier"`
	SmoothingAlpha      float64 `json:"smoothing_alpha"`
	MaxIncreasePerRound float64 `json:"max_increase_per_round"`
	MaxDecreasePerRound float64 `json:"max_decrease_per_round"`
}

type DegradeCriteria struct {
	Name                string  `json:"name"`
	ErrorRateThreshold  float64 `json:"error_rate_threshold"`
	SevereThreshold     float64 `json:"severe_threshold"`
	LatencyMultiplier   float64 `json:"latency_multiplier"`
	LatencyFloorSeconds float64 `json:"latency_floor_seconds"`
	SustainedWindows    int     `json:"sustained_windows"`
}

// ContinuousDispatchParams is the v3.0 continuous weighting policy.  The
// former shift/demotion fields are still decoded for rolling upgrades, but
// the v3 engine only evaluates this section.
type ContinuousDispatchParams struct {
	Sensitivity           float64 `json:"sensitivity"`
	OTPSCap               float64 `json:"otps_cap"`
	CircuitThreshold      float64 `json:"circuit_threshold"`
	RecoveryThreshold     float64 `json:"recovery_threshold"`
	SilentMinutes         int     `json:"silent_minutes"`
	ProbeIntervalSeconds  int     `json:"probe_interval_seconds"`
	ProbeCount            int     `json:"probe_count"`
	SoftStartMultiplier   float64 `json:"soft_start_multiplier"`
	WindowMinutes         int     `json:"window_minutes"`
	MinSamples            int64   `json:"min_samples"`
	SparseLookbackMinutes int     `json:"sparse_lookback_minutes"`
}

type Policy struct {
	Scheduling       SchedulingParams         `json:"scheduling"`
	DynamicWeighting DynamicWeightingParams   `json:"dynamic_weighting"`
	Criteria         []DegradeCriteria        `json:"criteria"`
	Assignments      map[string]string        `json:"assignments"`
	DispatchModes    map[string]string        `json:"dispatch_modes"`
	Continuous       ContinuousDispatchParams `json:"continuous"`
}

func DefaultPolicy() Policy {
	return Policy{
		Scheduling: SchedulingParams{
			WindowMinutes: 15, MinSamples: 20, SparseMinSamples: 10, SparseLookbackMinutes: 360, TrialInitialMinutes: 60,
			TrialBackoffFactor: 2, TrialMaxMinutes: 1440, TrialWindows: 2,
			CooldownMinutes: 10, DailyActionLimit: 6,
		},
		DynamicWeighting: DynamicWeightingParams{
			Mode: "observe", TTFTInfluence: .50, ErrorInfluence: .30,
			CacheInfluence: .10, OTPSInfluence: .10,
			MinMultiplier: .50, MaxMultiplier: 1.50, SmoothingAlpha: .30,
			MaxIncreasePerRound: .20, MaxDecreasePerRound: .30,
		},
		Criteria: []DegradeCriteria{{
			Name: "default", ErrorRateThreshold: .15, SevereThreshold: .50,
			LatencyMultiplier: 2, LatencyFloorSeconds: 10, SustainedWindows: 2,
		}},
		Assignments:   map[string]string{},
		DispatchModes: map[string]string{},
		Continuous: ContinuousDispatchParams{
			Sensitivity: 1, OTPSCap: 1.5, CircuitThreshold: .1, RecoveryThreshold: .2,
			SilentMinutes: 5, ProbeIntervalSeconds: 5, ProbeCount: 10, SoftStartMultiplier: .2,
			WindowMinutes: 15, MinSamples: 20, SparseLookbackMinutes: 360,
		},
	}
}

// DecodePolicyJSON accepts only the structured v2.9-B2.5 representation.
// A valid legacy flat policy is intentionally treated as the default policy:
// v2.9 was never deployed, and silently mapping fields would preserve stale
// v2.1 semantics instead of making the schema transition explicit.
func DecodePolicyJSON(raw []byte) (Policy, error) {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return Policy{}, err
	}
	if shape["scheduling"] == nil && shape["criteria"] == nil && shape["assignments"] == nil {
		return DefaultPolicy(), nil
	}
	p := DefaultPolicy()
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, err
	}
	var dynamicShape map[string]json.RawMessage
	if dynamicRaw := shape["dynamic_weighting"]; dynamicRaw != nil {
		if err := json.Unmarshal(dynamicRaw, &dynamicShape); err != nil {
			return Policy{}, err
		}
		if dynamicShape["mode"] == nil {
			var enabled bool
			if legacy := dynamicShape["enabled"]; legacy != nil {
				if err := json.Unmarshal(legacy, &enabled); err != nil {
					return Policy{}, err
				}
				if enabled {
					p.DynamicWeighting.Mode = "observe"
				} else {
					p.DynamicWeighting.Mode = "off"
				}
			}
		}
	}
	if p.Assignments == nil {
		p.Assignments = map[string]string{}
	}
	if p.DispatchModes == nil {
		p.DispatchModes = map[string]string{}
	}
	return p, nil
}

func criteriaFor(p Policy, model string) DegradeCriteria {
	name := p.Assignments[model]
	if name == "" {
		name = "default"
	}
	for _, criteria := range p.Criteria {
		if criteria.Name == name {
			return criteria
		}
	}
	for _, criteria := range p.Criteria {
		if criteria.Name == "default" {
			return criteria
		}
	}
	return DefaultPolicy().Criteria[0]
}

func (p Policy) Validate() map[string]string {
	e := map[string]string{}
	c := p.Continuous
	if c.Sensitivity <= 0 || c.Sensitivity > 5 {
		e["continuous.sensitivity"] = "must_be_greater_than_0_and_at_most_5"
	}
	if c.OTPSCap < 1 || c.OTPSCap > 3 {
		e["continuous.otps_cap"] = "must_be_between_1_and_3"
	}
	if c.CircuitThreshold <= 0 || c.CircuitThreshold >= c.RecoveryThreshold {
		e["continuous.circuit_threshold"] = "must_be_positive_and_less_than_recovery"
	}
	if c.RecoveryThreshold > 1 {
		e["continuous.recovery_threshold"] = "must_be_at_most_1"
	}
	if c.SilentMinutes < 1 || c.SilentMinutes > 1440 {
		e["continuous.silent_minutes"] = "must_be_between_1_and_1440"
	}
	if c.ProbeIntervalSeconds < 1 || c.ProbeCount < 1 {
		e["continuous.probe"] = "must_be_positive"
	}
	if c.SoftStartMultiplier <= 0 || c.SoftStartMultiplier > 1 {
		e["continuous.soft_start_multiplier"] = "must_be_greater_than_0_and_at_most_1"
	}
	if c.WindowMinutes < 1 || c.WindowMinutes > 1440 {
		e["continuous.window_minutes"] = "must_be_between_1_and_1440"
	}
	if c.MinSamples < 1 {
		e["continuous.min_samples"] = "must_be_positive"
	}
	if c.SparseLookbackMinutes < c.WindowMinutes || c.SparseLookbackMinutes > 2880 {
		e["continuous.sparse_lookback_minutes"] = "must_be_between_window_and_2880"
	}
	for name, value := range map[string]int{
		"window_minutes": p.Scheduling.WindowMinutes, "trial_initial_minutes": p.Scheduling.TrialInitialMinutes,
		"trial_max_minutes": p.Scheduling.TrialMaxMinutes, "cooldown_minutes": p.Scheduling.CooldownMinutes,
	} {
		if value < 1 || value > 2880 {
			e["scheduling."+name] = "must_be_between_1_and_2880"
		}
	}
	if p.Scheduling.MinSamples < 1 || p.Scheduling.MinSamples > 1000 {
		e["scheduling.min_samples"] = "must_be_between_1_and_1000"
	}
	if p.Scheduling.SparseMinSamples < 1 || p.Scheduling.SparseMinSamples > p.Scheduling.MinSamples {
		e["scheduling.sparse_min_samples"] = "must_be_between_1_and_min_samples"
	}
	if p.Scheduling.SparseLookbackMinutes < p.Scheduling.WindowMinutes || p.Scheduling.SparseLookbackMinutes > 2880 {
		e["scheduling.sparse_lookback_minutes"] = "must_be_between_window_minutes_and_2880"
	}
	if p.Scheduling.TrialBackoffFactor < 1 {
		e["scheduling.trial_backoff_factor"] = "must_be_at_least_1"
	}
	if p.Scheduling.TrialWindows < 1 {
		e["scheduling.trial_windows"] = "must_be_positive"
	}
	if p.Scheduling.DailyActionLimit < 1 {
		e["scheduling.daily_action_limit"] = "must_be_positive"
	}
	d := p.DynamicWeighting
	if d.Mode != "off" && d.Mode != "observe" && d.Mode != "auto" {
		e["dynamic_weighting.mode"] = "must_be_off_observe_or_auto"
	}
	for name, value := range map[string]float64{
		"ttft_influence": d.TTFTInfluence, "error_influence": d.ErrorInfluence,
		"cache_influence": d.CacheInfluence, "otps_influence": d.OTPSInfluence,
	} {
		if value < 0 || value > 1 {
			e["dynamic_weighting."+name] = "must_be_between_0_and_1"
		}
	}
	if d.MinMultiplier <= 0 || d.MinMultiplier > 1 {
		e["dynamic_weighting.min_multiplier"] = "must_be_greater_than_0_and_at_most_1"
	}
	if d.MaxMultiplier < 1 || d.MaxMultiplier > 5 || d.MaxMultiplier < d.MinMultiplier {
		e["dynamic_weighting.max_multiplier"] = "must_be_between_1_and_5_and_not_less_than_min"
	}
	for name, value := range map[string]float64{
		"smoothing_alpha":        d.SmoothingAlpha,
		"max_increase_per_round": d.MaxIncreasePerRound,
		"max_decrease_per_round": d.MaxDecreasePerRound,
	} {
		if value <= 0 || value > 1 {
			e["dynamic_weighting."+name] = "must_be_greater_than_0_and_at_most_1"
		}
	}
	if len(p.Criteria) == 0 {
		e["criteria"] = "must_include_default"
		return e
	}
	seen := map[string]bool{}
	for _, criteria := range p.Criteria {
		prefix := "criteria[" + criteria.Name + "]."
		if criteria.Name == "" {
			e["criteria[].name"] = "must_not_be_empty"
			continue
		}
		if seen[criteria.Name] {
			e[prefix+"name"] = "must_be_unique"
		}
		seen[criteria.Name] = true
		if criteria.ErrorRateThreshold <= 0 || criteria.ErrorRateThreshold > 1 {
			e[prefix+"error_rate_threshold"] = "must_be_greater_than_0_and_at_most_1"
		}
		if criteria.SevereThreshold <= 0 || criteria.SevereThreshold > 1 {
			e[prefix+"severe_threshold"] = "must_be_greater_than_0_and_at_most_1"
		}
		if criteria.ErrorRateThreshold >= criteria.SevereThreshold {
			e[prefix+"error_rate_threshold"] = "must_be_less_than_severe_threshold"
		}
		if criteria.LatencyMultiplier < 1 {
			e[prefix+"latency_multiplier"] = "must_be_at_least_1"
		}
		if criteria.LatencyFloorSeconds <= 0 {
			e[prefix+"latency_floor_seconds"] = "must_be_positive"
		}
		if criteria.SustainedWindows < 1 {
			e[prefix+"sustained_windows"] = "must_be_positive"
		}
	}
	if !seen["default"] {
		e["criteria"] = "must_include_default"
	}
	for model, name := range p.Assignments {
		if !seen[name] {
			e["assignments."+model] = "criteria_not_found"
		}
	}
	for model, mode := range p.DispatchModes {
		if strings.TrimSpace(model) == "" {
			e["dispatch_modes"] = "model_must_not_be_empty"
		}
		if mode != "off" && mode != "observe" && mode != "auto" {
			e["dispatch_modes."+model] = "must_be_off_observe_or_auto"
		}
	}
	return e
}

type ChannelBaseValue struct {
	InstanceID      string `json:"instance_id"`
	ChannelID       int64  `json:"channel_id"`
	ChannelName     string `json:"channel_name"`
	ModelName       string `json:"model_name"`
	BaseWeight      int64  `json:"base_weight"`
	BasePriority    int64  `json:"base_priority"`
	CurrentWeight   int64  `json:"current_weight"`
	CurrentPriority int64  `json:"current_priority"`
	// SnapshotAt is when CurrentWeight/CurrentPriority were captured from
	// new-api. Manual-override detection must compare against it: snapshots
	// refresh every ~10 minutes, so a value differing from our last write is
	// only evidence of an external change once the snapshot postdates the
	// write (plus command-apply grace).
	SnapshotAt time.Time `json:"snapshot_at,omitempty"`
	Models     []string  `json:"models,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	UpdatedBy  string    `json:"updated_by,omitempty"`
}

type PolicyRecord struct {
	InstanceID      string
	Policy          Policy
	Mode, UpdatedBy string
	UpdatedAt       time.Time
}
type ChannelMetric struct {
	ChannelID                                int64
	RequestCount, ErrorCount, UserErrorCount int64
	P95                                      float64
	TTFTP50, TTFTP90, TTFTP95                float64
	CacheHitRate                             float64
	OTPS                                     float64
}

type ContinuousState struct {
	InstanceID           string     `json:"instance_id"`
	ChannelID            int64      `json:"channel_id"`
	ModelName            string     `json:"model_name"`
	KError               float64    `json:"k_error"`
	KSpeed               float64    `json:"k_speed"`
	KCache               float64    `json:"k_cache"`
	KOTPS                float64    `json:"k_otps"`
	Multiplier           float64    `json:"multiplier"`
	ProposedWeight       int64      `json:"proposed_weight"`
	LastWrittenWeight    *int64     `json:"last_written_weight,omitempty"`
	LastWriteAt          *time.Time `json:"last_write_at,omitempty"`
	LastObservedRequests int64      `json:"last_observed_requests"`
	LastObservedErrors   int64      `json:"last_observed_errors"`
	// LastBucketAt is the newest metric bucket already folded into KError.
	// Buckets arrive late (agent reports every ~30s), so the decay must walk
	// complete buckets past this cursor instead of re-reading "the last
	// minute" — that both misses late counts and re-counts on jitter.
	LastBucketAt     *time.Time `json:"last_bucket_at,omitempty"`
	PausedReason     string     `json:"paused_reason,omitempty"`
	Phase            string     `json:"phase"`
	CircuitOpenedAt  *time.Time `json:"circuit_opened_at,omitempty"`
	NextProbeAt      *time.Time `json:"next_probe_at,omitempty"`
	ProbeCommandID   *string    `json:"probe_command_id,omitempty"`
	ProbeAttempts    int        `json:"probe_attempts"`
	ProbeSuccesses   int        `json:"probe_successes"`
	ProbeDurationSum float64    `json:"probe_duration_sum"`
	OriginalPriority *int64     `json:"original_priority,omitempty"`
	SoftStartPending bool       `json:"soft_start_pending"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type RecentChannelBucket struct {
	BucketTime                               time.Time
	RequestCount, ErrorCount, UserErrorCount int64
}

func (m ChannelMetric) ErrorRate() float64 {
	if m.RequestCount == 0 {
		return 0
	}
	return float64(max(m.ErrorCount-m.UserErrorCount, 0)) / float64(m.RequestCount)
}

func (m ChannelMetric) TotalErrorRate() float64 {
	if m.RequestCount == 0 {
		return 0
	}
	return float64(m.ErrorCount) / float64(m.RequestCount)
}

type Channel struct {
	ID           int64
	Name, Status string
	Weight       int64
	Models       []string
	Priority     int64
}
type DispatchState struct {
	InstanceID       string
	ChannelID        int64
	ModelName        string
	OriginalPriority int64
	DemotedAt        time.Time
	TrialAttempts    int
	NextTrialAt      *time.Time
	UpdatedAt        time.Time
}
type Recommendation struct {
	ID, InstanceID, ChannelName, Rule string
	ChannelID                         int64
	CreatedAt                         time.Time
	Evidence                          map[string]any
	CurrentWeight, ProposedWeight     int64
	CurrentPriority, ProposedPriority *int64
	ModeAtCreation, Status            string
	CommandID                         *string
	Outcome                           map[string]any
	OutcomeAt                         *time.Time
	Hit                               *bool
	ActedBy                           string
	ActedAt                           *time.Time
}
type RecommendationQuery struct {
	InstanceID string
	Rule       string
	Limit      int
	Before     time.Time
	Days       int
}
type Report struct {
	Total, Adopted int64
	ByRule         map[string]int64
	// Filled counts backfilled rows; Judged excludes insufficient-sample rows
	// (hit IS NULL) so the auto-mode criterion is not biased by unjudgeable data.
	Filled, Judged, Hits int64
}

func NewID(now time.Time, instance string, channel int64, rule string) string {
	return fmt.Sprintf("tun-%d-%s-%d-%s", now.UnixNano(), instance, channel, rule)
}
