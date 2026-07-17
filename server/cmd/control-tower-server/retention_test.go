package main

import (
	"testing"
	"time"
)

type retentionRecorder struct{ calls map[string]time.Time }

func (r *retentionRecorder) PruneBefore(k string, t time.Time) (int64, error) {
	r.calls[k] = t
	return 1, nil
}
func TestPruneRetentionGroupsAndZeroDisabled(t *testing.T) {
	now := time.Now().UTC()
	r := &retentionRecorder{calls: map[string]time.Time{}}
	pruneRetention(r, 0, 90, 7, 6, 30, now)
	if len(r.calls) != 7 {
		t.Fatalf("calls=%v", r.calls)
	}
	if _, ok := r.calls["log_events"]; ok {
		t.Fatal("zero-day detail pruned")
	}
	if !r.calls["metric_5m"].Equal(now.Add(-90 * 24 * time.Hour)) {
		t.Fatalf("metric cutoff=%v", r.calls["metric_5m"])
	}
	if !r.calls["server_metrics"].Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("runtime cutoff=%v", r.calls["server_metrics"])
	}
	if !r.calls["health_checks"].Equal(now.Add(-6 * time.Hour)) {
		t.Fatalf("health cutoff=%v", r.calls["health_checks"])
	}
	if !r.calls["alerts"].Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Fatalf("alerts cutoff=%v", r.calls["alerts"])
	}
	if _, ok := r.calls["notification_deliveries"]; !ok {
		t.Fatal("deliveries not pruned")
	}
}
