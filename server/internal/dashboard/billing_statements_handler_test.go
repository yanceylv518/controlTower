package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"controltower/server/internal/billing"
)

type statementStoreStub struct {
	job      billing.Job
	upstream billing.Upstream
	err      error
}

func (s *statementStoreStub) CreateBillingStatementJob(_ context.Context, j billing.Job, _ []billing.JobStep, _ string) error {
	s.job = j
	return s.err
}
func (s *statementStoreStub) BillingStatementUpstream(context.Context, string, int64) (billing.Upstream, error) {
	return s.upstream, nil
}

func TestBillingStatementsHandlerCreatesSingleUserStatement(t *testing.T) {
	s := &statementStoreStub{}
	h := BillingStatementsHandler{Store: s}
	r := httptest.NewRequest("POST", "/api/dashboard/billing/statements", strings.NewReader(`{"instance_id":"site-a","statement_type":"user","user_id":7,"from":"2026-08-01 00:00:00","to":"2026-08-03 00:00:00"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || s.job.JobType != "user_statement" || s.job.UserID != 7 || s.job.TotalSteps != 2 || s.job.RequestKey == "" {
		t.Fatalf("code=%d job=%+v body=%s", w.Code, s.job, w.Body.String())
	}
}
func TestBillingStatementsHandlerRequiresBoundUpstream(t *testing.T) {
	s := &statementStoreStub{upstream: billing.Upstream{ID: 3, InstanceID: "site-a", Name: "u", Enabled: true}}
	h := BillingStatementsHandler{Store: s}
	r := httptest.NewRequest("POST", "/api/dashboard/billing/statements", strings.NewReader(`{"instance_id":"site-a","statement_type":"upstream","upstream_id":3,"from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
func TestBillingStatementsHandlerMapsDuplicate(t *testing.T) {
	s := &statementStoreStub{err: billing.ErrStatementDuplicate}
	h := BillingStatementsHandler{Store: s}
	r := httptest.NewRequest("POST", "/api/dashboard/billing/statements", strings.NewReader(`{"instance_id":"site-a","statement_type":"user","user_id":7,"from":"2026-08-01 00:00:00","to":"2026-08-02 00:00:00"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "billing_statement_duplicate") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
