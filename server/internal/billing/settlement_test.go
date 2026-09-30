package billing

import (
	"testing"
	"time"
)

func TestSettlementHistoricalPriorityAndMinuteBoundaries(t *testing.T) {
	from := time.Date(2026, 9, 1, 10, 15, 0, 0, BusinessLocation)
	to := from.Add(time.Minute)
	rules := []StatementDiscount{{DiscountType: DiscountUserModel, SubjectID: 7, ModelName: "model-a", Discount: "0.8", EffectiveFrom: from, EffectiveTo: &to, SourceRuleID: 9}}
	for _, tc := range []struct {
		name, discount, before, want string
		offset                       int64
	}{
		{"missing-before", "", "", "100.000000000000", -1},
		{"missing-at-start", "", "", "80.000000000000", 0},
		{"one-at-start", "1.000000", "10000", "80.000000000000", 0},
		{"one-before-end", "1", "", "80.000000000000", 59},
		{"one-at-end", "1", "", "100.000000000000", 60},
		{"newapi-wins", "0.9", "11111", "100.000000000000", 0},
		{"newapi-zero-wins", "0", "11111", "100.000000000000", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ResolveSettlement(PagedLogRecord{UserID: 7, ModelName: "model-a", CreatedUnix: from.Unix() + tc.offset, Quota: 10000, UserModelDiscount: tc.discount, QuotaBeforeDiscount: tc.before}, "user_statement", 7, rules, "100")
			if err != nil || v.Amount != tc.want {
				t.Fatalf("got %+v %v, want %s", v, err, tc.want)
			}
		})
	}
}

func TestSettlementUpstreamUsesBeforeDiscount(t *testing.T) {
	log := PagedLogRecord{ID: 1, Quota: 500, QuotaBeforeDiscount: "1000", UserModelDiscount: "0.5", ChannelID: 252, CreatedUnix: 100}
	rules := []StatementDiscount{{DiscountType: DiscountUpstreamChannel, SubjectID: 8, ChannelID: 252, Discount: "0.7", EffectiveFrom: time.Unix(0, 0)}}
	v, err := ResolveSettlement(log, "upstream_statement", 8, rules, "100")
	if err != nil || v.Amount != "7.000000000000" || v.BeforeAmount != "10.000000000000" {
		t.Fatalf("%+v %v", v, err)
	}
	log.QuotaBeforeDiscount = ""
	if v, err = ResolveSettlement(log, "upstream_statement", 8, rules, "100"); err != nil || v.Amount != "3.500000000000" {
		t.Fatal("missing pre-discount quota did not fall back to request quota", v, err)
	}
	log.QuotaBeforeDiscount = "0"
	if v, err = ResolveSettlement(log, "upstream_statement", 8, rules, "100"); err != nil || v.Amount != "0.000000000000" {
		t.Fatal("explicit zero must not fall back", v, err)
	}
	log.UserModelDiscount = "invalid"
	if _, err = ResolveSettlement(log, "user_statement", 8, rules, "100"); err == nil {
		t.Fatal("invalid evidence silently treated as no discount")
	}
}

func TestSettlementScopesAndOverlap(t *testing.T) {
	log := PagedLogRecord{Quota: 100, ModelName: "m", CreatedUnix: 10}
	rule := StatementDiscount{DiscountType: DiscountUserModel, SubjectID: 1, ModelName: "m", Discount: "0.8", EffectiveFrom: time.Unix(0, 0)}
	v, err := ResolveSettlement(log, "user_statement", 2, []StatementDiscount{rule}, "100")
	if err != nil || v.Amount != "1.000000000000" {
		t.Fatal(v, err)
	}
	if _, err = ResolveSettlement(log, "user_statement", 1, []StatementDiscount{rule, rule}, "100"); err == nil {
		t.Fatal("ambiguous rules accepted")
	}
}

func TestUpstreamMissingDiscountUsesFullPrice(t *testing.T) {
	end := time.Unix(200, 0)
	rule := StatementDiscount{DiscountType: DiscountUpstreamChannel, SubjectID: 8, ChannelID: 252, Discount: "0.7", EffectiveFrom: time.Unix(100, 0), EffectiveTo: &end}
	for _, tc := range []struct {
		name, before, want, discount string
		at, channel                  int64
		rules                        []StatementDiscount
	}{
		{"no-rule", "1000", "10.000000000000", "1.000000", 150, 252, nil},
		{"no-base", "", "5.000000000000", "1.000000", 150, 252, nil},
		{"zero-base", "0", "0.000000000000", "1.000000", 150, 252, nil},
		{"before-start", "1000", "10.000000000000", "1.000000", 99, 252, []StatementDiscount{rule}},
		{"at-end", "1000", "10.000000000000", "1.000000", 200, 252, []StatementDiscount{rule}},
		{"other-channel", "1000", "10.000000000000", "1.000000", 150, 253, []StatementDiscount{rule}},
		{"at-start", "1000", "7.000000000000", "0.700000", 100, 252, []StatementDiscount{rule}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, e := ResolveSettlement(PagedLogRecord{Quota: 500, QuotaBeforeDiscount: tc.before, UserModelDiscount: "0.5", ChannelID: tc.channel, CreatedUnix: tc.at}, "upstream_statement", 8, tc.rules, "100")
			if e != nil || v.Amount != tc.want || v.Discount != tc.discount {
				t.Fatal(v, e)
			}
		})
	}
}

func TestUserAndUpstreamPerRequestPricingIndependent(t *testing.T) {
	rules := []StatementDiscount{
		{DiscountType: DiscountUpstreamChannel, SubjectID: 8, ChannelID: 252, Discount: "0.7", EffectiveFrom: time.Unix(0, 0)},
		{DiscountType: DiscountUpstreamChannel, SubjectID: 8, ChannelID: 253, Discount: "0.4", EffectiveFrom: time.Unix(0, 0)},
		{DiscountType: DiscountUserModel, SubjectID: 7, ModelName: "b", Discount: "0.8", EffectiveFrom: time.Unix(0, 0)},
	}
	cases := []struct {
		log            PagedLogRecord
		user, upstream string
	}{
		{PagedLogRecord{UserID: 7, ChannelID: 252, ModelName: "a", CreatedUnix: 100, Quota: 500, QuotaBeforeDiscount: "1000", UserModelDiscount: "0.5"}, "5.000000000000", "7.000000000000"},
		{PagedLogRecord{UserID: 7, ChannelID: 253, ModelName: "b", CreatedUnix: 100, Quota: 2000, QuotaBeforeDiscount: "2000", UserModelDiscount: "1"}, "16.000000000000", "8.000000000000"},
	}
	for _, tc := range cases {
		user, e := ResolveSettlement(tc.log, "user_statement", 7, rules, "100")
		if e != nil || user.Amount != tc.user {
			t.Fatal(user, e)
		}
		upstream, e := ResolveSettlement(tc.log, "upstream_statement", 8, rules, "100")
		if e != nil || upstream.Amount != tc.upstream {
			t.Fatal(upstream, e)
		}
	}
}

func TestOriginalAmountEvidence(t *testing.T) {
	for _, tc := range []struct{ discount, before, want string }{{"0.8", "1000", "10.000000000000"}, {"0.8", "", ""}, {"0", "", ""}, {"0", "0", "0.000000000000"}, {"1", "", "8.000000000000"}, {"", "", "8.000000000000"}} {
		got, err := ResolveSettlement(PagedLogRecord{Quota: 800, UserModelDiscount: tc.discount, QuotaBeforeDiscount: tc.before}, "user_statement", 1, nil, "100")
		if err != nil || got.BeforeAmount != tc.want || got.Amount != "8.000000000000" {
			t.Fatalf("%+v: %+v %v", tc, got, err)
		}
	}
	if MergeDiscount("0.8", "0.800000") != "0.800000" || MergeDiscount("0.8", "1") != "mixed" || MergeDiscount("", "1") != "" {
		t.Fatal("incorrect discount merge")
	}
	if MergeBefore("", "1") != "" || MergeBefore("0", "1") != "1.000000000000" {
		t.Fatal("unknown original must not become zero")
	}
}
func TestSharedOriginalBase(t *testing.T) {
	for _, tc := range []struct {
		before, discount, want string
		fallback               bool
	}{{"1000", "0.8", "10.000000000000", false}, {"", "0.8", "8.000000000000", true}, {"", "1", "8.000000000000", false}, {"", "", "8.000000000000", false}, {"0", "0", "0.000000000000", false}} {
		got, fallback, err := OriginalBillingBase(PagedLogRecord{Quota: 800, QuotaBeforeDiscount: tc.before, UserModelDiscount: tc.discount}, "100")
		if err != nil || got != tc.want || fallback != tc.fallback {
			t.Fatalf("%+v: %s %v %v", tc, got, fallback, err)
		}
	}
}

func TestDefaultSettlementPrice(t *testing.T) {
	for _, tc := range []struct{ before, discount, wantBefore, wantDiscount string }{
		{"", "", "8", "1"}, {"—", "", "8", "1"}, {"10", "", "10", "1"}, {"", "1.000000", "8", "1.000000"}, {"", "0.8", "", "0.8"}, {"", "0", "", "0"}, {"", "mixed", "", "mixed"},
	} {
		before, discount := DefaultSettlementPrice(tc.before, tc.discount, "8")
		if before != tc.wantBefore || discount != tc.wantDiscount {
			t.Fatalf("%+v got %s %s", tc, before, discount)
		}
	}
}

func TestDiscountLabelDefaultsToFullPrice(t *testing.T) {
	for _, v := range []string{"", " ", "1", "1.000000"} {
		if got := DiscountLabel(v); got != "原价" {
			t.Fatalf("%q: %s", v, got)
		}
	}
	if DiscountLabel("0") != "0 折" || DiscountLabel("0.8") != "8 折" || DiscountLabel("mixed") != "多种折扣" {
		t.Fatal("explicit discount changed")
	}
}
