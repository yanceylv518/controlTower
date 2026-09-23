package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/auditmeta"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

type tuningStub struct {
	recs            []tuning.Recommendation
	report          tuning.Report
	query           tuning.RecommendationQuery
	saved           tuning.PolicyRecord
	policy          tuning.PolicyRecord
	policyExists    bool
	putPolicyCalls  int
	baseValues      []tuning.ChannelBaseValue
	baseSaved       []tuning.ChannelBaseValue
	baseAudit       storage.OperationAudit
	syncModels      []string
	preflight       storage.ChannelCommand
	preflightStatus string
	preflightError  string
	states          []tuning.ContinuousState
	priorityWrites  []tuning.Recommendation
	priorityAudits  []storage.OperationAudit
}

func (s *tuningStub) GetPolicy(string) (tuning.PolicyRecord, bool, error) {
	return s.policy, s.policyExists, nil
}
func (s *tuningStub) PutPolicy(value tuning.PolicyRecord) error {
	s.saved = value
	s.policy = value
	s.policyExists = true
	s.putPolicyCalls++
	return nil
}
func (s *tuningStub) ListEnabledSites() ([]string, error) { return nil, nil }
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
func (s *tuningStub) SaveChannelBaseValuesWithAudit(_ string, _ string, values []tuning.ChannelBaseValue, _ time.Time, audit storage.OperationAudit) error {
	s.baseSaved = append([]tuning.ChannelBaseValue(nil), values...)
	s.baseValues = append([]tuning.ChannelBaseValue(nil), values...)
	s.baseAudit = audit
	return nil
}
func (s *tuningStub) ListContinuousStates(string) ([]tuning.ContinuousState, error) {
	return s.states, nil
}
func (s *tuningStub) CreateContinuousWeightChange(value tuning.Recommendation, _ string, _ time.Time) (string, error) {
	s.priorityWrites = append(s.priorityWrites, value)
	return "command-1", nil
}
func (s *tuningStub) CreateContinuousWeightChangeWithAudit(value tuning.Recommendation, _ string, _ time.Time, audit storage.OperationAudit) (string, error) {
	s.priorityWrites = append(s.priorityWrites, value)
	s.priorityAudits = append(s.priorityAudits, audit)
	return "command-1", nil
}
func (s *tuningStub) SyncChannelBaseValues(_ string, models []string) ([]tuning.ChannelBaseValue, error) {
	s.syncModels = append([]string(nil), models...)
	return []tuning.ChannelBaseValue{{InstanceID: "i", ChannelID: 7, ModelName: "m", BaseWeight: 10, BasePriority: 3}}, nil
}
func (s *tuningStub) CreateTuningPreflight(_ string, channelID int64, actor string, now time.Time) (storage.ChannelCommand, error) {
	status, errorSummary := s.preflightStatus, s.preflightError
	if status == "" {
		status = "pending"
	}
	s.preflight = storage.ChannelCommand{ID: "verify-1", ChannelID: channelID, CommandType: "channel.verify", Status: status, ErrorSummary: errorSummary, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	return s.preflight, nil
}
func (s *tuningStub) GetTuningPreflight(_ string, commandID string) (storage.ChannelCommand, bool, error) {
	return s.preflight, s.preflight.ID == commandID, nil
}

type tuningAuditRecorder struct{ entries []storage.OperationAudit }

func (s *tuningAuditRecorder) InsertOperationAudit(value storage.OperationAudit) error {
	s.entries = append(s.entries, value)
	return nil
}

func TestTuningPolicyNoOpDoesNotPersistOrAudit(t *testing.T) {
	s := &tuningStub{}
	audits := &tuningAuditRecorder{}
	h := NewHandler(nil).WithTuningStore(s).WithOperationAuditStore(audits)
	body, err := json.Marshal(tuning.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/policy?site_id=s", bytes.NewReader(append(append([]byte(`{"mode":"observe","policy":`), body...), []byte("}")...)))
	r = auditmeta.WithRequestMetadata(r, auditmeta.RequestMetadata{ActorID: "operator"})
	rr := httptest.NewRecorder()
	h.HandleTuningPolicy(rr, r)
	if rr.Code != http.StatusOK || s.putPolicyCalls != 0 || len(audits.entries) != 0 {
		t.Fatalf("default policy no-op must not persist or audit: status=%d puts=%d audits=%#v body=%s", rr.Code, s.putPolicyCalls, audits.entries, rr.Body.String())
	}
	if !auditmeta.AuditHandledWithoutRecord(r) || auditmeta.SemanticAuditRecorded(r) {
		t.Fatal("successful no-op must suppress generic HTTP audit")
	}
}

func TestTuningPolicyChangeIsPersistedAndAuditedOnce(t *testing.T) {
	policy := tuning.DefaultPolicy()
	policy.Continuous.Sensitivity = 1.5
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	s := &tuningStub{policy: tuning.PolicyRecord{InstanceID: "s", Policy: tuning.DefaultPolicy(), Mode: "observe"}, policyExists: true}
	audits := &tuningAuditRecorder{}
	h := NewHandler(nil).WithTuningStore(s).WithOperationAuditStore(audits)
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/policy?site_id=s", bytes.NewReader(append(append([]byte(`{"mode":"observe","policy":`), policyJSON...), []byte("}")...)))
	r = auditmeta.WithRequestMetadata(r, auditmeta.RequestMetadata{ActorID: "operator"})
	rr := httptest.NewRecorder()
	h.HandleTuningPolicy(rr, r)
	if rr.Code != http.StatusOK || s.putPolicyCalls != 1 || len(audits.entries) != 1 {
		t.Fatalf("changed policy must be persisted and audited once: status=%d puts=%d audits=%#v body=%s", rr.Code, s.putPolicyCalls, audits.entries, rr.Body.String())
	}
	if audits.entries[0].OperationType != "tuning.policy_update" || !auditmeta.SemanticAuditRecorded(r) {
		t.Fatalf("unexpected semantic audit: %#v", audits.entries[0])
	}
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

func TestTuningAutoModeRequiresSuccessfulPreflight(t *testing.T) {
	s := &tuningStub{}
	h := NewHandler(nil).WithTuningStore(s)
	policy := tuning.DefaultPolicy()
	policy.DispatchModes = map[string]string{"m": "auto"}
	policyJSON, _ := json.Marshal(policy)

	rr := httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/policy?site_id=s", bytes.NewBufferString(`{"mode":"observe","policy":`+string(policyJSON)+`}`)))
	if rr.Code != http.StatusConflict || !bytes.Contains(rr.Body.Bytes(), []byte("tuning_preflight_required")) {
		t.Fatalf("auto without preflight must fail: %d %s", rr.Code, rr.Body.String())
	}

	s.preflight = storage.ChannelCommand{ID: "verify-1", CommandType: "channel.verify", Status: "succeeded", UpdatedAt: time.Now().UTC()}
	rr = httptest.NewRecorder()
	h.HandleTuningPolicy(rr, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/policy?site_id=s", bytes.NewBufferString(`{"mode":"observe","preflight_command_id":"verify-1","policy":`+string(policyJSON)+`}`)))
	if rr.Code != http.StatusOK || s.saved.Policy.DispatchModes["m"] != "auto" {
		t.Fatalf("successful preflight must allow auto: %d %s", rr.Code, rr.Body.String())
	}
}
func TestTuningPreflightPostReturnsTerminalResultInline(t *testing.T) {
	s := &tuningStub{preflightStatus: "failed", preflightError: "new-api channel update failed: unauthorized"}
	h := NewHandler(nil).WithTuningStore(s)
	rr := httptest.NewRecorder()
	h.HandleTuningPreflight(rr, httptest.NewRequest(http.MethodPost, "/api/dashboard/tuning/preflight?site_id=s", bytes.NewBufferString(`{"channel_id":9}`)))
	if rr.Code != http.StatusAccepted || !bytes.Contains(rr.Body.Bytes(), []byte(`"status":"failed"`)) || !bytes.Contains(rr.Body.Bytes(), []byte("unauthorized")) {
		t.Fatalf("synchronous preflight result must ride the POST response: %d %s", rr.Code, rr.Body.String())
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

	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/base-values?instance_id=i", bytes.NewBufferString(`{"items":[{"channel_id":7,"model_name":"m","base_weight":10,"base_priority":3}]}`))
	r.RemoteAddr = "203.0.113.8:4321"
	r.Pattern = "PUT /api/dashboard/tuning/base-values"
	r = auditmeta.WithRequestMetadata(r, auditmeta.RequestMetadata{RequestID: "request-base-1", ActorID: "operator", ActorType: "human", ActorRole: "admin", AuthMethod: "session"})
	rr = httptest.NewRecorder()
	h.HandleTuningBaseValues(rr, r)
	if rr.Code != http.StatusOK || len(s.baseSaved) != 1 {
		t.Fatalf("valid values must persist: %d %s", rr.Code, rr.Body.String())
	}
	if s.baseAudit.RequestID != "request-base-1" || s.baseAudit.CorrelationID != "request-base-1" || s.baseAudit.ClientIP != "203.0.113.8" || s.baseAudit.AuthMethod != "session" || s.baseAudit.HTTPMethod != http.MethodPut || s.baseAudit.Route != r.Pattern || s.baseAudit.ActorRole != "admin" {
		t.Fatalf("base-value audit metadata was not forwarded: %+v", s.baseAudit)
	}
}

func TestSavingBasePrioritySyncsOnlineIncludingCircuitChannels(t *testing.T) {
	now := time.Now().UTC()
	before := []tuning.ChannelBaseValue{
		{ChannelID: 7, ChannelName: "normal", ModelName: "m", BaseWeight: 10, BasePriority: 1, CurrentWeight: 8, CurrentPriority: 1},
		{ChannelID: 8, ChannelName: "circuit", ModelName: "m", BaseWeight: 10, BasePriority: 1, CurrentWeight: 0, CurrentPriority: 0},
	}
	s := &tuningStub{baseValues: before, states: []tuning.ContinuousState{{ChannelID: 8, Phase: "circuit"}}}
	h := NewHandler(nil).WithTuningStore(s)
	body := `{"items":[{"channel_id":7,"channel_name":"normal","model_name":"m","base_weight":10,"base_priority":3},{"channel_id":8,"channel_name":"circuit","model_name":"m","base_weight":10,"base_priority":4}]}`
	r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/base-values?site_id=i", bytes.NewBufferString(body))
	r = auditmeta.WithRequestMetadata(r, auditmeta.RequestMetadata{RequestID: "request-priority-1", ActorID: "operator", ActorType: "human", ActorRole: "admin", AuthMethod: "session"})
	rr := httptest.NewRecorder()
	h.HandleTuningBaseValues(rr, r)
	if rr.Code != http.StatusOK || len(s.priorityWrites) != 2 {
		t.Fatalf("expected normal and circuit priority sync: %d %s %#v", rr.Code, rr.Body.String(), s.priorityWrites)
	}
	write := s.priorityWrites[0]
	if write.ChannelID != 7 || write.CurrentPriority == nil || *write.CurrentPriority != 1 || write.ProposedPriority == nil || *write.ProposedPriority != 3 || write.Rule != "base_priority_sync" || write.CreatedAt.Before(now.Add(-time.Minute)) {
		t.Fatalf("unexpected priority sync: %#v", write)
	}
	if r := s.priorityWrites[1]; r.ChannelID != 8 || r.ProposedPriority == nil || *r.ProposedPriority != 4 || r.ProposedWeight != 0 {
		t.Fatalf("circuit save must change only priority: %#v", r)
	}
	if len(s.baseSaved) != 2 || s.baseSaved[1].BasePriority != 4 {
		t.Fatalf("circuit channel must still save latest base priority: %#v", s.baseSaved)
	}
	if len(s.priorityAudits) != 2 || s.priorityAudits[0].RequestID != "request-priority-1" || s.priorityAudits[1].RequestID != "request-priority-1" {
		t.Fatalf("priority-sync audits must retain the initiating request: %#v", s.priorityAudits)
	}
}

func TestSavingWeightDoesNotSyncUnchangedBasePriority(t *testing.T) {
	before := []tuning.ChannelBaseValue{{
		ChannelID: 7, ChannelName: "channel", ModelName: "m", BaseWeight: 10, BasePriority: 9,
		CurrentWeight: 8, CurrentPriority: 3,
	}}
	s := &tuningStub{baseValues: before}
	h := NewHandler(nil).WithTuningStore(s)
	body := `{"items":[{"channel_id":7,"channel_name":"channel","model_name":"m","base_weight":20,"base_priority":9,"max_rpm":0,"max_tpm":0}]}`
	rr := httptest.NewRecorder()
	h.HandleTuningBaseValues(rr, httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/base-values?site_id=s", bytes.NewBufferString(body)))
	if rr.Code != http.StatusOK || len(s.priorityWrites) != 0 {
		t.Fatalf("weight-only update must not synchronize an unchanged base priority: status=%d writes=%#v body=%s", rr.Code, s.priorityWrites, rr.Body.String())
	}
}
