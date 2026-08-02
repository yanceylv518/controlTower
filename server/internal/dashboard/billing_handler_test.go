package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
)

type fakeBackfillRollupper struct{ days []string }

func (f *fakeBackfillRollupper) RollupDay(_ context.Context, site string, day time.Time) (billing.RollupResult, error) {
	f.days = append(f.days, day.Format("2006-01-02"))
	return billing.RollupResult{InstanceID: site, Day: day, Rows: 1}, nil
}

type fakeBackfillAudit struct{ values []storage.OperationAudit }

func (f *fakeBackfillAudit) InsertOperationAudit(v storage.OperationAudit) error {
	f.values = append(f.values, v)
	return nil
}

func TestBillingBackfillRunsSerialDaysWithRateLimitAndAudit(t *testing.T) {
	rollup, audit := &fakeBackfillRollupper{}, &fakeBackfillAudit{}
	var sleeps []time.Duration
	handler := BillingBackfillHandler{Rollup: rollup, Audit: audit, Sleep: func(d time.Duration) { sleeps = append(sleeps, d) }}
	req := httptest.NewRequest("POST", "/api/dashboard/billing/backfill", strings.NewReader(`{"instance_id":"cn","from":"2026-07-30","to":"2026-08-01"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Join(rollup.days, ",") != "2026-07-30,2026-07-31,2026-08-01" {
		t.Fatalf("days=%v", rollup.days)
	}
	if len(sleeps) != 2 || sleeps[0] != 500*time.Millisecond {
		t.Fatalf("sleeps=%v", sleeps)
	}
	if len(audit.values) != 1 || audit.values[0].OperationType != "billing.backfill" {
		t.Fatalf("audit=%#v", audit.values)
	}
}

func TestBillingBackfillRejectsOversizedRange(t *testing.T) {
	handler := BillingBackfillHandler{Rollup: &fakeBackfillRollupper{}}
	req := httptest.NewRequest("POST", "/api/dashboard/billing/backfill", strings.NewReader(`{"instance_id":"cn","from":"2025-01-01","to":"2026-08-01"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 400 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
