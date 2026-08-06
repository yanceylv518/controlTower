package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

type preflightJobsStore struct{ created bool }

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

// Generation must refuse with the missing-model list while any configured
// model lacks a context limit, and proceed once every model has one.
func TestBillingJobPreflightBlocksMissingModelContext(t *testing.T) {
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
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	var payload struct {
		Error  string   `json:"error"`
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "billing_model_context_missing" || len(payload.Models) != 1 || payload.Models[0] != "glm-5-air" {
		t.Fatalf("payload = %+v", payload)
	}
	if store.created {
		t.Fatal("job must not be created when context metadata is missing")
	}

	handler.Preflight = preflightMetadataStore{contexts: map[string]int64{"glm-5.2": 128000, "glm-5-air": 128000}}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("POST", "/api/dashboard/billing/jobs", strings.NewReader(body)))
	if rec.Code != 202 || !store.created {
		t.Fatalf("code = %d created=%v, want 202 with job created", rec.Code, store.created)
	}
}
