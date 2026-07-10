package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

func TestBuildOverviewSummarizesInstanceMetrics(t *testing.T) {
	successRate := 0.98
	errorRate := 0.02
	avgUseTime := 2.5
	p95UseTime := 6.5
	bucket := time.Date(2026, 7, 2, 12, 40, 0, 0, time.UTC)

	overview := BuildOverview([]aggregator.Metric{
		{
			InstanceID:    "inst-1",
			BucketTime:    bucket,
			DimensionType: "instance",
			DimensionKey:  "inst-1",
			RequestCount:  100,
			SuccessCount:  98,
			ErrorCount:    2,
			SuccessRate:   &successRate,
			ErrorRate:     &errorRate,
			TPM:           1000,
			AvgUseTime:    &avgUseTime,
			P95UseTime:    &p95UseTime,
		},
	})

	if overview.InstanceCount != 1 {
		t.Fatalf("unexpected instance count: %d", overview.InstanceCount)
	}
	if overview.Recent1m.RequestCount != 100 || overview.Recent1m.TPM != 1000 {
		t.Fatalf("unexpected recent summary: %#v", overview.Recent1m)
	}
	if overview.Recent1m.SuccessRate == nil || *overview.Recent1m.SuccessRate != 0.98 {
		t.Fatalf("unexpected success rate: %#v", overview.Recent1m.SuccessRate)
	}
	if overview.Recent1m.P95UseTime == nil || *overview.Recent1m.P95UseTime != 6.5 {
		t.Fatalf("unexpected p95: %#v", overview.Recent1m.P95UseTime)
	}
}

func TestBuildOverviewIgnoresNonInstanceMetricsForGlobalSummary(t *testing.T) {
	bucket := time.Date(2026, 7, 2, 12, 41, 0, 0, time.UTC)
	overview := BuildOverview([]aggregator.Metric{
		{
			InstanceID:    "inst-1",
			BucketTime:    bucket,
			DimensionType: "instance_user",
			DimensionKey:  "inst-1:user:7",
			RequestCount:  100,
			SuccessCount:  100,
			TPM:           1000,
		},
	})

	if overview.InstanceCount != 0 {
		t.Fatalf("non-instance metric should not count as instance")
	}
	if overview.Recent1m.RequestCount != 0 {
		t.Fatalf("non-instance metric should not affect global summary")
	}
}
func TestBuildOverviewWithRuntimeSummarizesLatestRuntimeState(t *testing.T) {
	oldTime := time.Date(2026, 7, 7, 11, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Minute)
	overview := BuildOverviewWithRuntime(nil,
		[]storage.ServerMetric{
			{InstanceID: "inst-1", CollectedAt: oldTime, CPUPercent: 10},
			{InstanceID: "inst-1", CollectedAt: newTime, CPUPercent: 20},
		},
		[]storage.HealthCheck{
			{InstanceID: "inst-1", Target: "status", CheckedAt: oldTime, Status: "down"},
			{InstanceID: "inst-1", Target: "status", CheckedAt: newTime, Status: "up"},
			{InstanceID: "inst-2", Target: "status", CheckedAt: newTime, Status: "down"},
		},
		[]storage.DockerStatus{
			{InstanceID: "inst-1", ContainerName: "api", CollectedAt: oldTime, Running: false},
			{InstanceID: "inst-1", ContainerName: "api", CollectedAt: newTime, Running: true},
			{InstanceID: "inst-1", ContainerName: "mysql", CollectedAt: newTime, Running: false},
		},
	)

	if len(overview.Runtime.LatestServerMetrics) != 1 || overview.Runtime.LatestServerMetrics[0].CPUPercent != 20 {
		t.Fatalf("unexpected latest metrics: %#v", overview.Runtime.LatestServerMetrics)
	}
	if overview.Runtime.Health.UpCount != 1 || overview.Runtime.Health.DownCount != 1 {
		t.Fatalf("unexpected health summary: %#v", overview.Runtime.Health)
	}
	if overview.Runtime.Docker.RunningCount != 1 || overview.Runtime.Docker.StoppedCount != 1 {
		t.Fatalf("unexpected docker summary: %#v", overview.Runtime.Docker)
	}
}
