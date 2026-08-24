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

type billingVerificationHandlerStore struct {
	jobs       map[string]billing.Job
	latest     billing.Job
	sourceByID map[string]billing.Job
	items      []billing.VerificationResult
	summary    billing.VerificationSummary
	created    billing.Job
}

func (s *billingVerificationHandlerStore) ActiveBillingJob(context.Context) (billing.Job, error) {
	return billing.Job{}, sql.ErrNoRows
}

func (s *billingVerificationHandlerStore) BillingJob(_ context.Context, id string) (billing.Job, error) {
	if job, ok := s.jobs[id]; ok {
		return job, nil
	}
	return billing.Job{}, sql.ErrNoRows
}

func (s *billingVerificationHandlerStore) CreateBillingVerificationJob(_ context.Context, job billing.Job, _ []billing.JobStep, sourceJobID string) error {
	s.created = job
	if s.jobs == nil {
		s.jobs = map[string]billing.Job{}
	}
	s.jobs[job.ID] = job
	if s.sourceByID == nil {
		s.sourceByID = map[string]billing.Job{}
	}
	s.sourceByID[job.ID] = s.jobs[sourceJobID]
	return nil
}

func (s *billingVerificationHandlerStore) LatestBillingVerificationJob(context.Context, string) (billing.Job, error) {
	if s.latest.ID == "" {
		return billing.Job{}, sql.ErrNoRows
	}
	return s.latest, nil
}

func (s *billingVerificationHandlerStore) VerificationSourceJob(_ context.Context, id string) (billing.Job, error) {
	if job, ok := s.sourceByID[id]; ok {
		return job, nil
	}
	return billing.Job{}, sql.ErrNoRows
}

func (s *billingVerificationHandlerStore) BillingVerificationResults(context.Context, string, bool, int, int) ([]billing.VerificationResult, billing.VerificationSummary, int, error) {
	return s.items, s.summary, len(s.items), nil
}

func completedVerificationSource() billing.Job {
	return billing.Job{
		ID:         "source-job",
		InstanceID: "site-a",
		JobType:    "generate",
		Status:     "complete",
		From:       time.Date(2026, 7, 1, 0, 0, 0, 0, billing.BusinessLocation),
		To:         time.Date(2026, 8, 1, 0, 0, 0, 0, billing.BusinessLocation),
	}
}

func TestBillingVerificationCreateIsIdempotent(t *testing.T) {
	source := completedVerificationSource()
	existing := billing.Job{ID: "verify-job", JobType: "verify", Status: "running", InstanceID: source.InstanceID}
	store := &billingVerificationHandlerStore{
		jobs:       map[string]billing.Job{source.ID: source, existing.ID: existing},
		latest:     existing,
		sourceByID: map[string]billing.Job{existing.ID: source},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/billing/verification", strings.NewReader(`{"source_job_id":"source-job"}`))
	w := httptest.NewRecorder()
	BillingVerificationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reused":true`) || store.created.ID != "" {
		t.Fatalf("unexpected reuse response: code=%d body=%s created=%q", w.Code, w.Body.String(), store.created.ID)
	}

	store.latest = billing.Job{}
	req = httptest.NewRequest(http.MethodPost, "/api/dashboard/billing/verification", strings.NewReader(`{"source_job_id":"source-job"}`))
	w = httptest.NewRecorder()
	BillingVerificationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || store.created.JobType != "verify" || store.created.TotalSteps != 31*24 {
		t.Fatalf("unexpected create response: code=%d body=%s job=%+v", w.Code, w.Body.String(), store.created)
	}
}

func TestBillingVerificationProgressAndCompletedResults(t *testing.T) {
	source := completedVerificationSource()
	verify := billing.Job{ID: "verify-job", JobType: "verify", Status: "running", InstanceID: source.InstanceID, TotalSteps: 10, CompletedSteps: 4}
	store := &billingVerificationHandlerStore{
		jobs:       map[string]billing.Job{source.ID: source, verify.ID: verify},
		latest:     verify,
		sourceByID: map[string]billing.Job{verify.ID: source},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/billing/verification?source_job_id=source-job", nil)
	w := httptest.NewRecorder()
	BillingVerificationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"completed_steps":4`) || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("unexpected progress response: %d %s", w.Code, w.Body.String())
	}

	verify.Status = "complete"
	store.jobs[verify.ID] = verify
	store.latest = verify
	store.items = []billing.VerificationResult{{Day: "2026-07-01", UserID: 4, ModelName: "gpt-4o", Status: "mismatch"}}
	store.summary = billing.VerificationSummary{SourceRows: 10, MismatchedRows: 1}
	req = httptest.NewRequest(http.MethodGet, "/api/dashboard/billing/verification?source_job_id=source-job&page_size=1", nil)
	w = httptest.NewRecorder()
	BillingVerificationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mismatched_rows":1`) || !strings.Contains(w.Body.String(), `"model_name":"gpt-4o"`) {
		t.Fatalf("unexpected completed response: %d %s", w.Code, w.Body.String())
	}
}

func TestBillingVerificationRejectsUnfinishedSource(t *testing.T) {
	source := completedVerificationSource()
	source.Status = "running"
	store := &billingVerificationHandlerStore{jobs: map[string]billing.Job{source.ID: source}}
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/billing/verification", strings.NewReader(`{"source_job_id":"source-job"}`))
	w := httptest.NewRecorder()
	BillingVerificationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "billing_source_job_unavailable") {
		t.Fatalf("unexpected source validation response: %d %s", w.Code, w.Body.String())
	}
}
