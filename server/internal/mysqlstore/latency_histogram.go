package mysqlstore

import (
	"fmt"
	"strings"

	"controltower/internal/latencyhist"
)

var latencyBucketColumns = []string{
	"latency_le_250ms",
	"latency_le_500ms",
	"latency_le_1s",
	"latency_le_2s",
	"latency_le_3s",
	"latency_le_5s",
	"latency_le_10s",
	"latency_le_30s",
	"latency_le_60s",
	"latency_gt_60s",
}

var latency2BucketColumns = []string{
	"latency2_le_250ms", "latency2_le_500ms", "latency2_le_1s", "latency2_le_2s",
	"latency2_le_3s", "latency2_le_5s", "latency2_le_8s", "latency2_le_10s",
	"latency2_le_12s", "latency2_le_20s", "latency2_le_30s", "latency2_le_45s",
	"latency2_le_60s", "latency2_le_90s", "latency2_gt_90s",
}

var ttft2BucketColumns = []string{
	"ttft2_le_250ms", "ttft2_le_500ms", "ttft2_le_1s", "ttft2_le_2s",
	"ttft2_le_3s", "ttft2_le_5s", "ttft2_le_8s", "ttft2_le_10s",
	"ttft2_le_12s", "ttft2_le_20s", "ttft2_le_30s", "ttft2_le_45s",
	"ttft2_le_60s", "ttft2_le_90s", "ttft2_gt_90s",
}

func latencyBucketColumnSQL() string {
	return strings.Join(latencyBucketColumns, ", ")
}

func v2BucketColumnSQL() string {
	return strings.Join(latency2BucketColumns, ", ") + ", " + strings.Join(ttft2BucketColumns, ", ")
}

func v2BucketReplaceAssignmentsSQL() string {
	assignments := make([]string, 0, 2*latencyhist.BucketCountV2)
	for _, column := range append(append([]string{}, latency2BucketColumns...), ttft2BucketColumns...) {
		assignments = append(assignments, column+" = VALUES("+column+")")
	}
	return strings.Join(assignments, ",\n  ")
}

// v2BucketMergeAssignmentsSQL adds partial histograms; plain SQL addition
// NULL-poisons when either side lacks V2 data, which is exactly the desired
// "both sides must carry it" merge semantics.
func v2BucketMergeAssignmentsSQL() string {
	assignments := make([]string, 0, 2*latencyhist.BucketCountV2)
	for _, column := range append(append([]string{}, latency2BucketColumns...), ttft2BucketColumns...) {
		assignments = append(assignments, column+" = "+column+" + VALUES("+column+")")
	}
	return strings.Join(assignments, ",\n  ")
}

func metricValuePlaceholders() string {
	count := 31 + latencyhist.BucketCount + 2*latencyhist.BucketCountV2
	values := make([]string, 0, count)
	for i := 0; i < count; i++ {
		values = append(values, "?")
	}
	return strings.Join(values, ", ")
}

func latencyBucketReplaceAssignmentsSQL() string {
	assignments := make([]string, 0, len(latencyBucketColumns))
	for _, column := range latencyBucketColumns {
		assignments = append(assignments, column+" = VALUES("+column+")")
	}
	return strings.Join(assignments, ",\n  ")
}

func latencyBucketMergeAssignmentsSQL() string {
	assignments := make([]string, 0, len(latencyBucketColumns))
	for _, column := range latencyBucketColumns {
		assignments = append(assignments, column+" = "+column+" + VALUES("+column+")")
	}
	return strings.Join(assignments, ",\n  ")
}

func latencyP95MergeSQL() string {
	terms := make([]string, 0, len(latencyBucketColumns))
	for _, column := range latencyBucketColumns {
		terms = append(terms, "("+column+" + VALUES("+column+"))")
	}
	total := strings.Join(terms, " + ")
	cumulative := make([]string, 0, len(latencyBucketColumns))
	cases := make([]string, 0, len(latencyBucketColumns))
	for i, term := range terms {
		cumulative = append(cumulative, term)
		cases = append(cases, fmt.Sprintf(
			"WHEN %s >= CEIL((%s) * 0.95) THEN %g",
			strings.Join(cumulative, " + "),
			total,
			latencyhist.UpperBounds[i],
		))
	}
	return "CASE\n" +
		"    WHEN (" + total + ") = 0 THEN CASE\n" +
		"      WHEN p95_use_time IS NULL THEN VALUES(p95_use_time)\n" +
		"      WHEN VALUES(p95_use_time) IS NULL THEN p95_use_time\n" +
		"      ELSE GREATEST(p95_use_time, VALUES(p95_use_time))\n" +
		"    END\n    " + strings.Join(cases, "\n    ") +
		"\n    ELSE " + fmt.Sprintf("%g", latencyhist.UpperBounds[latencyhist.BucketCount-1]) +
		"\n  END"
}
