package tuning

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type capacityFake struct {
	continuousFake
	asOf    time.Time
	rates   []ChannelMetric
	rateErr error
}

func (f *capacityFake) QueryCurrentChannelRateSnapshot(string, time.Time) ([]ChannelMetric, time.Time, error) {
	return f.rates, f.asOf, f.rateErr
}

func capacityFixture() (*capacityFake, *Engine, time.Time) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	f := &capacityFake{continuousFake: continuousFake{bases: []ChannelBaseValue{
		{ChannelID: 1, ModelName: "m", Models: []string{"m"}, GroupName: "default", BaseWeight: 100, CurrentWeight: 100, MaxTPM: 1000},
		{ChannelID: 2, ModelName: "m", Models: []string{"m"}, GroupName: "default", BaseWeight: 100, CurrentWeight: 100, MaxTPM: 1000},
	}, states: map[int64]ContinuousState{}}, rates: []ChannelMetric{{ChannelID: 1, TPM: 1500}, {ChannelID: 2, TPM: 200}}, asOf: now}
	return f, NewEngine(f), now
}

func TestCapacityHealthyPerformanceWritesDoNotLimitIncrease(t *testing.T) {
	f, e, start := capacityFixture()
	f.rates[0].TPM = 200
	f.bases[0].CurrentWeight = 50
	addNeutralPerformanceEvidence(&f.continuousFake)
	// Every 30s evaluation may raise by 10%; ordinary writes must not impose
	// an extra 60s capacity cooldown or leave a perpetual feedback indicator.
	for i, want := range []int64{55, 60, 66, 72, 79, 86, 94, 100} {
		f.asOf = start.Add(time.Duration(i) * 30 * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
		s := f.states[1]
		if s.ProposedWeight != want || s.Capacity.ConfirmedWeight != want || s.CapacityLimited || s.Capacity.Active || s.Capacity.Phase != "normal" {
			t.Fatalf("tick %d: want healthy weight %d, got %+v", i, want, s)
		}
	}
}

func TestCapacityHealthyRepeatedDecreasesAndLegacyFeedback(t *testing.T) {
	_, _, start := capacityFixture()
	b := ChannelBaseValue{BaseWeight: 100, CurrentWeight: 100, MaxTPM: 1000}
	s := ContinuousState{MetricTPM: 200}
	updateCapacity(b, &s, start, start, true, 25)
	for i := 0; i < 6; i++ {
		now := start.Add(time.Duration(i) * 30 * time.Second)
		confirmCapacityWrite(&s, 90-int64(i), now)
		// Persisted false-positive states from older versions clear on the
		// next fresh evaluation without requiring the user to remove a cap.
		s.Capacity.Phase, s.CapacityLimited = "waiting_feedback", true
		updateCapacity(b, &s, now, now, true, 25)
		if s.CapacityLimited || s.Capacity.Active || s.Capacity.Phase != "normal" {
			t.Fatalf("ordinary decrease must not latch capacity: %+v", s.Capacity)
		}
	}
}

func TestCapacityHealthyQueuedWriteStillWaitsForAcknowledgement(t *testing.T) {
	f, e, start := capacityFixture()
	f.rates[0].TPM, f.bases[0].CurrentWeight = 200, 50
	f.commandStatus = "pending"
	addNeutralPerformanceEvidence(&f.continuousFake)
	for i := 0; i < 3; i++ {
		f.asOf = start.Add(time.Duration(i) * 30 * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
		s := f.states[1]
		if s.Capacity.Phase != "awaiting_write" || !s.CapacityLimited || s.Capacity.ConfirmedWeight != 50 || s.ProposedWeight != 55 || len(f.writes) != 1 {
			t.Fatalf("pending writes must not stack: %+v", s)
		}
	}
	f.commandStatus = "succeeded"
	f.asOf = start.Add(90 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	s := f.states[1]
	if s.Capacity.Phase != "normal" || s.CapacityLimited || s.ProposedWeight != 60 {
		t.Fatalf("healthy acknowledgement must allow next ordinary increase: %+v", s)
	}
}

func TestCapacityConfirmedWritePreservesActualProtection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		active, fresh bool
		phase         string
		limited       bool
	}{
		{"healthy", false, true, "normal", false},
		{"overload", true, true, "waiting_feedback", true},
		{"missing_load", false, false, "unavailable", true},
		{"overload_missing_load", true, false, "unavailable", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := ContinuousState{Capacity: CapacityControl{Initialized: true, Active: tc.active, Fresh: tc.fresh, PendingCommandID: "cmd"}, CapacityLimited: true}
			confirmCapacityWrite(&s, 50, time.Now().UTC())
			if s.Capacity.Phase != tc.phase || s.CapacityLimited != tc.limited || s.Capacity.PendingCommandID != "" {
				t.Fatalf("confirmed write protection: %+v", s)
			}
		})
	}
}

type capacityDirectFake struct{ *capacityFake }

func (f *capacityDirectFake) CreateContinuousWeightChange(r Recommendation, _ string, _ time.Time) (string, error) {
	f.writes = append(f.writes, r)
	return "", nil
}

func TestCapacityDirectWriteOnlyWaitsForFeedbackDuringProtection(t *testing.T) {
	for _, active := range []bool{false, true} {
		f, e, now := capacityFixture()
		s := ContinuousState{MetricTPM: 200}
		updateCapacity(f.bases[0], &s, now, now, true, 25)
		s.Capacity.Active = active
		_, err := e.createTrackedWeightChange(&capacityDirectFake{f}, Recommendation{InstanceID: "i", ChannelID: 1, ProposedWeight: 75}, f.bases[0], &s, now)
		wantPhase := "normal"
		if active {
			wantPhase = "waiting_feedback"
		}
		if err != nil || s.Capacity.Phase != wantPhase || s.CapacityLimited != active || s.Capacity.ConfirmedWeight != 75 || s.Capacity.PendingCommandID != "" {
			t.Fatalf("direct write active=%v: err=%v state=%+v", active, err, s)
		}
	}
}

func TestCapacityTargetAndIntegerFloor(t *testing.T) {
	for _, test := range []struct {
		w            int64
		u            float64
		raw, bounded int64
	}{
		{100, 1.5, 60, 75}, {100, 1, 90, 90}, {100, 5, 18, 75}, {5, 2, 2, 4}, {3, 2, 1, 2}, {2, 2, 1, 1}, {1, 2, 1, 1}, {0, 2, 0, 0},
	} {
		a, b := capacityTarget(test.w, test.u, 25)
		if a != test.raw || b != test.bounded {
			t.Fatalf("%+v got %d/%d", test, a, b)
		}
	}
	b := ChannelBaseValue{MaxRPM: 100, MaxTPM: 1000}
	if u := capacityUtilization(b, 200, 1500); u != 2 {
		t.Fatalf("take maximum, not product: %v", u)
	}
}

func TestCapacityConfirmedReductionWithoutPerformanceSamples(t *testing.T) {
	f, e, now := capacityFixture()
	for _, sec := range []int{0, 30, 60} {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
		if sec < 60 && len(f.writes) != 0 {
			t.Fatal("transient overload must not reduce")
		}
	}
	s := f.states[1]
	if len(f.writes) != 1 || f.writes[0].Rule != "capacity_reduce" || s.ProposedWeight != 75 || s.Capacity.ConfirmedWeight != 75 {
		t.Fatalf("no-sample capacity reduction: %+v writes=%+v", s, f.writes)
	}
	// The old 60-second measurement cannot trigger a second reduction.
	f.asOf = now.Add(90 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if len(f.writes) != 1 || f.states[1].Capacity.Phase != "waiting_feedback" {
		t.Fatal("must wait for full post-write feedback")
	}
	f.asOf = now.Add(120 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if len(f.writes) != 2 || f.states[1].ProposedWeight != 57 {
		t.Fatalf("second bounded reduction: %+v", f.states[1])
	}
}

func TestCapacityDuplicatesMissingDataAndRestart(t *testing.T) {
	f, e, now := capacityFixture()
	e.evaluateContinuous("i", autoPolicy(), now, f)
	e.evaluateContinuous("i", autoPolicy(), now.Add(60*time.Second), f)
	if len(f.writes) != 0 {
		t.Fatal("repeated coverage is not new evidence")
	}
	f.rateErr = errors.New("missing source")
	e.evaluateContinuous("i", autoPolicy(), now.Add(90*time.Second), f)
	if !f.states[1].CapacityLimited || !f.states[1].Capacity.OverSince.IsZero() {
		t.Fatal("missing coverage must reset confirmation and block increases")
	}
	f.rateErr = nil
	for _, sec := range []int{120, 150, 180} {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if len(f.writes) != 1 {
		t.Fatalf("fresh sustained evidence should recover: %d", len(f.writes))
	}
	encoded, _ := json.Marshal(f.states[1])
	var restored ContinuousState
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	f.states[1] = restored
	e = NewEngine(f)
	f.asOf = now.Add(210 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if len(f.writes) != 1 {
		t.Fatal("restart must preserve feedback wait")
	}
}

func TestCapacityHysteresisAndRecovery(t *testing.T) {
	f, e, now := capacityFixture()
	for sec := 0; sec <= 60; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	f.rates[0].TPM = 900
	for sec := 90; sec <= 240; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if len(f.writes) != 1 || !f.states[1].CapacityLimited || f.states[1].Capacity.Phase != "holding" {
		t.Fatal("90% must hold without further reduction or premature recovery")
	}
	f.rates[0].TPM = 850
	for sec := 270; sec <= 330; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if !f.states[1].Capacity.Active {
		t.Fatal("85% is not strictly below exit threshold")
	}
	f.rates[0].TPM = 800
	for sec := 360; sec <= 420; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if f.states[1].CapacityLimited {
		t.Fatal("60s fresh underutilization should release")
	}
	addNeutralPerformanceEvidence(&f.continuousFake)
	f.asOf = now.Add(450 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if f.states[1].ProposedWeight != 82 {
		t.Fatalf("recovery must use configured 10%% increase cap: %d", f.states[1].ProposedWeight)
	}
}

func TestCapacityQueueAckFailureAndNoStacking(t *testing.T) {
	f, e, now := capacityFixture()
	f.commandStatus = "pending"
	for sec := 0; sec <= 120; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if len(f.writes) != 1 || f.states[1].LastWrittenWeight != nil || f.states[1].Capacity.ConfirmedWeight != 100 || f.states[1].ProposedWeight != 75 {
		t.Fatal("enqueue is not actual execution; must not stack")
	}
	f.commandStatus = "succeeded"
	f.asOf = now.Add(150 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if f.states[1].Capacity.AppliedAt != f.asOf || f.states[1].Capacity.ConfirmedWeight != 75 {
		t.Fatal("wait starts at observed acknowledgement")
	}
	f.asOf = now.Add(180 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if len(f.writes) != 1 {
		t.Fatal("acknowledgement needs new feedback window")
	}
	f.commandStatus = "failed"
	f.asOf = now.Add(210 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if f.states[1].Capacity.ConfirmedWeight != 75 || f.states[1].WriteFailureStreak != 1 {
		t.Fatal("failed receipt must not advance confirmed weight")
	}
}

func TestCapacityDiversionAndStrongerPenalty(t *testing.T) {
	f, _, _ := capacityFixture()
	b := f.bases[0]
	rates := map[int64]ChannelMetric{2: {TPM: 900}}
	if ok, _ := capacityDiversion(b, f.bases, f.states, rates, DefaultPolicy().Continuous); ok {
		t.Fatal("peer near cap has no safe headroom")
	}
	rates[2] = ChannelMetric{TPM: 100}
	b.GroupName = "default,vip"
	if ok, _ := capacityDiversion(b, f.bases, f.states, rates, DefaultPolicy().Continuous); !ok {
		t.Fatal("partial group coverage must not block capacity reduction")
	}
	b.GroupName = "default"
	b.CurrentPriority = 10
	if ok, _ := capacityDiversion(b, f.bases, f.states, rates, DefaultPolicy().Continuous); ok {
		t.Fatal("lower priority peer is not an alternative")
	}
	s := ContinuousState{CapacityLimited: true, ProposedWeight: 40, Capacity: CapacityControl{Phase: "reducing", ConfirmedWeight: 100, BoundWeight: 75}}
	if applyCapacityTarget(&s) || s.ProposedWeight != 40 {
		t.Fatal("capacity maximum fall must not weaken performance penalty")
	}
	s.ProposedWeight = 0
	applyCapacityTarget(&s)
	if s.ProposedWeight != 0 {
		t.Fatal("never raise a circuit zero to capacity minimum")
	}
}

func TestCapacityNoHeadroomObserveAndMinimum(t *testing.T) {
	for _, scenario := range []string{"no_peer", "overloaded_peer", "observe", "minimum", "write_error"} {
		t.Run(scenario, func(t *testing.T) {
			f, e, now := capacityFixture()
			policy := autoPolicy()
			switch scenario {
			case "no_peer":
				f.bases = f.bases[:1]
			case "overloaded_peer":
				f.rates[1].TPM = 1500
			case "observe":
				policy.Policy.DispatchModes["m"] = "observe"
			case "minimum":
				f.bases[0].CurrentWeight = 1
			case "write_error":
				f.writeErr = errors.New("unreachable")
			}
			for sec := 0; sec <= 60; sec += 30 {
				f.asOf = now.Add(time.Duration(sec) * time.Second)
				e.evaluateContinuous("i", policy, f.asOf, f)
			}
			if len(f.writes) != 0 {
				t.Fatal("unexpected executed write")
			}
			s := f.states[1]
			switch scenario {
			case "no_peer", "overloaded_peer":
				if s.Capacity.Phase != "no_headroom" {
					t.Fatalf("%+v", s.Capacity)
				}
			case "observe":
				if s.ProposedWeight != 75 {
					t.Fatal("observe must show same proposal")
				}
			case "minimum":
				if s.ProposedWeight != 1 {
					t.Fatal("minimum keeps channel usable")
				}
			case "write_error":
				if s.Capacity.ConfirmedWeight != 100 || s.WriteFailureStreak != 1 {
					t.Fatal("failed writes must not consume feedback")
				}
			}
		})
	}
}

func TestCapacityAllowsStrongerPerformanceDecreaseDuringFeedback(t *testing.T) {
	f, e, now := capacityFixture()
	for sec := 0; sec <= 60; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	addNeutralPerformanceEvidence(&f.continuousFake)
	s := f.states[1]
	s.SmoothedErrorRate = .2
	s.KError = .3
	f.states[1] = s
	f.asOf = now.Add(90 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if f.states[1].ProposedWeight != 50 || f.states[1].Capacity.ConfirmedWeight != 50 || !f.states[1].Capacity.AppliedAt.Equal(f.asOf) {
		t.Fatalf("performance may fall below capacity's 25%% bound and reset feedback: %+v", f.states[1])
	}
}

func TestCapacityGuardsBothRecoveryPaths(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "weight", true: "status"}[disabled], func(t *testing.T) {
			f, e, now := capacityFixture()
			f.bases[0].CurrentWeight = 0
			f.states[1] = ContinuousState{InstanceID: "i", ChannelID: 1, ModelName: "m", Phase: "probing", KError: 1, ProbeAttempts: 10, ProbeSuccesses: 10, CircuitDisabled: disabled}
			e.evaluateContinuous("i", autoPolicy(), now, f)
			if len(f.writes) != 0 || f.states[1].Phase != "probing" {
				t.Fatal("overloaded recovery must wait")
			}
			f.rateErr = errors.New("missing collector")
			f.asOf = now.Add(30 * time.Second)
			e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
			if len(f.writes) != 0 {
				t.Fatal("unavailable rates must not enable a channel")
			}
			f.rateErr = nil
			f.rates[0].TPM = 0
			for _, sec := range []int{60, 90, 120} {
				f.asOf = now.Add(time.Duration(sec) * time.Second)
				e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
				if sec < 120 && len(f.writes) != 0 {
					t.Fatal("recovery must also confirm 60s below 85%")
				}
			}
			if len(f.writes) != 1 || f.writes[0].ProposedWeight != 20 {
				t.Fatalf("fresh cleared capacity should permit soft start: %+v", f.states[1])
			}
		})
	}
}

func TestCapacityTransientOverloadDoesNotReenableIncreaseAtNinetyFivePercent(t *testing.T) {
	f, e, now := capacityFixture()
	e.evaluateContinuous("i", autoPolicy(), now, f)
	f.rates[0].TPM = 950
	f.asOf = now.Add(30 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	s := f.states[1]
	if len(f.writes) != 0 || !s.CapacityLimited || s.Capacity.Phase != "holding" || !s.Capacity.OverSince.IsZero() {
		t.Fatalf("transient spike cancels reduction confirmation but holds the increase guard: %+v", s.Capacity)
	}
}

func TestCapacityExclusionsAndEvidenceGap(t *testing.T) {
	f, e, now := capacityFixture()
	f.bases[0].Models = []string{"m", "other"}
	for sec := 0; sec <= 60; sec += 30 {
		f.asOf = now.Add(time.Duration(sec) * time.Second)
		e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	}
	if len(f.writes) != 0 {
		t.Fatal("mixed channels must remain excluded")
	}
	f, e, now = capacityFixture()
	e.evaluateContinuous("i", autoPolicy(), now, f)
	f.asOf = now.Add(61 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
	if len(f.writes) != 0 || !f.states[1].Capacity.OverSince.Equal(f.asOf) {
		t.Fatal("coverage gap restarts confirmation")
	}
	f.asOf = now.Add(30 * time.Second)
	e.evaluateContinuous("i", autoPolicy(), now.Add(90*time.Second), f)
	if f.states[1].Capacity.Fresh {
		t.Fatal("out-of-order evidence must not advance state")
	}
}

func TestCapacityReductionIgnoresGroups(t *testing.T) {
	for _, tc := range []struct{ name, current, peer string }{
		{"partial", "default,vip", "default"},
		{"disjoint", "vip", "default"},
		{"empty_current", "", "default"},
		{"empty_peer", "default", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, e, now := capacityFixture()
			f.bases[0].GroupName, f.bases[1].GroupName = tc.current, tc.peer
			for _, sec := range []int{0, 30, 60} {
				f.asOf = now.Add(time.Duration(sec) * time.Second)
				e.evaluateContinuous("i", autoPolicy(), f.asOf, f)
				if sec < 60 && len(f.writes) != 0 {
					t.Fatal("group-independent eligibility must still wait for sustained overload")
				}
			}
			if len(f.writes) != 1 || f.writes[0].ChannelID != 1 || f.writes[0].Rule != "capacity_reduce" || f.writes[0].ProposedWeight != 75 {
				t.Fatalf("expected bounded capacity reduction across groups: %+v", f.writes)
			}
		})
	}
}

func TestCapacityDiversionRetainsPeerGuards(t *testing.T) {
	for _, scenario := range []string{"self_only", "other_model", "other_priority", "zero_base", "zero_weight", "disabled", "circuit", "paused", "capacity_active", "pending_write", "errors", "at_headroom_limit", "unconfigured"} {
		t.Run(scenario, func(t *testing.T) {
			f, _, _ := capacityFixture()
			f.bases[0].GroupName, f.bases[1].GroupName = "vip", "other"
			peer := &f.bases[1]
			state := ContinuousState{}
			rates := map[int64]ChannelMetric{2: {TPM: 200}}
			p := DefaultPolicy().Continuous
			switch scenario {
			case "self_only":
				f.bases = f.bases[:1]
			case "other_model":
				peer.ModelName = "other"
			case "other_priority":
				peer.CurrentPriority = 1
			case "zero_base":
				peer.BaseWeight = 0
			case "zero_weight":
				peer.CurrentWeight = 0
			case "disabled":
				state.CircuitDisabled = true
			case "circuit":
				state.Phase = "circuit"
			case "paused":
				state.PausedReason = "mixed_channel"
			case "capacity_active":
				state.Capacity.Active = true
			case "pending_write":
				state.Capacity.PendingCommandID = "pending"
			case "errors":
				state.SmoothedErrorRate = p.ErrorDegradedRate
			case "at_headroom_limit":
				rates[2] = ChannelMetric{TPM: 850}
			case "unconfigured":
				peer.MaxTPM = 0
			}
			f.states[2] = state
			ok, reason := capacityDiversion(f.bases[0], f.bases, f.states, rates, p)
			if scenario == "unconfigured" {
				if !ok || reason != "peer_capacity_unconfigured" {
					t.Fatalf("unknown capacity must retain its warning: %v %s", ok, reason)
				}
			} else if ok || reason != "no_eligible_headroom" {
				t.Fatalf("ineligible peer must not permit reduction: %v %s", ok, reason)
			}
		})
	}
}

func TestCapacityConfiguredDecreaseReachesExecutionAndSnapshot(t *testing.T) {
	for _, tc := range []struct {
		percent float64
		want    int64
	}{{1, 99}, {10, 90}, {25, 75}, {40, 60}, {100, 60}} {
		f, e, now := capacityFixture()
		p := autoPolicy()
		p.Policy.Continuous.CapacityMaxDecreasePercent = tc.percent
		for _, sec := range []int{0, 30, 60} {
			f.asOf = now.Add(time.Duration(sec) * time.Second)
			e.evaluateContinuous("i", p, f.asOf, f)
		}
		s := f.states[1]
		if len(f.writes) != 1 || f.writes[0].ProposedWeight != tc.want || s.Capacity.ConfirmedWeight != tc.want {
			t.Fatalf("limit %v: want %d, writes=%+v state=%+v", tc.percent, tc.want, f.writes, s)
		}
		if s.Evaluation == nil || s.Evaluation.Params.CapacityMaxDecreasePercent != tc.percent {
			t.Fatalf("missing policy snapshot: %+v", s.Evaluation)
		}
	}
	for _, tc := range []struct {
		weight  int64
		percent float64
		want    int64
	}{{3, 1, 2}, {2, 10, 1}, {1, 100, 1}, {0, 100, 0}, {100, 100, 1}} {
		_, got := capacityTarget(tc.weight, 1000, tc.percent)
		if got != tc.want {
			t.Fatalf("integer minimum %+v: got %d", tc, got)
		}
	}
}
