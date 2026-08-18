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
	mappings []billing.UpstreamChannelMapping
}

func (s *upstreamStoreStub) UpsertBillingUpstreamChannels(_ context.Context, items []billing.UpstreamChannelMapping) error {
	byID := map[int64]billing.UpstreamChannelMapping{}
	for _, v := range s.mappings {
		byID[v.ChannelID] = v
	}
	for _, v := range items {
		byID[v.ChannelID] = v
	}
	s.mappings = nil
	for _, v := range byID {
		s.mappings = append(s.mappings, v)
	}
	return nil
}
func (s *upstreamStoreStub) ListBillingUpstreamChannels(context.Context, string) ([]billing.UpstreamChannelMapping, error) {
	return s.mappings, nil
}

type upstreamSourceStub struct {
	items []billing.UpstreamChannelMapping
	err   error
}

func (s upstreamSourceStub) UpstreamChannelMappings(context.Context, string) ([]billing.UpstreamChannelMapping, error) {
	return s.items, s.err
}

func TestBillingUpstreamRefreshFailureFallsBackToSnapshot(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	base := &fakeBillingChannelReadStore{jobs: map[string]billing.Job{"job": channelReadJob("job", "complete", from, to)}, rowsByJob: map[string][]billing.AggregateRow{"job": {{UserID: 1, Day: from, ModelName: "m", RequestCount: 1}}}}
	store := &upstreamStoreStub{fakeBillingChannelReadStore: base, mappings: []billing.UpstreamChannelMapping{{ChannelID: 1, UpstreamFP: "saved", BaseURL: "saved", KeyTail: "1234"}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/upstream-channels?instance_id=site-a&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00&job_id=job", nil)
	BillingUpstreamHandler{Store: store, Source: upstreamSourceStub{err: errors.New("offline")}}.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"upstream_fp":"saved"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBillingUpstreamRefreshRetainsDeletedSnapshotAndGroupsRows(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, billing.BusinessLocation)
	to := from.AddDate(0, 1, 0)
	base := &fakeBillingChannelReadStore{jobs: map[string]billing.Job{"job": channelReadJob("job", "complete", from, to)}, rowsByJob: map[string][]billing.AggregateRow{"job": {{UserID: 1, Username: "new", Day: from, ModelName: "m", RequestCount: 2}, {UserID: 2, Username: "deleted", Day: from, ModelName: "x", RequestCount: 3}}}}
	store := &upstreamStoreStub{fakeBillingChannelReadStore: base, mappings: []billing.UpstreamChannelMapping{{ChannelID: 1, UpstreamFP: "before", BaseURL: "before", KeyTail: "9999"}, {ChannelID: 2, UpstreamFP: "old", BaseURL: "old", KeyTail: "0002"}}}
	source := upstreamSourceStub{items: []billing.UpstreamChannelMapping{{ChannelID: 1, UpstreamFP: "new", BaseURL: "new", KeyTail: "0001"}, {ChannelID: 3, UpstreamFP: "inserted", BaseURL: "inserted", KeyTail: "0003"}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/upstream-channels?instance_id=site-a&from=2026-07-01+00%3A00%3A00&to=2026-08-01+00%3A00%3A00&job_id=job", nil)
	BillingUpstreamHandler{Store: store, Source: source}.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"upstream_fp":"new"`) || !strings.Contains(w.Body.String(), `"upstream_fp":"old"`) || !strings.Contains(w.Body.String(), `"request_count":2`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(store.mappings) != 3 {
		t.Fatalf("mappings=%#v", store.mappings)
	}
	inserted := false
	for _, mapping := range store.mappings {
		if mapping.ChannelID == 1 && mapping.UpstreamFP != "new" {
			t.Fatalf("changed key did not update fingerprint: %#v", mapping)
		}
		if mapping.ChannelID == 3 && mapping.UpstreamFP == "inserted" {
			inserted = true
		}
	}
	if !inserted {
		t.Fatalf("new channel snapshot was not inserted: %#v", store.mappings)
	}
}

func TestBillingUpstreamCSVHasTwoSections(t *testing.T) {
	w := httptest.NewRecorder()
	writeUpstreamCSV(w, "billing-upstream.csv", []upstreamDetailItem{{Day: "2026-07-01", ModelName: "m", RequestCount: 2}}, []billing.UpstreamMember{{ChannelID: 1, ChannelName: "c", ModelName: "m"}})
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") || !strings.Contains(body, "日×模型明细") || !strings.Contains(body, "成员渠道小计") {
		t.Fatalf("csv=%q", body)
	}
}

func TestBillingUpstreamUsesChannelJobReadConflict(t *testing.T) {
	store := &upstreamStoreStub{fakeBillingChannelReadStore: &fakeBillingChannelReadStore{jobs: map[string]billing.Job{}}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/dashboard/billing/upstream-channels?instance_id=site-a&month=2026-07&job_id=missing", nil)
	BillingUpstreamHandler{Store: store}.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "billing_not_generated") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
