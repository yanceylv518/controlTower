package tuning

import (
	"testing"
	"time"
)

func dutyChannel(id, priority int64, models ...string) Channel {
	return Channel{ID: id, Name: fmtInt(id), Status: "enabled", Weight: 1, Priority: priority, Models: models}
}

func TestDutyPolicyValidation(t *testing.T) {
	p := DefaultPolicy()
	if errors := p.Validate(); len(errors) != 0 {
		t.Fatalf("default invalid: %#v", errors)
	}
	p.Criteria[0].ErrorRateThreshold = p.Criteria[0].SevereThreshold
	p.Scheduling.MinSamples = 1001
	if errors := p.Validate(); errors["criteria[default].error_rate_threshold"] == "" || errors["scheduling.min_samples"] == "" {
		t.Fatalf("missing validation: %#v", errors)
	}
}

func TestDutyDemoteSustainedSevereAndTiedActive(t *testing.T) {
	p := testPolicy()
	channels := []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 100, "m"), dutyChannel(3, 50, "m")}
	f := &fakeStore{policy: p, channels: channels, metrics: []ChannelMetric{{1, 100, 20, 0, 1}, {2, 100, 20, 0, 1}}}
	e := NewEngine(f)
	now := time.Now().UTC()
	e.Tick(now)
	if len(f.recommendations) != 0 {
		t.Fatal("one ordinary bad window must not trigger")
	}
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 2 {
		t.Fatalf("both tied active channels must demote: %#v", f.recommendations)
	}
	for _, rec := range f.recommendations {
		if rec.Rule != "demote" || rec.ProposedPriority == nil || *rec.ProposedPriority != 49 {
			t.Fatalf("invalid demote: %#v", rec)
		}
		if rec.Evidence["criteria_name"] != "default" {
			t.Fatalf("demote evidence must record criteria: %#v", rec.Evidence)
		}
	}

	f2 := &fakeStore{policy: p, channels: channels, metrics: []ChannelMetric{{1, 100, 50, 0, 1}}}
	NewEngine(f2).Tick(now)
	if len(f2.recommendations) != 1 || f2.recommendations[0].Evidence["trigger"] != "severe" {
		t.Fatalf("severe must be immediate: %#v", f2.recommendations)
	}
}

func TestDutyLatencyMixedAndBackupBranches(t *testing.T) {
	p := testPolicy()
	p.Policy.Criteria[0].SevereThreshold = .4
	now := time.Now().UTC()
	baseline := []float64{5, 5, 5, 5, 5, 5, 5, 5}
	f := &fakeStore{
		policy: p, buckets: map[int64][]float64{1: baseline},
		channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m"), dutyChannel(9, 1, "a", "b")},
		metrics:  []ChannelMetric{{1, 100, 0, 0, 11}},
	}
	e := NewEngine(f)
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 2 || f.recommendations[0].Rule != "mixed_channel" || f.recommendations[1].Evidence["trigger"] != "latency_degrade" {
		t.Fatalf("latency/mixed=%#v", f.recommendations)
	}

	single := &fakeStore{policy: p, channels: []Channel{dutyChannel(1, 100, "m")}, metrics: []ChannelMetric{{1, 100, 50, 0, 1}}}
	NewEngine(single).Tick(now)
	if len(single.recommendations) != 1 || single.recommendations[0].Rule != "no_backup" {
		t.Fatalf("single ladder=%#v", single.recommendations)
	}
	next := now.Add(time.Hour)
	exhausted := &fakeStore{
		policy: p, channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:  []ChannelMetric{{1, 100, 50, 0, 1}},
		dispatch: map[int64]DispatchState{2: {InstanceID: "i", ChannelID: 2, ModelName: "m", NextTrialAt: &next}},
	}
	NewEngine(exhausted).Tick(now)
	if len(exhausted.recommendations) != 1 || exhausted.recommendations[0].Rule != "ladder_exhausted" {
		t.Fatalf("exhausted=%#v", exhausted.recommendations)
	}
}

func TestDutyTrialBackoffAndRestartRestore(t *testing.T) {
	p := testPolicy()
	p.Policy.Scheduling.TrialMaxMinutes = 180
	if trialDelay(p.Policy, 0) != time.Hour || trialDelay(p.Policy, 3) != 3*time.Hour {
		t.Fatal("unexpected trial backoff")
	}
	now := time.Now().UTC()
	due := now.Add(-time.Minute)
	f := &fakeStore{
		policy: p, channels: []Channel{dutyChannel(1, 50, "m")},
		dispatch: map[int64]DispatchState{1: {InstanceID: "i", ChannelID: 1, ModelName: "m", OriginalPriority: 100, NextTrialAt: &due}},
	}
	NewEngine(f).Tick(now)
	if len(f.recommendations) != 1 || f.recommendations[0].Rule != "trial" || f.dispatch[1].NextTrialAt != nil {
		t.Fatalf("restored trial=%#v %#v", f.recommendations, f.dispatch)
	}
	if f.recommendations[0].Evidence["criteria_name"] != "default" {
		t.Fatalf("trial evidence must record criteria: %#v", f.recommendations[0].Evidence)
	}
}

func TestDutyBaselineInsufficientCooldownAndDailyLimit(t *testing.T) {
	p := testPolicy()
	now := time.Now().UTC()
	channels := []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")}
	insufficient := &fakeStore{
		policy: p, buckets: map[int64][]float64{1: {5, 5, 5, 5, 5, 5, 5}},
		channels: channels, metrics: []ChannelMetric{{1, 100, 0, 0, 50}},
	}
	e := NewEngine(insufficient)
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(insufficient.recommendations) != 0 {
		t.Fatal("fewer than eight baseline buckets must skip latency decision")
	}

	cooldown := &fakeStore{
		policy: p, channels: channels, metrics: []ChannelMetric{{1, 100, 50, 0, 1}},
		lastAction: now.Add(-30 * time.Second),
	}
	NewEngine(cooldown).Tick(now)
	if len(cooldown.recommendations) != 0 {
		t.Fatal("cooldown must suppress action recommendation")
	}
	limited := &fakeStore{
		policy: p, channels: channels, metrics: []ChannelMetric{{1, 100, 50, 0, 1}},
		actionCount: p.Policy.Scheduling.DailyActionLimit,
	}
	NewEngine(limited).Tick(now)
	if len(limited.recommendations) != 0 {
		t.Fatal("daily action limit must suppress action recommendation")
	}
}
