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
	expiredBefore   time.Time
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
func (f *fakeStore) AdoptRecommendation(id, actor string, now time.Time) (Recommendation, error) {
	for i := range f.recommendations {
		if f.recommendations[i].ID != id || f.recommendations[i].Status != "pending" {
			continue
		}
		f.recommendations[i].Status = "auto_executed"
		f.recommendations[i].ActedBy = actor
		f.recommendations[i].ActedAt = &now
		if f.recommendations[i].Rule == "demote" {
			if f.dispatch == nil {
				f.dispatch = map[int64]DispatchState{}
			}
			f.dispatch[f.recommendations[i].ChannelID] = DispatchState{
				InstanceID: f.recommendations[i].InstanceID,
				ChannelID:  f.recommendations[i].ChannelID,
				DemotedAt:  now,
				UpdatedAt:  now,
			}
		}
		return f.recommendations[i], nil
	}
	return Recommendation{}, ErrRecommendationNotPending
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
func (f *fakeStore) ExpirePendingRecommendations(before time.Time) (int64, error) {
	f.expiredBefore = before
	for i := range f.recommendations {
		if f.recommendations[i].Status == "pending" && f.recommendations[i].CreatedAt.Before(before) {
			f.recommendations[i].Status = "expired"
		}
	}
	return 0, nil
}
func testPolicy() PolicyRecord {
	p := DefaultPolicy()
	p.Scheduling.WindowMinutes = 1
	p.Criteria[0].SustainedWindows = 2
	p.Scheduling.CooldownMinutes = 10
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
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: p.Policy.Scheduling.MinSamples - 1, ErrorCount: p.Policy.Scheduling.MinSamples - 1}},
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
	m := ChannelMetric{RequestCount: 110, ErrorCount: 20, UserErrorCount: 15}
	if got := m.ErrorRate(); got != 5.0/110 {
		t.Fatalf("weighted rate=%v", got)
	}
	if got := (ChannelMetric{RequestCount: 10, ErrorCount: 2, UserErrorCount: 3}).ErrorRate(); got != 0 {
		t.Fatalf("negative attributed error count must clamp to zero: %v", got)
	}
}

func TestEngineDoesNotDemoteForUserAttributedErrors(t *testing.T) {
	p := testPolicy()
	p.Policy.Criteria[0].SustainedWindows = 1
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "active", Status: "enabled", Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "backup", Status: "enabled", Priority: 50, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 60, UserErrorCount: 60}},
	}
	NewEngine(f).Tick(time.Now().UTC())
	if len(f.recommendations) != 0 {
		t.Fatalf("user-attributed errors must not demote channel: %#v", f.recommendations)
	}
}

func TestEngineDynamicWeightingUsesRelativeMetricsAndProtection(t *testing.T) {
	p := testPolicy()
	p.Policy.DynamicWeighting = DynamicWeightingParams{
		Enabled: true, TTFTInfluence: 1, ErrorInfluence: 0,
		CacheInfluence: 0, OTPSInfluence: 0,
		MinMultiplier: .5, MaxMultiplier: 1.5, SmoothingAlpha: 1,
		MaxIncreasePerRound: .2, MaxDecreasePerRound: .3,
	}
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "fast", Status: "enabled", Weight: 100, Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "slow", Status: "enabled", Weight: 100, Priority: 100, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{
			{ChannelID: 1, RequestCount: 100, TTFTP95: 2},
			{ChannelID: 2, RequestCount: 100, TTFTP95: 8},
		},
	}
	NewEngine(f).Tick(time.Now().UTC())
	if len(f.recommendations) != 2 {
		t.Fatalf("dynamic recommendations=%#v", f.recommendations)
	}
	byID := map[int64]Recommendation{}
	for _, recommendation := range f.recommendations {
		byID[recommendation.ChannelID] = recommendation
		if recommendation.Rule != "rebalance" || recommendation.Status != "recorded" {
			t.Fatalf("unexpected recommendation=%#v", recommendation)
		}
	}
	if byID[1].ProposedWeight != 120 {
		t.Fatalf("increase protection not applied: %#v", byID[1])
	}
	if byID[2].ProposedWeight != 70 {
		t.Fatalf("decrease protection not applied: %#v", byID[2])
	}
}

func TestEngineConfirmCreatesPendingWithoutAdvancingDispatchAndExpires(t *testing.T) {
	p := testPolicy()
	p.Mode = "confirm"
	p.Policy.Criteria[0].SustainedWindows = 1
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "active", Status: "enabled", Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "backup", Status: "enabled", Priority: 50, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 60}},
	}
	now := time.Now().UTC()
	NewEngine(f).Tick(now)
	if len(f.recommendations) != 1 || f.recommendations[0].Status != "pending" || f.recommendations[0].ModeAtCreation != "confirm" {
		t.Fatalf("confirm recommendation=%#v", f.recommendations)
	}
	if len(f.dispatch) != 0 {
		t.Fatalf("confirm must not advance dispatch before adoption: %#v", f.dispatch)
	}
	f.recommendations[0].CreatedAt = now.Add(-61 * time.Minute)
	NewEngine(f).Tick(now.Add(time.Minute))
	if f.recommendations[0].Status != "expired" || f.expiredBefore.IsZero() {
		t.Fatalf("pending recommendation not expired: %#v", f.recommendations[0])
	}
}

func TestEngineAutoExecutesDemoteAndAdvancesDispatch(t *testing.T) {
	p := testPolicy()
	p.Mode = "auto"
	p.Policy.Criteria[0].SustainedWindows = 1
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "active", Status: "enabled", Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "backup", Status: "enabled", Priority: 50, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 60}},
	}
	NewEngine(f).Tick(time.Now().UTC())
	if len(f.recommendations) != 1 || f.recommendations[0].Status != "auto_executed" || f.recommendations[0].ActedBy != "system:auto" {
		t.Fatalf("auto recommendation=%#v", f.recommendations)
	}
	if _, ok := f.dispatch[1]; !ok {
		t.Fatalf("auto demote must advance dispatch after command creation: %#v", f.dispatch)
	}
}

func TestEngineAutoExecutesProtectedDynamicWeights(t *testing.T) {
	p := testPolicy()
	p.Mode = "auto"
	p.Policy.DynamicWeighting = DynamicWeightingParams{
		Enabled: true, TTFTInfluence: 1, MinMultiplier: .5, MaxMultiplier: 1.5,
		SmoothingAlpha: 1, MaxIncreasePerRound: .2, MaxDecreasePerRound: .3,
	}
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "fast", Status: "enabled", Weight: 100, Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "slow", Status: "enabled", Weight: 100, Priority: 100, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{
			{ChannelID: 1, RequestCount: 100, TTFTP95: 2},
			{ChannelID: 2, RequestCount: 100, TTFTP95: 8},
		},
	}
	NewEngine(f).Tick(time.Now().UTC())
	if len(f.recommendations) != 2 {
		t.Fatalf("auto dynamic recommendations=%#v", f.recommendations)
	}
	for _, rec := range f.recommendations {
		if rec.Status != "auto_executed" || rec.Rule != "rebalance" {
			t.Fatalf("auto dynamic recommendation=%#v", rec)
		}
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
		{"demote-hit", "demote", ChannelMetric{ChannelID: 1, RequestCount: 20, ErrorCount: 4, P95: 1}, boolp(true)},
		{"demote-miss", "demote", ChannelMetric{ChannelID: 1, RequestCount: 20, P95: 1}, boolp(false)},
		{"trial-hit", "trial", ChannelMetric{ChannelID: 1, RequestCount: 20, P95: 1}, boolp(true)},
		{"trial-miss", "trial", ChannelMetric{ChannelID: 1, RequestCount: 20, ErrorCount: 4, P95: 1}, boolp(false)},
		{"few", "trial", ChannelMetric{ChannelID: 1, RequestCount: 4, P95: 1}, nil},
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

func (f *fakeStore) HasPendingActionRecommendation(_ string, ch int64) (bool, error) {
	for _, r := range f.recommendations {
		if r.ChannelID == ch && r.Status == "pending" && (r.Rule == "demote" || r.Rule == "trial") {
			return true, nil
		}
	}
	return false, nil
}

func TestEngineConfirmDoesNotStackPendingDuplicates(t *testing.T) {
	p := testPolicy()
	p.Mode = "confirm"
	p.Policy.Criteria[0].SustainedWindows = 1
	f := &fakeStore{
		policy: p,
		channels: []Channel{
			{ID: 1, Name: "active", Status: "enabled", Priority: 100, Models: []string{"m"}},
			{ID: 2, Name: "backup", Status: "enabled", Priority: 50, Models: []string{"m"}},
		},
		metrics: []ChannelMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 60}},
	}
	now := time.Now().UTC()
	engine := NewEngine(f)
	engine.Tick(now)
	// Past the cooldown, channel still degraded, first recommendation still
	// pending: the engine must not add a duplicate for the same open decision.
	engine.Tick(now.Add(11 * time.Minute))
	demotes := 0
	for _, r := range f.recommendations {
		if r.Rule == "demote" {
			demotes++
		}
	}
	if demotes != 1 {
		t.Fatalf("pending decision duplicated: %#v", f.recommendations)
	}
}
