package billing

import (
	"context"
	"math/big"
	"testing"
	"time"
)

func TestTierStatisticsPreservesHistoricalGroupsAndSettlement(t *testing.T) {
	var a TierStatisticsAccumulator
	tiers := []string{"高峰时段", "低谷时段", "base", "", "高峰时段"}
	for i, tier := range tiers {
		row := RequestDetail{ModelName: "deepseek", PromptTokens: 10, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, MultimediaUsage: MultimediaUsage{ImageInputTokens: 5, AudioOutputTokens: 6}, Charge: LogCharge{MatchedTier: tier, Total: "0.1", Settlement: &Settlement{BeforeAmount: "0.2", Discount: "0.5"}, UnitPrices: UnitPrices{"输入|20": true}}}
		if i == 4 {
			row.ModelName = "other"
		}
		if err := a.AddDetail(row); err != nil {
			t.Fatal(err)
		}
		if row.Charge.Total != "0.1" {
			t.Fatal("repriced source")
		}
	}
	rows := a.Rows()
	if len(rows) != 5 {
		t.Fatal(rows)
	}
	merged, err := TierStatisticsByTier(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 4 {
		t.Fatal(merged)
	}
	total := new(big.Rat)
	var count int64
	for _, v := range merged {
		x, _ := new(big.Rat).SetString(v.Amount)
		total.Add(total, x)
		count += v.Requests
		if v.Tier == "高峰时段" && (v.Requests != 2 || v.Input != 20 || v.ImageInputTokens != 10 || v.AudioOutputTokens != 12 || v.BeforeAmount != "0.400000000000") {
			t.Fatal(v)
		}
	}
	if count != 5 || total.Cmp(big.NewRat(1, 2)) != 0 {
		t.Fatal(count, total)
	}
	if TierLabel("") != "未记录档位" || TierLabel("base") != "base" {
		t.Fatal("tier guessed")
	}
}
func TestTierStatisticsSeparatesDiscountAndPreservesUnknownOriginal(t *testing.T) {
	var a TierStatisticsAccumulator
	for _, v := range []TierStatistics{
		{Model: "m", Tier: "peak", Discount: "0.5", Amount: "0.1", BeforeAmount: "0.2", Requests: 1},
		{Model: "m", Tier: "peak", Discount: "1", Amount: "0.3", BeforeAmount: "0.3", Requests: 1},
		{Model: "m", Tier: "peak", Discount: "0.5", Amount: "0.2", Requests: 1},
	} {
		if err := a.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	rows := a.Rows()
	if len(rows) != 2 || rows[0].Requests != 2 || rows[0].BeforeAmount != "" || rows[0].Amount != "0.300000000000" {
		t.Fatal(rows)
	}
	if err := a.Add(TierStatistics{Amount: "invalid"}); err == nil {
		t.Fatal("invalid amount accepted")
	}
}
func TestSaveTierStatisticsBothBillPeriods(t *testing.T) {
	for _, period := range []string{"daily", "temporary"} {
		store := &dailyFileStoreStub{}
		err := saveBillingTierStatistics(context.Background(), store, Job{ID: "job", JobType: "user_statement", UsageVersion: 3, BillPeriod: period}, UserDailyFile{BillDay: time.Now(), UserID: 9}, func(visit func(RequestDetail) error) error {
			return visit(RequestDetail{ModelName: "m", Charge: LogCharge{MatchedTier: "低谷", Total: "1.25"}})
		})
		if err != nil || len(store.tiers) != 1 || store.tiers[0].Tier != "低谷" {
			t.Fatal(period, store.tiers, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := saveBillingTierStatistics(ctx, &dailyFileStoreStub{}, Job{JobType: "user_statement", UsageVersion: 3}, UserDailyFile{}, func(v func(RequestDetail) error) error { return v(RequestDetail{}) }); err == nil {
		t.Fatal("ignored cancellation")
	}
}
