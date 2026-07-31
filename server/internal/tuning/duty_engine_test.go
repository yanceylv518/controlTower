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
	p.Scheduling.SparseMinSamples = 1002
	p.Scheduling.SparseLookbackMinutes = p.Scheduling.WindowMinutes - 1
	if errors := p.Validate(); errors["criteria[default].error_rate_threshold"] == "" ||
		errors["scheduling.min_samples"] == "" ||
		errors["scheduling.sparse_min_samples"] == "" ||
		errors["scheduling.sparse_lookback_minutes"] == "" {
		t.Fatalf("missing validation: %#v", errors)
	}
}

func sparseBuckets(now time.Time, requests, errors int64) []RecentChannelBucket {
	return []RecentChannelBucket{
		{BucketTime: now.Add(-30 * time.Second), RequestCount: 1, ErrorCount: min64(errors, 1)},
		{BucketTime: now.Add(-30 * time.Minute), RequestCount: requests - 1, ErrorCount: max64(errors-1, 0)},
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func TestDutySparseSamplingTriggersAndMarksEvidence(t *testing.T) {
	p := testPolicy()
	now := time.Now().UTC()
	f := &fakeStore{
		policy:        p,
		channels:      []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:       []ChannelMetric{{ChannelID: 1, RequestCount: 1, ErrorCount: 1}},
		recentBuckets: map[int64][]RecentChannelBucket{1: sparseBuckets(now, 10, 6)},
	}
	NewEngine(f).Tick(now)
	if len(f.recommendations) != 1 || f.recommendations[0].Evidence["sparse"] != true {
		t.Fatalf("sparse severe error must trigger with evidence: %#v", f.recommendations)
	}
	if f.recommendations[0].Evidence["sparse_samples"] != int64(10) {
		t.Fatalf("unexpected sparse sample evidence: %#v", f.recommendations[0].Evidence)
	}
}

func TestDutySparseSamplingFreshnessAndCountGuards(t *testing.T) {
	p := testPolicy()
	now := time.Now().UTC()
	base := fakeStore{
		policy:   p,
		channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:  []ChannelMetric{{ChannelID: 1, RequestCount: 1, ErrorCount: 1}},
	}
	stale := base
	stale.recentBuckets = map[int64][]RecentChannelBucket{1: {
		{BucketTime: now.Add(-2 * time.Minute), RequestCount: 10, ErrorCount: 10},
	}}
	NewEngine(&stale).Tick(now)
	if len(stale.recommendations) != 0 {
		t.Fatal("stale sparse data must not trigger")
	}
	insufficient := base
	insufficient.recentBuckets = map[int64][]RecentChannelBucket{1: sparseBuckets(now, 9, 9)}
	NewEngine(&insufficient).Tick(now)
	if len(insufficient.recommendations) != 0 {
		t.Fatal("insufficient sparse request count must not trigger")
	}
}

func TestDutySparseSamplingSupportsTrialHealthAndRedemotion(t *testing.T) {
	p := testPolicy()
	p.Policy.Scheduling.TrialWindows = 1
	now := time.Now().UTC()
	trialState := DispatchState{InstanceID: "i", ChannelID: 1, ModelName: "m", OriginalPriority: 100}
	healthy := &fakeStore{
		policy:        p,
		channels:      []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:       []ChannelMetric{{ChannelID: 1, RequestCount: 1}},
		recentBuckets: map[int64][]RecentChannelBucket{1: sparseBuckets(now, 10, 0)},
		dispatch:      map[int64]DispatchState{1: trialState},
	}
	NewEngine(healthy).Tick(now)
	if _, ok := healthy.dispatch[1]; ok {
		t.Fatal("healthy sparse trial must retain the channel")
	}
	bad := &fakeStore{
		policy:        p,
		channels:      []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:       []ChannelMetric{{ChannelID: 1, RequestCount: 1, ErrorCount: 1}},
		recentBuckets: map[int64][]RecentChannelBucket{1: sparseBuckets(now, 10, 6)},
		dispatch:      map[int64]DispatchState{1: trialState},
	}
	NewEngine(bad).Tick(now)
	if len(bad.recommendations) != 1 || bad.recommendations[0].Rule != "demote" {
		t.Fatalf("unhealthy sparse trial must be demoted again: %#v", bad.recommendations)
	}
}

func TestDutySparseSamplingDoesNotTriggerLatencyOrDynamicWeighting(t *testing.T) {
	p := testPolicy()
	p.Policy.DynamicWeighting.Mode = "observe"
	now := time.Now().UTC()
	f := &fakeStore{
		policy:   p,
		channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 100, "m"), dutyChannel(3, 50, "m")},
		metrics: []ChannelMetric{
			{ChannelID: 1, RequestCount: 1, P95: 100, TTFTP95: 100},
			{ChannelID: 2, RequestCount: 1, P95: 1, TTFTP95: 1},
		},
		buckets: map[int64][]float64{1: {1, 1, 1, 1, 1, 1, 1, 1}},
		recentBuckets: map[int64][]RecentChannelBucket{
			1: sparseBuckets(now, 10, 0),
			2: sparseBuckets(now, 10, 0),
		},
	}
	e := NewEngine(f)
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 0 {
		t.Fatalf("sparse path must not affect latency or dynamic weighting: %#v", f.recommendations)
	}
}

func TestDutyNormalSamplePathDoesNotQuerySparseBuckets(t *testing.T) {
	p := testPolicy()
	f := &fakeStore{
		policy:   p,
		channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:  []ChannelMetric{{ChannelID: 1, RequestCount: 100}},
	}
	NewEngine(f).Tick(time.Now().UTC())
	if f.recentQueries != 0 {
		t.Fatalf("normal sample path queried sparse buckets %d times", f.recentQueries)
	}
}

func TestDutyDemoteSustainedSevereAndTiedActive(t *testing.T) {
	p := testPolicy()
	channels := []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 100, "m"), dutyChannel(3, 50, "m")}
	f := &fakeStore{policy: p, channels: channels, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 20, P95: 1}, {ChannelID: 2, RequestCount: 100, ErrorCount: 20, P95: 1}}}
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

	f2 := &fakeStore{policy: p, channels: channels, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 50, P95: 1}}}
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
		metrics:  []ChannelMetric{{ChannelID: 1, RequestCount: 100, P95: 11}},
	}
	e := NewEngine(f)
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 2 || f.recommendations[0].Rule != "mixed_channel" || f.recommendations[1].Evidence["trigger"] != "latency_degrade" {
		t.Fatalf("latency/mixed=%#v", f.recommendations)
	}

	single := &fakeStore{policy: p, channels: []Channel{dutyChannel(1, 100, "m")}, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 50, P95: 1}}}
	NewEngine(single).Tick(now)
	if len(single.recommendations) != 1 || single.recommendations[0].Rule != "no_backup" {
		t.Fatalf("single ladder=%#v", single.recommendations)
	}
	next := now.Add(time.Hour)
	exhausted := &fakeStore{
		policy: p, channels: []Channel{dutyChannel(1, 100, "m"), dutyChannel(2, 50, "m")},
		metrics:  []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 50, P95: 1}},
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
		channels: channels, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, P95: 50}},
	}
	e := NewEngine(insufficient)
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(insufficient.recommendations) != 0 {
		t.Fatal("fewer than eight baseline buckets must skip latency decision")
	}

	cooldown := &fakeStore{
		policy: p, channels: channels, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 50, P95: 1}},
		lastAction: now.Add(-30 * time.Second),
	}
	NewEngine(cooldown).Tick(now)
	if len(cooldown.recommendations) != 0 {
		t.Fatal("cooldown must suppress action recommendation")
	}
	limited := &fakeStore{
		policy: p, channels: channels, metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 50, P95: 1}},
		actionCount: p.Policy.Scheduling.DailyActionLimit,
	}
	NewEngine(limited).Tick(now)
	if len(limited.recommendations) != 0 {
		t.Fatal("daily action limit must suppress action recommendation")
	}
}
