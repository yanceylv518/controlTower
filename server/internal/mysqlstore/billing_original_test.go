package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"
)

func TestBillingOriginalAggregation(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
	}
	db, e := Open(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	if e = ApplyDir(ctx, db, "../../migrations"); e != nil {
		t.Fatal(e)
	}
	site := fmt.Sprintf("original-%d", time.Now().UnixNano())
	s := New(db)
	day := time.Date(2025, 1, 1, 0, 0, 0, 0, billing.BusinessLocation)
	job, _, e := billing.NewJob(site, day, day.AddDate(0, 0, 1), "test")
	if e != nil {
		t.Fatal(e)
	}
	job.JobType = "user_statement"
	job.UserID = 7
	job.UsageVersion = 3
	job.BillPeriod = "daily"
	job.Status = "complete"
	job.RequestKey = job.ID
	job.MoneySnapshot, _ = billing.NewMoneySnapshot(site, `{"QuotaPerUnit":500000,"USDExchangeRate":7.2,"DisplayInCurrencyEnabled":true}`, time.Now())
	defer func() {
		db.Exec("DELETE FROM billing_compact_daily_totals WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_generation_tasks WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_jobs WHERE instance_id=?", site)
		db.Exec("DELETE FROM billing_money_snapshots WHERE instance_id=?", site)
	}()
	if e = s.CreateBillingStatementJob(ctx, job, nil, ""); e != nil {
		t.Fatal(e)
	}
	write := func(before, discount string) {
		t.Helper()
		e := s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, []billing.RequestDetail{{BillDay: day, UserID: 7, ModelName: "m", MultimediaUsage: billing.MultimediaUsage{ImageInputTokens: 513, ImageOutputTokens: 7, AudioInputTokens: 31, AudioOutputTokens: 11}, Charge: billing.LogCharge{Total: "4", UnitPrices: billing.SimpleUnitPrices(billing.LogCharge{InputPrice: before, OutputPrice: "8"}), Settlement: &billing.Settlement{BeforeAmount: before, Discount: discount}}}}, nil, nil, billing.LogCursor{}, 1)
		if e != nil {
			t.Fatal(e)
		}
	}
	write("5", "0.8")
	write("5", "0.800000")
	check := func(before, discount string) {
		t.Helper()
		rows, e := s.QueryBillingStatementAggregates(ctx, job.ID)
		gotBefore, gotDiscount := "", ""
		for i, row := range rows {
			if i == 0 {
				gotBefore, gotDiscount = row.BeforeAmount, row.SettlementDiscount
			} else {
				gotBefore = billing.MergeBefore(gotBefore, row.BeforeAmount)
				gotDiscount = billing.MergeDiscount(gotDiscount, row.SettlementDiscount)
			}
		}
		if e != nil || len(rows) == 0 || gotBefore != before || gotDiscount != discount {
			t.Fatalf("rows=%+v error=%v", rows, e)
		}
	}
	check("10.000000000000", "0.800000")
	write("4", "1")
	check("14.000000000000", "mixed")
	write("", "0.8")
	check("", "mixed")
	workspace, e := s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
	if e != nil || len(workspace) != 1 || workspace[0].ImageInputTokens != 2052 || workspace[0].ImageOutputTokens != 28 || workspace[0].AudioInputTokens != 124 || workspace[0].AudioOutputTokens != 44 {
		t.Fatalf("workspace media: %+v %v", workspace, e)
	}
	// Excluded diagnostics survive without changing bill aggregates or exports.
	beforeRequests, beforeAmount := workspace[0].Requests, workspace[0].Amount
	if e = s.AppendBillingHour(ctx, job, billing.JobStep{}, nil, nil, nil, []billing.RequestDetail{{DiagnosticOnly: true, EmptyOutput: true, ModelName: "m", Charge: billing.LogCharge{Total: "0.25"}}}, nil, nil, billing.LogCursor{}, 1); e != nil {
		t.Fatal(e)
	}
	workspace, e = s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
	if e != nil || len(workspace) != 1 || workspace[0].EmptyCount != 1 || workspace[0].EmptyAmount != "0.250000000000" || workspace[0].Requests != beforeRequests || workspace[0].Amount != beforeAmount {
		t.Fatalf("excluded diagnostics: %+v %v", workspace, e)
	}
	check("", "mixed")
	// Historical bills did not record discount metadata; use full-price fallback.
	if _, e = db.Exec("UPDATE billing_compact_daily_totals SET channel_id=CASE WHEN settlement_discount='1.000000' THEN 99 ELSE channel_id END,before_known_count=0,settlement_discount='' WHERE job_id=?", job.ID); e != nil {
		t.Fatal(e)
	}
	check("16.000000000000", "1.000000")
	workspace, e = s.BillingWorkspace(ctx, site, "user_statement", 7, day, day.AddDate(0, 0, 1))
	if e != nil || len(workspace) != 1 || workspace[0].BeforeAmount != "16.000000000000" || workspace[0].Discount != "1.000000" {
		t.Fatalf("workspace fallback: %+v %v", workspace, e)
	}
	tokens, e := s.QueryBillingTokenRows(ctx, job.ID, 7, -1, day, day.AddDate(0, 0, 1))
	if e != nil || len(tokens) != 1 || tokens[0].BeforeAmount != "16.000000000000" || tokens[0].SettlementDiscount != "1.000000" {
		t.Fatalf("token fallback: %+v %v", tokens, e)
	}
	if got := billing.UnitPriceLabel(tokens[0].UnitPrices, big.NewRat(1, 1)); got != "输入 4 / 5 / 未记录；输出 8" {
		t.Fatal("cross-page price union:", got)
	}
	// A pre-migration row mixed with new data must preserve missing evidence.
	if _, e = db.Exec("UPDATE billing_compact_daily_totals SET unit_prices=NULL WHERE job_id=?", job.ID); e != nil {
		t.Fatal(e)
	}
	write("5", "1")
	rows, e := s.QueryBillingStatementAggregates(ctx, job.ID)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	var prices billing.UnitPrices
	for _, row := range rows {
		prices.Merge(row.UnitPrices)
	}
	if got := billing.UnitPriceLabel(prices, big.NewRat(1, 1)); got != "输入 5；输出 8；部分未记录" {
		t.Fatal(got)
	}

}
