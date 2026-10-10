package errorstats

import (
	"context"
	"controltower/agent/internal/logcollector"
	es "controltower/internal/errorstats"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRestartReplayAndNoHistory(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	path := filepath.Join(t.TempDir(), "stats.json")
	s, err := Open(path, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now); err != nil {
		t.Fatal(err)
	}
	events := []logcollector.Event{{SourceLogID: 1, CreatedAt: now.Add(-time.Second), LogType: "error", ErrorSummary: "HTTP 429"}, {SourceLogID: 2, CreatedAt: now, LogType: "error", ErrorSummary: "HTTP 429"}, {SourceLogID: 3, CreatedAt: now, LogType: "consume", CompletionTokens: 0}, {SourceLogID: 4, CreatedAt: now, LogType: "consume", CompletionTokens: 10}}
	if err = s.Record(events, now); err != nil {
		t.Fatal(err)
	}
	var first es.Batch
	err = s.Flush(context.Background(), func(_ context.Context, b es.Batch) error { first = b; return errors.New("offline") })
	if err == nil {
		t.Fatal("expected failure")
	}
	s, err = Open(path, "test", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Record(events, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	seen := false
	err = s.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		if b.ID == first.ID {
			seen = true
		}
		if !b.StartedAt.Equal(now) {
			t.Fatal("activation changed")
		}
		for _, r := range b.Rows {
			counts[r.Code] += r.Count
		}
		return nil
	})
	if err != nil || !seen || counts["http:429"] != 1 || counts["zero_output"] != 1 || len(counts) != 2 {
		t.Fatalf("err=%v counts=%v retried=%v", err, counts, seen)
	}
	s, err = Open(path, "test", now)
	if err != nil || len(s.state.Queue) != 0 {
		t.Fatalf("ack not persisted: %v", err)
	}
}
func TestBoundedQueueExposesLoss(t *testing.T) {
	now := time.Now().UTC()
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxBatches+2; i++ {
		err = s.Record([]logcollector.Event{{SourceLogID: int64(i), CreatedAt: now, LogType: "error"}}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(s.state.Queue) != maxBatches || s.state.Dropped != 2 || s.state.Queue[len(s.state.Queue)-1].Dropped != 2 {
		t.Fatalf("unbounded/invisible drop: %+v", s.state)
	}
}

func TestActivationWaitsForSnapshotAndExcludesBacklogWithBadTime(t *testing.T) {
	now := time.Now().UTC()
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "test", now)
	if err != nil {
		t.Fatal(err)
	}
	old := []logcollector.Event{{SourceLogID: 99, CreatedAt: now, LogType: "error"}}
	if err = s.Record(old, now); err != nil {
		t.Fatal(err)
	}
	if len(s.state.Queue) != 0 {
		t.Fatal("recorded before source snapshot")
	}
	if err = s.Activate(100, now); err != nil {
		t.Fatal(err)
	}
	events := append(old, logcollector.Event{SourceLogID: 101, CreatedAt: now, LogType: "error"})
	if err = s.Record(events, now); err != nil {
		t.Fatal(err)
	}
	if len(s.state.Queue[0].Rows) != 1 || s.state.Queue[0].Rows[0].Count != 1 {
		t.Fatalf("historical invalid timestamp included: %+v", s.state.Queue)
	}
}

func TestLargePageSplitCoverageAndReplay(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "large", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	events := make([]logcollector.Event, 5000)
	for i := range events {
		events[i] = logcollector.Event{SourceLogID: int64(i + 1), CreatedAt: now, LogType: "error", ChannelID: int64(i + 1), ErrorSummary: "HTTP 429"}
	}
	covered := now.Add(time.Minute)
	if err = s.RecordCovered(events, covered, &covered); err != nil {
		t.Fatal(err)
	}
	if len(s.state.Queue) != 3 || s.state.Dropped != 0 {
		t.Fatalf("split/loss %+v", s.state)
	}
	for i, b := range s.state.Queue {
		if err = b.Validate(covered); err != nil {
			t.Fatal(err)
		}
		if (b.CoveredUntil != nil) != (i == 2) {
			t.Fatal("coverage advanced before final chunk")
		}
	}
	if err = s.RecordCovered(events, covered, &covered); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = s.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		for _, r := range b.Rows {
			count += r.Count
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 5000 {
		t.Fatalf("replay counted %d", count)
	}
}
func TestBacklogDoesNotAdvanceCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "backlog", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Record([]logcollector.Event{{SourceLogID: 1, CreatedAt: now, LogType: "error"}}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if s.state.Queue[0].CoveredUntil != nil {
		t.Fatal("backlog claimed coverage")
	}
	covered := now.Add(2 * time.Minute)
	if err = s.RecordCovered(nil, covered, &covered); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.path, "backlog", covered)
	if err != nil || reopened.state.CoveredUntil == nil || !reopened.state.CoveredUntil.Equal(covered) {
		t.Fatal("coverage not durable", err)
	}
}

func TestFailedSnapshotRetainsWatermarkForRetry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	path := filepath.Join(t.TempDir(), "stats.json")
	s, err := Open(path, "disk", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(t.TempDir(), "parent-file")
	bad := filepath.Join(blocked, "stats.json")
	if err = os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	s.path = bad
	events := []logcollector.Event{{SourceLogID: 1, CreatedAt: now, LogType: "error", ErrorSummary: "HTTP 500"}}
	covered := now.Add(time.Minute)
	if err = s.RecordCovered(events, covered, &covered); err == nil {
		t.Fatal("expected disk failure")
	}
	if s.state.LastID != 0 || s.state.CoveredUntil != nil || len(s.state.Queue) != 0 {
		t.Fatal("non-durable watermark advanced")
	}
	s.path = path
	if err = s.RecordCovered(events, covered, &covered); err != nil {
		t.Fatal(err)
	}
	if s.state.Queue[0].Rows[0].Count != 1 || s.state.Dropped != 0 {
		t.Fatal("retry lost/duplicated data")
	}
}

func TestUpgradeLossWithoutTimestampStartsSafeRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	path := filepath.Join(t.TempDir(), "stats.json")
	s, err := Open(path, "upgrade", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, now); err != nil {
		t.Fatal(err)
	}
	s.state.Dropped = 3
	if err = s.save(s.state); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, "upgrade", now.Add(time.Hour))
	if err != nil || s.state.LastLossAt == nil || !s.state.LastLossAt.Equal(now.Add(time.Hour)) {
		t.Fatal("legacy loss must not claim old completeness", err)
	}
}

func TestEvictionReportsLossBeforeRetainedCoverage(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "eviction-order", start)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, start); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= maxBatches+1; i++ {
		now := start.Add(time.Duration(i) * time.Minute)
		if err = s.RecordCovered([]logcollector.Event{{SourceLogID: int64(i), CreatedAt: now.Add(-time.Second), LogType: "error"}}, now, &now); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(s.path, s.instance, start.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	first := true
	if err = reopened.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		if first {
			first = false
			if b.LastLossAt == nil || b.Dropped == 0 {
				t.Fatal("retained coverage can arrive before loss is visible")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMainCursorGapCreatesDurablePriorityNotice(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "cursor-gap", start)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(100, start); err != nil {
		t.Fatal(err)
	}
	now := start.Add(time.Minute)
	if err = s.RecordPass([]logcollector.Event{{SourceLogID: 101, CreatedAt: now.Add(-time.Second), LogType: "error"}}, now, &now, 100); err != nil {
		t.Fatal(err)
	}
	if s.state.LossNotice != nil {
		t.Fatal("contiguous pass treated as loss")
	}
	now = now.Add(time.Minute)
	if err = s.RecordPass([]logcollector.Event{{SourceLogID: 151, CreatedAt: now.Add(-time.Second), LogType: "error"}}, now, &now, 150); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(s.path, s.instance, now)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.state.LastID != 151 || reopened.state.LastLossAt == nil || reopened.state.LossNotice == nil {
		t.Fatal("skipped source range not durable")
	}
	seenLoss := false
	if err = reopened.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		if !seenLoss {
			if b.LastLossAt == nil || len(b.Rows) != 0 || b.CoveredUntil != nil {
				t.Fatal("loss did not precede data")
			}
			seenLoss = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestNewLossNoticeSurvivesOlderInflightAcknowledgement(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "loss-race", start)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, start); err != nil {
		t.Fatal(err)
	}
	now := start.Add(time.Minute)
	if err = s.RecordPass(nil, now, &now, 10); err != nil {
		t.Fatal(err)
	}
	first := true
	seen := 0
	if err = s.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		if b.LastLossAt != nil && len(b.Rows) == 0 && b.CoveredUntil == nil {
			seen++
		}
		if first {
			first = false
			later := now.Add(time.Minute)
			return s.RecordPass(nil, later, &later, 20)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if seen != 2 || s.state.LossNotice != nil {
		t.Fatal("new notice lost to old acknowledgement", seen)
	}
}

func TestUpgradeKnownLossPrecedesOlderCoverage(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	s, err := Open(filepath.Join(t.TempDir(), "stats.json"), "known-loss", start)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Activate(0, start); err != nil {
		t.Fatal(err)
	}
	covered := start.Add(time.Minute)
	if err = s.RecordCovered(nil, covered, &covered); err != nil {
		t.Fatal(err)
	}
	lost := covered.Add(time.Minute)
	s.state.LastLossAt = &lost
	s.state.Dropped = 1
	if err = s.save(s.state); err != nil {
		t.Fatal(err)
	}
	s, err = Open(s.path, s.instance, lost.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	first := true
	if err = s.Flush(context.Background(), func(_ context.Context, b es.Batch) error {
		if first {
			first = false
			if b.LastLossAt == nil || !b.LastLossAt.Equal(lost) || b.CoveredUntil != nil {
				t.Fatal("legacy coverage uploaded before known loss")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
