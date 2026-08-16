package tuning

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type SchedulingParams struct {
	WindowMinutes         int   `json:"window_minutes"`
	MinSamples            int64 `json:"min_samples"`
	SparseMinSamples      int64 `json:"sparse_min_samples"`
	SparseLookbackMinutes int   `json:"sparse_lookback_minutes"`
}

// ContinuousDispatchParams is the v3.0 continuous weighting policy.
type ContinuousDispatchParams struct {
	Sensitivity             float64 `json:"sensitivity"`
	SpeedExponent           float64 `json:"speed_exponent"`
	SpeedMinFactor          float64 `json:"speed_min_factor"`
	SpeedMaxFactor          float64 `json:"speed_max_factor"`
	CacheExponent           float64 `json:"cache_exponent"`
	CacheMinFactor          float64 `json:"cache_min_factor"`
	CacheMaxFactor          float64 `json:"cache_max_factor"`
	OTPSExponent            float64 `json:"otps_exponent"`
	OTPSMinFactor           float64 `json:"otps_min_factor"`
	OTPSMaxFactor           float64 `json:"otps_max_factor"`
	ErrorHealthyRate        float64 `json:"error_healthy_rate"`
	ErrorDegradedRate       float64 `json:"error_degraded_rate"`
	ErrorPoorRate           float64 `json:"error_poor_rate"`
	ErrorFloorRate          float64 `json:"error_floor_rate"`
	ErrorDegradedFactor     float64 `json:"error_degraded_factor"`
	ErrorPoorFactor         float64 `json:"error_poor_factor"`
	ErrorMinFactor          float64 `json:"error_min_factor"`
	CombinedMinFactor       float64 `json:"combined_min_factor"`
	CombinedMaxFactor       float64 `json:"combined_max_factor"`
	OTPSCap                 float64 `json:"otps_cap"`
	CircuitThreshold        float64 `json:"circuit_threshold"`
	RecoveryThreshold       float64 `json:"recovery_threshold"`
	CircuitErrorRate        float64 `json:"circuit_error_rate"`
	RecoveryErrorRate       float64 `json:"recovery_error_rate"`
	SilentMinutes           int     `json:"silent_minutes"`
	ProbeIntervalSeconds    int     `json:"probe_interval_seconds"`
	ProbeCount              int     `json:"probe_count"`
	SoftStartMultiplier     float64 `json:"soft_start_multiplier"`
	WindowMinutes           int     `json:"window_minutes"`
	MinSamples              int64   `json:"min_samples"`
	SparseLookbackMinutes   int     `json:"sparse_lookback_minutes"`
	WriteDeadbandPercent    float64 `json:"write_deadband_percent"`
	MinWriteIntervalMinutes int     `json:"min_write_interval_minutes"`
}

type Policy struct {
	Scheduling    SchedulingParams         `json:"scheduling"`
	DispatchModes map[string]string        `json:"dispatch_modes"`
	Continuous    ContinuousDispatchParams `json:"continuous"`
}

func DefaultPolicy() Policy {
	return Policy{
		Scheduling: SchedulingParams{
			WindowMinutes: 15, MinSamples: 20, SparseMinSamples: 10, SparseLookbackMinutes: 360,
		},
		DispatchModes: map[string]string{},
		Continuous: ContinuousDispatchParams{
			Sensitivity: 1, SpeedExponent: .35, SpeedMinFactor: .75, SpeedMaxFactor: 1.25,
			CacheExponent: .15, CacheMinFactor: .90, CacheMaxFactor: 1.10,
			OTPSExponent: .25, OTPSMinFactor: .80, OTPSMaxFactor: 1.20,
			ErrorHealthyRate: .01, ErrorDegradedRate: .05, ErrorPoorRate: .15, ErrorFloorRate: .30,
			ErrorDegradedFactor: .85, ErrorPoorFactor: .50, ErrorMinFactor: .20,
			CombinedMinFactor: .50, CombinedMaxFactor: 1.50,
			OTPSCap: 1.5, CircuitThreshold: .1, RecoveryThreshold: .2,
			CircuitErrorRate: .30, RecoveryErrorRate: .10,
			SilentMinutes: 5, ProbeIntervalSeconds: 5, ProbeCount: 10, SoftStartMultiplier: .2,
			WindowMinutes: 15, MinSamples: 20, SparseLookbackMinutes: 360,
			WriteDeadbandPercent: 5, MinWriteIntervalMinutes: 5,
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
	if shape["scheduling"] == nil && shape["dispatch_modes"] == nil && shape["continuous"] == nil {
		return DefaultPolicy(), nil
	}
	p := DefaultPolicy()
	if err := json.Unmarshal(raw, &p); err != nil {
		return Policy{}, err
	}
	// encoding/json deliberately ignores retired duty-rotation fields. This
	// keeps persisted policies readable without reactivating old semantics.
	if p.DispatchModes == nil {
		p.DispatchModes = map[string]string{}
	}
	return p, nil
}

func (p Policy) Validate() map[string]string {
	e := map[string]string{}
	c := p.Continuous
	if c.Sensitivity <= 0 || c.Sensitivity > 5 {
		e["continuous.sensitivity"] = "must_be_greater_than_0_and_at_most_5"
	}
	if c.SpeedExponent <= 0 || c.SpeedExponent > 2 {
		e["continuous.speed_exponent"] = "must_be_greater_than_0_and_at_most_2"
	}
	if c.SpeedMinFactor <= 0 || c.SpeedMinFactor > 1 {
		e["continuous.speed_min_factor"] = "must_be_greater_than_0_and_at_most_1"
	}
	if c.SpeedMaxFactor < 1 || c.SpeedMaxFactor > 3 || c.SpeedMaxFactor <= c.SpeedMinFactor {
		e["continuous.speed_max_factor"] = "must_be_greater_than_min_and_between_1_and_3"
	}
	if c.CacheExponent <= 0 || c.CacheExponent > 2 {
		e["continuous.cache_exponent"] = "must_be_greater_than_0_and_at_most_2"
	}
	if c.CacheMinFactor <= 0 || c.CacheMinFactor > 1 || c.CacheMaxFactor < 1 || c.CacheMaxFactor > 3 || c.CacheMaxFactor <= c.CacheMinFactor {
		e["continuous.cache_factor_range"] = "must_include_1_and_be_ordered"
	}
	if c.OTPSExponent <= 0 || c.OTPSExponent > 2 {
		e["continuous.otps_exponent"] = "must_be_greater_than_0_and_at_most_2"
	}
	if c.OTPSMinFactor <= 0 || c.OTPSMinFactor > 1 || c.OTPSMaxFactor < 1 || c.OTPSMaxFactor > 3 || c.OTPSMaxFactor <= c.OTPSMinFactor {
		e["continuous.otps_factor_range"] = "must_include_1_and_be_ordered"
	}
	if c.ErrorHealthyRate < 0 || c.ErrorDegradedRate <= c.ErrorHealthyRate || c.ErrorPoorRate <= c.ErrorDegradedRate || c.ErrorFloorRate <= c.ErrorPoorRate || c.ErrorFloorRate > 1 {
		e["continuous.error_rate_breakpoints"] = "must_be_strictly_increasing_between_0_and_1"
	}
	if c.ErrorDegradedFactor <= 0 || c.ErrorDegradedFactor >= 1 || c.ErrorPoorFactor <= 0 || c.ErrorPoorFactor >= c.ErrorDegradedFactor || c.ErrorMinFactor <= 0 || c.ErrorMinFactor >= c.ErrorPoorFactor {
		e["continuous.error_factors"] = "must_be_strictly_decreasing_between_0_and_1"
	}
	if c.CombinedMinFactor <= 0 || c.CombinedMinFactor > 1 || c.CombinedMaxFactor < 1 || c.CombinedMaxFactor > 5 || c.CombinedMaxFactor <= c.CombinedMinFactor {
		e["continuous.combined_factor_range"] = "must_include_1_and_be_ordered"
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
	if c.CircuitErrorRate <= 0 || c.CircuitErrorRate > 1 {
		e["continuous.circuit_error_rate"] = "must_be_between_0_and_1"
	}
	if c.RecoveryErrorRate < 0 || c.RecoveryErrorRate >= c.CircuitErrorRate {
		e["continuous.recovery_error_rate"] = "must_be_nonnegative_and_less_than_circuit_error_rate"
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
	if c.WriteDeadbandPercent < 0 || c.WriteDeadbandPercent > 50 {
		e["continuous.write_deadband_percent"] = "must_be_between_0_and_50"
	}
	if c.MinWriteIntervalMinutes < 1 || c.MinWriteIntervalMinutes > 60 {
		e["continuous.min_write_interval_minutes"] = "must_be_between_1_and_60"
	}
	if p.Scheduling.WindowMinutes < 1 || p.Scheduling.WindowMinutes > 2880 {
		e["scheduling.window_minutes"] = "must_be_between_1_and_2880"
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
	GroupName       string `json:"group_name"`
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
	CachePromptTokens, OTPSSampleTokens      int64
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
	MetricReady          bool       `json:"metric_ready"`
	BaselineReady        bool       `json:"baseline_ready"`
	MetricTTFTP50        float64    `json:"metric_ttft_p50"`
	MetricTTFTP90        float64    `json:"metric_ttft_p90"`
	MetricTTFTP95        float64    `json:"metric_ttft_p95"`
	BaselineTTFTP50      float64    `json:"baseline_ttft_p50"`
	BaselineTTFTP90      float64    `json:"baseline_ttft_p90"`
	BaselineTTFTP95      float64    `json:"baseline_ttft_p95"`
	MetricCache          float64    `json:"metric_cache"`
	BaselineCache        float64    `json:"baseline_cache"`
	CacheReady           bool       `json:"cache_ready"`
	MetricOTPS           float64    `json:"metric_otps"`
	BaselineOTPS         float64    `json:"baseline_otps"`
	OTPSReady            bool       `json:"otps_ready"`
	SmoothedErrorRate    float64    `json:"smoothed_error_rate"`
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
	// Direct-control write failure accounting: after a streak of failed
	// new-api writes the channel pauses (paused_reason=write_failed) and
	// retries on a slow interval instead of hammering every tick.
	WriteFailureStreak int        `json:"write_failure_streak,omitempty"`
	LastWriteFailureAt *time.Time `json:"last_write_failure_at,omitempty"`
	LastWriteError     string     `json:"last_write_error,omitempty"`
	// LastObservedWeight anchors the observe-event deadband at the value of
	// the last recorded weight_observed event (level filter). Comparing with
	// the previous tick instead would rate-filter: a slow drift never exceeds
	// the threshold per step and stays unrecorded no matter how far it goes.
	LastObservedWeight *int64    `json:"last_observed_weight,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
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
	GroupName    string
	Weight       int64
	Models       []string
	Priority     int64
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
	Total  int64
	ByRule map[string]int64
}

func NewID(now time.Time, instance string, channel int64, rule string) string {
	return fmt.Sprintf("tun-%d-%s-%d-%s", now.UnixNano(), instance, channel, rule)
}
