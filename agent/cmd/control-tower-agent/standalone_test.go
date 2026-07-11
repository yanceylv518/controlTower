package main

import (
	"context"
	"path/filepath"
	"testing"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logcollector"
	"controltower/agent/internal/state"
)

type fakeStandaloneCollector struct {
	latest       int64
	events       []logcollector.Event
	lastAfterID  int64
	collectCalls int
}

func (f *fakeStandaloneCollector) Collect(ctx context.Context, afterID int64, limit int) ([]logcollector.Event, int64, error) {
	f.collectCalls++
	f.lastAfterID = afterID
	lastID := afterID
	var out []logcollector.Event
	for _, event := range f.events {
		if event.SourceLogID > afterID {
			out = append(out, event)
			if event.SourceLogID > lastID {
				lastID = event.SourceLogID
			}
		}
	}
	return out, lastID, nil
}

func (f *fakeStandaloneCollector) Backlog(ctx context.Context, afterID int64) (logcollector.BacklogStats, error) {
	return logcollector.BacklogStats{SourceLatestLogID: f.latest, BacklogEstimate: f.latest - afterID}, nil
}

func TestStandalonePassSeedsCursorOnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	stateStore := state.NewFileStore(filepath.Join(dir, "state.json"))
	collector := &fakeStandaloneCollector{latest: 5000}
	cfg := config.Config{LogBatchSize: 1000}

	if err := runStandalonePass(context.Background(), cfg, collector, nil, stateStore); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if collector.collectCalls != 0 {
		t.Fatalf("fresh install must seed the cursor without collecting history")
	}
	saved, err := stateStore.Load()
	if err != nil || saved.LastLogID != 5000 {
		t.Fatalf("expected cursor seeded to 5000, got %+v err=%v", saved, err)
	}

	// Second pass collects only rows newer than the seeded cursor.
	collector.events = []logcollector.Event{{SourceLogID: 5001, LogType: "consume", ChannelID: 1}}
	if err := runStandalonePass(context.Background(), cfg, collector, nil, stateStore); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if collector.collectCalls != 1 || collector.lastAfterID != 5000 {
		t.Fatalf("expected one collect after cursor 5000, got calls=%d afterID=%d", collector.collectCalls, collector.lastAfterID)
	}
	saved, _ = stateStore.Load()
	if saved.LastLogID != 5001 {
		t.Fatalf("expected cursor advanced to 5001, got %d", saved.LastLogID)
	}
}
