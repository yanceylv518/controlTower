package dashboard

import (
	"controltower/internal/outputstats"
	"controltower/server/internal/aggregator"
	"testing"
)

func TestMonitorUsesTotalTimeIncludingRetriesAndNotLegacy(t *testing.T) {
	s := &outputstats.Stats{}
	s.Add(600, 15, 1, true)
	s.Add(400, 25, 2, true)
	metrics := []aggregator.Metric{{DimensionType: "instance_channel", DimensionKey: "i:channel:1", OTPSOutputTokens: 1000, OTPSDurationSecs: 10, OutputSpeed: s}}
	items := filterMetricItems(metrics, "instance_channel", "")
	if len(items) != 1 || items[0].OTPS == nil || *items[0].OTPS != 25 || items[0].OTPSSampleTokens != 1000 || items[0].OTPSDurationSecs != 40 {
		t.Fatalf("%+v", items)
	}
	metrics[0].OutputSpeed = nil
	items = filterMetricItems(metrics, "instance_channel", "")
	if items[0].OTPS != nil || items[0].OTPSSampleTokens != 0 {
		t.Fatal("old generation-time speed relabelled")
	}
}
