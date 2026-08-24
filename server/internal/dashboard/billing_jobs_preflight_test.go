package dashboard

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type preflightJobsStore struct {
	created bool
	active  *billing.Job
	jobs    []billing.Job
}

func (s *preflightJobsStore) CreateBillingJob(context.Context, billing.Job, []billing.JobStep) error {
	s.created = true
	return nil
}
func (s *preflightJobsStore) BillingJob(context.Context, string) (billing.Job, error) {
	return billing.Job{}, context.Canceled
}
func (s *preflightJobsStore) BillingJobByRequestKey(context.Context, string) (billing.Job, error) {
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
	return billing.Job{}, sql.ErrNoRows
}
func (s *preflightJobsStore) CancelBillingJob(context.Context, string) error { return nil }

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
