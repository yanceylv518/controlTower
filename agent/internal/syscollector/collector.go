package syscollector

import (
	"context"
	"sync"
	"time"

	"controltower/agent/internal/reporter"
)

type sample struct {
	collectedAt    time.Time
	cpuIdle        uint64
	cpuTotal       uint64
	memoryUsed     float64
	diskUsed       float64
	networkRxBytes int64
	networkTxBytes int64
	load1m         float64
}

type Collector struct {
	mu       sync.Mutex
	diskPath string
	previous *sample
	now      func() time.Time
}

func New(diskPath string) *Collector {
	if diskPath == "" {
		diskPath = "."
	}
	return &Collector{diskPath: diskPath, now: time.Now}
}

func (c *Collector) Collect(ctx context.Context) reporter.ServerMetricPayload {
	current := readSample(ctx, c.diskPath, c.now().UTC())

	c.mu.Lock()
	defer c.mu.Unlock()

	metric := reporter.ServerMetricPayload{
		CollectedAt:       current.collectedAt,
		MemoryUsedPercent: clampPercent(current.memoryUsed),
		DiskUsedPercent:   clampPercent(current.diskUsed),
		Load1m:            current.load1m,
	}
	if c.previous != nil {
		metric.CPUPercent = clampPercent(cpuPercent(*c.previous, current))
		metric.NetworkRxBytesPerSecond = bytesPerSecond(c.previous.networkRxBytes, current.networkRxBytes, c.previous.collectedAt, current.collectedAt)
		metric.NetworkTxBytesPerSecond = bytesPerSecond(c.previous.networkTxBytes, current.networkTxBytes, c.previous.collectedAt, current.collectedAt)
	}
	c.previous = &current
	return metric
}

func readSample(ctx context.Context, diskPath string, collectedAt time.Time) sample {
	select {
	case <-ctx.Done():
		return sample{collectedAt: collectedAt}
	default:
	}
	idle, total := readCPUTimes()
	rx, tx := readNetworkTotals()
	return sample{
		collectedAt:    collectedAt,
		cpuIdle:        idle,
		cpuTotal:       total,
		memoryUsed:     readMemoryUsedPercent(),
		diskUsed:       readDiskUsedPercent(diskPath),
		networkRxBytes: rx,
		networkTxBytes: tx,
		load1m:         readLoad1m(),
	}
}

func cpuPercent(previous sample, current sample) float64 {
	totalDelta := current.cpuTotal - previous.cpuTotal
	idleDelta := current.cpuIdle - previous.cpuIdle
	if current.cpuTotal < previous.cpuTotal || current.cpuIdle < previous.cpuIdle || totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return (float64(totalDelta-idleDelta) / float64(totalDelta)) * 100
}

func bytesPerSecond(previous int64, current int64, previousAt time.Time, currentAt time.Time) int64 {
	if current < previous {
		return 0
	}
	seconds := currentAt.Sub(previousAt).Seconds()
	if seconds <= 0 {
		return 0
	}
	return int64(float64(current-previous) / seconds)
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
