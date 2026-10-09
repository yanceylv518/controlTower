package tuning

import "time"

// finishFailedWrite retires an exhausted operation, not the channel. Failed
// disable intents never established CT ownership. Confirmed CT-disabled
// channels keep their recovery cycle and must prove recovery with fresh probes.
func (e *Engine) finishFailedWrite(s *ContinuousState, base ChannelBaseValue, p ContinuousDispatchParams, now time.Time) {
	failedWeight, failedStatus := s.ProposedWeight, s.CircuitStatusTarget
	if s.CircuitStatusTarget == 2 || s.CircuitStatusTarget == 3 {
		s.CircuitDisabled = false
	}
	s.CircuitStatusTarget, s.CircuitStatusCommandID = 0, ""
	s.ProbeCommandID = nil
	s.ProbeAttempts, s.ProbeSuccesses, s.ProbeSlowStreak, s.ProbeDurationSum = 0, 0, 0, 0
	s.LastWrittenWeight, s.LastWriteAt = nil, nil
	// Preserve capacity hysteresis; the next regular update reconciles its weight.
	if s.CircuitDisabled {
		resetFailedCircuitEnable(s, p, now)
	} else {
		s.Phase, s.SoftStartPending = "normal", false
		s.CircuitOpenedAt, s.NextProbeAt, s.OriginalPriority = nil, nil, nil
		s.Multiplier, s.ProposedWeight = 1, base.CurrentWeight
	}
	// Preserve the failure in the event before clearing the active task.
	rec := continuousEvent(s.InstanceID, base, *s, "write_abandoned", "auto", now)
	rec.Evidence["error"] = s.LastWriteError
	rec.Evidence["failed_weight"], rec.Evidence["failed_status"] = failedWeight, failedStatus
	rec.Evidence["failure_count"] = s.WriteFailureStreak
	_ = e.store.InsertRecommendation(rec)
	e.noteWriteSuccess(s)
}
