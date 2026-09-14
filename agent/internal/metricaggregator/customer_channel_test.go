package metricaggregator

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"
	"time"

	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
)

func TestCustomerChannelTokensRemainIndependent(t *testing.T) {
	now := time.Date(2026, 9, 14, 10, 0, 10, 0, time.UTC)
	metrics := Aggregate("site", []logcollector.Event{
		{CreatedAt: now, UserID: 7, ChannelID: 3, ModelName: "a", TotalTokens: 30},
		{CreatedAt: now, UserID: 7, ChannelID: 3, ModelName: "b", TotalTokens: 20},
		{CreatedAt: now, UserID: 7, ChannelID: 9, ModelName: "a", TotalTokens: 40},
		{CreatedAt: now, UserID: 8, ChannelID: 3, TotalTokens: 60},
		{CreatedAt: now, UserID: 7, ChannelID: 0, TotalTokens: 10},
		{CreatedAt: now, UserID: 0, ChannelID: 3, TotalTokens: 15},
	}, 512)
	want := map[string]int64{"site:user:7:channel:3": 50, "site:user:7:channel:9": 40, "site:user:8:channel:3": 60}
	for _, m := range metrics {
		if m.DimensionType != "instance_user_channel" {
			continue
		}
		if tokens, ok := want[m.DimensionKey]; !ok || m.TPM != tokens {
			t.Fatalf("unexpected user/channel metric: %#v", m)
		}
		delete(want, m.DimensionKey)
	}
	if len(want) != 0 {
		t.Fatalf("missing metrics: %v", want)
	}
}

// Reference retains the original complete aggregation path for both the
// pre-feature eight dimensions and the first implementation's nine dimensions.
func referenceCustomerAggregate(events []logcollector.Event, fullChannel bool) []reporter.AggregatedMetricPayload {
	accs := make(map[string]*accumulator)
	userCodes := map[int]bool{400: true, 413: true, 422: true, 424: true}
	for _, event := range events {
		bucket := event.CreatedAt.Truncate(time.Minute)
		dims := dimensionsFor("site", event)
		if fullChannel && event.UserID > 0 && event.ChannelID > 0 {
			dims = append(dims, dimension{"instance_user_channel", "site:user:" + strconv.FormatInt(event.UserID, 10) + ":channel:" + strconv.FormatInt(event.ChannelID, 10)})
		}
		for _, dim := range dims {
			key := bucket.Format(time.RFC3339) + "|" + dim.dimensionType + "|" + dim.dimensionKey
			acc := accs[key]
			if acc == nil {
				acc = &accumulator{metric: reporter.AggregatedMetricPayload{BucketTime: bucket, WindowSeconds: 60, DimensionType: dim.dimensionType, DimensionKey: dim.dimensionKey}}
				accs[key] = acc
			}
			acc.add(event, 512, userCodes)
		}
	}
	result := make([]reporter.AggregatedMetricPayload, 0, len(accs))
	for _, acc := range accs {
		result = append(result, acc.finalize())
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].BucketTime.Equal(result[j].BucketTime) {
			if result[i].DimensionType == result[j].DimensionType {
				return result[i].DimensionKey < result[j].DimensionKey
			}
			return result[i].DimensionType < result[j].DimensionType
		}
		return result[i].BucketTime.Before(result[j].BucketTime)
	})
	return result
}

func customerTrafficEvents(count, users int) []logcollector.Event {
	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	cache, ttft := int64(700), int64(125)
	result := make([]logcollector.Event, count)
	for i := range result {
		result[i] = logcollector.Event{CreatedAt: now.Add(time.Duration(i%60) * time.Second), UserID: int64(i%users + 1), ChannelID: int64((i/users)%8 + 1), ModelName: fmt.Sprintf("model-%d", i%5), LogType: "consume", TotalTokens: 1300, PromptTokens: 1000, CompletionTokens: 300, UseTime: float64(i%37+1) / 3, IsStream: true, FirstResponseMs: &ttft, CacheFieldPresent: true, CacheTokens: &cache}
	}
	return result
}

func TestCustomerChannelLeavesExistingMetricsUnchanged(t *testing.T) {
	events := customerTrafficEvents(1200, 25)
	events = append(events, logcollector.Event{CreatedAt: events[0].CreatedAt.Add(time.Minute), UserID: 7, ChannelID: 3, LogType: "error", ErrorSummary: "400 bad request"})
	want := referenceCustomerAggregate(events, false)
	got := Aggregate("site", events, 512)
	var existing []reporter.AggregatedMetricPayload
	for _, m := range got {
		if m.DimensionType != "instance_user_channel" {
			existing = append(existing, m)
			continue
		}
		onlyTokens := reporter.AggregatedMetricPayload{BucketTime: m.BucketTime, WindowSeconds: 60, DimensionType: m.DimensionType, DimensionKey: m.DimensionKey, TPM: m.TPM}
		if !reflect.DeepEqual(m, onlyTokens) {
			t.Fatalf("TPM-only metric contains unrelated data: %#v", m)
		}
	}
	if !reflect.DeepEqual(existing, want) {
		t.Fatal("existing metrics changed")
	}
	full := referenceCustomerAggregate(events, true)
	for i := range got {
		if got[i].DimensionType == "instance_user_channel" && (got[i].DimensionKey != full[i].DimensionKey || got[i].TPM != full[i].TPM) {
			t.Fatal("channel TPM changed")
		}
	}
}

func BenchmarkCustomerTraffic(b *testing.B) {
	for _, scenario := range []struct {
		name         string
		count, users int
	}{{"repeated", 1000, 20}, {"busy", 5000, 100}, {"sparse", 1000, 1000}} {
		events := customerTrafficEvents(scenario.count, scenario.users)
		for _, mode := range []string{"original8", "full9", "lean9"} {
			b.Run(scenario.name+"/"+mode, func(b *testing.B) {
				run := func() []reporter.AggregatedMetricPayload {
					if mode == "lean9" {
						return Aggregate("site", events, 512)
					}
					return referenceCustomerAggregate(events, mode == "full9")
				}
				sample := run()
				wire, err := json.Marshal(sample)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					run()
				}
				b.StopTimer()
				b.ReportMetric(float64(len(wire)), "wire-B/batch")
				b.ReportMetric(float64(len(sample)), "rows/batch")
			})
		}
	}
}

func TestCustomerTrafficBudgetPreservesOtherMetrics(t *testing.T) {
	for _, total := range []int{9999, 10000, 10001, 11000} {
		metrics := make([]reporter.AggregatedMetricPayload, total)
		var existing []reporter.AggregatedMetricPayload
		for i := range metrics {
			metrics[i] = reporter.AggregatedMetricPayload{DimensionType: "instance_user", DimensionKey: strconv.Itoa(i), TPM: int64(i)}
			if i%10 == 0 {
				metrics[i].DimensionType = "instance_user_channel"
			} else if i%10 == 1 {
				metrics[i].DimensionType = "channel_rate_second"
			}
			if metrics[i].DimensionType != "instance_user_channel" {
				existing = append(existing, metrics[i])
			}
		}
		before := append([]reporter.AggregatedMetricPayload(nil), metrics...)
		got, dropped := LimitCustomerTraffic(metrics, 10000)
		if total <= 10000 {
			if dropped != 0 || !reflect.DeepEqual(got, before) {
				t.Fatalf("changed report below limit: %d", total)
			}
		} else if dropped != total-len(existing) || !reflect.DeepEqual(got, existing) {
			t.Fatalf("changed existing metrics or kept partial breakdown: %d", total)
		}
	}
}
