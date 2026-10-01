package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type savedReportsFake struct {
	billing.ReportStore
	docs []billing.ReportDocument
}

type reportRetryFake struct {
	billing.ReportStore
	billing.ReportCheckpointStore
	site, id string
	err      error
}

func (s *reportRetryFake) RetryReportTask(_ context.Context, site, id string) error {
	s.site, s.id = site, id
	return s.err
}
func TestReportRetryEndpoint(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{nil, 202, ""}, {billing.ErrReportBusy, 409, "report_task_busy"}, {errors.New("report_retry_obsolete"), 409, "report_retry_obsolete"},
	} {
		store := &reportRetryFake{err: tc.err}
		req := httptest.NewRequest("POST", "/api/dashboard/billing/report-tasks?action=retry", strings.NewReader(`{"id":"existing","instance_id":"site"}`))
		response := httptest.NewRecorder()
		ReportTasksHandler{Store: store}.ServeHTTP(response, req)
		if response.Code != tc.status || store.id != "existing" || store.site != "site" || !strings.Contains(response.Body.String(), tc.code) {
			t.Fatal(response.Code, response.Body.String(), store)
		}
	}
}

func (s savedReportsFake) ReadReportDays(context.Context, string, string, string) ([]billing.ReportDocument, error) {
	return s.docs, nil
}
func TestSavedReportReadCoverage(t *testing.T) {
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	zero := int64(0)
	for _, tc := range []struct {
		name      string
		docs      []billing.ReportDocument
		generated int
		failures  any
	}{
		{name: "missing"},
		{name: "generated empty", docs: []billing.ReportDocument{{From: day, To: day.AddDate(0, 0, 1), Items: []billing.ReportRow{}, FailedRequests: &zero, Currency: billing.CurrencyDisplay{Type: "CNY"}}}, generated: 1, failures: float64(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := SavedReportHandler{Store: savedReportsFake{docs: tc.docs}}
			r := httptest.NewRequest("GET", "/api/dashboard/billing/reports?instance_id=site&from=2025-01-01&to=2025-01-03", nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var result map[string]any
			if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
				t.Fatal(e)
			}
			if result["generated_days"] != float64(tc.generated) || result["expected_days"] != float64(2) || result["failed_requests"] != tc.failures {
				t.Fatalf("%v", result)
			}
		})
	}
}
func TestReportRange(t *testing.T) {
	for _, v := range []struct {
		from, to string
		ok       bool
	}{{"2025-01-01", "2025-02-01", true}, {"2025-01-01T00:01:00+08:00", "2025-01-02T00:00:00+08:00", false}, {"2025-01-01", "2025-03-01", false}, {"2025-01-02", "2025-01-01", false}} {
		_, _, ok := reportRange(v.from, v.to)
		if ok != v.ok {
			t.Fatalf("%+v", v)
		}
	}
}
