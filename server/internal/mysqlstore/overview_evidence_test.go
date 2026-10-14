package mysqlstore

import (
	"controltower/internal/latencyhist"
	"controltower/server/internal/storage"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestClosedCoveredMinutesDoesNotInventRestartHistory(t *testing.T) {
	at := time.Date(2026, 10, 14, 6, 0, 0, 0, time.UTC)
	if len(closedCoveredMinutes(time.Time{}, at)) != 0 {
		t.Fatal("first marker certified history")
	}
	if len(closedCoveredMinutes(at.Add(20*time.Second), at.Add(time.Minute))) != 0 {
		t.Fatal("partial first minute certified")
	}
	ms := closedCoveredMinutes(at.Add(20*time.Second), at.Add(2*time.Minute))
	if len(ms) != 1 || !ms[0].Equal(at.Add(time.Minute)) {
		t.Fatalf("closed minutes: %v", ms)
	}
}
func TestOverviewAlertConfirmationDoesNotRefreshStaleEvidence(t *testing.T) {
	now := time.Now().UTC()
	for _, age := range []time.Duration{3 * time.Minute, 48 * time.Hour, -time.Minute} {
		if overviewAlertConfirmation(storage.Alert{RuleKey: "error_rate", LastSeenAt: now.Add(-age)}, now) != nil {
			t.Fatal("stale/future sample confirmed")
		}
	}
	if overviewAlertConfirmation(storage.Alert{RuleKey: "error_rate", LastSeenAt: now.Add(-time.Minute)}, now) == nil {
		t.Fatal("fresh evidence missing")
	}
	if overviewAlertConfirmation(storage.Alert{RuleKey: "instance_offline", LastSeenAt: now.Add(-time.Hour)}, now) == nil {
		t.Fatal("offline must use evaluation time")
	}
}
func TestOverviewQuantilesUseOneHistogramInsteadOfStoredP95(t *testing.T) {
	emptyV2 := latencyhist.BucketsV2{}
	legacy := latencyhist.Buckets{100}
	if v := quantilePreferV2(&emptyV2, sql.NullFloat64{Float64: 120, Valid: true}, legacy, .95); v == nil || *v > .25 {
		t.Fatal("empty V2 hid available V1 distribution")
	}
	b := latencyhist.BucketsV2{}
	b[12] = 94
	b[13] = 6
	v1 := latencyhist.DeriveV1(b)
	var previous float64
	for _, q := range []float64{.5, .95, .99} {
		v := quantilePreferV2(&b, sql.NullFloat64{Float64: 120, Valid: true}, v1, q)
		if v == nil || *v < previous || *v > 90 {
			t.Fatalf("non-monotone quantile q=%v: %v", q, v)
		}
		previous = *v
	}
	b[14] = 100
	if quantilePreferV2(&b, sql.NullFloat64{Float64: 120, Valid: true}, v1, .95) != nil {
		t.Fatal("open tail returned a fabricated exact value")
	}
}
func TestOverviewAlertSiteFilterPrecedesLimit(t *testing.T) {
	query, args := buildAlertQuery(storage.AlertQuery{SiteID: "site-a", ActiveOnly: true, Limit: 5})
	if !strings.Contains(query, "CASE WHEN site_id='' THEN id ELSE site_id END=?") || len(args) != 5 || args[0] != "site-a" || args[1] != "site-a" {
		t.Fatalf("site not filtered in SQL: %s %#v", query, args)
	}
}
