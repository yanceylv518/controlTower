package dashboard

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestUpstreamMappingQueryNeverProjectsPlainKey(t *testing.T) {
	if strings.Count(upstreamChannelMappingsQuery, "`key`") != 2 {
		t.Fatalf("mapping SQL must use key exactly in SHA2 and RIGHT: %s", upstreamChannelMappingsQuery)
	}
	clean := strings.ReplaceAll(upstreamChannelMappingsQuery, "SHA2(CONCAT(COALESCE(base_url,''),'|',COALESCE(`key`,'')),256)", "")
	clean = strings.ReplaceAll(clean, "RIGHT(COALESCE(`key`,''),4)", "")
	if strings.Contains(strings.ToLower(clean), "key") {
		t.Fatalf("mapping SQL exposes key outside SHA2/RIGHT: %s", upstreamChannelMappingsQuery)
	}
}

type upstreamStoreStub struct {
	*fakeBillingChannelReadStore
	upstreams []billing.Upstream
}

func (s *upstreamStoreStub) ListBillingUpstreams(context.Context, string) ([]billing.Upstream, error) {
	return s.upstreams, nil
}
func (s *upstreamStoreStub) QueryBillingChannelAnomalies(context.Context, string, time.Time, time.Time) ([]billing.ChannelAnomalyRow, error) {
	return nil, nil
}
func (s *upstreamStoreStub) QueryBillingChannelRequestDetails(context.Context, string, int64, time.Time, time.Time) ([]billing.RequestDetail, error) {
	return nil, nil
}
func (s *upstreamStoreStub) QueryBillingChannelAnomalyOrders(context.Context, string, int64, time.Time, time.Time) ([]billing.AnomalyOrder, error) {
	return nil, nil
}
func (s *upstreamStoreStub) ActiveBillingChannelDailyFile(context.Context, string, time.Time, int64) (billing.ChannelDailyFile, error) {
	return billing.ChannelDailyFile{}, errors.New("not found")
}

func TestBillingUpstreamUsesManualSiteConfiguration(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, billing.BusinessLocation)
	base := &fakeBillingChannelReadStore{activeRows: []billing.AggregateRow{{UserID: 1, Day: from, ModelName: "m", RequestCount: 1}, {UserID: 2, Day: from, ModelName: "x", RequestCount: 2}}}
	store := &upstreamStoreStub{fakeBillingChannelReadStore: base, upstreams: []billing.Upstream{{ID: 9, InstanceID: "site-a", Name: "生产上游", Enabled: true, Channels: []billing.UpstreamChannel{{ChannelID: 1, ChannelName: "渠道 A"}}}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/upstream-channels?instance_id=site-a&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00&job_id=job", nil)
	BillingUpstreamHandler{Store: store}.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"upstream_fp":"9"`) || !strings.Contains(w.Body.String(), `"display_name":"生产上游"`) || !strings.Contains(w.Body.String(), `"unmapped_channels":1`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBillingUpstreamCSVHasTwoSections(t *testing.T) {
	w := httptest.NewRecorder()
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, billing.BusinessLocation)
	group := billing.UpstreamGroup{DisplayName: "https://up …1234", BaseURL: "https://up", Members: []billing.UpstreamMember{{ChannelID: 1, ChannelName: "渠道 A", ModelName: "m", Totals: billing.UpstreamTotals{Amount: "3.000000"}}}}
	writeUpstreamCSV(w, "billing-upstream.csv", from, from.AddDate(0, 1, 0), group, []upstreamDetailItem{{Day: "2026-07-01", ModelName: "m", RequestCount: 2, Amount: "3.000000"}})
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") || !strings.Contains(body, "上游,https://up …1234") || !strings.Contains(body, "成员渠道,渠道 A (#1)") || !strings.Contains(body, "日×模型明细") || !strings.Contains(body, "成员渠道小计") || !strings.Contains(body, "金额") {
		t.Fatalf("csv=%q", body)
	}
}

func TestBillingUpstreamDoesNotRequireLegacyChannelJob(t *testing.T) {
	store := &upstreamStoreStub{fakeBillingChannelReadStore: &fakeBillingChannelReadStore{jobs: map[string]billing.Job{}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/upstream-channels?instance_id=site-a&month=2026-07&job_id=missing", nil)
	BillingUpstreamHandler{Store: store}.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
