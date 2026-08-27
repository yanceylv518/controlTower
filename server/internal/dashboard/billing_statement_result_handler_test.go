package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestUserStatementGroupsAcrossChannelsWithoutDiscount(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, billing.BusinessLocation)
	rows := []billing.StatementAggregateRow{
		{AggregateRow: billing.AggregateRow{Day: day, ModelName: "kimi-k3", RequestCount: 2, PromptTokens: 10, Amount: "1.25000000"}, ChannelID: 146, ChannelName: "a"},
		{AggregateRow: billing.AggregateRow{Day: day, ModelName: "kimi-k3", RequestCount: 3, PromptTokens: 20, Amount: "2.75000000"}, ChannelID: 176, ChannelName: "b"},
	}
	discounts := []billing.StatementDiscount{{DiscountType: billing.DiscountUpstreamChannel, ChannelID: 146, Discount: "0.500000", EffectiveFrom: day}}
	grouped := groupStatementRows(billing.Job{JobType: "user_statement"}, rows, discounts, false)
	if len(grouped) != 1 {
		t.Fatalf("group count=%d, want 1", len(grouped))
	}
	got := grouped[0]
	if got.Row.ChannelID != 0 || got.Row.ChannelName != "" || got.Row.RequestCount != 5 || got.Row.PromptTokens != 30 || got.Row.Amount != "4.00000000" || got.Discount != "1.000000" {
		t.Fatalf("unexpected user aggregate: %#v", got)
	}
}

func TestUpstreamStatementKeepsChannelDiscountGroups(t *testing.T) {
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, billing.BusinessLocation)
	rows := []billing.StatementAggregateRow{
		{AggregateRow: billing.AggregateRow{Day: day, ModelName: "kimi-k3", RequestCount: 2, Amount: "1.00000000"}, ChannelID: 146, ChannelName: "a"},
		{AggregateRow: billing.AggregateRow{Day: day, ModelName: "kimi-k3", RequestCount: 3, Amount: "2.00000000"}, ChannelID: 176, ChannelName: "b"},
	}
	discounts := []billing.StatementDiscount{{DiscountType: billing.DiscountUpstreamChannel, ChannelID: 146, Discount: "0.500000", EffectiveFrom: day}}
	grouped := groupStatementRows(billing.Job{JobType: "upstream_statement"}, rows, discounts, false)
	if len(grouped) != 2 || grouped[0].Row.ChannelID != 146 || grouped[0].Discount != "0.500000" || grouped[1].Row.ChannelID != 176 || grouped[1].Discount != "1.000000" {
		t.Fatalf("unexpected upstream aggregates: %#v", grouped)
	}
}
