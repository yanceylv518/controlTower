package tuning

import (
	"testing"
	"time"
)

func TestEmptySamplesDoNotBlockCircuitRecovery(t *testing.T) {
	now := time.Now().UTC()
	due := now.Add(-11 * time.Minute)
	for _, tc := range []struct {
		name                   string
		state                  ContinuousState
		wantPhase              string
		wantProbes, wantWrites int
	}{
		{"start probe", ContinuousState{Phase: "circuit", NextProbeAt: &due}, "probing", 1, 0},
		{"successful probe", ContinuousState{Phase: "probing", ProbeAttempts: 10, ProbeSuccesses: 10, ProbeDurationSum: 10}, "soft_start", 0, 1},
		{"expired probe", ContinuousState{Phase: "probing", NextProbeAt: &due}, "circuit", 0, 0},
		{"soft start hold", ContinuousState{Phase: "soft_start", SoftStartPending: true, ProposedWeight: 20}, "soft_start", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.state
			s.InstanceID, s.ChannelID, s.ModelName = "i", 1, "m"
			s.LastObservedRequests, s.KError = 100, 1
			peer := ContinuousState{InstanceID: "i", ChannelID: 2, ModelName: "m", Phase: "normal", LastObservedRequests: 42, KError: 1, ProposedWeight: 80, UpdatedAt: now.Add(-time.Minute)}
			f := &continuousFake{
				bases: []ChannelBaseValue{
					{ChannelID: 1, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 7},
					{ChannelID: 2, ModelName: "m", Models: []string{"m"}, BaseWeight: 100, CurrentWeight: 80},
				},
				states: map[int64]ContinuousState{1: s, 2: peer},
			}
			NewEngine(f).evaluateContinuous("i", autoPolicy(), now, f)
			if got := f.states[1]; got.Phase != tc.wantPhase || !got.UpdatedAt.Equal(now) {
				t.Fatalf("recovery must advance without production samples: %#v", got)
			}
			if len(f.probes) != tc.wantProbes || len(f.writes) != tc.wantWrites {
				t.Fatalf("probes=%d writes=%d, want %d/%d", len(f.probes), len(f.writes), tc.wantProbes, tc.wantWrites)
			}
			if f.states[2] != peer {
				t.Fatal("normal peer must retain its last evaluation without a weight write")
			}
			if tc.wantWrites > 0 && (f.writes[0].ProposedWeight != 20 || f.writes[0].ProposedPriority != nil) {
				t.Fatalf("successful probe must restore traffic with soft start: %#v", f.writes[0])
			}
			if tc.name == "soft start hold" {
				NewEngine(f).evaluateContinuous("i", autoPolicy(), now.Add(time.Minute), f)
				if f.states[1].Phase != "normal" {
					t.Fatal("soft start must finish even while normal peers lack new samples")
				}
			}
		})
	}
}

func TestEmptySamplesIgnoreInactiveHistoricalState(t *testing.T) {
	f := &continuousFake{
		bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 100, CurrentWeight: 100}},
		states: map[int64]ContinuousState{2: {ChannelID: 2, ModelName: "disabled", LastObservedRequests: 100}},
	}
	_, evaluated := NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
	if evaluated != 1 || f.states[1].Phase != "normal" {
		t.Fatal("historical inactive channels must not block active channel initialization")
	}
}

func TestEmptySamplesStillHonorExplicitZeroBase(t *testing.T) {
	f := &continuousFake{
		bases:  []ChannelBaseValue{{ChannelID: 1, ModelName: "m", BaseWeight: 0}},
		states: map[int64]ContinuousState{1: {ChannelID: 1, ModelName: "m", Phase: "normal", ProposedWeight: 80, LastObservedRequests: 100}},
	}
	NewEngine(f).evaluateContinuous("i", autoPolicy(), time.Now().UTC(), f)
	if f.states[1].ProposedWeight != 0 || len(f.writes) != 0 {
		t.Fatal("explicit zero baseline must remain excluded from automatic tuning")
	}
}
