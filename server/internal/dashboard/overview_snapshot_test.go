package dashboard

import (
	"context"
	"controltower/internal/latencyhist"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOverviewUnboundedP95StillRaisesLatencyAlert(t *testing.T) {
	v2 := latencyhist.BucketsV2{}
	v2[len(v2)-1] = 100
	metric := aggregator.Metric{RequestCount: 100, LatencyBuckets: latencyhist.DeriveV1(v2), LatencyBucketsV2: &v2}
	values := defaultAlertSettings()
	values.P95Warn, values.P95Crit = 10, 80
	alerts := appendMetricAlertsWithSettings(nil, metric, values)
	if len(alerts) != 1 || alerts[0].Severity != "critical" || !strings.Contains(alerts[0].Summary, "≥90s") {
		t.Fatalf("unbounded P95 lost its alert: %+v", alerts)
	}
	values.P95Warn = 100
	if alerts := appendMetricAlertsWithSettings(nil, metric, values); len(alerts) != 0 {
		t.Fatalf("lower bound cannot prove a higher threshold: %+v", alerts)
	}
	metric.LatencyBucketsV2 = nil
	values.P95Warn, values.P95Crit = 10, 80
	alerts = appendMetricAlertsWithSettings(nil, metric, values)
	if len(alerts) != 1 || alerts[0].Severity != "warning" || !strings.Contains(alerts[0].Summary, "≥60s") {
		t.Fatalf("legacy lower bound lost: %+v", alerts)
	}
}

type overviewTestSource struct {
	fail       bool
	stale      bool
	site       string
	start, end time.Time
}

func (s *overviewTestSource) Recent1mMetrics() ([]aggregator.Metric, error) { return nil, nil }
func (s *overviewTestSource) OverviewCoverage(_ context.Context, site string, from, to time.Time) ([]string, []storage.OverviewCoverage, error) {
	s.site, s.start, s.end = site, from, to
	if s.fail {
		return nil, nil, errors.New("unavailable")
	}
	return []string{"collector"}, []storage.OverviewCoverage{{InstanceID: "collector", BucketTime: to.Add(-time.Minute), ConfirmedAt: to}}, nil
}
func (s *overviewTestSource) OverviewMetrics(context.Context, []string, time.Time, time.Time) ([]aggregator.Metric, error) {
	return nil, nil
}
func (s *overviewTestSource) OverviewRuntime(context.Context, string) ([]storage.OverviewInstance, error) {
	return []storage.OverviewInstance{}, nil
}
func (s *overviewTestSource) OverviewAlerts(context.Context, string, time.Time) (storage.OverviewAlertSnapshot, error) {
	at := time.Now().UTC()
	if s.stale {
		at = at.Add(-time.Hour)
	}
	return storage.OverviewAlertSnapshot{EvaluatedAt: &at, Total: 2, Items: []storage.Alert{{ID: "one"}, {ID: "two"}}}, nil
}
func TestOverviewSnapshotAPIClosedWindowAndFailure(t *testing.T) {
	source := &overviewTestSource{}
	handler := NewHandler(source)
	r := httptest.NewRecorder()
	handler.HandleOverviewSnapshot(r, httptest.NewRequest("GET", "/?site=chosen&section=requests&hours=24", nil))
	var value OverviewRequests
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &value) != nil {
		t.Fatalf("response %d %s", r.Code, r.Body.String())
	}
	if source.site != "chosen" || source.end.Second() != 0 || source.end.After(time.Now()) || source.end.Sub(source.start) != 24*time.Hour || len(value.Points) != 288 || value.StepSeconds != 300 || !value.Recent.Complete || value.Recent.Metrics.RequestCount != 0 {
		t.Fatalf("bad snapshot: %+v", value.Recent)
	}
	source.fail = true
	r = httptest.NewRecorder()
	handler.HandleOverviewSnapshot(r, httptest.NewRequest("GET", "/?site=chosen&section=requests&hours=1", nil))
	if r.Code != 503 {
		t.Fatalf("failure disguised as zero: %d", r.Code)
	}
	r = httptest.NewRecorder()
	handler.HandleOverviewSnapshot(r, httptest.NewRequest("GET", "/?site=chosen&section=runtime", nil))
	if r.Code != 200 {
		t.Fatal("request failure leaked into runtime")
	}
	source.stale = true
	r = httptest.NewRecorder()
	handler.HandleOverviewSnapshot(r, httptest.NewRequest("GET", "/?site=chosen&section=alerts", nil))
	var a struct {
		Fresh              bool
		Items              []AlertItem
		Unconfirmed, Total int
	}
	_ = json.Unmarshal(r.Body.Bytes(), &a)
	if r.Code != 200 || a.Fresh || len(a.Items) > 0 || a.Unconfirmed != 2 || a.Total != 0 {
		t.Fatalf("stale evaluation: %s", r.Body.String())
	}
}

func TestOverviewPointCoverageAndSiteIsolation(t *testing.T) {
	at := time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)
	ids := []string{"a", "b"}
	metrics := []aggregator.Metric{
		{InstanceID: "a", BucketTime: at, DimensionType: "instance", RequestCount: 9, SuccessCount: 9, TPM: 90},
		{InstanceID: "b", BucketTime: at, DimensionType: "instance", RequestCount: 1, ErrorCount: 1, TPM: 10},
		{InstanceID: "foreign", BucketTime: at, DimensionType: "instance", RequestCount: 999},
		{InstanceID: "a", BucketTime: at.Add(-time.Minute), DimensionType: "instance", RequestCount: 999},
	}
	coverage := []storage.OverviewCoverage{{InstanceID: "a", BucketTime: at, ConfirmedAt: at.Add(time.Minute)}}
	p := buildOverviewPoint(at, at.Add(time.Minute), ids, metrics, coverage)
	if p.Complete || p.Metrics.RequestCount != 10 || p.Metrics.SuccessRate != nil || p.ConfirmedAt != nil {
		t.Fatalf("partial source coverage: %+v %+v", p, p.Metrics)
	}
	coverage = append(coverage, storage.OverviewCoverage{InstanceID: "b", BucketTime: at, ConfirmedAt: at.Add(time.Minute)})
	// A retransmission of the same instance minute cannot multiply totals.
	metrics = append(metrics, metrics[0])
	p = buildOverviewPoint(at, at.Add(time.Minute), ids, metrics, coverage)
	if !p.Complete || p.Metrics.RequestCount != 10 || p.Metrics.SuccessRate == nil || *p.Metrics.SuccessRate != .9 {
		t.Fatalf("complete aggregation: %+v %+v", p, p.Metrics)
	}
	p = buildOverviewPoint(at, at.Add(time.Minute), ids, nil, coverage)
	if !p.Complete || p.Metrics == nil || p.Metrics.RequestCount != 0 || p.Metrics.SuccessRate != nil || p.Metrics.P95UseTime != nil {
		t.Fatalf("confirmed zero: %+v", p)
	}
	p = buildOverviewPoint(at, at.Add(time.Minute), ids, nil, nil)
	if p.Complete || p.Metrics != nil {
		t.Fatalf("missing is not zero: %+v", p)
	}
}

func TestOverviewFiveMinuteWeightedRatesAndGaps(t *testing.T) {
	at := time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)
	var ms []aggregator.Metric
	var cs []storage.OverviewCoverage
	for i := 0; i < 5; i++ {
		minute := at.Add(time.Duration(i) * time.Minute)
		n := int64(1)
		success := int64(1)
		if i == 4 {
			n = 96
			success = 0
		}
		ms = append(ms, aggregator.Metric{InstanceID: "a", BucketTime: minute, DimensionType: "instance", RequestCount: n, SuccessCount: success, ErrorCount: n - success})
		cs = append(cs, storage.OverviewCoverage{InstanceID: "a", BucketTime: minute})
	}
	p := buildOverviewPoint(at, at.Add(5*time.Minute), []string{"a"}, ms, cs)
	if !p.Complete || p.Metrics.RequestCount != 100 || *p.Metrics.SuccessRate != .04 {
		t.Fatalf("weighted rate: %+v", p.Metrics)
	}
	p = buildOverviewPoint(at, at.Add(5*time.Minute), []string{"a"}, ms, cs[1:])
	if p.Complete || p.Metrics.SuccessRate != nil {
		t.Fatal("partial five minute interval was marked complete")
	}
}

func TestOverviewSameMinuteAverageAndMergedQuantiles(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Minute)
	zero := 0.0
	huge := 120.0
	a := latencyhist.BucketsV2{}
	a[0] = 99
	b := latencyhist.BucketsV2{}
	b[13] = 1
	ms := []aggregator.Metric{
		{InstanceID: "a", BucketTime: at, DimensionType: "instance", RequestCount: 99, AvgUseTime: &zero, LatencyBuckets: latencyhist.DeriveV1(a), LatencyBucketsV2: &a, P95UseTime: &huge},
		{InstanceID: "b", BucketTime: at, DimensionType: "instance", RequestCount: 1, LatencyBuckets: latencyhist.DeriveV1(b), LatencyBucketsV2: &b, P95UseTime: &huge},
		{InstanceID: "c", BucketTime: at.Add(-time.Minute), DimensionType: "instance", RequestCount: 999},
	}
	p := BuildOverview(ms).Recent1m
	if p.RequestCount != 100 || p.AvgUseTime == nil || *p.AvgUseTime != 0 || p.P95UseTime == nil || math.Abs(*p.P95UseTime-.25*95/99) > .00001 {
		t.Fatalf("summary %+v", p)
	}
	b = latencyhist.BucketsV2{}
	b[14] = 100
	p = BuildOverview([]aggregator.Metric{{BucketTime: at, DimensionType: "instance", RequestCount: 100, LatencyBucketsV2: &b}}).Recent1m
	if p.P95UseTime == nil || *p.P95UseTime != 90 || !p.P95LowerBound {
		t.Fatalf("open-ended tail: %+v", p)
	}
}
