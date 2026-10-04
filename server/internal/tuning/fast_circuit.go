package tuning

import (
	"log"
	"time"

	"controltower/server/internal/channelupdates"
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
	baseByChannel := make(map[int64]ChannelBaseValue, len(bases))
	for _, base := range bases {
		baseByChannel[base.ChannelID] = base
	}
	p := policy.Policy.Continuous
	var jobs []func()
	for _, metric := range batch.Metrics {
		channelErrors := max(metric.ErrorCount-metric.UserErrorCount, 0)
		if metric.RequestCount < p.FastCircuitMinSamples || float64(channelErrors)/float64(metric.RequestCount) < p.FastCircuitErrorRate {
			continue
		}
		base, exists := baseByChannel[metric.ChannelID]
		if !exists || base.BaseWeight <= 0 || len(base.Models) > 1 || policy.Policy.DispatchModes[base.ModelName] != "auto" {
			continue
		}
		jobs = append(jobs, func() {
			guard := e.guard(siteID, metric.ChannelID)
			guard.Lock()
			defer guard.Unlock()
			if e.context().Err() != nil {
				return
			}
			// Same-channel IO cannot be preempted. Recheck freshness after waiting,
			// then load the latest state under the guard instead of a batch snapshot.
			now := now
			if e.parallelism > 1 {
				now = time.Now().UTC()
			}
			if now.Sub(batch.ReportedAt) > fastCircuitReportMaxAge {
				return
			}
			states, err := store.ListContinuousStates(siteID)
			if err != nil {
				return
			}
			var state ContinuousState
			for _, latest := range states {
				if latest.ChannelID == metric.ChannelID {
					state = latest
					break
				}
			}
			if state.Phase == "" {
				state.Phase = "normal"
			}
			if state.Phase != "normal" || !writeAttemptAllowed(state, now) {
				return
			}
			defer func() { guard.version = e.version.Add(1) }()
			state.InstanceID, state.ChannelID, state.ModelName = siteID, base.ChannelID, base.ModelName
			state.Evaluation = &EvaluationContext{BaseWeight: base.BaseWeight, BaseUpdatedAt: base.UpdatedAt, PolicyUpdatedAt: policy.UpdatedAt, EvaluatedAt: now, Params: p}
			state.Phase, state.Multiplier, state.ProposedWeight = "circuit", 0, 0
			opened := now
			next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
			original := base.BasePriority
			state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = &opened, &next, &original
			state.ProbeCommandID = nil
			state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum = 0, 0, 0
			state.SoftStartPending = false
			rec := continuousEvent(siteID, base, state, "circuit_opened", "auto", now)
			rec.ProposedPriority = nil
			rec.Evidence["trigger"] = "agent_report_batch"
			rec.Evidence["request_count"] = metric.RequestCount
			rec.Evidence["channel_error_count"] = channelErrors
			rec.Evidence["error_rate"] = float64(channelErrors) / float64(metric.RequestCount)
			rec.Evidence["threshold"] = p.FastCircuitErrorRate
			rec.Evidence["min_samples"] = p.FastCircuitMinSamples
			rec.Evidence["agent_id"] = batch.AgentID
			rec.Evidence["metric_batch_id"] = batch.BatchID
			if _, err = e.createTrackedWeightChange(store, rec, base, &state, now); err != nil {
				state.Phase = "normal"
				state.CircuitOpenedAt, state.NextProbeAt, state.OriginalPriority = nil, nil, nil
				e.noteWriteFailure(siteID, base, &state, "auto", err, now)

			}
			state.UpdatedAt = now
			if store.PutContinuousState(state) == nil {
				channelupdates.Notify(siteID)
			}
		})
	}
	e.runChannels(jobs)
}
