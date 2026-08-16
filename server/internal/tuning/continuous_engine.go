package tuning

import (
	"log"
	"math"
	"sort"
	"time"
)

type ContinuousStore interface {
	ListChannelBaseValues(string, string) ([]ChannelBaseValue, error)
	ListContinuousStates(string) ([]ContinuousState, error)
	PutContinuousState(ContinuousState) error
	CreateContinuousWeightChange(Recommendation, string, time.Time) (string, error)
	CreateContinuousProbe(Recommendation, string, int, int, time.Time) (string, error)
}

type continuousRecentBucketsStore interface {
	QueryRecentChannelBucketsBySite(string, time.Time) (map[int64][]RecentChannelBucket, error)
}

type continuousBaseline struct {
	ttft50, ttft90, ttft95 float64
	cache, otps            float64
	cacheReady, otpsReady  bool
}

const (
	cacheEvidenceTokens = int64(10_000)
	otpsEvidenceTokens  = int64(100)

	// Control writes fail loudly on the direct path (immediate error) and as
	// enqueue errors on the agent path. A short streak pauses the channel so
	// the engine stops hammering new-api, then keeps probing on a slow
	// interval and self-heals when writes succeed again.
	writeFailurePauseThreshold = 3
	writeFailureRetryInterval  = 10 * time.Minute
)

// evaluateContinuous implements the v3.0 continuous dispatch state machine,
// including B3 circuit breaking, active probes, and one-cycle soft start.
func (e *Engine) evaluateContinuous(id string, pr PolicyRecord, now time.Time, cs ContinuousStore) (int, int) {
	evaluationStarted := time.Now()
	p := pr.Policy.Continuous
	activeModes := make(map[string]bool)
	for model, mode := range pr.Policy.DispatchModes {
		if mode == "observe" || mode == "auto" {
			activeModes[model] = true
		}
	}
	if len(activeModes) == 0 {
		log.Printf("tuning continuous evaluation site=%s skipped=no_active_models", id)
		return 0, 0
	}
	stageStarted := time.Now()
	values, err := cs.ListChannelBaseValues(id, "")
	baseDuration := time.Since(stageStarted)
	if err != nil {
		log.Printf("tuning continuous evaluation site=%s stage=base_values failed duration=%s error=%v", id, baseDuration, err)
		return 0, 0
	}
	if len(values) == 0 {
		log.Printf("tuning continuous evaluation site=%s stage=base_values empty duration=%s", id, baseDuration)
		return 0, 0
	}
	activeValues := values[:0]
	for _, value := range values {
		if activeModes[value.ModelName] {
			activeValues = append(activeValues, value)
		}
	}
	values = activeValues
	if len(values) == 0 {
		log.Printf("tuning continuous evaluation site=%s skipped=no_active_channels duration=%s", id, baseDuration)
		return 0, 0
	}
	stageStarted = time.Now()
	metrics, err := e.store.QueryMetrics(id, now.Add(-time.Duration(p.WindowMinutes)*time.Minute), now)
	metricsDuration := time.Since(stageStarted)
	if err != nil {
		log.Printf("tuning continuous evaluation site=%s stage=metrics failed duration=%s error=%v", id, metricsDuration, err)
		return 0, 0
	}
	stageStarted = time.Now()
	states, err := cs.ListContinuousStates(id)
	statesDuration := time.Since(stageStarted)
	if err != nil {
		log.Printf("tuning continuous evaluation site=%s stage=states failed duration=%s error=%v", id, statesDuration, err)
		return 0, 0
	}
	log.Printf("tuning continuous evaluation site=%s stages base_values=%s metrics=%s states=%s channels=%d metrics_rows=%d state_rows=%d", id, baseDuration, metricsDuration, statesDuration, len(values), len(metrics), len(states))
	var recentBuckets map[int64][]RecentChannelBucket
	if batchStore, ok := e.store.(continuousRecentBucketsStore); ok {
		stageStarted = time.Now()
		recentBuckets, err = batchStore.QueryRecentChannelBucketsBySite(id, now.Add(-15*time.Minute))
		if err != nil {
			log.Printf("tuning continuous evaluation site=%s stage=recent_buckets_batch failed duration=%s error=%v; falling back to per-channel queries", id, time.Since(stageStarted), err)
			recentBuckets = nil
		} else {
			log.Printf("tuning continuous evaluation site=%s stage=recent_buckets_batch duration=%s channels=%d", id, time.Since(stageStarted), len(recentBuckets))
		}
	}
	channelLoopStarted := time.Now()
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
			// manual_override is obsolete in authoritative auto tuning. Clear any
			// legacy state before the early-return branches below (mixed channel,
			// zero baseline, circuit and probing), so an old pause can never remain
			// visible or influence execution merely because the channel is not in
			// the normal phase.
			if state.PausedReason == "manual_override" {
				state.PausedReason = ""
			}
			// A write_failed pause is only meaningful while auto writes can
			// run; on observe/off models nothing could ever clear it, so the
			// stale label would stick until the mode flips back.
			if mode != "auto" && state.PausedReason == "write_failed" {
				e.noteWriteSuccess(&state)
			}
			m := metricByID[base.ChannelID]
			// Operator-facing sample progress describes the complete current
			// evaluation window, not only error buckets folded since the last pass.
			state.LastObservedRequests = m.RequestCount
			state.LastObservedErrors = max(m.ErrorCount-m.UserErrorCount, 0)
			state.MetricReady = m.RequestCount >= p.MinSamples && m.TTFTP50 > 0 && m.TTFTP90 > 0 && m.TTFTP95 > 0
			state.BaselineReady = healthy
			state.MetricTTFTP50, state.MetricTTFTP90, state.MetricTTFTP95 = m.TTFTP50, m.TTFTP90, m.TTFTP95
			state.BaselineTTFTP50, state.BaselineTTFTP90, state.BaselineTTFTP95 = baseline.ttft50, baseline.ttft90, baseline.ttft95
			state.MetricCache, state.BaselineCache = m.CacheHitRate, baseline.cache
			state.CacheReady = baseline.cacheReady && m.CachePromptTokens >= cacheEvidenceTokens
			state.MetricOTPS, state.BaselineOTPS = m.OTPS, baseline.otps
			state.OTPSReady = baseline.otpsReady && m.OTPSSampleTokens >= otpsEvidenceTokens

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
			// A zero base weight is an explicit no-traffic baseline. It cannot
			// produce a meaningful recovery weight, so exclude it from the
			// circuit state machine instead of recording "recovered to zero".
			if base.BaseWeight <= 0 {
				state.Phase = "normal"
				state.Multiplier, state.ProposedWeight = 1, 0
				state.CircuitOpenedAt, state.NextProbeAt, state.ProbeCommandID, state.OriginalPriority = nil, nil, nil, nil
				state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
				state.SoftStartPending = false
				if state.PausedReason == "write_failed" {
					e.noteWriteSuccess(&state)
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}

			wasCircuit := state.Phase == "circuit"
			foldedRequests, foldedErrors := e.foldErrorDecayWithBuckets(id, base.ChannelID, &state, now, recentBuckets, p)
			// In observe mode, settled production traffic is the passive probe.
			// Keep its evidence independent from the larger performance-ranking
			// sample threshold so a low-traffic channel can still prove recovery.
			if wasCircuit && mode == "observe" && foldedRequests > 0 {
				state.ProbeAttempts += int(foldedRequests)
				state.ProbeSuccesses += int(max(foldedRequests-foldedErrors, 0))
			}
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
					// The probes just proved the channel serves again, but KError
					// still carries the crushed pre-circuit value. Without a floor
					// the first normal cycle after soft start recomputes
					// M ≈ KError < circuit threshold and re-opens the circuit in a
					// probe/recover loop. Ten successful probes are success
					// evidence; floor the decay state at the recovery bar.
					state.SmoothedErrorRate = math.Min(state.SmoothedErrorRate, p.RecoveryErrorRate)
					state.KError = reliabilityFactorWithPolicy(state.SmoothedErrorRate, p)
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
							e.noteWriteSuccess(&state)
							writes++
						} else {
							state.Phase, state.SoftStartPending = "circuit", false
							next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
							state.NextProbeAt = &next
							e.noteWriteFailure(id, base, &state, mode, err, now)
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
			// Observe mode never removes traffic from the real channel, so normal
			// production requests remain valid recovery evidence. Re-evaluate an
			// observed circuit from those passive samples instead of waiting for an
			// active probe that is intentionally only available in auto mode.
			if state.Phase == "circuit" && mode == "observe" && state.ProbeAttempts >= p.ProbeCount {
				passiveErrors := state.ProbeAttempts - state.ProbeSuccesses
				passiveErrorRate := float64(passiveErrors) / float64(state.ProbeAttempts)
				if passiveErrorRate <= p.RecoveryErrorRate {
					state.SmoothedErrorRate = passiveErrorRate
					state.KError = reliabilityFactorWithPolicy(passiveErrorRate, p)
					if healthy && m.RequestCount >= p.MinSamples {
						state.KSpeed, state.KCache, state.KOTPS = performanceFactors(m, baseline, p, state.CacheReady, state.OTPSReady)
					}
					passiveMultiplier := combinedFactor(state, p)
					state.Phase = "normal"
					state.Multiplier = passiveMultiplier
					state.ProposedWeight = int64(math.Round(float64(base.BaseWeight) * passiveMultiplier))
					if state.ProposedWeight < 1 && base.BaseWeight > 0 {
						state.ProposedWeight = 1
					}
					state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = nil, nil, nil
					state.SoftStartPending = false
					state.ProbeAttempts, state.ProbeSuccesses = 0, 0
					_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "circuit_recovered", mode, now))
					state.UpdatedAt = now
					_ = cs.PutContinuousState(state)
					evaluated++
					continue
				}
				// A failed passive recovery round starts a fresh evidence batch;
				// otherwise an old failure would poison a quiet channel forever.
				state.ProbeAttempts, state.ProbeSuccesses = 0, 0
			}
			if state.Phase == "circuit" {
				if mode == "auto" && state.NextProbeAt != nil && !now.Before(*state.NextProbeAt) {
					// The probe counters are shared with observe-mode passive
					// accumulation. An active round must start from zero so a
					// mode switch cannot dilute the probe success ratio with
					// leftover passive evidence.
					state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
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
				state.KSpeed, state.KCache, state.KOTPS = performanceFactors(m, baseline, p, state.CacheReady, state.OTPSReady)
			}
			state.Multiplier = combinedFactor(state, p)
			if mode == "off" || !healthy {
				state.Multiplier = 1
			}
			state.ProposedWeight = int64(math.Round(float64(base.BaseWeight) * state.Multiplier))
			if state.ProposedWeight < 1 && base.BaseWeight > 0 {
				state.ProposedWeight = 1
			}
			// In auto mode entering circuit means "the zeroing write happened";
			// during a write_failed pause the transition must wait for the slow
			// retry window and only commit once the real write succeeds —
			// otherwise CT would show a circuit (and run recovery probes) while
			// new-api still serves the channel at full weight.
			if mode != "off" && state.Phase == "normal" && m.RequestCount >= p.MinSamples && state.SmoothedErrorRate >= p.CircuitErrorRate &&
				(mode != "auto" || writeAttemptAllowed(state, now)) {
				state.Phase = "circuit"
				state.Multiplier = 0
				state.ProposedWeight = 0
				opened := now
				next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
				original := base.BasePriority
				state.CircuitOpenedAt = &opened
				state.NextProbeAt = &next
				state.OriginalPriority = &original
				state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
				rec := continuousEvent(id, base, state, "circuit_opened", mode, now)
				zero := int64(0)
				rec.ProposedPriority = &zero
				if mode == "auto" {
					if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
						w := int64(0)
						at := now
						state.LastWrittenWeight = &w
						state.LastWriteAt = &at
						e.noteWriteSuccess(&state)
						writes++
					} else {
						state.Phase = "normal"
						state.Multiplier = combinedFactor(state, p)
						state.ProposedWeight = max(int64(1), int64(math.Round(float64(base.BaseWeight)*state.Multiplier)))
						state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = nil, nil, nil
						e.noteWriteFailure(id, base, &state, mode, err, now)
					}
				} else {
					_ = e.store.InsertRecommendation(rec)
				}
				state.UpdatedAt = now
				_ = cs.PutContinuousState(state)
				evaluated++
				continue
			}

			// Preserve useful observation evidence without adding one event per
			// channel every minute: only record a proposal outside the write
			// deadband RELATIVE TO THE LAST RECORDED EVENT. Anchoring on the
			// previous tick instead would rate-filter — a slow drift never
			// exceeds the threshold per step and would stay unrecorded no
			// matter how far it travels.
			if mode == "observe" && healthy && m.RequestCount >= p.MinSamples &&
				(state.LastObservedWeight == nil || weightChangeOutsideDeadband(state.ProposedWeight, *state.LastObservedWeight, base.BaseWeight, p.WriteDeadbandPercent)) {
				_ = e.store.InsertRecommendation(continuousEvent(id, base, state, "weight_observed", mode, now))
				anchor := state.ProposedWeight
				state.LastObservedWeight = &anchor
			}

			// Auto mode is authoritative: a confirmed external change is written
			// back to the current calculated weight instead of permanently pausing
			// the channel. The snapshot must postdate our write plus apply grace;
			// otherwise it is merely the expected stale snapshot after our own write.
			confirmedExternalChange := state.LastWrittenWeight != nil && state.LastWriteAt != nil &&
				base.CurrentWeight != *state.LastWrittenWeight && base.SnapshotAt.After(state.LastWriteAt.Add(2*time.Minute))
			// Recalculate every minute and write every integer target change. Keep
			// only exact-value deduplication so a stale channel snapshot does not
			// cause the same command to be sent repeatedly before it refreshes.
			needsWrite := state.ProposedWeight != writeReference(state, base) || confirmedExternalChange
			retryingWriteFailure := state.PausedReason == "write_failed" && writeAttemptAllowed(state, now)
			// A due retry with no write left has nothing useful to test.
			// Clear the pause; the next real change will exercise the path again.
			if mode == "auto" && retryingWriteFailure && !needsWrite {
				e.noteWriteSuccess(&state)
				retryingWriteFailure = false
			}
			if mode == "auto" && writeAttemptAllowed(state, now) && needsWrite {
				rec := continuousEvent(id, base, state, "weight_write", mode, now)
				if _, err = cs.CreateContinuousWeightChange(rec, "system:auto", now); err == nil {
					written := state.ProposedWeight
					at := now
					state.LastWrittenWeight, state.LastWriteAt = &written, &at
					e.noteWriteSuccess(&state)
					writes++
				} else {
					e.noteWriteFailure(id, base, &state, mode, err, now)
				}
			}
			state.UpdatedAt = now
			_ = cs.PutContinuousState(state)
			evaluated++
		}
	}
	log.Printf("tuning continuous evaluation site=%s stage=channel_loop duration=%s total_duration=%s", id, time.Since(channelLoopStarted), time.Since(evaluationStarted))
	return writes, evaluated
}

// foldErrorDecay advances KError over the complete metric buckets newer than
// the stored cursor. Buckets land late (agent reports every ~30s), so only
// buckets older than a 90s settling lag are folded, each exactly once.
func (e *Engine) foldErrorDecay(id string, channelID int64, state *ContinuousState, now time.Time) (int64, int64) {
	return e.foldErrorDecayWithBuckets(id, channelID, state, now, nil, DefaultPolicy().Continuous)
}

func (e *Engine) foldErrorDecayWithBuckets(id string, channelID int64, state *ContinuousState, now time.Time, recentBuckets map[int64][]RecentChannelBucket, p ContinuousDispatchParams) (int64, int64) {
	// One-time migration from the v1 multiplicative decay: a legacy state has
	// a decayed KError but no smoothed rate yet (EWMA never returns to exact
	// zero once fed). Seed the rate by inverting the reliability mapping so
	// upgraded channels keep their error memory instead of restarting from
	// an optimistic blank slate.
	if state.SmoothedErrorRate == 0 && state.KError > 0 && state.KError < 1 {
		state.SmoothedErrorRate = legacyErrorRateWithPolicy(state.KError, p)
		state.KError = reliabilityFactorWithPolicy(state.SmoothedErrorRate, p)
	}
	since := now.Add(-15 * time.Minute)
	if state.LastBucketAt != nil && state.LastBucketAt.After(since) {
		since = *state.LastBucketAt
	}
	buckets := recentBuckets[channelID]
	if recentBuckets == nil {
		var err error
		buckets, err = e.store.QueryRecentChannelBuckets(id, channelID, since, 240)
		if err != nil {
			return state.LastObservedRequests, state.LastObservedErrors
		}
	}
	settled := now.Add(-90 * time.Second)
	var requests, channelErrors int64
	newest := state.LastBucketAt
	// The store returns newest buckets first so LIMIT keeps the most recent
	// evidence. Fold them oldest-to-newest because EWMA is order-sensitive.
	for i := len(buckets) - 1; i >= 0; i-- {
		bucket := buckets[i]
		if bucket.BucketTime.After(settled) {
			continue
		}
		if state.LastBucketAt != nil && !bucket.BucketTime.After(*state.LastBucketAt) {
			continue
		}
		errs := max(bucket.ErrorCount-bucket.UserErrorCount, 0)
		if bucket.RequestCount > 0 {
			rate := float64(errs) / float64(bucket.RequestCount)
			state.SmoothedErrorRate = .3*rate + .7*state.SmoothedErrorRate
			state.KError = reliabilityFactorWithPolicy(state.SmoothedErrorRate, p)
		}
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
	var ttft50, ttft90, ttft95, caches, otps []float64
	for _, row := range rows {
		m := metrics[row.ChannelID]
		if m.RequestCount < minSamples || m.TTFTP50 <= 0 || m.TTFTP90 <= 0 || m.TTFTP95 <= 0 {
			continue
		}
		ttft50, ttft90, ttft95 = append(ttft50, m.TTFTP50), append(ttft90, m.TTFTP90), append(ttft95, m.TTFTP95)
		if m.CachePromptTokens >= cacheEvidenceTokens {
			caches = append(caches, m.CacheHitRate)
		}
		if m.OTPSSampleTokens >= otpsEvidenceTokens && m.OTPS > 0 {
			otps = append(otps, m.OTPS)
		}
	}
	if len(ttft50) < 2 {
		return b, false
	}
	b.ttft50, b.ttft90, b.ttft95 = median(ttft50), median(ttft90), median(ttft95)
	if len(caches) >= 2 {
		b.cache, b.cacheReady = median(caches), true
	}
	if len(otps) >= 2 {
		b.otps, b.otpsReady = median(otps), true
	}
	return b, true
}

func speedFactor(m ChannelMetric, b continuousBaseline, sensitivity, exponent, minFactor, maxFactor float64) float64 {
	if m.TTFTP50 <= 0 || m.TTFTP90 <= 0 || m.TTFTP95 <= 0 {
		return 1
	}
	r := .5*(m.TTFTP50/b.ttft50) + .3*(m.TTFTP90/b.ttft90) + .2*(m.TTFTP95/b.ttft95)
	if r <= 0 {
		return 1
	}
	return clamp(math.Pow(1/r, exponent*sensitivity), minFactor, maxFactor)
}

func performanceFactors(m ChannelMetric, b continuousBaseline, p ContinuousDispatchParams, cacheReady, otpsReady bool) (float64, float64, float64) {
	speed, cache, otps := speedFactor(m, b, p.Sensitivity, p.SpeedExponent, p.SpeedMinFactor, p.SpeedMaxFactor), 1.0, 1.0
	if cacheReady && b.cache > 0 {
		cache = clamp(math.Pow(m.CacheHitRate/b.cache, p.CacheExponent*p.Sensitivity), p.CacheMinFactor, p.CacheMaxFactor)
	}
	if otpsReady && b.otps > 0 {
		otps = clamp(math.Pow(m.OTPS/b.otps, p.OTPSExponent*p.Sensitivity), p.OTPSMinFactor, p.OTPSMaxFactor)
	}
	return speed, cache, otps
}

func combinedFactor(state ContinuousState, p ContinuousDispatchParams) float64 {
	return clamp(state.KSpeed*state.KCache*state.KOTPS*state.KError, p.CombinedMinFactor, p.CombinedMaxFactor)
}

// legacyErrorRate inverts reliabilityFactor's piecewise mapping so a v1
// KError value can seed an equivalent smoothed error rate on upgrade.
func legacyErrorRate(factor float64) float64 {
	return legacyErrorRateWithPolicy(factor, DefaultPolicy().Continuous)
}

func legacyErrorRateWithPolicy(factor float64, p ContinuousDispatchParams) float64 {
	switch {
	case factor >= 1:
		return 0
	case factor >= p.ErrorDegradedFactor:
		return p.ErrorHealthyRate + (1-factor)/(1-p.ErrorDegradedFactor)*(p.ErrorDegradedRate-p.ErrorHealthyRate)
	case factor >= p.ErrorPoorFactor:
		return p.ErrorDegradedRate + (p.ErrorDegradedFactor-factor)/(p.ErrorDegradedFactor-p.ErrorPoorFactor)*(p.ErrorPoorRate-p.ErrorDegradedRate)
	case factor >= p.ErrorMinFactor:
		return p.ErrorPoorRate + (p.ErrorPoorFactor-factor)/(p.ErrorPoorFactor-p.ErrorMinFactor)*(p.ErrorFloorRate-p.ErrorPoorRate)
	default:
		return p.ErrorFloorRate
	}
}

func reliabilityFactor(rate float64) float64 {
	return reliabilityFactorWithPolicy(rate, DefaultPolicy().Continuous)
}

func reliabilityFactorWithPolicy(rate float64, p ContinuousDispatchParams) float64 {
	switch {
	case rate <= p.ErrorHealthyRate:
		return 1
	case rate <= p.ErrorDegradedRate:
		return 1 - (rate-p.ErrorHealthyRate)/(p.ErrorDegradedRate-p.ErrorHealthyRate)*(1-p.ErrorDegradedFactor)
	case rate <= p.ErrorPoorRate:
		return p.ErrorDegradedFactor - (rate-p.ErrorDegradedRate)/(p.ErrorPoorRate-p.ErrorDegradedRate)*(p.ErrorDegradedFactor-p.ErrorPoorFactor)
	case rate <= p.ErrorFloorRate:
		return p.ErrorPoorFactor - (rate-p.ErrorPoorRate)/(p.ErrorFloorRate-p.ErrorPoorRate)*(p.ErrorPoorFactor-p.ErrorMinFactor)
	default:
		return p.ErrorMinFactor
	}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func (e *Engine) noteWriteSuccess(state *ContinuousState) {
	state.WriteFailureStreak = 0
	state.LastWriteError = ""
	state.LastWriteFailureAt = nil
	if state.PausedReason == "write_failed" {
		state.PausedReason = ""
	}
}

// noteWriteFailure records a failed control write; reaching the streak
// threshold pauses the channel and surfaces one auto_paused event carrying
// the transport error.
func (e *Engine) noteWriteFailure(id string, base ChannelBaseValue, state *ContinuousState, mode string, writeErr error, now time.Time) {
	state.WriteFailureStreak++
	at := now
	state.LastWriteFailureAt = &at
	state.LastWriteError = writeErr.Error()
	if state.WriteFailureStreak >= writeFailurePauseThreshold && state.PausedReason == "" {
		state.PausedReason = "write_failed"
		rec := continuousEvent(id, base, *state, "auto_paused", mode, now)
		rec.Evidence["reason"] = "write_failed"
		rec.Evidence["error"] = state.LastWriteError
		_ = e.store.InsertRecommendation(rec)
	}
}

// writeAttemptAllowed keeps a write_failed pause retryable: one attempt per
// slow interval instead of every tick, so recovery is automatic once the
// control path works again.
func writeAttemptAllowed(state ContinuousState, now time.Time) bool {
	if state.PausedReason == "" {
		return true
	}
	return state.PausedReason == "write_failed" && state.LastWriteFailureAt != nil && !now.Before(state.LastWriteFailureAt.Add(writeFailureRetryInterval))
}

func writeReference(state ContinuousState, base ChannelBaseValue) int64 {
	if state.LastWrittenWeight != nil {
		return *state.LastWrittenWeight
	}
	return base.CurrentWeight
}

func writeDeadband(baseWeight int64, percent float64) int64 {
	return max(int64(1), int64(math.Round(float64(baseWeight)*percent/100)))
}

func weightChangeOutsideDeadband(proposed, reference, baseWeight int64, percent float64) bool {
	return absInt64(proposed-reference) >= writeDeadband(baseWeight, percent)
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func continuousEvent(id string, base ChannelBaseValue, state ContinuousState, rule, mode string, now time.Time) Recommendation {
	return Recommendation{ID: NewID(now, id, base.ChannelID, rule), InstanceID: id, ChannelID: base.ChannelID, ChannelName: base.ChannelName, CreatedAt: now, Rule: rule,
		Evidence: map[string]any{
			"model": base.ModelName, "phase": state.Phase, "multiplier": state.Multiplier,
			"k_speed": state.KSpeed, "k_cache": state.KCache, "k_otps": state.KOTPS, "k_error": state.KError,
			"metric_ttft_p50": state.MetricTTFTP50, "metric_ttft_p90": state.MetricTTFTP90, "metric_ttft_p95": state.MetricTTFTP95,
			"baseline_ttft_p50": state.BaselineTTFTP50, "baseline_ttft_p90": state.BaselineTTFTP90, "baseline_ttft_p95": state.BaselineTTFTP95,
			"metric_cache": state.MetricCache, "baseline_cache": state.BaselineCache, "cache_ready": state.CacheReady,
			"metric_otps": state.MetricOTPS, "baseline_otps": state.BaselineOTPS, "otps_ready": state.OTPSReady,
			"smoothed_error_rate": state.SmoothedErrorRate, "probe_attempts": state.ProbeAttempts, "probe_successes": state.ProbeSuccesses,
		},
		CurrentWeight: base.CurrentWeight, ProposedWeight: state.ProposedWeight, CurrentPriority: &base.CurrentPriority, ProposedPriority: &base.BasePriority, ModeAtCreation: mode, Status: "recorded"}
}
