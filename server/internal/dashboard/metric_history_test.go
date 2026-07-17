package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type metricSourceStub struct {
	metrics []aggregator.Metric
	usage   []storage.UsageRow
	since   time.Time
}

func (s *metricSourceStub) QueryMetricHistoryPrefix(_ string, _ string, _ string, since time.Time) ([]aggregator.Metric, error) {
	s.since = since
	return s.metrics, nil
}

func (s *metricSourceStub) Recent1mMetrics() ([]aggregator.Metric, error) { return s.metrics, nil }
func (s *metricSourceStub) Recent5mMetrics() ([]aggregator.Metric, error) { return s.metrics, nil }
func (s *metricSourceStub) Latest1mMetrics(_ string) ([]aggregator.Metric, error) {
	return s.metrics, nil
}
func (s *metricSourceStub) Latest5mMetrics(_ string) ([]aggregator.Metric, error) {
	return s.metrics, nil
}
func (s *metricSourceStub) QueryMetricHistory(_ string, _ string, _ string, since time.Time) ([]aggregator.Metric, error) {
	s.since = since
	return s.metrics, nil
}
func (s *metricSourceStub) UsageSummary(since time.Time) ([]storage.UsageRow, error) {
	s.since = since
	return s.usage, nil
}

func TestMetricHistoryValidatesQuery(t *testing.T) {
	h := NewHandler(nil).WithMetricSource(&metricSourceStub{})
	response := httptest.NewRecorder()
	h.HandleMetricHistory(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?window=bad", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestMetricHistoryReturnsItemsWithQuantiles(t *testing.T) {
	now := time.Now().UTC()
	buckets := latencyhist.Buckets{1, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	source := &metricSourceStub{metrics: []aggregator.Metric{{BucketTime: now.Add(-time.Minute), DimensionType: "instance", DimensionKey: "inst", LatencyBuckets: buckets}, {BucketTime: now, DimensionType: "instance", DimensionKey: "inst"}}}
	h := NewHandler(nil).WithMetricSource(source)
	response := httptest.NewRecorder()
	h.HandleMetricHistory(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance&dimension_key=inst&hours=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if source.since.IsZero() || !strings.Contains(response.Body.String(), "p50_use_time") || !strings.Contains(response.Body.String(), "p99_use_time") {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestMetricHistoryAggregateReturnsRangeTotal(t *testing.T) {
	now := time.Now().UTC()
	source := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "inst", BucketTime: now.Add(-time.Minute), DimensionType: "channel", DimensionKey: "inst:channel:1", RequestCount: 7, ErrorCount: 1, PromptTokens: 10},
		{InstanceID: "inst", BucketTime: now, DimensionType: "channel", DimensionKey: "inst:channel:1", RequestCount: 5, ErrorCount: 2, PromptTokens: 20},
	}}
	h := NewHandler(nil).WithMetricSource(source)
	response := httptest.NewRecorder()
	h.HandleMetricHistory(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=channel&dimension_key=inst:channel:1&hours=1&aggregate=true", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"request_count":12`) || !strings.Contains(response.Body.String(), `"error_count":3`) || !strings.Contains(response.Body.String(), `"prompt_tokens":30`) {
		t.Fatalf("unexpected aggregate response: %s", response.Body.String())
	}
}

func TestMetricHistoryPrefixAggregateKeepsDimensionRowsSeparate(t *testing.T) {
	now := time.Now().UTC()
	source := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "inst", BucketTime: now.Add(-time.Minute), DimensionType: "instance_user_model", DimensionKey: "inst:user:9:model:a", RequestCount: 7},
		{InstanceID: "inst", BucketTime: now, DimensionType: "instance_user_model", DimensionKey: "inst:user:9:model:a", RequestCount: 5},
		{InstanceID: "inst", BucketTime: now, DimensionType: "instance_user_model", DimensionKey: "inst:user:9:model:b", RequestCount: 3},
	}}
	h := NewHandler(nil).WithMetricSource(source)
	response := httptest.NewRecorder()
	h.HandleMetricHistory(response, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance_user_model&dimension_key_prefix=inst:user:9:model:&hours=1&aggregate=true", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"dimension_key":"inst:user:9:model:a"`) || !strings.Contains(body, `"request_count":12`) || !strings.Contains(body, `"dimension_key":"inst:user:9:model:b"`) || !strings.Contains(body, `"request_count":3`) {
		t.Fatalf("unexpected prefix aggregate response: %s", body)
	}
}

func TestUsageValidatesHoursAndReturnsTotals(t *testing.T) {
	source := &metricSourceStub{usage: []storage.UsageRow{{DimensionType: "instance_user", DimensionKey: "inst:user:7", RequestCount: 2, PromptTokens: 3, CompletionTokens: 4, Quota: 5}}}
	h := NewHandler(nil).WithMetricSource(source)
	bad := httptest.NewRecorder()
	h.HandleUsage(bad, httptest.NewRequest(http.MethodGet, "/api/dashboard/usage?hours=0", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad status=%d", bad.Code)
	}
	ok := httptest.NewRecorder()
	h.HandleUsage(ok, httptest.NewRequest(http.MethodGet, "/api/dashboard/usage?hours=24", nil))
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"total_tokens":7`) {
		t.Fatalf("response=%s", ok.Body.String())
	}
}
