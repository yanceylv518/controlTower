package tuning

import (
	"fmt"
	"time"
)

type Rule struct {
	ErrorRateThreshold float64 `json:"error_rate_threshold"`
	SustainedWindows   int     `json:"sustained_windows"`
	WeightStepRatio    float64 `json:"weight_step_ratio"`
	WeightFloor        int64   `json:"weight_floor,omitempty"`
}
type Severe struct {
	// Parsed and stored for forward compatibility; B1 does not evaluate priority_drop.
	ErrorRateThreshold float64 `json:"error_rate_threshold"`
	Action             string  `json:"action"`
}
type Policy struct {
	EvaluationWindowMinutes int    `json:"evaluation_window_minutes"`
	MinSamples              int64  `json:"min_samples"`
	Degrade                 Rule   `json:"degrade"`
	Severe                  Severe `json:"severe"`
	Recover                 Rule   `json:"recover"`
	CooldownMinutes         int    `json:"cooldown_minutes"`
}

func DefaultPolicy() Policy {
	return Policy{15, 20, Rule{.15, 2, .5, 1}, Severe{.5, "priority_drop"}, Rule{.02, 4, 2, 0}, 10}
}
func (p Policy) Validate() map[string]string {
	e := map[string]string{}
	if p.EvaluationWindowMinutes <= 0 {
		e["evaluation_window_minutes"] = "must_be_positive"
	}
	if p.MinSamples <= 0 {
		e["min_samples"] = "must_be_positive"
	}
	if p.CooldownMinutes < 1 {
		e["cooldown_minutes"] = "must_be_at_least_1"
	}
	check := func(n string, r Rule, recover bool) {
		if r.ErrorRateThreshold < 0 || r.ErrorRateThreshold > 1 {
			e[n+".error_rate_threshold"] = "must_be_between_0_and_1"
		}
		if r.SustainedWindows <= 0 {
			e[n+".sustained_windows"] = "must_be_positive"
		}
		if r.WeightStepRatio <= 0 || (!recover && r.WeightStepRatio > 1) {
			e[n+".weight_step_ratio"] = "invalid_step_ratio"
		}
		if !recover && r.WeightFloor < 1 {
			e[n+".weight_floor"] = "must_be_at_least_1"
		}
	}
	check("degrade", p.Degrade, false)
	check("recover", p.Recover, true)
	if p.Severe.ErrorRateThreshold < 0 || p.Severe.ErrorRateThreshold > 1 {
		e["severe.error_rate_threshold"] = "must_be_between_0_and_1"
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
}
type RecommendationQuery struct {
	InstanceID string
	Limit      int
	Before     time.Time
	Days       int
}
type Report struct {
	Total        int64
	ByRule       map[string]int64
	Filled, Hits int64
}

func NewID(now time.Time, instance string, channel int64, rule string) string {
	return fmt.Sprintf("tun-%d-%s-%d-%s", now.UnixNano(), instance, channel, rule)
}
