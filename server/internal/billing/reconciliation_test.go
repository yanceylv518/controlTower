package billing

import (
	"database/sql"
	"math/big"
	"testing"
	"time"
)

func TestClassifyReconciliationUsesLargestAbsoluteComponent(t *testing.T) {
	if got := ClassifyReconciliation(big.NewRat(2, 1), big.NewRat(-5, 1), big.NewRat(3, 1)); got != ReconciliationCacheWrite {
		t.Fatalf("classification = %s", got)
	}
}

func TestReconcileRequestsRebuildsActualAndSumsComponents(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, BusinessLocation)
	logs := []PagedLogRecord{
		{ID: 1, ModelName: "m", GroupName: "vip", RequestID: "r1", Quota: 1_000_000, PromptTokens: sql.NullInt64{Int64: 1_000_000, Valid: true}, ModelRatio: "1", GroupRatio: "1"},
		{ID: 2, ModelName: "m", GroupName: "vip", RequestID: "r2", Quota: 1_000_000, PromptTokens: sql.NullInt64{Int64: 1_000_000, Valid: true}, ModelRatio: "1", GroupRatio: "1"},
	}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "0.5", Output: "0", Cache: "0", CacheWrite: "0"}}}
	result := ReconcileRequests(logs, "m", day, prices, []GroupRatio{{GroupName: "vip", Ratio: "1"}}, `{"QuotaPerUnit":1000000}`)
	if result.Matched != 2 || result.RebuildResidual != "0.000000" || result.ComponentDiffs.Input != "1.000000" {
		t.Fatalf("unexpected request reconciliation: %+v", result)
	}
	if len(result.Items) != 2 || result.Items[0].RebuiltAmount != "1.000000" || result.Items[0].DiffAmount != "0.500000" {
		t.Fatalf("unexpected request rows: %+v", result.Items)
	}
}

func TestReconcileRequestsMarksMissingRatiosUnexplained(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, BusinessLocation)
	log := PagedLogRecord{ID: 1, ModelName: "m", GroupName: "default", Quota: 1, PromptTokens: sql.NullInt64{Int64: 1, Valid: true}}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "1", Output: "0", Cache: "0", CacheWrite: "0"}}}
	result := ReconcileRequests([]PagedLogRecord{log}, "m", day, prices, nil, `{"QuotaPerUnit":1000000}`)
	if len(result.Items) != 1 || !result.Items[0].Unexplained || result.Items[0].RebuiltAmount != "" {
		t.Fatalf("unexpected legacy row: %+v", result.Items)
	}
}

func TestBuildReconciliationFallbackDoesNotCreateDifference(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	report := BuildReconciliation([]AggregateRow{{UserID: 7, Username: "u", ModelName: "missing", GroupName: "default", Day: day, RequestCount: 1, Quota: 500000}}, nil, nil, map[string]string{"2026-08-01": `{"QuotaPerUnit":500000}`}, nil, false)
	if len(report.Rows) != 1 || !report.Rows[0].FallbackPriced || report.Rows[0].DiffAmount != "0.000000" {
		t.Fatalf("unexpected fallback row: %+v", report.Rows)
	}
}

func TestBuildReconciliationResidualFormula(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	rows := []AggregateRow{{UserID: 1, Username: "u", ModelName: "m", GroupName: "default", Day: day, RequestCount: 1, PromptTokens: 1_000_000, Quota: 10_000_000}}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "10", Output: "0", Cache: "0", CacheWrite: "0"}}}
	report := BuildReconciliation(rows, prices, nil, map[string]string{"2026-08-01": `{"QuotaPerUnit":500000}`}, []AnomalyCount{{UserID: 1, Day: day, ModelName: "m", GroupName: "default", Count: 1, Amount: "2"}}, false)
	row := report.Rows[0]
	if row.BillingAmount != "10.000000" || row.ActualAmount != "22.000000" || row.DiffAmount != "12.000000" || row.Breakdown.Anomaly != "2.000000" || row.Breakdown.Residual != "10.000000" {
		t.Fatalf("unexpected reconciliation: %+v", row)
	}
}

// When both new-api (CreateCacheRatio configured) and CT (write price kept by
// the snapshot alignment) bill cache writes, the policy component must be the
// gap between them - near zero - not the full new-api write cost dragging the
// residual negative.
func TestBuildReconciliationCacheWriteComponentIsGapNotFullCost(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{UserID: 7, Username: "alice", ModelName: "m", Day: day, RequestCount: 1, CacheWrite1hTokens: 1_000_000, CacheWriteTokens: 1_000_000, Quota: 10_000_000}}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "10", Output: "0", Cache: "0", CacheWrite: "12.5"}}}
	snapshots := map[string]string{"2026-08-06": `{"ModelRatio":"{\"m\":5}","CreateCacheRatio":"{\"m\":1.25}","QuotaPerUnit":"500000"}`}
	report := BuildReconciliation(rows, prices, nil, snapshots, nil, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d", len(report.Rows))
	}
	// new-api estimate: input 10/1M, 1h price 20/1M -> 20; CT charged: 12.5*1.6 = 20/1M -> 20. Gap = 0.
	if report.Rows[0].Breakdown.CacheWritePolicy != "0.000000" {
		t.Fatalf("cache write component = %s, want 0.000000 (gap, not full cost)", report.Rows[0].Breakdown.CacheWritePolicy)
	}
}

func TestBuildReconciliationDefaultCacheWriteMatchesNewAPI(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{UserID: 7, Username: "alice", ModelName: "m", Day: day, RequestCount: 1, CacheWriteTokens: 1_000_000, Quota: 6_250_000}}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "10", Output: "0", Cache: "0", CacheWrite: "0"}}}
	snapshots := map[string]string{"2026-08-06": `{"ModelRatio":"{\"m\":5}","QuotaPerUnit":"500000"}`}
	report := BuildReconciliation(rows, prices, nil, snapshots, nil, false)
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %d", len(report.Rows))
	}
	if report.Rows[0].BillingAmount != "12.500000" || report.Rows[0].Breakdown.CacheWritePolicy != "0.000000" || report.Rows[0].DiffAmount != "0.000000" {
		t.Fatalf("default cache write did not match new-api: %+v", report.Rows[0])
	}
}

// Signed diffs must survive parsing: the rebuild self-check counts mismatches
// on BOTH sides, negative lane diffs reach the component totals, and rows
// with negative diffs sort by magnitude instead of collapsing to zero.
func TestReconcileRequestsHandlesNegativeDiffs(t *testing.T) {
	day := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	logs := []PagedLogRecord{{
		ID: 1, CreatedUnix: day.Unix(), ModelName: "m", GroupName: "default",
		Quota:      1_000_000, // actual 2.0 with qpu 500000
		ModelRatio: "2.5", CompletionRatio: "1", CacheRatio: "1", CacheCreationRatio: "", GroupRatio: "1",
		SourcePromptTokens: sql.NullInt64{Valid: true, Int64: 1_000_000},
		PromptTokens:       sql.NullInt64{Valid: true, Int64: 1_000_000},
		CompletionTokens:   sql.NullInt64{Valid: true, Int64: 0},
	}}
	// CT input price 10 > rebuilt input price 5: input lane diff is negative.
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, Input: "10", Output: "10", Cache: "10"}}}
	result := ReconcileRequests(logs, "m", day, prices, nil, `{"ModelRatio":"{\"m\":2.5}","QuotaPerUnit":"500000"}`)
	if result.ComponentDiffs.Input != "-5.000000" {
		t.Fatalf("input component = %s, want -5.000000 (negative must not be dropped)", result.ComponentDiffs.Input)
	}
	// actual 2.0 vs rebuilt 5.0: mismatch is on the negative side and must count.
	if result.RebuildResidual != "3.000000" {
		t.Fatalf("rebuild residual = %s, want 3.000000", result.RebuildResidual)
	}
}
