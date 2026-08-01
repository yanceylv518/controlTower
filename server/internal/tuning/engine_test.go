package tuning

import (
	"testing"
	"time"
)

type sentinelStore struct {
	policy  PolicyRecord
	expired bool
	events  []Recommendation
}

func (s *sentinelStore) GetPolicy(string) (PolicyRecord, bool, error) { return s.policy, true, nil }
func (s *sentinelStore) PutPolicy(p PolicyRecord) error               { s.policy = p; return nil }
func (s *sentinelStore) ListEnabledInstances() ([]string, error)      { return nil, nil }
func (s *sentinelStore) QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error) {
	return nil, nil
}
func (s *sentinelStore) QueryRecentChannelBuckets(string, int64, time.Time, int) ([]RecentChannelBucket, error) {
	return nil, nil
}
func (s *sentinelStore) InsertRecommendation(r Recommendation) error {
	s.events = append(s.events, r)
	return nil
}
func (s *sentinelStore) ListRecommendations(RecommendationQuery) ([]Recommendation, error) {
	return nil, nil
}
func (s *sentinelStore) RecommendationReport(RecommendationQuery) (Report, error) {
	return Report{}, nil
}
func (s *sentinelStore) HasExpiredAutoCommands(string, time.Time) (bool, error) {
	return s.expired, nil
}

func TestAutoSentinelPausesContinuousAutoModes(t *testing.T) {
	now := time.Now().UTC()
	p := PolicyRecord{InstanceID: "i", Policy: DefaultPolicy(), Mode: "auto", UpdatedAt: now.Add(-time.Hour)}
	p.Policy.DispatchModes = map[string]string{"m1": "auto", "m2": "observe"}
	s := &sentinelStore{policy: p, expired: true}
	NewEngine(s).runAutoSentinel(&p, now)
	if p.Policy.DispatchModes["m1"] != "observe" || p.Mode != "auto" {
		t.Fatalf("unexpected sentinel result: %#v", p)
	}
	if len(s.events) != 1 || s.events[0].Rule != "auto_paused" {
		t.Fatalf("events=%#v", s.events)
	}
}

func TestAutoSentinelIgnoresLegacyGlobalAuto(t *testing.T) {
	now := time.Now().UTC()
	p := PolicyRecord{InstanceID: "i", Policy: DefaultPolicy(), Mode: "auto", UpdatedAt: now.Add(-time.Hour)}
	s := &sentinelStore{policy: p, expired: true}
	NewEngine(s).runAutoSentinel(&p, now)
	if len(s.events) != 0 || p.Mode != "auto" {
		t.Fatalf("legacy global mode affected: %#v %#v", p, s.events)
	}
}
