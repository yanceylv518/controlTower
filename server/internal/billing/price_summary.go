package billing

import (
	"math/big"
	"strconv"
	"strings"
)

// DiscountGroupKey keeps full-price and each recorded discount in separate
// aggregates. Historical mixed rows have no recoverable per-rate allocation.
func DiscountGroupKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "1.000000"
	}
	if n, ok := new(big.Rat).SetString(value); ok && n.Sign() >= 0 {
		return n.FloatString(6)
	}
	return value
}

func DiscountGroupLabel(value string) string {
	if value == "mixed" {
		return "旧账单未拆分"
	}
	return DiscountLabel(value)
}

// Empty means unknown, not zero. Mixed rates are never averaged.
func MergeDiscount(a, b string) string {
	if a == "" || b == "" {
		return ""
	}
	if a == "mixed" || b == "mixed" {
		return "mixed"
	}
	x, ok := new(big.Rat).SetString(a)
	y, ok2 := new(big.Rat).SetString(b)
	if !ok || !ok2 {
		return ""
	}
	if x.Cmp(y) != 0 {
		return "mixed"
	}
	return x.FloatString(6)
}
func DiscountLabel(v string) string {
	if strings.TrimSpace(v) == "" {
		return "原价"
	}
	if v == "mixed" {
		return "多种折扣"
	}
	r, ok := new(big.Rat).SetString(v)
	if !ok {
		return "—"
	}
	if r.Cmp(big.NewRat(1, 1)) == 0 {
		return "原价"
	}
	f, _ := new(big.Rat).Mul(r, big.NewRat(10, 1)).Float64()
	return strconv.FormatFloat(f, 'f', -1, 64) + " 折"
}
func MergeBefore(a, b string) string {
	x, ok := new(big.Rat).SetString(a)
	y, ok2 := new(big.Rat).SetString(b)
	if !ok || !ok2 {
		return ""
	}
	return new(big.Rat).Add(x, y).FloatString(12)
}
func ChargeOriginal(c LogCharge) (string, string) {
	if c.Settlement == nil {
		return "", ""
	}
	discount := c.Settlement.Discount
	if n, ok := new(big.Rat).SetString(discount); ok {
		discount = n.FloatString(6)
	}
	return c.Settlement.BeforeAmount, discount
}

// OriginalBillingBase is shared by report income/cost presentation. Fallback
// is not guaranteed to be pre-discount when the request has a user discount.
func OriginalBillingBase(log PagedLogRecord, quotaPerUnit string) (string, bool, error) {
	value, err := ResolveSettlement(log, "upstream_statement", 0, nil, quotaPerUnit)
	fallback := false
	if log.QuotaBeforeDiscount == "" && log.UserModelDiscount != "" {
		if d, ok := new(big.Rat).SetString(log.UserModelDiscount); ok {
			fallback = d.Cmp(big.NewRat(1, 1)) != 0
		}
	}
	return value.BeforeAmount, fallback, err
}

// DefaultSettlementPrice fills missing historical discount metadata at full price.
// A recorded non-unit discount still requires its original amount evidence.
func DefaultSettlementPrice(before, discount, amount string) (string, string) {
	if strings.TrimSpace(discount) == "" {
		discount = "1"
	}
	if rate, ok := new(big.Rat).SetString(discount); ok && rate.Cmp(big.NewRat(1, 1)) == 0 && (before == "" || before == "—") {
		before = amount
	}
	return before, discount
}
