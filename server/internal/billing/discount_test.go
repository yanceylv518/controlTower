package billing

import (
	"testing"
	"time"
)

func TestUserStatementDefaultsToFullPriceWithoutConfiguredFeature(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	rules := []StatementDiscount{{DiscountType: DiscountUpstreamChannel, ChannelID: 3, Discount: "0.850000", EffectiveFrom: from, EffectiveTo: &to}}
	if got := DiscountForDay(rules, "user_statement", 0, "m", time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)); got != "1.000000" {
		t.Fatalf("user discount=%s", got)
	}
}

func TestDiscountForDayMatchesUpstreamChannel(t *testing.T) {
	rules := []StatementDiscount{{DiscountType: DiscountUpstreamChannel, ChannelID: 3, Discount: "0.900000", EffectiveFrom: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}}
	if got := DiscountForDay(rules, "upstream_statement", 3, "any", time.Now()); got != "0.900000" {
		t.Fatalf("discount=%s", got)
	}
	if got := DiscountForDay(rules, "upstream_statement", 4, "any", time.Now()); got != "1.000000" {
		t.Fatalf("wrong channel discount=%s", got)
	}
}
