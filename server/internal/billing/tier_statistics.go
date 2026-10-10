package billing

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"
)

// TierStatistics contains saved settlement amounts, never a price reconstruction.
type TierStatistics struct {
	Model      string `json:"model"`
	Tier       string `json:"tier"`
	Discount   string `json:"discount"`
	Requests   int64  `json:"requests"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
	MultimediaUsage
	Amount       string     `json:"amount"`
	BeforeAmount string     `json:"before_amount"`
	Prices       UnitPrices `json:"prices,omitempty"`
}

type tierStatisticsKey struct{ model, tier, discount string }
type TierStatisticsAccumulator struct {
	groups map[tierStatisticsKey]*TierStatistics
}

func (a *TierStatisticsAccumulator) AddDetail(v RequestDetail) error {
	before, discount := ChargeOriginal(v.Charge)
	return a.Add(TierStatistics{Model: v.ModelName, Tier: v.Charge.MatchedTier, Discount: DiscountGroupKey(discount), Requests: 1,
		Input: v.PromptTokens, Output: v.CompletionTokens, CacheRead: v.CacheReadTokens, CacheWrite: v.CacheWriteTokens,
		MultimediaUsage: v.MultimediaUsage, Amount: v.Charge.Total, BeforeAmount: before, Prices: v.Charge.UnitPrices})
}
func (a *TierStatisticsAccumulator) Add(v TierStatistics) error {
	amount, ok := new(big.Rat).SetString(v.Amount)
	if !ok {
		return fmt.Errorf("invalid tier settlement amount")
	}
	if a.groups == nil {
		a.groups = map[tierStatisticsKey]*TierStatistics{}
	}
	key := tierStatisticsKey{v.Model, v.Tier, v.Discount}
	g := a.groups[key]
	if g == nil {
		g = &TierStatistics{Model: v.Model, Tier: v.Tier, Discount: v.Discount, Amount: "0", BeforeAmount: v.BeforeAmount}
		a.groups[key] = g
	} else {
		g.BeforeAmount = MergeBefore(g.BeforeAmount, v.BeforeAmount)
	}
	current, _ := new(big.Rat).SetString(g.Amount)
	g.Amount = new(big.Rat).Add(current, amount).FloatString(12)
	g.Requests += v.Requests
	g.Input += v.Input
	g.Output += v.Output
	g.CacheRead += v.CacheRead
	g.CacheWrite += v.CacheWrite
	g.MultimediaUsage.Add(v.MultimediaUsage)
	g.Prices.Merge(v.Prices)
	return nil
}
func (a *TierStatisticsAccumulator) Rows() []TierStatistics {
	rows := make([]TierStatistics, 0, len(a.groups))
	for _, v := range a.groups {
		rows = append(rows, *v)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		if rows[i].Tier != rows[j].Tier {
			return rows[i].Tier < rows[j].Tier
		}
		return rows[i].Discount < rows[j].Discount
	})
	return rows
}
func TierLabel(tier string) string {
	if tier == "" {
		return "未记录档位"
	}
	return tier
}

type TierStatisticsStore interface {
	PutBillingTierStatistics(context.Context, string, time.Time, int64, []TierStatistics) error
}

func saveBillingTierStatistics(ctx context.Context, store any, job Job, group UserDailyFile, iterate func(func(RequestDetail) error) error) error {
	if job.JobType != "user_statement" || job.UsageVersion < SettlementUsageVersion {
		return nil
	}
	target, ok := store.(TierStatisticsStore)
	if !ok {
		return fmt.Errorf("billing tier statistics store unavailable")
	}
	var a TierStatisticsAccumulator
	if err := iterate(func(v RequestDetail) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return a.AddDetail(v)
	}); err != nil {
		return err
	}
	return target.PutBillingTierStatistics(ctx, job.ID, group.BillDay, group.UserID, a.Rows())
}

func TierStatisticsByTier(rows []TierStatistics) ([]TierStatistics, error) {
	var a TierStatisticsAccumulator
	for _, v := range rows {
		v.Model = ""
		if err := a.Add(v); err != nil {
			return nil, err
		}
	}
	return a.Rows(), nil
}
