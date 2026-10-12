package mysqlstore

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestProbeOverviewTotalsMatchDirectAggregation(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	duration := func(v float64) *float64 { return &v }
	rows := []tuning.ProbeRecord{
		{ChannelID: 7, ModelName: "a", Outcome: "success", DurationSeconds: duration(0)},
		{ChannelID: 7, ModelName: "a", Outcome: "success", DurationSeconds: duration(2)},
		{ChannelID: 8, ModelName: "a", Kind: "accelerated", Outcome: "success", DurationSeconds: duration(10)},
		{ChannelID: 8, ModelName: "b", Kind: "recovery", Outcome: "success"},
		{ChannelID: 8, ModelName: "a", Outcome: "slow", DurationSeconds: duration(30)},
		{ChannelID: 9, ModelName: "b", Outcome: "error"},
		{ChannelID: 9, ModelName: "b", Outcome: "error", DurationSeconds: duration(6)},
		{ChannelID: 7, ModelName: "a", Outcome: "unknown", DurationSeconds: duration(500)},
		{ChannelID: 7, ModelName: "a", Outcome: "skipped", Reason: "channel_concurrency"},
		{ChannelID: 9, ModelName: "b", Outcome: "pending"},
		{ChannelID: 7, ModelName: "a", Outcome: "pending", DeadlineAt: now.Add(time.Minute)},
		{ChannelID: 10, ModelName: "c", Outcome: "canceled", DurationSeconds: duration(1)},
		{ChannelID: 10, ModelName: "c", Outcome: "skipped", Reason: "global_concurrency"},
		{ChannelID: 10, ModelName: "c", Outcome: "skipped", Reason: "pending_evidence"},
	}
	for i := range rows {
		v := &rows[i]
		v.ID, v.InstanceID, v.ChannelName = rand.Text(), site, "channel"
		v.StartedAt = now.Add(-time.Duration(i+1) * time.Minute)
		if v.DeadlineAt.IsZero() {
			v.DeadlineAt = v.StartedAt.Add(30 * time.Second)
		}
		if v.Kind == "" {
			v.Kind = "scheduled"
		}
	}
	require.NoError(t, s.StoreProbeRecords(ctx, rows, nil))
	t.Cleanup(func() { _, _ = s.db.Exec("DELETE FROM tuning_probe_records WHERE instance_id=?", site) })
	base := tuning.ProbeRecordQuery{Site: site, Now: now, From: now.Add(-time.Hour), To: now}
	cases := []struct {
		name string
		edit func(*tuning.ProbeRecordQuery)
	}{
		{"all", func(*tuning.ProbeRecordQuery) {}},
		{"channel", func(q *tuning.ProbeRecordQuery) { q.ChannelID = 7 }},
		{"model", func(q *tuning.ProbeRecordQuery) { q.Model = "b" }},
		{"kind", func(q *tuning.ProbeRecordQuery) { q.Kind = "accelerated" }},
		{"search", func(q *tuning.ProbeRecordQuery) { q.Search = "9" }},
		{"no measured durations", func(q *tuning.ProbeRecordQuery) { q.ChannelID = 10 }},
		{"empty", func(q *tuning.ProbeRecordQuery) { q.ChannelID = 99 }},
		{"time boundary", func(q *tuning.ProbeRecordQuery) { q.From = now.Add(-5 * time.Minute) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := base
			tc.edit(&q)
			got, err := s.QueryProbeOverview(ctx, q)
			require.NoError(t, err)
			source, args := probeRecordSource(q)
			var want tuning.ProbeMetrics
			var scan metricScan
			require.NoError(t, s.db.QueryRowContext(ctx, "SELECT "+probeMetricsSQL+" FROM "+source, args...).Scan(scan.args(&want)...))
			scan.finish(&want)
			for _, values := range [][2]*float64{{want.MeanSeconds, got.Total.MeanSeconds}, {want.MeanSuccessSeconds, got.Total.MeanSuccessSeconds}} {
				if values[0] == nil {
					require.Nil(t, values[1])
				} else {
					require.NotNil(t, values[1])
					require.InDelta(t, *values[0], *values[1], 1e-12)
				}
			}
			want.MeanSeconds, got.Total.MeanSeconds = nil, nil
			want.MeanSuccessSeconds, got.Total.MeanSuccessSeconds = nil, nil
			require.Equal(t, want, got.Total)
		})
	}
	got, err := s.QueryProbeOverview(ctx, base)
	require.NoError(t, err)
	require.EqualValues(t, 14, got.Total.Due)
	require.InDelta(t, 48.0/5, *got.Total.MeanSeconds, 1e-12)
	require.InDelta(t, 4, *got.Total.MeanSuccessSeconds, 1e-12)
}
