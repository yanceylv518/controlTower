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
	outcomes        map[string]*bool
	buckets         map[int64][]float64
	dispatch        map[int64]DispatchState
	actionCount     int
	lastAction      time.Time
}

func (f *fakeStore) GetPolicy(string) (PolicyRecord, bool, error) { return f.policy, true, nil }
func (f *fakeStore) PutPolicy(PolicyRecord) error                 { return nil }
func (f *fakeStore) ListEnabledInstances() ([]string, error)      { return []string{"i"}, nil }
func (f *fakeStore) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	return f.metrics, nil
}
func (f *fakeStore) LatestChannels(string) ([]Channel, error) { return f.channels, nil }
func (f *fakeStore) QueryP95Buckets(_ string, ch int64, _, _ time.Time, _ int64) ([]float64, error) {
	return f.buckets[ch], nil
}
func (f *fakeStore) InsertRecommendation(r Recommendation) error {
	f.recommendations = append(f.recommendations, r)
	return nil
}
func (f *fakeStore) HasRecentRecommendation(id string, ch int64, rule string, since time.Time) (bool, error) {
	for _, r := range f.recommendations {
		if r.InstanceID == id && r.ChannelID == ch && r.Rule == rule && !r.CreatedAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) CountActionRecommendations(string, int64, time.Time) (int, error) {
	return f.actionCount, nil
}
func (f *fakeStore) LastActionRecommendationAt(string, int64) (time.Time, bool, error) {
	return f.lastAction, !f.lastAction.IsZero(), nil
}
func (f *fakeStore) ListDispatchStates(string) ([]DispatchState, error) {
	var out []DispatchState
	for _, s := range f.dispatch {
		out = append(out, s)
	}
	return out, nil
}
func (f *fakeStore) PutDispatchState(s DispatchState) error {
	if f.dispatch == nil {
		f.dispatch = map[int64]DispatchState{}
	}
	f.dispatch[s.ChannelID] = s
	return nil
}
func (f *fakeStore) DeleteDispatchState(_ string, ch int64) error {
	delete(f.dispatch, ch)
	return nil
}
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
	p.WindowMinutes = 1
	p.SustainedWindows = 2
	p.CooldownMinutes = 10
	return PolicyRecord{InstanceID: "i", Policy: p, Mode: "observe"}
}
func TestEngineMinSamplesSustainedCooldownAndFloor(t *testing.T) {
	p := testPolicy()
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "active", Status: "enabled", Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "backup", Status: "enabled", Priority: 50, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: p.Policy.MinSamples - 1, ErrorCount: p.Policy.MinSamples - 1}},
	}
	e := NewEngine(f)
	now := time.Now().UTC()
	e.Tick(now)
	e.Tick(now.Add(time.Minute))
	if len(f.recommendations) != 0 {
		t.Fatal("minimum samples must block dispatch")
	}
}
func TestEngineWeightedRateAndRecoverSimulation(t *testing.T) {
	m := ChannelMetric{RequestCount: 110, ErrorCount: 20}
	if got := m.ErrorRate(); got != 20.0/110 {
		t.Fatalf("weighted rate=%v", got)
	}
}
func TestOutcomeHitMissAndInsufficient(t *testing.T) {
	p := testPolicy()
	now := time.Now().UTC()
	cases := []struct {
		id, rule string
		metric   ChannelMetric
		want     *bool
	}{
		{"demote-hit", "demote", ChannelMetric{1, 20, 4, 1}, boolp(true)},
		{"demote-miss", "demote", ChannelMetric{1, 20, 0, 1}, boolp(false)},
		{"trial-hit", "trial", ChannelMetric{1, 20, 0, 1}, boolp(true)},
		{"trial-miss", "trial", ChannelMetric{1, 20, 4, 1}, boolp(false)},
		{"few", "trial", ChannelMetric{1, 4, 0, 1}, nil},
	}
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
