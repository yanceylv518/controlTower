package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
	"controltower/internal/speedstats"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Exercise raw source metadata and the actual report JSON together. Small
// collection batches must not impose a per-batch minimum or lose evidence.
func TestSpeedCountsFromRawRowsAcrossReportBatches(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		other                        string
		stream                       bool
		output                       int64
		direct, retry, unknown, ttft int64
	}{
		{"direct", `{"frt":1000,"admin_info":{"use_channel":["255"]}}`, true, 100, 530, 0, 0, 530},
		{"cross_channel_retry", `{"frt":1000,"admin_info":{"use_channel":["141","255"]}}`, true, 100, 0, 530, 0, 530},
		{"same_channel_retry", `{"frt":1000,"admin_info":{"use_channel":["255","255"]}}`, true, 100, 0, 530, 0, 530},
		{"missing_route", `{"frt":1000}`, true, 100, 0, 0, 530, 530},
		{"route_mismatch", `{"frt":1000,"admin_info":{"use_channel":["141"]}}`, true, 100, 0, 0, 530, 530},
		{"no_output", `{"frt":1000,"admin_info":{"use_channel":["255"]}}`, true, 0, 0, 0, 530, 530},
		{"not_streaming", `{"frt":1000,"admin_info":{"use_channel":["255"]}}`, false, 100, 0, 0, 0, 0},
		{"missing_ttft", `{"admin_info":{"use_channel":["255"]}}`, true, 100, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var merged *speedstats.Stats
			var requests, ttft int64
			for i := 0; i < 530; i++ {
				e, ok, err := logcollector.ConvertRow(logcollector.Row{ID: int64(i + 1), Type: 2, ChannelID: 255, CreatedAt: time.Unix(1800000000+int64(i%60), 0), IsStream: tc.stream, CompletionTokens: tc.output, Other: tc.other})
				if err != nil || !ok {
					t.Fatalf("convert: %v %v", ok, err)
				}
				report := reporter.AgentReportRequest{AggregatedMetrics: Aggregate("i", []logcollector.Event{e}, 512)}
				wire, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				var decoded reporter.AgentReportRequest
				if err := json.Unmarshal(wire, &decoded); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, m := range decoded.AggregatedMetrics {
					if m.DimensionType != "instance_channel" {
						continue
					}
					found = true
					if m.TTFTCount == nil || !m.SpeedTTFT.Valid(*m.TTFTCount) {
						t.Fatalf("invalid report evidence: %+v", m)
					}
					requests += m.RequestCount
					ttft += *m.TTFTCount
					merged = speedstats.Merge(merged, m.SpeedTTFT)
				}
				if !found {
					t.Fatal("channel missing from report")
				}
			}
			if requests != 530 || ttft != tc.ttft || merged.Samples() != tc.direct || merged.RetryCount != tc.retry || merged.UnknownCount != tc.unknown {
				t.Fatalf("requests=%d ttft=%d stats=%+v", requests, ttft, merged)
			}
		})
	}
}

func TestSpeedFilterLeavesMonitoringUnchanged(t *testing.T) {
	ms := int64(11000)
	events := []logcollector.Event{}
	for _, n := range []int{1, 2, 2, 0} {
		events = append(events, logcollector.Event{
			CreatedAt: time.Unix(1800000000, 0), ChannelID: 7, UserID: 4, ModelName: "m", LogType: "consume", IsStream: true,
			FirstResponseMs: &ms, UseTime: 16, CompletionTokens: 500, PromptTokens: 1000, TotalTokens: 1500, AttemptCount: n,
		})
	}
	got := Aggregate("i", events, 512)
	for i := range events {
		events[i].AttemptCount = 1
	}
	allDirect := Aggregate("i", events, 512)
	for i := range got {
		if got[i].DimensionType == "instance_channel" {
			s := got[i].SpeedTTFT
			if s == nil || s.Samples() != 1 || s.RetryCount != 2 || s.UnknownCount != 1 {
				t.Fatalf("stats %+v", s)
			}
			if *got[i].TTFTCount != 4 || got[i].OTPSOutputTokens != 2000 || got[i].OTPSDurationSecs != 20 {
				t.Fatal("public metrics changed")
			}
		} else if got[i].SpeedTTFT != nil {
			t.Fatal("speed evidence duplicated on other dimensions")
		}
		got[i].SpeedTTFT = nil
		allDirect[i].SpeedTTFT = nil
	}
	if !reflect.DeepEqual(got, allDirect) {
		t.Fatal("attempt metadata affected public aggregation")
	}
}

func BenchmarkSpeedEvidenceAggregation(b *testing.B) {
	ms := int64(11000)
	events := make([]logcollector.Event, 1000)
	for i := range events {
		events[i] = logcollector.Event{CreatedAt: time.Unix(1800000000, 0), ChannelID: int64(i%20 + 1), LogType: "consume", IsStream: true, FirstResponseMs: &ms, UseTime: 16, CompletionTokens: 500, AttemptCount: 1}
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Aggregate("i", events, 512)
	}
}

func TestSpeedPayloadSizeIsBounded(t *testing.T) {
	ms := int64(1000)
	items := Aggregate("i", []logcollector.Event{{CreatedAt: time.Now(), ChannelID: 1, LogType: "consume", IsStream: true, CompletionTokens: 1, FirstResponseMs: &ms, AttemptCount: 1}}, 512)
	for _, m := range items {
		if m.SpeedTTFT != nil {
			with, _ := json.Marshal(m)
			m.SpeedTTFT = nil
			without, _ := json.Marshal(m)
			if len(with)-len(without) > 200 {
				t.Fatal("unexpected payload growth")
			}
			t.Logf("additional channel payload bytes: %d", len(with)-len(without))
		}
	}
}
