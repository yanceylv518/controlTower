package dashboard

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/tuning"
)

type tuningStub struct {
	recs   []tuning.Recommendation
	report tuning.Report
	query  tuning.RecommendationQuery
}

func (s *tuningStub) GetPolicy(string) (tuning.PolicyRecord, bool, error) {
	return tuning.PolicyRecord{}, false, nil
}
func (s *tuningStub) PutPolicy(tuning.PolicyRecord) error     { return nil }
func (s *tuningStub) ListEnabledInstances() ([]string, error) { return nil, nil }
func (s *tuningStub) QueryMetrics(string, time.Time, time.Time) ([]tuning.ChannelMetric, error) {
	return nil, nil
}
func (s *tuningStub) LatestChannels(string) ([]tuning.Channel, error)    { return nil, nil }
func (s *tuningStub) InsertRecommendation(tuning.Recommendation) error   { return nil }
func (s *tuningStub) OriginalDegrade(string, int64) (int64, bool, error) { return 0, false, nil }
func (s *tuningStub) HasUnrecoveredDegrade(string, int64) (bool, error)  { return false, nil }
func (s *tuningStub) PendingOutcomes(time.Time, int) ([]tuning.Recommendation, error) {
	return nil, nil
}
func (s *tuningStub) UpdateOutcome(string, map[string]any, time.Time, *bool) error { return nil }
func (s *tuningStub) ListRecommendations(q tuning.RecommendationQuery) ([]tuning.Recommendation, error) {
	s.query = q
	return s.recs, nil
}
func (s *tuningStub) RecommendationReport(tuning.RecommendationQuery) (tuning.Report, error) {
	return s.report, nil
}
func TestTuningPolicyDefaultValidationAndMode(t *testing.T) {
	s := &tuningStub{}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/policy?instance_id=i", nil))
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"isDefault":true`)) {
		t.Fatalf("default: %d %s", rr.Code, rr.Body.String())
	}
	bad := `{"mode":"observe","policy":{"evaluation_window_minutes":0}}`
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("PUT", "/api/dashboard/tuning/policy?instance_id=i", bytes.NewBufferString(bad)))
	if rr.Code != 400 || !bytes.Contains(rr.Body.Bytes(), []byte("validation_failed")) {
		t.Fatalf("validation: %d %s", rr.Code, rr.Body.String())
	}
	auto := `{"mode":"auto","policy":{}}`
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("PUT", "/api/dashboard/tuning/policy?instance_id=i", bytes.NewBufferString(auto)))
	if rr.Code != 400 || !bytes.Contains(rr.Body.Bytes(), []byte("mode_not_supported")) {
		t.Fatalf("mode: %d %s", rr.Code, rr.Body.String())
	}
}
func TestTuningRecommendationsPaginationAndReport(t *testing.T) {
	s := &tuningStub{recs: []tuning.Recommendation{{ID: "r", InstanceID: "i", Evidence: map[string]any{"samples": 20}}}, report: tuning.Report{Total: 3, ByRule: map[string]int64{"degrade": 3}, Filled: 3, Judged: 2, Hits: 1}}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningRecommendations(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/tuning/recommendations?instance_id=i&limit=12&before=2026-07-14T00:00:00Z", nil))
	if rr.Code != 200 || s.query.Limit != 12 || s.query.Before.IsZero() || !bytes.Contains(rr.Body.Bytes(), []byte(`"evidence"`)) {
		t.Fatalf("recommendations: %d %s %#v", rr.Code, rr.Body.String(), s.query)
	}
	rr = httptest.NewRecorder()
	h.HandleTuningReport(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/tuning/report?instance_id=i&days=7", nil))
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"hit_rate":0.5`)) || !bytes.Contains(rr.Body.Bytes(), []byte("autoCriteria")) {
		t.Fatalf("report: %d %s", rr.Code, rr.Body.String())
	}
}
