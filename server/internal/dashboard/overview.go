package dashboard

import (
	"controltower/internal/latencyhist"
	"sort"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type Overview struct {
	InstanceCount int            `json:"instance_count"`
	Recent1m      MetricSummary  `json:"recent_1m"`
	Runtime       RuntimeSummary `json:"runtime"`
}

type MetricSummary struct {
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	P95LowerBound    bool     `json:"p95_lower_bound"`
	RequestCount     int64    `json:"request_count"`
	SuccessCount     int64    `json:"success_count"`
	ErrorCount       int64    `json:"error_count"`
	SuccessRate      *float64 `json:"success_rate"`
	ErrorRate        *float64 `json:"error_rate"`
	TPM              int64    `json:"tpm"`
	AvgUseTime       *float64 `json:"avg_use_time"`
	P95UseTime       *float64 `json:"p95_use_time"`
}

type RuntimeSummary struct {
	LatestServerMetrics []ServerMetricSummary `json:"latest_server_metrics"`
	Health              HealthRuntimeSummary  `json:"health"`
	Docker              DockerRuntimeSummary  `json:"docker"`
}

type HealthRuntimeSummary struct {
	UpCount   int                  `json:"up_count"`
	DownCount int                  `json:"down_count"`
	Latest    []HealthCheckSummary `json:"latest"`
}

type DockerRuntimeSummary struct {
	RunningCount int                   `json:"running_count"`
	StoppedCount int                   `json:"stopped_count"`
	Latest       []DockerStatusSummary `json:"latest"`
}

func BuildOverview(metrics []aggregator.Metric) Overview {
	return BuildOverviewWithRuntime(metrics, nil, nil, nil)
}

func BuildOverviewWithRuntime(metrics []aggregator.Metric, serverMetrics []storage.ServerMetric, healthChecks []storage.HealthCheck, dockerStatuses []storage.DockerStatus) Overview {
	instanceIDs := make(map[string]struct{})
	var summary MetricSummary
	var avgWeightedTotal float64
	var avgSamples int64
	var buckets latencyhist.Buckets
	var bucketsV2 latencyhist.BucketsV2
	allV2, allHist := true, true
	var selected []aggregator.Metric
	var latest time.Time
	for _, m := range metrics {
		if m.DimensionType == "instance" && m.BucketTime.After(latest) {
			latest = m.BucketTime
		}
	}

	for _, metric := range metrics {
		if metric.DimensionType != "instance" || !metric.BucketTime.Equal(latest) {
			continue
		}
		instanceIDs[metric.InstanceID] = struct{}{}
		summary.RequestCount += metric.RequestCount
		summary.SuccessCount += metric.SuccessCount
		summary.ErrorCount += metric.ErrorCount
		summary.TPM += metric.TPM
		summary.PromptTokens += metric.PromptTokens
		summary.CompletionTokens += metric.CompletionTokens
		selected = append(selected, metric)
		if metric.AvgUseTime != nil && metric.RequestCount > 0 {
			avgWeightedTotal += *metric.AvgUseTime * float64(metric.RequestCount)
			avgSamples += metric.RequestCount
		}
		if metric.RequestCount > 0 {
			var samples int64
			for _, n := range metric.LatencyBuckets {
				samples += n
			}
			if samples != metric.RequestCount {
				allHist = false
			}
			buckets = latencyhist.Add(buckets, metric.LatencyBuckets)
			if metric.LatencyBucketsV2 == nil {
				allV2 = false
			} else {
				var v2Samples int64
				for _, n := range *metric.LatencyBucketsV2 {
					v2Samples += n
				}
				if v2Samples != metric.RequestCount {
					allV2 = false
				}
				bucketsV2 = latencyhist.AddV2(bucketsV2, *metric.LatencyBucketsV2)
			}
		}
	}

	summary.SuccessRate = ratio(summary.SuccessCount, summary.RequestCount)
	summary.ErrorRate = ratio(summary.ErrorCount, summary.RequestCount)
	if avgSamples > 0 {
		value := avgWeightedTotal / float64(avgSamples)
		summary.AvgUseTime = &value
	}
	if allHist || allV2 {
		var v2 *latencyhist.BucketsV2
		if allV2 {
			v2 = &bucketsV2
		}
		summary.P95UseTime, summary.P95LowerBound = latencyhist.Estimate(buckets, v2, .95)
	} else if len(selected) == 1 {
		summary.P95UseTime = selected[0].P95UseTime
	}

	return Overview{
		InstanceCount: len(instanceIDs),
		Recent1m:      summary,
		Runtime:       BuildRuntimeSummary(serverMetrics, healthChecks, dockerStatuses),
	}
}

func BuildRuntimeSummary(serverMetrics []storage.ServerMetric, healthChecks []storage.HealthCheck, dockerStatuses []storage.DockerStatus) RuntimeSummary {
	return RuntimeSummary{
		LatestServerMetrics: summarizeServerMetrics(latestServerMetricsByInstance(serverMetrics)),
		Health:              buildHealthRuntimeSummary(healthChecks),
		Docker:              buildDockerRuntimeSummary(dockerStatuses),
	}
}

func latestServerMetricsByInstance(items []storage.ServerMetric) []storage.ServerMetric {
	latest := make(map[string]storage.ServerMetric)
	for _, item := range items {
		current, ok := latest[item.InstanceID]
		if !ok || item.CollectedAt.After(current.CollectedAt) {
			latest[item.InstanceID] = item
		}
	}
	result := make([]storage.ServerMetric, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].InstanceID < result[j].InstanceID
	})
	return result
}

func buildHealthRuntimeSummary(items []storage.HealthCheck) HealthRuntimeSummary {
	latest := make(map[string]storage.HealthCheck)
	for _, item := range items {
		key := item.InstanceID + "\x00" + item.Target
		current, ok := latest[key]
		if !ok || item.CheckedAt.After(current.CheckedAt) {
			latest[key] = item
		}
	}
	result := make([]storage.HealthCheck, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].Target < result[j].Target
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	summary := HealthRuntimeSummary{Latest: summarizeHealthChecks(result)}
	for _, item := range result {
		if item.Status == "up" {
			summary.UpCount++
		} else {
			summary.DownCount++
		}
	}
	return summary
}

func buildDockerRuntimeSummary(items []storage.DockerStatus) DockerRuntimeSummary {
	latest := make(map[string]storage.DockerStatus)
	for _, item := range items {
		key := item.InstanceID + "\x00" + item.ContainerName
		current, ok := latest[key]
		if !ok || item.CollectedAt.After(current.CollectedAt) {
			latest[key] = item
		}
	}
	result := make([]storage.DockerStatus, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].InstanceID == result[j].InstanceID {
			return result[i].ContainerName < result[j].ContainerName
		}
		return result[i].InstanceID < result[j].InstanceID
	})
	summary := DockerRuntimeSummary{Latest: summarizeDockerStatuses(result)}
	for _, item := range result {
		if item.Running {
			summary.RunningCount++
		} else {
			summary.StoppedCount++
		}
	}
	return summary
}

func ratio(numerator int64, denominator int64) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
