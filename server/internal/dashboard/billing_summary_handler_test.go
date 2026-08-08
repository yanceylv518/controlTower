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

type fakeBillingSummaryStore struct {
	rows       []billing.AggregateRow
	missingJob bool
}

type fakeBillingLiveSource struct {
	ratio string
}

func (f fakeBillingLiveSource) RatioSnapshot(context.Context, string) (string, error) {
	if f.ratio != "" {
		return f.ratio, nil
	}
	return `{"QuotaPerUnit":"500000","general_setting":"{\"quota_display_type\":\"USD\"}"}`, nil
}

func (f fakeBillingLiveSource) CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error) {
	return nil, nil
}

func (f fakeBillingSummaryStore) QueryBillingAggregates(context.Context, string, time.Time, time.Time, []int64) ([]billing.AggregateRow, error) {
	return f.rows, nil
}
func (f fakeBillingSummaryStore) QueryBillingAggregatesForJob(context.Context, string, []int64) ([]billing.AggregateRow, error) {
	return f.rows, nil
}
func (f fakeBillingSummaryStore) LatestBillingJob(context.Context, string, string, time.Time, time.Time) (billing.Job, error) {
	if f.missingJob {
		return billing.Job{}, sql.ErrNoRows
	}
	return billing.Job{ID: "job-1", Status: "complete"}, nil
}
func (f fakeBillingSummaryStore) BillingJob(context.Context, string) (billing.Job, error) {
	if f.missingJob {
		return billing.Job{}, sql.ErrNoRows
	}
	return billing.Job{ID: "job-1", InstanceID: "site-a", JobType: "generate", Status: "complete"}, nil
}

func TestBillingSummaryDoesNotFallBackToAnotherInterval(t *testing.T) {
	billing.MonthlySummaryCache.InvalidateInstance("site-no-job")
	store := fakeBillingSummaryStore{missingJob: true, rows: []billing.AggregateRow{{UserID: 1, RequestCount: 99}}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/summary?instance_id=site-no-job&month=2026-08", nil)
	w := httptest.NewRecorder()
	BillingSummaryHandler{Store: store, Source: fakeBillingLiveSource{}}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) || !strings.Contains(w.Body.String(), `"request_count":0`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}
func (f fakeBillingSummaryStore) ListBillingPrices(context.Context, string) ([]billing.PriceRecord, error) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	return []billing.PriceRecord{{ModelName: "m", Price: billing.Price{EffectiveFrom: day, Input: "1", Output: "1", Cache: "1"}}}, nil
}
func (f fakeBillingSummaryStore) ListBillingGroupRatios(context.Context, string) ([]billing.GroupRatio, error) {
	return nil, nil
}
func (f fakeBillingSummaryStore) BillingRatioSnapshots(context.Context, string, time.Time, time.Time) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f fakeBillingSummaryStore) LatestBillingBalances(context.Context, string, time.Time, []int64) (map[int64]int64, error) {
	return map[int64]int64{}, nil
}

func TestBillingSummaryPaginationDoesNotChangeTotals(t *testing.T) {
	billing.MonthlySummaryCache.InvalidateInstance("site-a")
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	store := fakeBillingSummaryStore{rows: []billing.AggregateRow{
		{UserID: 1, Username: "a", ModelName: "m", Day: day, PromptTokens: 1_000_000, RequestCount: 1},
		{UserID: 2, Username: "b", ModelName: "m", Day: day, PromptTokens: 2_000_000, RequestCount: 2},
	}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/summary?instance_id=site-a&month=2026-08&page=2&page_size=1", nil)
	w := httptest.NewRecorder()
	BillingSummaryHandler{Store: store, Source: fakeBillingLiveSource{}}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"total":2`) || !strings.Contains(w.Body.String(), `"amount":"3.000000"`) || !strings.Contains(w.Body.String(), `"generation_job":{"id":"job-1"`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestBillingSummaryCSVIncludesAllRows(t *testing.T) {
	billing.MonthlySummaryCache.InvalidateInstance("site-a")
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	store := fakeBillingSummaryStore{rows: []billing.AggregateRow{
		{UserID: 1, Username: "alice", ModelName: "m", Day: day},
		{UserID: 2, Username: "bob", ModelName: "m", Day: day},
	}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/summary?instance_id=site-a&month=2026-08&format=csv&page_size=1", nil)
	w := httptest.NewRecorder()
	BillingSummaryHandler{Store: store, Source: fakeBillingLiveSource{}}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "alice") || !strings.Contains(w.Body.String(), "bob") {
		t.Fatalf("unexpected CSV: %d %q", w.Code, w.Body.String())
	}
}

func TestBillingSummaryUsesCurrentNewAPICurrency(t *testing.T) {
	billing.MonthlySummaryCache.InvalidateInstance("site-a")
	store := fakeBillingSummaryStore{}
	source := fakeBillingLiveSource{ratio: `{"QuotaPerUnit":"500000","USDExchangeRate":"7.2","general_setting":"{\"quota_display_type\":\"CNY\"}"}`}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/summary?instance_id=site-a&month=2026-08", nil)
	w := httptest.NewRecorder()
	BillingSummaryHandler{Store: store, Source: source}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"currency":{"type":"CNY","symbol":"¥","exchange_rate":"7.2"}`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

type failingBillingCurrencySource struct{}

func (failingBillingCurrencySource) RatioSnapshot(context.Context, string) (string, error) {
	return "", context.DeadlineExceeded
}

// A dead new-api connection must not take down historical bill viewing:
// currency degrades to the snapshot-derived default instead of a 500.
func TestBillingSummaryDegradesCurrencyWhenSourceUnavailable(t *testing.T) {
	billing.MonthlySummaryCache.InvalidateInstance("site-degrade")
	store := fakeBillingSummaryStore{rows: []billing.AggregateRow{{UserID: 1, RequestCount: 1}}}
	req := httptest.NewRequest("GET", "/api/dashboard/billing/summary?instance_id=site-degrade&month=2026-08", nil)
	w := httptest.NewRecorder()
	BillingSummaryHandler{Store: store, Source: failingBillingCurrencySource{}}.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"currency":{"type":"USD","symbol":"$","exchange_rate":"1"}`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}
