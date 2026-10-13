package tuning

import (
	"testing"
	"time"

	"controltower/server/internal/channelschedule"
)

type scheduledContinuousFake struct {
	*continuousFake
	controls map[int64]channelschedule.Control
}

func (f *scheduledContinuousFake) ListChannelScheduleControls(string, time.Time) (map[int64]channelschedule.Control, error) {
	return f.controls, nil
}

func TestScheduleOutsideWindowStillConfirmsAndRetriesDisabling(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry disable", true: "settle receipt"}[pending], func(t *testing.T) {
			base := failedRound()
			state := base.states[1]
			state.Phase = "circuit"
			state.CircuitDisabled = true
			state.CircuitStatusTarget = 2
			if pending {
				state.CircuitStatusCommandID = "existing-disable"
			}
			base.states[1] = state
			base.commandStatus = "succeeded"
			f := &scheduledContinuousFake{base, map[int64]channelschedule.Control{1: {Enabled: true, Allowed: false}}}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
			wantWrites := 1
			if pending {
				wantWrites = 0
			}
			if len(base.writes) != wantWrites || base.states[1].CircuitStatusTarget != 0 || !base.states[1].CircuitDisabled {
				t.Fatalf("state=%+v writes=%+v", base.states[1], base.writes)
			}
		})
	}
}

func TestScheduleOutsideWindowCancelsUnsentEnableButSettlesExistingReceipt(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh recovery required", true: "settle sent enable"}[pending], func(t *testing.T) {
			base := failedRound()
			state := base.states[1]
			state.CircuitDisabled = true
			state.CircuitStatusTarget = 1
			state.ProposedWeight = 20
			if pending {
				state.CircuitStatusCommandID = "existing-enable"
			}
			base.states[1] = state
			base.commandStatus = "succeeded"
			f := &scheduledContinuousFake{base, map[int64]channelschedule.Control{1: {Enabled: true, Allowed: false}}}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
			state = base.states[1]
			if len(base.writes) != 0 || state.CircuitStatusTarget != 0 || state.CircuitDisabled == pending {
				t.Fatalf("state=%+v writes=%+v", state, base.writes)
			}
			if !pending && (state.Phase != "circuit" || state.ProbeAttempts != 0 || state.NextProbeAt == nil) {
				t.Fatalf("old probe evidence retained: %+v", state)
			}
		})
	}
}

func TestScheduleRecoveryRespectsCurrentWindow(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "outside", true: "inside"}[allowed], func(t *testing.T) {
			base := failedRound()
			state := base.states[1]
			state.CircuitDisabled = true
			state.ProbeSuccesses = 10
			state.ProbeDurationSum = 10
			base.states[1] = state
			f := &scheduledContinuousFake{base, map[int64]channelschedule.Control{1: {Enabled: true, Allowed: allowed}}}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
			if (len(base.writes) > 0) != allowed || base.states[1].CircuitDisabled == allowed {
				t.Fatalf("allowed=%v state=%+v writes=%+v", allowed, base.states[1], base.writes)
			}
		})
	}
}
