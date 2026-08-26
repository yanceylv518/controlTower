package tuning

import (
	"log"
	"time"
)

const fastCircuitReportMaxAge = 2 * time.Minute

// FastCircuitMetric is one channel's incremental metric from a single Agent
// report. It deliberately bypasses settled minute buckets and EWMA.
type FastCircuitMetric struct {
	ChannelID      int64
	RequestCount   int64
	ErrorCount     int64
	UserErrorCount int64
}

type FastCircuitBatch struct {
	InstanceID string
	AgentID    string
	BatchID    string
	ReportedAt time.Time
	Metrics    []FastCircuitMetric
}

type FastCircuitSink interface {
	SubmitFastCircuitBatch(FastCircuitBatch) bool
}

type fastCircuitStore interface {
	ContinuousStore
	SiteIDForInstance(string) (string, error)
}

func (e *Engine) evaluateFastCircuit(batch FastCircuitBatch, now time.Time) {
	if batch.InstanceID == "" || batch.ReportedAt.IsZero() || now.Sub(batch.ReportedAt) > fastCircuitReportMaxAge || batch.ReportedAt.After(now.Add(time.Minute)) {
		return
	}
	store, ok := e.store.(fastCircuitStore)
	if !ok {
		return
	}
	siteID, err := store.SiteIDForInstance(batch.InstanceID)
	if err != nil {
		log.Printf("tuning fast circuit resolve site instance=%s: %v", batch.InstanceID, err)
		return
	}
	policy, found, err := e.store.GetPolicy(siteID)
	if err != nil || !found || !policy.Policy.Continuous.FastCircuitEnabled {
		return
	}
	bases, err := store.ListChannelBaseValues(siteID, "")
	if err != nil {
		return
	}
	states, err := store.ListContinuousStates(siteID)
	if err != nil {
		return
	}
	baseByChannel := make(map[int64]ChannelBaseValue, len(bases))
	for _, base := range bases {
		baseByChannel[base.ChannelID] = base
	}
	stateByChannel := make(map[int64]ContinuousState, len(states))
	for _, state := range states {
		stateByChannel[state.ChannelID] = state
	}
	p := policy.Policy.Continuous
	for _, metric := range batch.Metrics {
		channelErrors := max(metric.ErrorCount-metric.UserErrorCount, 0)
		if metric.RequestCount < p.FastCircuitMinSamples || float64(channelErrors)/float64(metric.RequestCount) < p.FastCircuitErrorRate {
			continue
		}
		base, exists := baseByChannel[metric.ChannelID]
		if !exists || base.BaseWeight <= 0 || len(base.Models) > 1 || policy.Policy.DispatchModes[base.ModelName] != "auto" {
			continue
		}
		state := stateByChannel[metric.ChannelID]
		if state.Phase == "" {
			state.Phase = "normal"
		}
		if state.Phase != "normal" || !writeAttemptAllowed(state, now) {
			continue
		}
		state.InstanceID, state.ChannelID, state.ModelName = siteID, base.ChannelID, base.ModelName
		state.Phase, state.Multiplier, state.ProposedWeight = "circuit", 0, 0
		opened := now
		next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
		original := base.BasePriority
		state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = &opened, &next, &original
		state.ProbeCommandID = nil
		state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
		state.SoftStartPending = false
		rec := continuousEvent(siteID, base, state, "circuit_opened", "auto", now)
		zero := int64(0)
		rec.ProposedPriority = &zero
		rec.Evidence["trigger"] = "agent_report_batch"
		rec.Evidence["request_count"] = metric.RequestCount
		rec.Evidence["channel_error_count"] = channelErrors
		rec.Evidence["error_rate"] = float64(channelErrors) / float64(metric.RequestCount)
		rec.Evidence["threshold"] = p.FastCircuitErrorRate
		rec.Evidence["min_samples"] = p.FastCircuitMinSamples
		rec.Evidence["agent_id"] = batch.AgentID
		rec.Evidence["metric_batch_id"] = batch.BatchID
		if _, err = store.CreateContinuousWeightChange(rec, "system:auto", now); err != nil {
			state.Phase = "normal"
			state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = nil, nil, nil
			e.noteWriteFailure(siteID, base, &state, "auto", err, now)
		} else {
			written := int64(0)
			state.LastWrittenWeight, state.LastWriteAt = &written, &opened
			e.noteWriteSuccess(&state)
		}
		state.UpdatedAt = now
		_ = store.PutContinuousState(state)
		stateByChannel[metric.ChannelID] = state
	}
}
