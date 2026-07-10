package syscollector

import (
	"context"
	"testing"
	"time"
)

func TestCPUPercentUsesCounterDelta(t *testing.T) {
	previous := sample{cpuIdle: 100, cpuTotal: 1000}
	current := sample{cpuIdle: 150, cpuTotal: 1200}
	got := cpuPercent(previous, current)
	if got != 75 {
		t.Fatalf("cpu percent = %v, want 75", got)
	}
}

func TestBytesPerSecondUsesElapsedTime(t *testing.T) {
	previousAt := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	currentAt := previousAt.Add(2 * time.Second)
	got := bytesPerSecond(100, 500, previousAt, currentAt)
	if got != 200 {
		t.Fatalf("bytes per second = %d, want 200", got)
	}
}

func TestCollectorReturnsClampedPayload(t *testing.T) {
	collector := New(".")
	metric := collector.Collect(context.Background())
	if metric.CollectedAt.IsZero() {
		t.Fatalf("collected_at should be set")
	}
	if metric.MemoryUsedPercent < 0 || metric.MemoryUsedPercent > 100 {
		t.Fatalf("memory percent out of range: %v", metric.MemoryUsedPercent)
	}
	if metric.DiskUsedPercent < 0 || metric.DiskUsedPercent > 100 {
		t.Fatalf("disk percent out of range: %v", metric.DiskUsedPercent)
	}
}
