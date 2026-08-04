package billing

import (
	"testing"
	"time"
)

func TestBuildChannelSummaryAppliesDiscount(t *testing.T) {
	day := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	rows := []AggregateRow{{UserID: 9, Username: "渠道A", ModelName: "m", Day: day, RequestCount: 1, PromptTokens: 1_000_000}}
	prices := []PriceRecord{{InstanceID: "s", ModelName: "m", Price: Price{EffectiveFrom: day, TierFrom: 0, Input: "2", Output: "0", Cache: "0"}}}
	got := BuildChannelSummary(rows, prices, nil, nil, map[int64]ChannelSetting{9: {Discount: "0.5"}})
	if len(got) != 1 || got[0].Amount != "2.000000" || got[0].DiscountedAmount != "1.000000" {
		t.Fatalf("unexpected summary: %+v", got)
	}
}
