package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

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
