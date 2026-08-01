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
	CreateContinuousProbe(Recommendation, string, int, int, time.Time) (string, error)
}

type continuousBaseline struct {
	ttft50, ttft90, ttft95 float64
	cache, otps            float64
}

// evaluateContinuous implements the v3.0 continuous dispatch state machine,
// including B3 circuit breaking, active probes, and one-cycle soft start.
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
	states, err := cs.ListContinuousStates(id)
	if err != nil {
		return 0, 0
	}
	metricByID := map[int64]ChannelMetric{}
	stateByID := map[int64]ContinuousState{}
	for _, v := range metrics {
		metricByID[v.ChannelID] = v
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
			if state.Phase == "" {
				state.Phase = "normal"
			}
			state.KSpeed, state.KCache, state.KOTPS = 1, 1, 1

			// Mixed-channel fuse (design §4): the weight knob is channel-wide,
			// so a channel serving several models must never be auto-tuned on
			// one model's metrics.
			if len(base.Models) > 1 {
				state.Multiplier = 1
				state.ProposedWeight = base.BaseWeight
				if state.PausedReason != "mixed_channel" {
					state.PausedReason = "mixed_channel"
					_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "mixed_channel", mode, now))
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				continue
			}
			if state.PausedReason == "mixed_channel" {
				state.PausedReason = ""
			}

			m := metricByID[base.ChannelID]
			requests, channelErrors := e.foldErrorDecay(id, base.ChannelID, &state, now)
			recoveredNow := false

			// A completed probe round is folded before normal factor evaluation.
			if state.Phase == "probing" && state.ProbeCommandID == nil && state.ProbeAttempts > 0 {
				successRatio := float64(state.ProbeSuccesses) / float64(state.ProbeAttempts)
				probeSpeed := 1.0
				if state.ProbeSuccesses > 0 && healthy && baseline.ttft95 > 0 {
					avg := state.ProbeDurationSum / float64(state.ProbeSuccesses)
					if avg > 0 {
						probeSpeed = clamp(math.Sqrt(baseline.ttft95/avg), .1, 1)
					}
				}
				probeMultiplier := successRatio * probeSpeed
				if probeMultiplier >= p.RecoveryThreshold {
					state.Phase, state.SoftStartPending = "soft_start", true
					state.Multiplier = p.SoftStartMultiplier
					state.ProposedWeight = max(int64(1), int64(math.Round(float64(base.BaseWeight)*state.Multiplier)))
					rec := continuousEvent(id, base, state, "circuit_recovered", mode, now)
					if state.OriginalPriority != nil {
						rec.ProposedPriority = state.OriginalPriority
					}
					if mode == "auto" {
						if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
							recoveredNow = true
							w := state.ProposedWeight
							at := now
							state.LastWrittenWeight = &w
							state.LastWriteAt = &at
							writes++
						} else {
							state.Phase, state.SoftStartPending = "circuit", false
							next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
							state.NextProbeAt = &next
						}
					} else {
						recoveredNow = true
						_ = e.store.InsertRecommendation(rec)
					}
				} else {
					state.Phase = "circuit"
					next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
					state.NextProbeAt = &next
					_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "probe_failed", mode, now))
				}
				state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
			}
			if recoveredNow {
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}
			if state.Phase == "circuit" {
				if mode == "auto" && state.NextProbeAt != nil && !now.Before(*state.NextProbeAt) {
					rec := continuousEvent(id, base, state, "probe_started", mode, now)
					if commandID, probeErr := cs.CreateContinuousProbe(rec, model, p.ProbeCount, p.ProbeIntervalSeconds, now); probeErr == nil {
						state.Phase = "probing"
						state.ProbeCommandID = &commandID
					}
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}
			if state.Phase == "probing" {
				// Do not strand a channel forever when a probe command is lost or
				// expires before an Agent reports it.
				if state.NextProbeAt != nil && now.After(state.NextProbeAt.Add(10*time.Minute)) {
					state.Phase, state.ProbeCommandID = "circuit", nil
					next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
					state.NextProbeAt = &next
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}
			if state.Phase == "soft_start" && state.SoftStartPending {
				state.SoftStartPending = false
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			} else if state.Phase == "soft_start" {
				state.Phase = "normal"
				state.CircuitOpenedAt = nil
				state.NextProbeAt = nil
			}
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
			if mode != "off" && state.Phase == "normal" && state.Multiplier < p.CircuitThreshold {
				state.Phase = "circuit"
				state.Multiplier = 0
				state.ProposedWeight = 0
				opened := now
				next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
				original := base.BasePriority
				state.CircuitOpenedAt = &opened
				state.NextProbeAt = &next
				state.OriginalPriority = &original
				rec := continuousEvent(id, base, state, "circuit_opened", mode, now)
				zero := int64(0)
				rec.ProposedPriority = &zero
				if mode == "auto" {
					if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
						w := int64(0)
						at := now
						state.LastWrittenWeight = &w
						state.LastWriteAt = &at
						writes++
					} else {
						state.Phase = "normal"
						state.Multiplier = state.KSpeed * state.KCache * state.KOTPS * state.KError
						state.ProposedWeight = max(int64(1), int64(math.Round(float64(base.BaseWeight)*state.Multiplier)))
						state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = nil, nil, nil
					}
				} else {
					_ = e.store.InsertRecommendation(rec)
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}

			// Manual-override detection. Snapshots refresh every ~10 minutes,
			// so a stale snapshot differing from our write is expected, not
			// evidence: only flag when the snapshot was captured well after
			// the write (command-apply grace) and still disagrees.
			if state.LastWrittenWeight != nil && base.CurrentWeight == *state.LastWrittenWeight {
				if state.PausedReason == "manual_override" {
					state.PausedReason = ""
				}
			} else if state.LastWrittenWeight != nil && state.LastWriteAt != nil &&
				base.SnapshotAt.After(state.LastWriteAt.Add(2*time.Minute)) && state.PausedReason == "" {
				state.PausedReason = "manual_override"
				_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "manual_takeover", mode, now))
			}

			// Deduplicate against our own last write, not the snapshot value:
			// the snapshot lags for minutes after a write and would otherwise
			// re-issue the same command every evaluation.
			alreadyWritten := state.LastWrittenWeight != nil && *state.LastWrittenWeight == state.ProposedWeight
			if mode == "auto" && state.PausedReason == "" && !alreadyWritten && state.ProposedWeight != base.CurrentWeight {
				rec := continuousEvent(id, base, state, "weight_write", mode, now)
				if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
					written := state.ProposedWeight
					at := now
					state.LastWrittenWeight, state.LastWriteAt = &written, &at
					writes++
				}
			}
			state.LastObservedRequests, state.LastObservedErrors = requests, channelErrors
			state.UpdatedAt = now
			_ = cs.PutContinuousState(state)
			evaluated++
		}
	}
	return writes, evaluated
}

// foldErrorDecay advances KError over the complete metric buckets newer than
// the stored cursor. Buckets land late (agent reports every ~30s), so only
// buckets older than a 90s settling lag are folded, each exactly once.
func (e *Engine) foldErrorDecay(id string, channelID int64, state *ContinuousState, now time.Time) (int64, int64) {
	since := now.Add(-15 * time.Minute)
	if state.LastBucketAt != nil && state.LastBucketAt.After(since) {
		since = *state.LastBucketAt
	}
	buckets, err := e.store.QueryRecentChannelBuckets(id, channelID, since, 240)
	if err != nil {
		return state.LastObservedRequests, state.LastObservedErrors
	}
	settled := now.Add(-90 * time.Second)
	var requests, channelErrors int64
	newest := state.LastBucketAt
	for _, bucket := range buckets {
		if bucket.BucketTime.After(settled) {
			continue
		}
		if state.LastBucketAt != nil && !bucket.BucketTime.After(*state.LastBucketAt) {
			continue
		}
		errs := max(bucket.ErrorCount-bucket.UserErrorCount, 0)
		successes := max(bucket.RequestCount-bucket.ErrorCount, 0)
		state.KError = clamp(state.KError*math.Pow(.8, float64(errs))*math.Pow(1.08, float64(successes)), .001, 1)
		requests += bucket.RequestCount
		channelErrors += errs
		if newest == nil || bucket.BucketTime.After(*newest) {
			at := bucket.BucketTime
			newest = &at
		}
	}
	state.LastBucketAt = newest
	return requests, channelErrors
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
		Evidence:      map[string]any{"model": base.ModelName, "phase": state.Phase, "multiplier": state.Multiplier, "k_speed": state.KSpeed, "k_cache": state.KCache, "k_otps": state.KOTPS, "k_error": state.KError, "probe_attempts": state.ProbeAttempts, "probe_successes": state.ProbeSuccesses},
		CurrentWeight: base.CurrentWeight, ProposedWeight: state.ProposedWeight, CurrentPriority: &base.CurrentPriority, ProposedPriority: &base.BasePriority, ModeAtCreation: mode, Status: "recorded"}
}
