package dashboard

import (
	"controltower/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type nginxStoreStub struct {
	b              []storage.NginxTimingBucket
	s              []storage.NginxSlowSample
	d              []storage.RequestDimension
	q              storage.NginxTimingQuery
	slowQ          storage.NginxSlowSampleQuery
	dimensionCalls int
}

func (n *nginxStoreStub) QueryNginxTiming(q storage.NginxTimingQuery) ([]storage.NginxTimingBucket, error) {
	n.q = q
	return n.b, nil
}
func (n *nginxStoreStub) QueryNginxSlowSamples(q storage.NginxSlowSampleQuery) ([]storage.NginxSlowSample, error) {
	n.slowQ = q
	return n.s, nil
}
func (n *nginxStoreStub) QueryRequestDimensions(string, []string) ([]storage.RequestDimension, error) {
	n.dimensionCalls++
	return n.d, nil
}

func TestNginxSlowSamplesCorrelatesInOneBatchAndFilters(t *testing.T) {
	now := time.Now().UTC()
	s := &nginxStoreStub{s: []storage.NginxSlowSample{{ID: 1, InstanceID: "i", OccurredAt: now, RequestID: "req-1"}, {ID: 2, InstanceID: "i", OccurredAt: now, RequestID: "req-2"}, {ID: 3, InstanceID: "i", OccurredAt: now}}, d: []storage.RequestDimension{{Source: "sample", InstanceID: "i", RequestID: "req-1", SourceLogID: 8, UserID: 7, Username: "alice", ChannelID: 9, ModelName: "model-a", TokenName: "token-a"}, {Source: "event", InstanceID: "i", RequestID: "req-1", SourceLogID: 8, UserID: 7}, {Source: "sample", InstanceID: "i", RequestID: "req-2", SourceLogID: 10}, {Source: "event", InstanceID: "i", RequestID: "req-2", SourceLogID: 11}}}
	h := NewHandler(nil).WithNginxTimingStore(s)
	rr := httptest.NewRecorder()
	h.HandleNginxSlowSamples(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing/slow-samples?instance_id=i&user_id=7&match_status=matched", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"match_status":"matched"`) || !strings.Contains(rr.Body.String(), `"model_name":"model-a"`) || strings.Contains(rr.Body.String(), `req-2`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if s.dimensionCalls != 1 {
		t.Fatalf("dimension calls=%d", s.dimensionCalls)
	}
}

func TestNginxSlowSamplesRejectsInvalidFilters(t *testing.T) {
	h := NewHandler(nil).WithNginxTimingStore(&nginxStoreStub{})
	for _, q := range []string{"user_id=x", "channel_id=0", "match_status=other"} {
		rr := httptest.NewRecorder()
		h.HandleNginxSlowSamples(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing/slow-samples?instance_id=i&"+q, nil))
		if rr.Code != 400 {
			t.Fatalf("query=%s status=%d", q, rr.Code)
		}
	}
}

func TestNginxSlowSamplesAppliesOffsetAfterCorrelation(t *testing.T) {
	now := time.Now().UTC()
	s := &nginxStoreStub{s: []storage.NginxSlowSample{
		{ID: 1, InstanceID: "i", OccurredAt: now, RequestID: "req-1"},
		{ID: 2, InstanceID: "i", OccurredAt: now, RequestID: "req-2"},
	}, d: []storage.RequestDimension{
		{Source: "sample", InstanceID: "i", RequestID: "req-1", UserID: 7},
		{Source: "sample", InstanceID: "i", RequestID: "req-2", UserID: 7},
	}}
	h := NewHandler(nil).WithNginxTimingStore(s)
	rr := httptest.NewRecorder()
	h.HandleNginxSlowSamples(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing/slow-samples?instance_id=i&user_id=7&limit=1&offset=1", nil))
	if rr.Code != 200 || strings.Contains(rr.Body.String(), `req-1`) || !strings.Contains(rr.Body.String(), `req-2`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if s.slowQ.Limit != 200 {
		t.Fatalf("raw limit=%d", s.slowQ.Limit)
	}
}
func TestNginxTimingSummaryAndFiltering(t *testing.T) {
	now := time.Now().UTC()
	s := &nginxStoreStub{b: []storage.NginxTimingBucket{{InstanceID: "i", BucketAt: now, RequestCount: 10, Status5xx: 2, Status504: 1, SlowCount: 4, SlowTTFTCount: 3, SlowTransferCount: 1}}}
	h := NewHandler(nil).WithNginxTimingStore(s)
	rr := httptest.NewRecorder()
	h.HandleNginxTiming(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing?instance_id=i&hours=6", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"slow_ttft_percent":75`) {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if time.Since(s.q.Since) < 5*time.Hour {
		t.Fatalf("since=%v", s.q.Since)
	}
}
func TestNginxTimingValidatesBoundsAndEmpty(t *testing.T) {
	h := NewHandler(nil).WithNginxTimingStore(&nginxStoreStub{})
	bad := httptest.NewRecorder()
	h.HandleNginxTiming(bad, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing?instance_id=i&hours=169", nil))
	if bad.Code != 400 {
		t.Fatalf("bad=%d", bad.Code)
	}
	ok := httptest.NewRecorder()
	h.HandleNginxSlowSamples(ok, httptest.NewRequest(http.MethodGet, "/api/dashboard/nginx-timing/slow-samples?instance_id=i", nil))
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"items":[]`) {
		t.Fatalf("body=%s", ok.Body.String())
	}
}
