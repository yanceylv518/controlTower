package mysqlstore

import (
	"context"
	es "controltower/internal/errorstats"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestErrorStatisticsAtomicDedupAndDimensions(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for MySQL integration")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	instance := "error-statistics-test"
	cleanup := func() {
		for _, table := range []string{"error_statistics_minutes", "error_statistics_batches", "error_statistics_state"} {
			if _, err := db.Exec("DELETE FROM "+table+" WHERE instance_id=?", instance); err != nil {
				t.Error(err)
			}
		}
	}
	cleanup()
	defer cleanup()
	s := New(db)
	now := time.Now().UTC().Truncate(time.Minute)
	b := es.Batch{InstanceID: instance, ID: "one", StartedAt: now, ObservedAt: now.Add(time.Second), Rows: []es.Row{{Minute: now, UserID: 1, ChannelID: 2, Model: "GPT", Code: "http:429", Count: 3}, {Minute: now, UserID: 2, ChannelID: 2, Model: "gpt", Code: "business:quota", Count: 5}, {Minute: now, UserID: 1, ChannelID: 3, Model: "GPT", Code: "zero_output", Count: 2}}}
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	q := es.Query{Details: true, InstanceID: instance, Dimension: "instance_user", Key: "1", Since: now, Until: now.Add(time.Minute), Channels: true}
	result, err := s.QueryErrorStatistics(ctx, q)
	if err != nil || result.TotalErrors != 3 || result.ZeroOutputs != 2 || len(result.Channels) != 1 || result.Channels[0].Key != "2" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	q.Channels = false
	q.Code = "zero_output"
	result, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || len(result.Channels) != 0 || len(result.Trend) != 1 || result.Trend[0].Count != 2 {
		t.Fatalf("zero output/viewer result=%+v err=%v", result, err)
	}
	q.Dimension = "instance_model"
	q.Key = "GPT"
	q.Code = ""
	result, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || result.TotalErrors != 3 {
		t.Fatalf("model case isolation: %+v %v", result, err)
	}
	b.ID = "bad"
	b.Rows[1].Count = -1
	if err = s.SaveErrorStatistics(ctx, b); err == nil {
		t.Fatal("invalid batch accepted")
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM error_statistics_batches WHERE instance_id=? AND batch_id='bad'`, instance).Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid batch changed ledger", err)
	}
}

func TestErrorStatisticsTimelineLargeCountsAndCoverage(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	instance := "error-timeline-integration"
	defer func() {
		for _, table := range []string{"error_statistics_minutes", "error_statistics_batches", "error_statistics_state"} {
			db.Exec("DELETE FROM "+table+" WHERE instance_id=?", instance)
		}
	}()
	s := New(db)
	now := time.Now().UTC().Truncate(5 * time.Minute).Add(-time.Hour)
	start := now.Add(-time.Minute)
	covered := now.Add(10 * time.Minute)
	b := es.Batch{InstanceID: instance, ID: "timeline", StartedAt: start, ObservedAt: covered, CoveredUntil: &covered}
	for i := 0; i < 601; i++ {
		b.Rows = append(b.Rows, es.Row{Minute: now, UserID: 1, ChannelID: 2, Model: "GPT:variant", Code: fmt.Sprintf("business:code%03d", i), Count: int64(601 - i)})
	}
	b.Rows = append(b.Rows, es.Row{Minute: now.Add(time.Minute), UserID: 1, ChannelID: 2, Model: "GPT:variant", Code: "http:429", Count: 25001}, es.Row{Minute: now, UserID: 1, Code: "zero_output", Count: 99999})
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	q := es.Query{Timeline: true, BucketSeconds: 300, InstanceID: instance, Dimension: "instance_user", Key: "1", Since: start, Until: covered.Add(time.Minute)}
	out, err := s.QueryErrorStatistics(ctx, q)
	want := int64(601*602/2 + 25001)
	if err != nil || out.TotalErrors != want || !out.Truncated || !out.Since.Equal(now) || !out.Until.Equal(covered) || len(out.Buckets) != 1 || len(out.Buckets[0].Counts) != 6 {
		t.Fatalf("timeline %+v %v", out, err)
	}
	var total, listed int64
	for _, bucket := range out.Buckets {
		for _, n := range bucket.Counts {
			total += n
		}
	}
	for _, c := range out.Codes {
		listed += c.Count
	}
	if total != want || listed != want {
		t.Fatalf("counts %d/%d want %d", total, listed, want)
	}
	q.Code = "business:code600"
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || len(out.Buckets) != 1 || out.Buckets[0].Counts[q.Code] != 1 || out.TotalErrors != want {
		t.Fatalf("tail focus %+v %v", out, err)
	}
	// Upload freshness does not create source coverage; previously covered data is retained.
	b.ID = "backlog"
	b.ObservedAt = covered.Add(10 * time.Minute)
	b.CoveredUntil = nil
	b.Rows = nil
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || !out.Until.Equal(covered) {
		t.Fatal("backlog advanced coverage", out, err)
	}
	b.ID = "loss"
	lost := now.Add(5 * time.Minute)
	b.LastLossAt = &lost
	b.Dropped = 2
	b.CoveredUntil = &covered
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	q.Code = ""
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || !out.Since.Equal(covered) || out.TotalErrors != 0 {
		t.Fatal("loss became false zero", out, err)
	}

	// A new activation epoch must not inherit an old unknown-loss counter.
	restart := covered.Add(time.Minute)
	restartCovered := restart.Add(5 * time.Minute)
	fresh := es.Batch{InstanceID: instance, ID: "fresh", StartedAt: restart, ObservedAt: restartCovered, CoveredUntil: &restartCovered}
	if err = s.SaveErrorStatistics(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	q.Since = restart
	q.Until = restartCovered
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || out.Dropped != 0 || out.LastLossAt != nil || !out.StartedAt.Equal(restart) || !out.CoveredUntil.Equal(restartCovered) {
		t.Fatal("reset epoch leaked old completeness", out, err)
	}
	// An older queued batch cannot overwrite the new epoch's coverage or loss.
	b.ID = "old-epoch"
	if err = s.SaveErrorStatistics(ctx, b); err != nil {
		t.Fatal(err)
	}
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || out.Dropped != 0 || out.LastLossAt != nil || !out.CoveredUntil.Equal(restartCovered) {
		t.Fatal("older epoch overwrote state", out, err)
	}
	q.InstanceID = "unknown"
	out, err = s.QueryErrorStatistics(ctx, q)
	if err != nil || out.StartedAt != nil {
		t.Fatal("unknown became covered", out, err)
	}
}

func TestErrorStatisticsCompleteMinutePrecision(t *testing.T) {
	base := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	for _, step := range []time.Duration{time.Minute, 5 * time.Minute} {
		since, until := errorStatisticsWindow(base.Add(time.Nanosecond), base.Add(2*step+time.Second), nil, base, base.Add(3*step), step)
		if !since.Equal(base.Add(step)) || !until.Equal(base.Add(2*step)) {
			t.Fatal("partial boundary treated as complete", since, until)
		}
		exact, _ := errorStatisticsWindow(base, base.Add(step), nil, base, base.Add(step), step)
		if !exact.Equal(base) {
			t.Fatal("exact minute lost")
		}
	}
}
func TestErrorStatisticsSupersededRowsAndMicroseconds(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []time.Duration{123456789 * time.Nanosecond, time.Nanosecond} {
		instance := "stats-epochs-" + offset.String()
		defer func() {
			for _, table := range []string{"error_statistics_minutes", "error_statistics_batches", "error_statistics_state"} {
				db.Exec("DELETE FROM "+table+" WHERE instance_id=?", instance)
			}
		}()
		s := New(db)
		base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
		start := base.Add(offset)
		covered := base.Add(4*time.Minute - time.Nanosecond)
		row := es.Row{Minute: base.Add(time.Minute), UserID: 1, Code: "http:429", Count: 7}
		fresh := es.Batch{InstanceID: instance, ID: "new", StartedAt: start, ObservedAt: covered, CoveredUntil: &covered, Rows: []es.Row{row}}
		if err = s.SaveErrorStatistics(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		old := fresh
		old.ID = "old-delayed"
		old.StartedAt = base.Add(-time.Hour)
		old.Rows = []es.Row{row}
		old.Rows[0].Count = 50
		if err = s.SaveErrorStatistics(ctx, old); err != nil {
			t.Fatal(err)
		}
		out, err := s.QueryErrorStatistics(ctx, es.Query{Timeline: true, BucketSeconds: 60, InstanceID: instance, Dimension: "instance_user", Key: "1", Since: base, Until: base.Add(4 * time.Minute)})
		if err != nil || out.TotalErrors != 7 || !out.Since.Equal(base.Add(time.Minute)) || !out.Until.Equal(base.Add(3*time.Minute)) {
			t.Fatal("stale epoch rows or fractional boundaries", offset, out, err)
		}
	}
}
