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

func latencyBucketColumnSQL() string {
	return strings.Join(latencyBucketColumns, ", ")
}

func metricValuePlaceholders() string {
	values := make([]string, 0, 29+latencyhist.BucketCount)
	for i := 0; i < 29+latencyhist.BucketCount; i++ {
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
