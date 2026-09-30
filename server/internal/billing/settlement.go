package billing

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// SettlementUsageVersion evaluates historical discounts per request. Older
// statements retain their original export and calculation semantics.
const SettlementUsageVersion = 3

type Settlement struct {
	Amount       string
	BeforeAmount string
	Discount     string
	Source       string
	RuleID       int64
}

// ResolveSettlement is shared by statements and reports. Rules use [from,to)
// instants, never the report's calendar date or today's NewAPI configuration.
func ResolveSettlement(log PagedLogRecord, kind string, subjectID int64, rules []StatementDiscount, quotaPerUnit string) (Settlement, error) {
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 || log.Quota < 0 {
		return Settlement{}, fmt.Errorf("invalid settlement quota")
	}
	actual := new(big.Rat).Quo(big.NewRat(log.Quota, 1), qpu)
	discount := big.NewRat(1, 1)
	if kind == "user_statement" && strings.TrimSpace(log.UserModelDiscount) != "" {
		discount, err = decimalRat(log.UserModelDiscount)
		if err != nil || discount.Sign() < 0 || discount.Cmp(big.NewRat(1, 1)) > 0 {
			return Settlement{}, fmt.Errorf("order %d: invalid user_model_discount", log.ID)
		}
	}
	out := Settlement{Amount: actual.FloatString(12), BeforeAmount: actual.FloatString(12), Discount: "1", Source: "newapi"}
	if kind == "user_statement" && discount.Cmp(big.NewRat(1, 1)) != 0 {
		out.Discount = discount.FloatString(6)
		// The charged quota is rounded. Do not infer an exact original amount
		// by dividing the discounted quota (including a zero discount).
		out.BeforeAmount = ""
		if log.QuotaBeforeDiscount != "" {
			base, e := decimalRat(log.QuotaBeforeDiscount)
			if e == nil && base.Sign() >= 0 {
				out.BeforeAmount = new(big.Rat).Quo(base, qpu).FloatString(12)
			}
		}
		return out, nil
	}
	var matched *StatementDiscount
	at := time.Unix(log.CreatedUnix, 0)
	for i := range rules {
		r := &rules[i]
		if r.SubjectID != subjectID || at.Before(r.EffectiveFrom) || (r.EffectiveTo != nil && !at.Before(*r.EffectiveTo)) {
			continue
		}
		if kind == "user_statement" && (r.DiscountType != DiscountUserModel || r.ModelName != log.ModelName) {
			continue
		}
		if kind == "upstream_statement" && (r.DiscountType != DiscountUpstreamChannel || r.ChannelID != log.ChannelID) {
			continue
		}
		if matched != nil {
			return Settlement{}, fmt.Errorf("order %d: overlapping settlement rules", log.ID)
		}
		matched = r
	}
	if kind == "user_statement" && matched == nil {
		return out, nil
	}
	if kind != "user_statement" && kind != "upstream_statement" {
		return Settlement{}, fmt.Errorf("invalid settlement kind")
	}
	// Upstream uses per-request pre-discount quota when present, otherwise the
	// request quota. User model discounts do not affect this fallback.
	base := new(big.Rat).SetInt64(log.Quota)
	if log.QuotaBeforeDiscount != "" {
		base, err = decimalRat(log.QuotaBeforeDiscount)
		if err != nil || base.Sign() < 0 {
			return Settlement{}, fmt.Errorf("order %d: invalid quota_before_discount", log.ID)
		}
	}
	out.BeforeAmount = new(big.Rat).Quo(base, qpu).FloatString(12)
	// No channel rule at the request time means full price.
	rate := big.NewRat(1, 1)
	if matched != nil {
		rate, err = decimalRat(matched.Discount)
		if err != nil || rate.Sign() < 0 || rate.Cmp(big.NewRat(1, 1)) > 0 {
			return Settlement{}, fmt.Errorf("invalid settlement rule %d", matched.SourceRuleID)
		}
		out.RuleID, out.Source = matched.SourceRuleID, "ct_supplement"
	}
	if kind == "upstream_statement" {
		out.Source = "upstream_channel"
	}
	out.Discount = rate.FloatString(6)
	out.Amount = new(big.Rat).Quo(new(big.Rat).Mul(base, rate), qpu).FloatString(12)
	return out, nil
}
