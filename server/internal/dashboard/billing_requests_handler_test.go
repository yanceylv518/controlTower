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

type billingRequestsSourceStub struct {
	logs   []billing.PagedLogRecord
	cursor billing.LogCursor
	limit  int
}

func (s *billingRequestsSourceStub) DetailedLogsPage(_ context.Context, _ string, _ int64, _, _ time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	s.cursor, s.limit = cursor, limit
	return s.logs, nil
}

func TestBillingRequestsReturnsBoundedRequestIDs(t *testing.T) {
	source := &billingRequestsSourceStub{logs: []billing.PagedLogRecord{
		{ID: 11, CreatedUnix: 1785513600, RequestID: "req-11", ModelName: "gpt-x", PromptTokens: sql.NullInt64{Int64: 10, Valid: true}, Quota: 20},
		{ID: 12, CreatedUnix: 1785513601, ModelName: "gpt-x"},
	}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/requests?instance_id=site-a&user_id=7&from=2026-08-01+00:00:00&to=2026-08-02+00:00:00&page_size=1&after_id=10&after_created=1785513599", nil)
	w := httptest.NewRecorder()
	BillingRequestsHandler{Store: fakeBillingSummaryStore{}, Source: source}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"request_id":"req-11"`) || !strings.Contains(w.Body.String(), `"has_more":true`) || !strings.Contains(w.Body.String(), `"next_after_id":11`) {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
	if source.cursor.ID != 10 || source.cursor.CreatedUnix != 1785513599 || source.limit != 2 {
		t.Fatalf("cursor=%#v limit=%d", source.cursor, source.limit)
	}
}

func TestBillingRequestsRequiresGeneratedBill(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/dashboard/billing/requests?instance_id=site-a&user_id=7&month=2026-08", nil)
	w := httptest.NewRecorder()
	BillingRequestsHandler{Store: fakeBillingSummaryStore{missingJob: true}, Source: &billingRequestsSourceStub{}}.ServeHTTP(w, req)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "billing_not_generated") {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
}
