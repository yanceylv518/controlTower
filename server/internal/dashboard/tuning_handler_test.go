package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/tuning"
)

type tuningStub struct {
	recs       []tuning.Recommendation
	report     tuning.Report
	query      tuning.RecommendationQuery
	saved      tuning.PolicyRecord
	baseValues []tuning.ChannelBaseValue
	baseSaved  []tuning.ChannelBaseValue
	syncModels []string
}

func (s *tuningStub) GetPolicy(string) (tuning.PolicyRecord, bool, error) {
	return tuning.PolicyRecord{}, false, nil
}
func (s *tuningStub) PutPolicy(value tuning.PolicyRecord) error { s.saved = value; return nil }
func (s *tuningStub) ListEnabledSites() ([]string, error)       { return nil, nil }
func (s *tuningStub) QueryMetrics(string, time.Time, time.Time) ([]tuning.ChannelMetric, error) {
	return nil, nil
}
func (s *tuningStub) QueryRecentChannelBuckets(string, int64, time.Time, int) ([]tuning.RecentChannelBucket, error) {
	return nil, nil
}
func (s *tuningStub) InsertRecommendation(tuning.Recommendation) error { return nil }
func (s *tuningStub) ListRecommendations(q tuning.RecommendationQuery) ([]tuning.Recommendation, error) {
	s.query = q
	return s.recs, nil
}
func (s *tuningStub) RecommendationReport(tuning.RecommendationQuery) (tuning.Report, error) {
	return s.report, nil
}
func (s *tuningStub) ListChannelBaseValues(string, string) ([]tuning.ChannelBaseValue, error) {
	return s.baseValues, nil
}
func (s *tuningStub) SaveChannelBaseValues(_ string, _ string, values []tuning.ChannelBaseValue, _ time.Time) error {
	s.baseSaved = append([]tuning.ChannelBaseValue(nil), values...)
	s.baseValues = append([]tuning.ChannelBaseValue(nil), values...)
	return nil
}
func (s *tuningStub) SyncChannelBaseValues(_ string, models []string) ([]tuning.ChannelBaseValue, error) {
	s.syncModels = append([]string(nil), models...)
	return []tuning.ChannelBaseValue{{InstanceID: "i", ChannelID: 7, ModelName: "m", BaseWeight: 10, BasePriority: 3}}, nil
}
func TestTuningPolicyDefaultValidationAndMode(t *testing.T) {
	s := &tuningStub{}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("GET", "/api/dashboard/tuning/policy?site_id=site-a", nil))
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"isDefault":true`)) || !bytes.Contains(rr.Body.Bytes(), []byte(`"site_id":"site-a"`)) {
		t.Fatalf("default: %d %s", rr.Code, rr.Body.String())
	}
	bad := `{"mode":"observe","policy":{"window_minutes":0}}`
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("PUT", "/api/dashboard/tuning/policy?instance_id=i", bytes.NewBufferString(bad)))
	if rr.Code != 400 || !bytes.Contains(rr.Body.Bytes(), []byte("validation_failed")) {
		t.Fatalf("validation: %d %s", rr.Code, rr.Body.String())
	}
	policyJSON, _ := json.Marshal(tuning.DefaultPolicy())
	auto := `{"mode":"auto","policy":` + string(policyJSON) + `}`
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("PUT", "/api/dashboard/tuning/policy?instance_id=i", bytes.NewBufferString(auto)))
	if rr.Code != 200 || s.saved.Mode != "auto" {
		t.Fatalf("mode: %d %s", rr.Code, rr.Body.String())
	}
	confirm := `{"mode":"confirm","policy":` + string(policyJSON) + `}`
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest("PUT", "/api/dashboard/tuning/policy?instance_id=i", bytes.NewBufferString(confirm)))
	if rr.Code != 200 || s.saved.Mode != "confirm" {
		t.Fatalf("confirm: %d %s %#v", rr.Code, rr.Body.String(), s.saved)
	}
}
func TestTuningRecommendationsPaginationAndReport(t *testing.T) {
	s := &tuningStub{recs: []tuning.Recommendation{{ID: "r", InstanceID: "i", Evidence: map[string]any{"samples": 20}}}, report: tuning.Report{Total: 4, ByRule: map[string]int64{"demote": 4}}}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningRecommendations(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/tuning/recommendations?instance_id=i&limit=12&before=2026-07-14T00:00:00Z&rule=rebalance", nil))
	if rr.Code != 200 || s.query.Limit != 12 || s.query.Rule != "rebalance" || s.query.Before.IsZero() || !bytes.Contains(rr.Body.Bytes(), []byte(`"evidence"`)) {
		t.Fatalf("recommendations: %d %s %#v", rr.Code, rr.Body.String(), s.query)
	}
	rr = httptest.NewRecorder()
	h.HandleTuningReport(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/tuning/report?instance_id=i&days=7", nil))
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"total":4`)) || !bytes.Contains(rr.Body.Bytes(), []byte(`"demote":4`)) || bytes.Contains(rr.Body.Bytes(), []byte("hit_rate")) {
		t.Fatalf("report: %d %s", rr.Code, rr.Body.String())
	}
}

func TestTuningBaseValuesSyncDoesNotPersistAndPutValidates(t *testing.T) {
	s := &tuningStub{}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningBaseValuesSync(rr, httptest.NewRequest(http.MethodPost, "/api/dashboard/tuning/base-values/sync?instance_id=i", bytes.NewBufferString(`{"models":["m"]}`)))
	if rr.Code != http.StatusOK || len(s.syncModels) != 1 || len(s.baseSaved) != 0 {
		t.Fatalf("sync must only return a preview: %d %s %#v", rr.Code, rr.Body.String(), s)
	}

	rr = httptest.NewRecorder()
	h.HandleTuningBaseValues(rr, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/base-values?instance_id=i", bytes.NewBufferString(`{"items":[{"channel_id":0,"model_name":"m","base_weight":1,"base_priority":1}]}`)))
	if rr.Code != http.StatusBadRequest || len(s.baseSaved) != 0 {
		t.Fatalf("invalid values must not persist: %d %s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	h.HandleTuningBaseValues(rr, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/base-values?instance_id=i", bytes.NewBufferString(`{"items":[{"channel_id":7,"model_name":"m","base_weight":10,"base_priority":3}]}`)))
	if rr.Code != http.StatusOK || len(s.baseSaved) != 1 {
		t.Fatalf("valid values must persist: %d %s", rr.Code, rr.Body.String())
	}
}
