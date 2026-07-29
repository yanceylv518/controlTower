package tuning

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrRecommendationNotFound   = errors.New("recommendation_not_found")
	ErrRecommendationNotPending = errors.New("recommendation_not_pending")
	ErrNoTargetInstance         = errors.New("no_target_instance")
)

type Policy struct {
	WindowMinutes       int     `json:"window_minutes"`
	MinSamples          int64   `json:"min_samples"`
	ErrorRateThreshold  float64 `json:"error_rate_threshold"`
	SevereThreshold     float64 `json:"severe_threshold"`
	LatencyMultiplier   float64 `json:"latency_multiplier"`
	LatencyFloorSeconds float64 `json:"latency_floor_seconds"`
	SustainedWindows    int     `json:"sustained_windows"`
	TrialInitialMinutes int     `json:"trial_initial_minutes"`
	TrialBackoffFactor  float64 `json:"trial_backoff_factor"`
	TrialMaxMinutes     int     `json:"trial_max_minutes"`
	TrialWindows        int     `json:"trial_windows"`
	CooldownMinutes     int     `json:"cooldown_minutes"`
	DailyActionLimit    int     `json:"daily_action_limit"`
}

func DefaultPolicy() Policy {
	return Policy{15, 20, .15, .50, 2, 10, 2, 60, 2, 1440, 2, 10, 6}
}
func (p Policy) Validate() map[string]string {
	e := map[string]string{}
	for name, value := range map[string]int{
		"window_minutes": p.WindowMinutes, "trial_initial_minutes": p.TrialInitialMinutes,
		"trial_max_minutes": p.TrialMaxMinutes, "cooldown_minutes": p.CooldownMinutes,
	} {
		if value < 1 || value > 2880 {
			e[name] = "must_be_between_1_and_2880"
		}
	}
	if p.MinSamples < 1 || p.MinSamples > 1000 {
		e["min_samples"] = "must_be_between_1_and_1000"
	}
	if p.ErrorRateThreshold <= 0 || p.ErrorRateThreshold > 1 {
		e["error_rate_threshold"] = "must_be_greater_than_0_and_at_most_1"
	}
	if p.SevereThreshold <= 0 || p.SevereThreshold > 1 {
		e["severe_threshold"] = "must_be_greater_than_0_and_at_most_1"
	}
	if p.ErrorRateThreshold >= p.SevereThreshold {
		e["error_rate_threshold"] = "must_be_less_than_severe_threshold"
	}
	if p.LatencyMultiplier < 1 {
		e["latency_multiplier"] = "must_be_at_least_1"
	}
	if p.LatencyFloorSeconds <= 0 {
		e["latency_floor_seconds"] = "must_be_positive"
	}
	if p.SustainedWindows < 1 {
		e["sustained_windows"] = "must_be_positive"
	}
	if p.TrialBackoffFactor < 1 {
		e["trial_backoff_factor"] = "must_be_at_least_1"
	}
	if p.TrialWindows < 1 {
		e["trial_windows"] = "must_be_positive"
	}
	if p.DailyActionLimit < 1 {
		e["daily_action_limit"] = "must_be_positive"
	}
	return e
}

type PolicyRecord struct {
	InstanceID      string
	Policy          Policy
	Mode, UpdatedBy string
	UpdatedAt       time.Time
}
type ChannelMetric struct {
	ChannelID                int64
	RequestCount, ErrorCount int64
	P95                      float64
}

func (m ChannelMetric) ErrorRate() float64 {
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
