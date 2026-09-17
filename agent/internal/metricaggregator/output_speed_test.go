package metricaggregator

import (
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
	"controltower/internal/outputstats"
	"encoding/json"
	"testing"
	"time"
)

func TestOutputSpeedRawLogsAcrossBatches(t *testing.T) {
	rows := []logcollector.Row{
		{Type: 2, CompletionTokens: 684, UseTime: 15, Other: `{"admin_info":{"use_channel":["7"]}}`},
		{Type: 2, IsStream: true, CompletionTokens: 100, UseTime: 5, Other: `{"frt":2000,"admin_info":{"use_channel":[7]}}`},
		{Type: 2, IsStream: true, CompletionTokens: 100, UseTime: 20, Other: `{"frt":12000,"admin_info":{"use_channel":[3,7]}}`},
		{Type: 2, CompletionTokens: 100, UseTime: 10, Other: `{"admin_info":{"use_channel":[7,7]}}`},
		{Type: 2, CompletionTokens: 16, UseTime: 10, Other: `{}`},
		{Type: 2, CompletionTokens: 0, UseTime: 50},
		{Type: 2, CompletionTokens: 100, UseTime: 0},
		{Type: 5, CompletionTokens: 100, UseTime: 10},
	}
	var got *outputstats.Stats
	for i, row := range rows {
		row.ID = int64(i + 1)
		row.ChannelID = 7
		row.ModelName = "m"
		row.UserID = 1
		row.CreatedAt = time.Unix(1800000000+int64(i*60), 0)
		e, ok, err := logcollector.ConvertRow(row)
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
		wire, err := json.Marshal(Aggregate("i", []logcollector.Event{e}, 512))
		if err != nil {
			t.Fatal(err)
		}
		var metrics []reporter.AggregatedMetricPayload
		if err = json.Unmarshal(wire, &metrics); err != nil {
			t.Fatal(err)
		}
		for _, m := range metrics {
			if m.DimensionType == "instance_user_channel" {
				continue
			}
			if !m.OutputSpeed.Valid(m.RequestCount, m.DimensionType == "instance_channel") {
				t.Fatalf("invalid %s %+v", m.DimensionType, m.OutputSpeed)
			}
			if m.DimensionType == "instance_channel" {
				got = outputstats.Merge(got, m.OutputSpeed)
			}
		}
	}
	if got.Tokens != 1000 || got.Seconds != 60 || got.Samples != 5 || got.DirectTokens != 784 || got.DirectSeconds != 20 || got.DirectSamples != 2 || got.RetrySamples != 2 || got.UnknownSamples != 1 {
		t.Fatalf("%+v", got)
	}
}
