package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logcollector"
)

type rateSnapshotCollector struct {
	calls                   []string
	latest, last            int64
	events                  []logcollector.Event
	snapshotErr, collectErr error
}

func (c *rateSnapshotCollector) Backlog(context.Context, int64) (logcollector.BacklogStats, error) {
	c.calls = append(c.calls, "snapshot")
	return logcollector.BacklogStats{SourceLatestLogID: c.latest}, c.snapshotErr
}

func (c *rateSnapshotCollector) Collect(context.Context, int64, int) ([]logcollector.Event, int64, error) {
	c.calls = append(c.calls, "collect")
	// Reproduce new requests arriving while this page is being processed.
	c.latest += 2
	return c.events, c.last, c.collectErr
}

func TestRateCoverageUsesPreCollectionSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 4, 11, 16, 9, 0, time.UTC)
	for _, tc := range []struct {
		name                                  string
		latest, last, wantLatest, wantBacklog int64
		idle, snapshotFailed, disabled        bool
		wantMarker                            bool
	}{
		{name: "new traffic during collection", latest: 100, last: 100, wantLatest: 100, wantMarker: true},
		{name: "page includes newer traffic", latest: 100, last: 102, wantLatest: 102, wantMarker: true},
		{name: "real backlog", latest: 200, last: 100, wantLatest: 200, wantBacklog: 100},
		{name: "idle source", latest: 100, last: 100, wantLatest: 100, idle: true, wantMarker: true},
		{name: "empty source", idle: true, wantMarker: true},
		{name: "snapshot unavailable", last: 100, snapshotFailed: true},
		{name: "metrics only agent", latest: 100, last: 100, wantLatest: 100, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collector := &rateSnapshotCollector{latest: tc.latest, last: tc.last}
			if !tc.idle {
				collector.events = []logcollector.Event{{SourceLogID: tc.last, CreatedAt: now, ChannelID: 143, LogType: "consume", PromptTokens: 20, CompletionTokens: 10}}
			}
			if tc.snapshotFailed {
				collector.snapshotErr = errors.New("snapshot unavailable")
			}
			events, last, stats, err := collectWithRateSnapshot(context.Background(), collector, 0, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(collector.calls, []string{"snapshot", "collect"}) {
				t.Fatalf("query order/count = %v", collector.calls)
			}
			if last != tc.last || !reflect.DeepEqual(events, collector.events) {
				t.Fatal("collection results lost")
			}
			if stats.SourceLatestLogID != tc.wantLatest || stats.BacklogEstimate != tc.wantBacklog || stats.SnapshotKnown == tc.snapshotFailed {
				t.Fatalf("unexpected snapshot: %+v", stats)
			}
			report := buildReport(context.Background(), config.Config{AgentID: "test", LogCollectEnabled: !tc.disabled}, now, 1, last, stats, events, nil, nil, nil, nil)
			marker, counters := false, 0
			for _, metric := range report.AggregatedMetrics {
				if metric.DimensionType != "channel_rate_second" {
					continue
				}
				if metric.DimensionKey == "0" {
					marker = true
					if !metric.BucketTime.Equal(now) {
						t.Fatalf("marker timestamp = %s", metric.BucketTime)
					}
				} else {
					counters++
					if metric.RequestCount != 1 || metric.TPM != 30 {
						t.Fatalf("counter changed: %+v", metric)
					}
				}
			}
			if marker != tc.wantMarker {
				t.Fatalf("marker = %v, want %v", marker, tc.wantMarker)
			}
			if (!tc.idle && counters != 1) || (tc.idle && counters != 0) {
				t.Fatalf("real counters = %d", counters)
			}
		})
	}
}

func TestRateSnapshotCollectionFailureDoesNotAdvanceCursor(t *testing.T) {
	wantErr := errors.New("collection failed")
	collector := &rateSnapshotCollector{latest: 100, last: 100, collectErr: wantErr}
	events, last, stats, err := collectWithRateSnapshot(context.Background(), collector, 90, 1000)
	if !errors.Is(err, wantErr) || events != nil || last != 90 || stats.SnapshotKnown {
		t.Fatalf("events=%v last=%d stats=%+v err=%v", events, last, stats, err)
	}
}
