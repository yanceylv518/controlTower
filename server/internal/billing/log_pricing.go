package billing

import (
	"database/sql"
	"fmt"
	"math/big"
)

// LogCharge is the user-facing monetary reconstruction of one new-api consume
// log. Ratios are deliberately absent: they are only inputs used to derive the
// effective per-million-token prices captured below.
type LogCharge struct {
	Mode                                                                   string
	MatchedTier                                                            string
	InputPrice, OutputPrice, CacheReadPrice                                string
	CacheWritePrice, CacheWrite5mPrice, CacheWrite1hPrice, PerRequestPrice string
	InputAmount, OutputAmount, CacheReadAmount                             string
	CacheWriteAmount, CacheWrite5mAmount, CacheWrite1hAmount, Total        string
	ReconstructedQuota                                                     string
}

type LogChargeVerification struct {
	Charge          LogCharge
	CalculatedQuota int64
	LoggedQuota     int64
	Difference      int64
	Verified        bool
}

const (
	PricingReasonIncomplete = "billing_price_incomplete"
	PricingReasonMismatch   = "billing_quota_mismatch"
)

// CalculateLogCharge converts the effective ratios recorded on an individual
// new-api log row into model prices, then prices that row's normalized token
// lanes. CT price schedules and CT tiers are intentionally not consulted.
func CalculateLogCharge(log PagedLogRecord, quotaPerUnit string) (LogCharge, error) {
	if log.BillingMode == "tiered_expr" {
		return calculateTieredExprCharge(log, quotaPerUnit)
	}
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		return LogCharge{}, fmt.Errorf("invalid QuotaPerUnit")
	}
	group, err := requiredLogRatio(log.GroupRatio, "group_ratio")
	if err != nil {
		return LogCharge{}, err
	}
	if modelPrice, priceErr := decimalRat(log.ModelPrice); priceErr == nil && modelPrice.Sign() >= 0 {
		total := new(big.Rat).Mul(modelPrice, group)
		return finishLogCharge(LogCharge{Mode: "per_request", PerRequestPrice: total.FloatString(6)}, total, qpu), nil
	}
	modelRatio, err := requiredLogRatio(log.ModelRatio, "model_ratio")
	if err != nil {
		return LogCharge{}, err
	}
	base := new(big.Rat).Quo(new(big.Rat).Mul(modelRatio, big.NewRat(tokensPerMillion, 1)), qpu)
	inputUnit := new(big.Rat).Mul(new(big.Rat).Set(base), group)
	charge := LogCharge{Mode: "token", InputPrice: inputUnit.FloatString(6)}
	inputAmount := tokenCost(nullableInt64(log.PromptTokens), inputUnit)
	charge.InputAmount = FormatAmount(inputAmount, 6)
	total := new(big.Rat).Set(inputAmount)

	if tokens := nullableInt64(log.CompletionTokens); tokens > 0 {
		ratio, ratioErr := requiredLogRatio(log.CompletionRatio, "completion_ratio")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(tokens, unit)
		charge.OutputPrice, charge.OutputAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}
	if log.CacheTokens > 0 {
		ratio, ratioErr := requiredLogRatio(log.CacheRatio, "cache_ratio")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(log.CacheTokens, unit)
		charge.CacheReadPrice, charge.CacheReadAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}
	remainingWrite := log.CacheWriteTokens - log.CacheWrite5mTokens - log.CacheWrite1hTokens
	if remainingWrite < 0 {
		remainingWrite = 0
	}
	if remainingWrite > 0 {
		ratio, ratioErr := requiredLogRatio(log.CacheCreationRatio, "cache_creation_ratio")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(remainingWrite, unit)
		charge.CacheWritePrice, charge.CacheWriteAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}
	if log.CacheWrite5mTokens > 0 {
		raw := log.CacheCreationRatio5m
		if raw == "" {
			raw = log.CacheCreationRatio
		}
		ratio, ratioErr := requiredLogRatio(raw, "cache_creation_ratio_5m")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(log.CacheWrite5mTokens, unit)
		charge.CacheWrite5mPrice, charge.CacheWrite5mAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}
	if log.CacheWrite1hTokens > 0 {
		ratio, ratioErr := requiredLogRatio(log.CacheCreationRatio1h, "cache_creation_ratio_1h")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(log.CacheWrite1hTokens, unit)
		charge.CacheWrite1hPrice, charge.CacheWrite1hAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}
	return finishLogCharge(charge, total, qpu), nil
}

func finishLogCharge(charge LogCharge, total, qpu *big.Rat) LogCharge {
	charge.Total = FormatAmount(total, 6)
	charge.ReconstructedQuota = new(big.Rat).Mul(new(big.Rat).Set(total), qpu).FloatString(6)
	return charge
}

// VerifyLogCharge mirrors new-api's QuotaFromDecimal conversion: decimal
// quota is rounded half away from zero and then saturated to signed int32.
func VerifyLogCharge(log PagedLogRecord, quotaPerUnit string) (LogChargeVerification, error) {
	charge, err := CalculateLogCharge(log, quotaPerUnit)
	if err != nil {
		return LogChargeVerification{}, err
	}
	value, err := decimalRat(charge.ReconstructedQuota)
	if err != nil {
		return LogChargeVerification{}, err
	}
	calculated := quotaFromRat(value)
	result := LogChargeVerification{Charge: charge, CalculatedQuota: calculated, LoggedQuota: log.Quota}
	result.Difference = calculated - log.Quota
	result.Verified = result.Difference == 0
	return result, nil
}

func VerifyLogChargeReason(log PagedLogRecord, quotaPerUnit string) (LogChargeVerification, string) {
	result, err := VerifyLogCharge(log, quotaPerUnit)
	if err != nil {
		return LogChargeVerification{}, PricingReasonIncomplete
	}
	if !result.Verified {
		return result, PricingReasonMismatch
	}
	return result, ""
}

func quotaFromRat(value *big.Rat) int64 {
	sign := value.Sign()
	abs := new(big.Rat).Abs(new(big.Rat).Set(value))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(abs.Num(), abs.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(abs.Denom()) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if sign < 0 {
		quotient.Neg(quotient)
	}
	max, min := big.NewInt(1<<31-1), big.NewInt(-1<<31)
	if quotient.Cmp(max) > 0 {
		return max.Int64()
	}
	if quotient.Cmp(min) < 0 {
		return min.Int64()
	}
	return quotient.Int64()
}

func requiredLogRatio(value, name string) (*big.Rat, error) {
	if value == "" {
		return nil, fmt.Errorf("%s missing", name)
	}
	ratio, err := decimalRat(value)
	if err != nil || ratio.Sign() < 0 {
		return nil, fmt.Errorf("invalid %s", name)
	}
	return ratio, nil
}

func multipliedRat(left, right *big.Rat) *big.Rat {
	return new(big.Rat).Mul(new(big.Rat).Set(left), right)
}

func nullableInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}
