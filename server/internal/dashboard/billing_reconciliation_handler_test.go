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

type reconciliationHandlerStore struct {
	fakeBillingSummaryStore
	job       billing.Job
	anomalies []billing.AnomalyCount
}

func (s reconciliationHandlerStore) LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error) {
	if s.job.ID == "" {
		return billing.Job{}, sql.ErrNoRows
	}
	return s.job, nil
}

func (s reconciliationHandlerStore) BillingJob(context.Context, string) (billing.Job, error) {
	if s.job.ID == "" {
		return billing.Job{}, sql.ErrNoRows
	}
	return s.job, nil
}

func (s reconciliationHandlerStore) BillingAnomalyCountsForJob(context.Context, string) ([]billing.AnomalyCount, error) {
	return s.anomalies, nil
}

func reconciliationJob() billing.Job {
	return billing.Job{
		ID:         "job-r1",
		InstanceID: "site-a",
		JobType:    "generate",
		From:       time.Date(2026, 8, 1, 0, 0, 0, 0, billing.BusinessLocation),
		To:         time.Date(2026, 8, 2, 0, 0, 0, 0, billing.BusinessLocation),
		Status:     "complete",
	}
}

func TestBillingReconciliationRejectsMissingOrMismatchedJob(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/dashboard/billing/reconciliation?instance_id=site-a&from=2026-08-01+00:00:00&to=2026-08-02+00:00:00", nil)
	w := httptest.NewRecorder()
	BillingReconciliationHandler{Store: reconciliationHandlerStore{}}.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "billing_not_generated") {
		t.Fatalf("unexpected missing-job response: %d %s", w.Code, w.Body.String())
	}

	job := reconciliationJob()
	job.InstanceID = "another-site"
	req = httptest.NewRequest("GET", "/api/dashboard/billing/reconciliation?instance_id=site-a&from=2026-08-01+00:00:00&to=2026-08-02+00:00:00&job_id=job-r1", nil)
	w = httptest.NewRecorder()
	BillingReconciliationHandler{Store: reconciliationHandlerStore{job: job}}.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("mismatched job must be rejected: %d %s", w.Code, w.Body.String())
	}
}

func TestBillingReconciliationCSVHasExpectedColumns(t *testing.T) {
	job := reconciliationJob()
	store := reconciliationHandlerStore{job: job, fakeBillingSummaryStore: fakeBillingSummaryStore{rows: []billing.AggregateRow{{UserID: 1, Username: "alice", Day: job.From, ModelName: "m", GroupName: "default", RequestCount: 1}}}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/reconciliation?instance_id=site-a&from=2026-08-01+00:00:00&to=2026-08-02+00:00:00&job_id=job-r1&format=csv", nil)
	w := httptest.NewRecorder()
	BillingReconciliationHandler{Store: store}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "异常订单差额") || !strings.Contains(w.Body.String(), "缓存写策略差额") || !strings.Contains(w.Body.String(), "分类小计") {
		t.Fatalf("unexpected CSV: %d %q", w.Code, w.Body.String())
	}
}

type fullPageReconciliationSource struct{ calls int }

func (s *fullPageReconciliationSource) DetailedLogsPage(context.Context, string, int64, time.Time, time.Time, billing.LogCursor, int) ([]billing.PagedLogRecord, error) {
	s.calls++
	rows := make([]billing.PagedLogRecord, billing.BillingPageSize)
	for i := range rows {
		rows[i] = billing.PagedLogRecord{ID: int64(s.calls*billing.BillingPageSize + i), ModelName: "other"}
	}
	return rows, nil
}

func TestBillingReconciliationRequestsRequiresJobAndReportsTruncation(t *testing.T) {
	job := reconciliationJob()
	store := reconciliationHandlerStore{job: job}
	source := &fullPageReconciliationSource{}
	base := "/api/dashboard/billing/reconciliation/requests?instance_id=site-a&from=2026-08-01+00:00:00&to=2026-08-02+00:00:00&user_id=1&day=2026-08-01&model_name=m"

	req := httptest.NewRequest("GET", base, nil)
	w := httptest.NewRecorder()
	BillingReconciliationRequestsHandler{Store: store, Source: source}.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("job_id must be required: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest("GET", base+"&job_id=job-r1", nil)
	w = httptest.NewRecorder()
	BillingReconciliationRequestsHandler{Store: store, Source: source}.ServeHTTP(w, req)
	if w.Code != 200 || source.calls != 10 || !strings.Contains(w.Body.String(), `"scanned":20000`) || !strings.Contains(w.Body.String(), `"truncated":true`) {
		t.Fatalf("unexpected bounded scan: calls=%d code=%d body=%s", source.calls, w.Code, w.Body.String())
	}
}
