package billing

import (
	"testing"
	"time"
)

func TestAmountMatchesAcceptedExampleDigitForDigit(t *testing.T) {
	amount, err := Amount(Usage{PromptTokens: 298, CacheTokens: 8507, CompletionTokens: 194}, Price{Input: "2.10", Cache: "0.42", Output: "8.40"}, "1")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatAmount(amount, 6); got != "0.005828" {
		t.Fatalf("amount = %s, want 0.005828", got)
	}
}

func TestAmountSubtractsCacheWhenItIsPartOfPrompt(t *testing.T) {
	amount, err := Amount(Usage{PromptTokens: 1000, CacheTokens: 400}, Price{Input: "2", Cache: "1", Output: "0"}, "1.5")
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatAmount(amount, 6); got != "0.002400" {
		t.Fatalf("amount = %s, want 0.002400", got)
	}
}

func TestSelectPriceUsesCurrentScheduleRegardlessOfLogDate(t *testing.T) {
	day1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	prices := []Price{{EffectiveFrom: day1, TierFrom: 0, Input: "1"}, {EffectiveFrom: day1, TierFrom: 128000, Input: "2"}, {EffectiveFrom: day2, TierFrom: 0, Input: "3"}, {EffectiveFrom: day2, TierFrom: 128000, Input: "4"}}
	price, ok := SelectPrice(prices, day2, 128000)
	if !ok || price.Input != "4" {
		t.Fatalf("selected %#v, ok=%v", price, ok)
	}
	price, ok = SelectPrice(prices, day1, 127999)
	if !ok || price.Input != "3" {
		t.Fatalf("selected %#v, ok=%v", price, ok)
	}
}

func TestValidateTierSchedule(t *testing.T) {
	if err := ValidateTierSchedule([]Price{{TierFrom: 0}, {TierFrom: 128000}}); err != nil {
		t.Fatalf("valid schedule: %v", err)
	}
	for _, prices := range [][]Price{{{TierFrom: 1}}, {{TierFrom: 0}, {TierFrom: 0}}} {
		if err := ValidateTierSchedule(prices); err == nil {
			t.Fatalf("expected invalid schedule: %#v", prices)
		}
	}
}
