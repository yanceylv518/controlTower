package dashboard

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type preflightJobsStore struct {
	created      bool
	active       *billing.Job
	covering     *billing.Job
	jobs         []billing.Job
	createdJob   billing.Job
	createdSteps []billing.JobStep
	activeDays   map[string]string
	jobsByID     map[string]billing.Job
	requestJob   *billing.Job
}

func (s *preflightJobsStore) CreateBillingJob(_ context.Context, job billing.Job, steps []billing.JobStep) error {
	s.created = true
	s.createdJob = job
	s.createdSteps = steps
	return nil
}

func TestBillingJobCanTargetOneUserEvenWhenFullRangeExists(t *testing.T) {
	covering := billing.Job{ID: "full-job", Status: "complete"}
	store := &preflightJobsStore{covering: &covering}
	handler := BillingJobsHandler{Store: store}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00","scope":"user","user_id":42}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 202 || !store.created || store.createdJob.UserID != 42 || store.createdJob.JobType != "generate" {
		t.Fatalf("code=%d created=%v job=%+v body=%s", rec.Code, store.created, store.createdJob, rec.Body.String())
	}
}
func (s *preflightJobsStore) BillingJob(_ context.Context, id string) (billing.Job, error) {
	if job, ok := s.jobsByID[id]; ok {
		return job, nil
	}
	return billing.Job{}, context.Canceled
}
func (s *preflightJobsStore) BillingActiveDays(context.Context, string, int64, time.Time, time.Time) (map[string]string, error) {
	return s.activeDays, nil
}
func (s *preflightJobsStore) BillingJobByRequestKey(context.Context, string) (billing.Job, error) {
	if s.requestJob != nil {
		return *s.requestJob, nil
	}
	return billing.Job{}, sql.ErrNoRows
}
func (s *preflightJobsStore) ActiveBillingJob(context.Context) (billing.Job, error) {
	if s.active == nil {
		return billing.Job{}, sql.ErrNoRows
	}
	return *s.active, nil
}
func (s *preflightJobsStore) ListBillingJobs(context.Context, string, string, int) ([]billing.Job, error) {
	return s.jobs, nil
}
func (s *preflightJobsStore) LatestCoveringBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error) {
	if s.covering != nil {
		return *s.covering, nil
	}
	return billing.Job{}, sql.ErrNoRows
}
func (s *preflightJobsStore) CancelBillingJob(context.Context, string) error { return nil }

func TestBillingUserRangeReusesActiveDays(t *testing.T) {
	store := &preflightJobsStore{activeDays: map[string]string{"2026-08-02": "old-day"}}
	handler := BillingJobsHandler{Store: store}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-04 00:00:00","scope":"user","user_id":42}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 202 || len(store.createdSteps) != 2 || store.createdJob.TotalSteps != 2 {
		t.Fatalf("code=%d steps=%#v job=%+v body=%s", rec.Code, store.createdSteps, store.createdJob, rec.Body.String())
	}
	for _, step := range store.createdSteps {
		if step.From.In(billing.BusinessLocation).Format("2006-01-02") == "2026-08-02" {
			t.Fatal("active user-day was scheduled again")
		}
	}
}

func TestBillingUserRangeFullyCoveredReturnsExistingBill(t *testing.T) {
	existing := billing.Job{ID: "old-day", InstanceID: "demo", UserID: 42, Status: "complete"}
	store := &preflightJobsStore{activeDays: map[string]string{"2026-08-01": existing.ID}, jobsByID: map[string]billing.Job{existing.ID: existing}}
	handler := BillingJobsHandler{Store: store}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00","scope":"user","user_id":42}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 200 || store.created || !strings.Contains(rec.Body.String(), `"reused":true`) {
		t.Fatalf("code=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}
}

type preflightMetadataStore struct{ contexts map[string]int64 }

func (s preflightMetadataStore) UpsertBillingModels(context.Context, string, []string, time.Time, string) error {
	return nil
}
func (s preflightMetadataStore) ListBillingModelMetadata(_ context.Context, site string) ([]billing.ModelMetadata, error) {
	out := []billing.ModelMetadata{}
	for name, max := range s.contexts {
		out = append(out, billing.ModelMetadata{InstanceID: site, ModelName: name, MaxContextTokens: max})
	}
	return out, nil
}

type preflightModelSource struct{ models []string }

func (s preflightModelSource) RatioSnapshot(context.Context, string) (string, error) { return "", nil }
func (s preflightModelSource) ConfiguredModels(context.Context, string) ([]string, error) {
	return s.models, nil
}

// Generation only requires a successfully synchronized, non-empty model
// list. Context limits are optional; without one, the worker skips only the
// context-limit anomaly rule.
func TestBillingJobPreflightAllowsMissingModelContext(t *testing.T) {
	store := &preflightJobsStore{}
	handler := BillingJobsHandler{
		Store:     store,
		Preflight: preflightMetadataStore{contexts: map[string]int64{"glm-5.2": 128000}},
		Source:    preflightModelSource{models: []string{"glm-5.2", "glm-5-air"}},
	}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`
	req := httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 202 || !store.created {
		t.Fatalf("code = %d created=%v, want 202 with job created", rec.Code, store.created)
	}
}

func TestBillingJobPreflightBlocksEmptyModelList(t *testing.T) {
	store := &preflightJobsStore{}
	handler := BillingJobsHandler{Store: store, Preflight: preflightMetadataStore{}, Source: preflightModelSource{}}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "billing_models_missing") || store.created {
		t.Fatalf("code=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}
}

func TestBillingJobCreationBlocksWhileAnotherJobIsActive(t *testing.T) {
	active := billing.Job{ID: "active-job", Status: "running", TotalSteps: 10, CompletedSteps: 4}
	store := &preflightJobsStore{active: &active}
	handler := BillingJobsHandler{Store: store}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "billing_job_busy") || !strings.Contains(rec.Body.String(), "active-job") || store.created {
		t.Fatalf("code=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}
}

// Legacy channel-generation tasks still use the covered-range interlock.
func TestBillingJobCoveredGateYieldsToForce(t *testing.T) {
	covering := billing.Job{ID: "covering-job", Status: "complete"}
	store := &preflightJobsStore{covering: &covering}
	handler := BillingJobsHandler{Store: store}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(`{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00","scope":"channel"}`)))
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "billing_range_already_covered") || !strings.Contains(rec.Body.String(), "covering-job") || store.created {
		t.Fatalf("covered without force: code=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(`{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00","force":true}`)))
	if rec.Code != 202 || !store.created {
		t.Fatalf("force must bypass the covered gate: code=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}
}

func TestBillingWholeSiteUsesActiveDaysInsteadOfCompletedTaskRange(t *testing.T) {
	completed := billing.Job{ID: "old-range", InstanceID: "demo", Status: "complete"}
	store := &preflightJobsStore{covering: &completed, requestJob: &completed, activeDays: map[string]string{"2026-08-02": completed.ID}}
	handler := BillingJobsHandler{Store: store}
	body := `{"instance_id":"demo","from":"2026-08-01 00:00:00","to":"2026-08-04 00:00:00","scope":"all"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted || !store.created || len(store.createdSteps) != 2 || store.createdJob.TotalSteps != 2 || store.createdJob.RequestKey == billingRequestKey("demo", "all:0", store.createdJob.From, store.createdJob.To) {
		t.Fatalf("code=%d created=%v steps=%#v job=%+v body=%s", rec.Code, store.created, store.createdSteps, store.createdJob, rec.Body.String())
	}
}
