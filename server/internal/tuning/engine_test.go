package tuning

import (
	"testing"
	"time"
)

type fakeStore struct {
	policy          PolicyRecord
	metrics         []ChannelMetric
	channels        []Channel
	recommendations []Recommendation
	pending         []Recommendation
	original        int64
	hasDegrade      bool
	outcomes        map[string]*bool
}

func (f *fakeStore) GetPolicy(string) (PolicyRecord, bool, error) { return f.policy, true, nil }
func (f *fakeStore) PutPolicy(PolicyRecord) error                 { return nil }
func (f *fakeStore) ListEnabledInstances() ([]string, error)      { return []string{"i"}, nil }
func (f *fakeStore) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	return f.metrics, nil
}
func (f *fakeStore) LatestChannels(string) ([]Channel, error) { return f.channels, nil }
func (f *fakeStore) InsertRecommendation(r Recommendation) error {
	f.recommendations = append(f.recommendations, r)
	if r.Rule == "degrade" {
		f.hasDegrade = true
		f.original = r.CurrentWeight
	}
	if r.Rule == "recover" {
		f.hasDegrade = false
	}
	return nil
}
func (f *fakeStore) OriginalDegrade(string, int64) (int64, bool, error) {
	return f.original, f.original > 0, nil
}
func (f *fakeStore) HasUnrecoveredDegrade(string, int64) (bool, error) { return f.hasDegrade, nil }
func (f *fakeStore) PendingOutcomes(time.Time, int) ([]Recommendation, error) {
	x := f.pending
	f.pending = nil
	return x, nil
}
func (f *fakeStore) UpdateOutcome(id string, _ map[string]any, _ time.Time, h *bool) error {
	if f.outcomes == nil {
		f.outcomes = map[string]*bool{}
	}
	f.outcomes[id] = h
	return nil
}
func (f *fakeStore) ListRecommendations(RecommendationQuery) ([]Recommendation, error) {
	return f.recommendations, nil
}
func (f *fakeStore) RecommendationReport(RecommendationQuery) (Report, error) { return Report{}, nil }
func testPolicy() PolicyRecord {
	p := DefaultPolicy()
	p.EvaluationWindowMinutes = 1
	p.Degrade.SustainedWindows = 2
	p.Recover.SustainedWindows = 2
	p.CooldownMinutes = 10
	return PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}
}
func TestEngineMinSamplesSustainedCooldownAndFloor(t *testing.T) {
	f := &fakeStore{policy: testPolicy(), channels: []Channel{{1, "c", "enabled", 1}}, metrics: []ChannelMetric{{1, 10, 10, 9}}}
	e := NewEngine(f)
	now := time.Now().UTC()
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 0 {
		t.Fatal("min samples should block")
	}
	f.metrics = []ChannelMetric{{1, 100, 20, 9}}
	e.Tick(now.Add(2 * time.Minute))
	f.metrics = []ChannelMetric{{1, 100, 1, 9}}
	e.Tick(now.Add(3 * time.Minute))
	f.metrics = []ChannelMetric{{1, 100, 20, 9}}
	e.Tick(now.Add(4 * time.Minute))
	if len(f.recommendations) != 0 {
		t.Fatal("discontinuous windows triggered")
	}
	e.Tick(now.Add(5 * time.Minute))
	if len(f.recommendations) != 1 || f.recommendations[0].ProposedWeight != 1 {
		t.Fatalf("degrade/floor: %#v", f.recommendations)
	}
	e.Tick(now.Add(6 * time.Minute))
	e.Tick(now.Add(7 * time.Minute))
	if len(f.recommendations) != 1 {
		t.Fatal("cooldown not enforced")
	}
}
func TestEngineWeightedRateAndRecoverSimulation(t *testing.T) {
	m := ChannelMetric{RequestCount: 110, ErrorCount: 20}
	if got := m.ErrorRate(); got != 20.0/110 {
		t.Fatalf("weighted rate=%v", got)
	}
	p := testPolicy()
	f := &fakeStore{policy: p, channels: []Channel{{1, "c", "enabled", 4}}, metrics: []ChannelMetric{{1, 100, 0, 1}}, original: 8}
	e := NewEngine(f)
	now := time.Now().UTC()
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 0 {
		t.Fatal("recover without degrade history")
	}
	f.hasDegrade = true
	e.Tick(now.Add(2 * time.Minute))
	e.Tick(now.Add(3 * time.Minute))
	if len(f.recommendations) != 1 || f.recommendations[0].Rule != "recover" || f.recommendations[0].ProposedWeight != 8 || f.recommendations[0].Evidence["simulated"] != true {
		t.Fatalf("recover: %#v", f.recommendations)
	}
}
func TestOutcomeHitMissAndInsufficient(t *testing.T) {
	p := testPolicy()
	now := time.Now().UTC()
	cases := []struct {
		id, rule string
		metric   ChannelMetric
		want     *bool
	}{{"hit", "degrade", ChannelMetric{1, 10, 2, 1}, boolp(true)}, {"miss", "degrade", ChannelMetric{1, 10, 0, 1}, boolp(false)}, {"few", "recover", ChannelMetric{1, 4, 0, 1}, nil}}
	for _, c := range cases {
		f := &fakeStore{policy: p, metrics: []ChannelMetric{c.metric}, pending: []Recommendation{{ID: c.id, InstanceID: "i", ChannelID: 1, Rule: c.rule, CreatedAt: now.Add(-time.Hour)}}}
		NewEngine(f).fillOutcomes(now)
		got := f.outcomes[c.id]
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Fatalf("%s hit=%v", c.id, got)
		}
	}
}
func boolp(v bool) *bool { return &v }
