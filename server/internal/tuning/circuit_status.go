package tuning

import (
	"fmt"
	"math"
	"time"
)

// advanceCircuitStatus keeps the recovery round intact until the status write
// is acknowledged. Agent commands are asynchronous; enqueueing is not success.
func (e *Engine) advanceCircuitStatus(cs ContinuousStore, site string, base ChannelBaseValue, state *ContinuousState, p ContinuousDispatchParams, now time.Time) int {
	target := state.CircuitStatusTarget
	// 未投递的旧禁用意图改用自动禁用；已有命令仍先等待回执，避免重复执行。
	if target == 2 && state.CircuitStatusCommandID == "" {
		state.CircuitStatusTarget = 3
		if err := cs.PutContinuousState(*state); err != nil {
			e.noteWriteFailure(site, base, state, "auto", fmt.Errorf("persist automatic disable target: %w", err), now)
			return 0
		}
		target = state.CircuitStatusTarget
	}
	rule := "circuit_disabled"
	if target == 1 {
		rule = "circuit_recovered"
	}
	if state.CircuitStatusCommandID == "" {
		if target == 1 && capacityRecoveryBlocked(base, *state) {
			return 0
		}
		if !writeAttemptAllowed(*state, now) {
			return 0
		}
		rec := continuousEvent(site, base, *state, rule, "auto", now)
		rec.ProposedChannelStatus = &target
		rec.Evidence["channel_status"] = target
		id, err := cs.CreateContinuousWeightChange(rec, "system:auto", now)
		if err != nil {
			e.noteWriteFailure(site, base, state, "auto", err, now)
			if target == 1 {
				resetFailedCircuitEnable(state, p, now)
			}
			return 0
		}
		state.CircuitStatusCommandID = id
		// An empty id is only returned by direct control after a confirmed write
		// whose audit persistence failed. Do not repeat that successful API call.
	}
	if state.CircuitStatusCommandID != "" {
		status, err := cs.ContinuousCommandStatus(state.CircuitStatusCommandID)
		if err != nil {
			return 0
		}
		switch status {
		case "pending", "delivered":
			return 0
		case "succeeded":
		default:
			state.CircuitStatusCommandID = ""
			e.noteWriteFailure(site, base, state, "auto", fmt.Errorf("channel status command %s", status), now)
			if target == 1 {
				resetFailedCircuitEnable(state, p, now)
			}
			return 0
		}
	}
	state.CircuitStatusCommandID, state.CircuitStatusTarget = "", 0
	state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum, state.ProbeSlowStreak = 0, 0, 0, 0
	weight := state.ProposedWeight
	state.LastWrittenWeight, state.LastWriteAt = &weight, &now
	e.noteWriteSuccess(state)
	// 兼容升级前已投递的状态 2 命令，成功回执仍按禁用处理。
	if target == 2 || target == 3 {
		state.Phase, state.Multiplier = "circuit", 0
		next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
		state.NextProbeAt = &next
	} else {
		state.CircuitDisabled = false
		state.Phase, state.SoftStartPending = "soft_start", true
		state.Multiplier = p.SoftStartMultiplier
		state.SmoothedErrorRate = math.Min(state.SmoothedErrorRate, p.RecoveryErrorRate)
		state.KError = reliabilityFactorWithPolicy(state.SmoothedErrorRate, p)
	}
	return 1
}

func resetFailedCircuitEnable(state *ContinuousState, p ContinuousDispatchParams, now time.Time) {
	// A later retry must prove recovery again, as the original weight-only
	// recovery path does. Do not enable from arbitrarily old probe evidence.
	state.CircuitStatusTarget, state.CircuitStatusCommandID = 0, ""
	state.Phase, state.SoftStartPending = "circuit", false
	state.Multiplier, state.ProposedWeight = 0, 0
	state.ProbeAttempts, state.ProbeSuccesses, state.ProbeDurationSum, state.ProbeSlowStreak = 0, 0, 0, 0
	next := now.Add(time.Duration(p.SilentMinutes) * time.Minute)
	state.NextProbeAt = &next
}
