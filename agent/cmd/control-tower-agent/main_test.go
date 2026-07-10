package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/localbuffer"
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/reporter"
)

func TestRunCollectorLoopRunOnceReturnsCollectError(t *testing.T) {
	expectedErr := errors.New("collect failed")
	calls := 0
	err := runCollectorLoop(context.Background(), config.Config{
		RunOnce:                true,
		LogPollIntervalSeconds: 10,
		LogQueryTimeoutSeconds: 1,
		ReportTimeoutSeconds:   1,
	}, func(context.Context) error {
		calls++
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected collect error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("collect calls = %d", calls)
	}
}

func TestRunCollectorLoopStopsAfterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := runCollectorLoop(ctx, config.Config{
		RunOnce:                false,
		LogPollIntervalSeconds: 60,
		LogQueryTimeoutSeconds: 1,
		ReportTimeoutSeconds:   1,
	}, func(context.Context) error {
		calls++
		cancel()
		return nil
	})
	if err != nil {
		t.Fatalf("run loop error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("collect calls = %d", calls)
	}
}

func TestCollectPassTimeoutCombinesQueryAndReportTimeouts(t *testing.T) {
	timeout := collectPassTimeout(config.Config{
		LogQueryTimeoutSeconds: 4,
		ReportTimeoutSeconds:   6,
	})
	if timeout != 10*time.Second {
		t.Fatalf("timeout = %s", timeout)
	}
}

func TestFlushBufferedReportsSendsAndDropsEntries(t *testing.T) {
	store := localbuffer.NewFileStore(filepath.Join(t.TempDir(), "buffer.json"))
	if err := store.Append(bufferEntry(10), 10); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := store.Append(bufferEntry(15), 10); err != nil {
		t.Fatalf("append second: %v", err)
	}

	client := &fakeReporter{}
	lastLogID, flushed, err := flushBufferedReports(context.Background(), client, store)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if !flushed || lastLogID != 15 {
		t.Fatalf("unexpected flush result: flushed=%v last=%d", flushed, lastLogID)
	}
	if len(client.reports) != 2 {
		t.Fatalf("reports sent = %d", len(client.reports))
	}
	entries, err := store.Load()
	if err != nil {
		t.Fatalf("load buffer: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("buffer should be empty: %#v", entries)
	}
}

func TestFlushBufferedReportsRetainsEntryOnFailure(t *testing.T) {
	store := localbuffer.NewFileStore(filepath.Join(t.TempDir(), "buffer.json"))
	if err := store.Append(bufferEntry(10), 10); err != nil {
		t.Fatalf("append: %v", err)
	}
	expectedErr := errors.New("report failed")
	client := &fakeReporter{reportErr: expectedErr}
	lastLogID, flushed, err := flushBufferedReports(context.Background(), client, store)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected report error, got %v", err)
	}
	if flushed || lastLogID != 0 {
		t.Fatalf("unexpected flush result: flushed=%v last=%d", flushed, lastLogID)
	}
	entries, err := store.Load()
	if err != nil {
		t.Fatalf("load buffer: %v", err)
	}
	if len(entries) != 1 || entries[0].LastLogID != 10 {
		t.Fatalf("buffer entry should remain: %#v", entries)
	}
}

func TestBuildReportIncludesBacklogTelemetry(t *testing.T) {
	report := buildReport(context.Background(), config.Config{InstanceID: "inst-1", AgentID: "agent-1"}, time.Now().UTC(), 1, 100, logcollector.BacklogStats{SourceLatestLogID: 4500, BacklogEstimate: 4400}, nil, nil, nil, nil, nil)
	if report.SourceLatestLogID != 4500 || report.BacklogEstimate != 4400 {
		t.Fatalf("unexpected backlog telemetry: %#v", report)
	}
}

func TestToPayloadsPreservesLogEventFields(t *testing.T) {
	cacheTokens := int64(128)
	createdAt := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	payloads := toPayloads([]logcollector.Event{{
		SourceLogID:       101,
		CreatedAt:         createdAt,
		LogType:           "consume",
		UserID:            7,
		Username:          "alice",
		ChannelID:         18,
		ModelName:         "gpt-4o",
		TokenID:           9,
		TokenName:         "prod-token",
		PromptTokens:      30,
		CompletionTokens:  70,
		TotalTokens:       100,
		Quota:             500,
		UseTime:           3.2,
		IsStream:          true,
		Group:             "default",
		RequestID:         "req-1",
		UpstreamRequestID: "up-1",
		ErrorSummary:      "",
		CacheTokens:       &cacheTokens,
		CacheFieldPresent: true,
	}})
	if len(payloads) != 1 {
		t.Fatalf("payloads len = %d", len(payloads))
	}
	payload := payloads[0]
	if payload.SourceLogID != 101 || payload.CreatedAt != createdAt || payload.ModelName != "gpt-4o" || payload.TotalTokens != 100 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	if payload.CacheTokens == nil || *payload.CacheTokens != 128 || !payload.CacheFieldPresent {
		t.Fatalf("cache fields not preserved: %#v", payload)
	}
}

func TestSelectLogEventsAndSamplesForReportModes(t *testing.T) {
	events := []logcollector.Event{
		{SourceLogID: 1, LogType: "consume", UseTime: 1},
		{SourceLogID: 2, LogType: "error", UseTime: 2},
		{SourceLogID: 3, LogType: "consume", UseTime: 12},
	}

	aggregateOnly := config.Config{LogEventMode: "aggregate_only", LogSampleLimit: 50, SlowLogThresholdSeconds: 10}
	if got := selectLogEventsForReport(aggregateOnly, events); len(got) != 0 {
		t.Fatalf("aggregate_only log events = %#v", got)
	}
	if got := selectLogSamplesForReport(aggregateOnly, events); len(got) != 0 {
		t.Fatalf("aggregate_only samples = %#v", got)
	}

	fullDebug := config.Config{LogEventMode: "full_debug", LogSampleLimit: 1, SlowLogThresholdSeconds: 10}
	if got := selectLogEventsForReport(fullDebug, events); len(got) != 3 {
		t.Fatalf("full_debug log events len = %d", len(got))
	}
	if got := selectLogSamplesForReport(fullDebug, events); len(got) != 0 {
		t.Fatalf("full_debug samples = %#v", got)
	}

	withSamples := config.Config{LogEventMode: "aggregate_with_samples", LogSampleLimit: 50, SlowLogThresholdSeconds: 10}
	if got := selectLogEventsForReport(withSamples, events); len(got) != 0 {
		t.Fatalf("aggregate_with_samples log events = %#v", got)
	}
	got := selectLogSamplesForReport(withSamples, events)
	if len(got) != 2 || got[0].SourceLogID != 2 || got[0].SampleKind != "error" || got[1].SourceLogID != 3 || got[1].SampleKind != "slow" {
		t.Fatalf("unexpected samples: %#v", got)
	}
}
func bufferEntry(lastLogID int64) localbuffer.Entry {
	return localbuffer.Entry{
		CreatedAt: time.Now().UTC(),
		LastLogID: lastLogID,
		Report: reporter.AgentReportRequest{
			InstanceID: "inst-1",
			AgentID:    "agent-1",
			LogEvents:  []reporter.LogEventPayload{{SourceLogID: lastLogID}},
		},
	}
}

type fakeReporter struct {
	reportErr         error
	heartbeatResponse reporter.AgentHeartbeatResponse
	reports           []reporter.AgentReportRequest
}

func (f *fakeReporter) Heartbeat(context.Context, reporter.AgentHeartbeatRequest) (reporter.AgentHeartbeatResponse, error) {
	return f.heartbeatResponse, nil
}

func (f *fakeReporter) Report(_ context.Context, report reporter.AgentReportRequest) error {
	if f.reportErr != nil {
		return f.reportErr
	}
	f.reports = append(f.reports, report)
	return nil
}
