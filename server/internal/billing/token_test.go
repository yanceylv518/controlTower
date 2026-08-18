package billing

import (
	"testing"
	"time"
)

func TestTokenRowsConserveUserTotalsAndSplitTokens(t *testing.T) {
	rows := []TokenDailyRow{{UserID: 1, TokenID: 10, ModelName: "m", RequestCount: 2, PromptTokens: 3, Quota: 4}, {UserID: 1, TokenID: 11, ModelName: "m", RequestCount: 5, PromptTokens: 7, Quota: 8}}
	aggs := TokenRowsAsAggregates(rows)
	var requests, prompt, quota int64
	for _, v := range aggs {
		requests += v.RequestCount
		prompt += v.PromptTokens
		quota += v.Quota
	}
	if requests != 7 || prompt != 10 || quota != 12 {
		t.Fatalf("conservation failed: %d/%d/%d", requests, prompt, quota)
	}
	summaries := BuildTokenSummaries(rows, nil, nil, nil)
	if len(summaries) != 2 {
		t.Fatalf("tokens=%#v", summaries)
	}
}

func TestTokenPricingUsesSamePriceSourceAsUserRows(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, BusinessLocation)
	token := TokenDailyRow{UserID: 1, TokenID: 2, ModelName: "m", GroupName: "default", Day: day, PromptTokens: 1_000_000, RequestCount: 1}
	prices := []PriceRecord{{ModelName: "m", Price: Price{EffectiveFrom: day, TierFrom: 0, Input: "2", Output: "0", Cache: "0"}}}
	tokenDetail := BuildDetails(TokenRowsAsAggregates([]TokenDailyRow{token}), prices, nil, nil)
	userDetail := BuildDetails([]AggregateRow{{UserID: 1, ModelName: "m", GroupName: "default", Day: day, PromptTokens: 1_000_000, RequestCount: 1}}, prices, nil, nil)
	if tokenDetail[0].Amount != userDetail[0].Amount || tokenDetail[0].PriceSource != userDetail[0].PriceSource {
		t.Fatalf("token=%#v user=%#v", tokenDetail, userDetail)
	}
}
