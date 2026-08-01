package tuning

import (
	"math"
	"time"
)

type continuousStore interface {
	ListChannelBaseValues(string, string) ([]ChannelBaseValue, error)
	ListContinuousStates(string) ([]ContinuousState, error)
	PutContinuousState(ContinuousState) error
	CreateContinuousWeightChange(Recommendation, string, time.Time) (string, error)
}

type continuousBaseline struct {
	ttft50, ttft90, ttft95 float64
	cache, otps            float64
}

// evaluateContinuous implements v3.0-B2. Circuit/probe transitions are
// intentionally deferred to B3; B2 persists the factor state and exposes the
// value that would be written in observe mode.
func (e *Engine) evaluateContinuous(id string, pr PolicyRecord, now time.Time, cs continuousStore) (int, int) {
	p := pr.Policy.Continuous
	values, err := cs.ListChannelBaseValues(id, "")
	if err != nil || len(values) == 0 {
		return 0, 0
	}
	metrics, err := e.store.QueryMetrics(id, now.Add(-time.Duration(p.WindowMinutes)*time.Minute), now)
	if err != nil {
		return 0, 0
	}
	minute, _ := e.store.QueryMetrics(id, now.Add(-time.Minute), now)
	states, err := cs.ListContinuousStates(id)
	if err != nil {
		return 0, 0
	}
	metricByID := map[int64]ChannelMetric{}
	minuteByID := map[int64]ChannelMetric{}
	stateByID := map[int64]ContinuousState{}
	for _, v := range metrics {
		metricByID[v.ChannelID] = v
	}
	for _, v := range minute {
		minuteByID[v.ChannelID] = v
	}
	for _, v := range states {
		stateByID[v.ChannelID] = v
	}

	groups := map[string][]ChannelBaseValue{}
	for _, v := range values {
		groups[v.ModelName] = append(groups[v.ModelName], v)
	}
	writes := 0
	evaluated := 0
	for model, rows := range groups {
		mode := pr.Policy.DispatchModes[model]
		if mode == "" {
			mode = "off"
		}
		baseline, healthy := buildContinuousBaseline(rows, metricByID, p.MinSamples)
		for _, base := range rows {
			previous, exists := stateByID[base.ChannelID]
			state := previous
			state.InstanceID, state.ChannelID, state.ModelName = id, base.ChannelID, model
			if !exists || state.KError <= 0 {
				state.KError = 1
			}
			state.KSpeed, state.KCache, state.KOTPS = 1, 1, 1
			m := metricByID[base.ChannelID]
			last := minuteByID[base.ChannelID]
			channelErrors := max(last.ErrorCount-last.UserErrorCount, 0)
			successes := max(last.RequestCount-last.ErrorCount, 0)
			state.KError = clamp(state.KError*math.Pow(.8, float64(channelErrors))*math.Pow(1.08, float64(successes)), .001, 1)
			if healthy && mode != "off" && m.RequestCount >= p.MinSamples {
				state.KSpeed = speedFactor(m, baseline, p.Sensitivity)
				state.KCache = continuousRatioFactor(m.CacheHitRate, baseline.cache, p.Sensitivity, 1.5)
				state.KOTPS = continuousRatioFactor(m.OTPS, baseline.otps, p.Sensitivity, p.OTPSCap)
			}
			state.Multiplier = math.Min(1.5, state.KSpeed*state.KCache*state.KOTPS*state.KError)
			if mode == "off" || !healthy {
				state.Multiplier = 1
			}
			state.ProposedWeight = int64(math.Round(float64(base.BaseWeight) * state.Multiplier))
			if state.ProposedWeight < 1 && base.BaseWeight > 0 {
				state.ProposedWeight = 1
			}

			// A differing value after the command grace period means somebody
			// changed new-api outside CT. Pause this channel until it is synced.
			if state.LastWrittenWeight != nil && base.CurrentWeight == *state.LastWrittenWeight {
				state.PausedReason = ""
			} else if state.LastWrittenWeight != nil && state.LastWriteAt != nil && now.Sub(*state.LastWriteAt) > 2*time.Minute && state.PausedReason == "" {
				state.PausedReason = "manual_override"
				_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "manual_takeover", mode, now))
			}

			if mode == "auto" && state.PausedReason == "" && state.ProposedWeight != base.CurrentWeight {
				rec := continuousEvent(id, base, state, "weight_write", mode, now)
				if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
					written := state.ProposedWeight
					at := now
					state.LastWrittenWeight, state.LastWriteAt = &written, &at
					writes++
				}
			}
			state.LastObservedRequests, state.LastObservedErrors = last.RequestCount, channelErrors
			state.UpdatedAt = now
			_ = cs.PutContinuousState(state)
			evaluated++
		}
	}
	return writes, evaluated
}

func buildContinuousBaseline(rows []ChannelBaseValue, metrics map[int64]ChannelMetric, minSamples int64) (continuousBaseline, bool) {
	var b continuousBaseline
	n := 0.0
	for _, row := range rows {
		m := metrics[row.ChannelID]
		if m.RequestCount < minSamples || m.TTFTP50 <= 0 || m.TTFTP90 <= 0 || m.TTFTP95 <= 0 {
			continue
		}
		b.ttft50 += m.TTFTP50
		b.ttft90 += m.TTFTP90
		b.ttft95 += m.TTFTP95
		b.cache += m.CacheHitRate
		b.otps += m.OTPS
		n++
	}
	if n < 2 {
		return b, false
	}
	b.ttft50 /= n
	b.ttft90 /= n
	b.ttft95 /= n
	b.cache /= n
	b.otps /= n
	return b, true
}

func speedFactor(m ChannelMetric, b continuousBaseline, sensitivity float64) float64 {
	if m.TTFTP50 <= 0 || m.TTFTP90 <= 0 || m.TTFTP95 <= 0 {
		return 1
	}
	r := .5*(m.TTFTP50/b.ttft50) + .3*(m.TTFTP90/b.ttft90) + .2*(m.TTFTP95/b.ttft95)
	if r <= 0 {
		return 1
	}
	return math.Pow(1/r, .5*sensitivity)
}

func continuousRatioFactor(value, average, sensitivity, upper float64) float64 {
	if value <= 0 || average <= 0 {
		return 1
	}
	return math.Min(upper, math.Pow(value/average, .5*sensitivity))
}

func continuousEvent(id string, base ChannelBaseValue, state ContinuousState, rule, mode string, now time.Time) Recommendation {
	return Recommendation{ID: NewID(now, id, base.ChannelID, rule), InstanceID: id, ChannelID: base.ChannelID, ChannelName: base.ChannelName, CreatedAt: now, Rule: rule,
		Evidence:      map[string]any{"model": base.ModelName, "multiplier": state.Multiplier, "k_speed": state.KSpeed, "k_cache": state.KCache, "k_otps": state.KOTPS, "k_error": state.KError},
		CurrentWeight: base.CurrentWeight, ProposedWeight: state.ProposedWeight, CurrentPriority: &base.CurrentPriority, ProposedPriority: &base.BasePriority, ModeAtCreation: mode, Status: "recorded"}
}
