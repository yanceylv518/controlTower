package billing

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"time"
)

// LogCharge is the user-facing monetary reconstruction of one new-api consume
// log. Ratios are deliberately absent: they are only inputs used to derive the
// effective per-million-token prices captured below.
type LogCharge struct {
	Mode                                                                                 string
	MatchedTier                                                                          string
	InputPrice, OutputPrice, CacheReadPrice, ImagePrice                                  string
	CacheWritePrice, CacheWrite5mPrice, CacheWrite1hPrice, PerRequestPrice               string
	InputAmount, OutputAmount, CacheReadAmount, ImageAmount                              string
	CacheWriteAmount, CacheWrite5mAmount, CacheWrite1hAmount, ToolSurchargeAmount, Total string
	ReconstructedQuota                                                                   string
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
	// new-api stores quota as an integer while several pricing paths carry
	// decimal intermediates. A one-unit difference is a rounding boundary, not
	// a materially different charge, and must remain in the normal bill.
	quotaVerificationTolerance int64 = 1
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
		charge := LogCharge{Mode: "per_request", PerRequestPrice: total.FloatString(6)}
		if err = addToolSurcharges(&charge, total, log.ToolSurcharges, group); err != nil {
			return LogCharge{}, err
		}
		return finishLogCharge(charge, total, qpu), nil
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
	if log.ImageInputTokens > 0 {
		ratio, ratioErr := requiredLogRatio(log.ImageRatio, "image_ratio")
		if ratioErr != nil {
			return LogCharge{}, ratioErr
		}
		unit := multipliedRat(inputUnit, ratio)
		amount := tokenCost(log.ImageInputTokens, unit)
		charge.ImagePrice, charge.ImageAmount = unit.FloatString(6), FormatAmount(amount, 6)
		total.Add(total, amount)
	}

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
	if err = addToolSurcharges(&charge, total, log.ToolSurcharges, group); err != nil {
		return LogCharge{}, err
	}
	return finishLogCharge(charge, total, qpu), nil
}

type toolSurchargeItem struct {
	Name  string      `json:"name"`
	Count int64       `json:"count"`
	Price json.Number `json:"price"`
}

// NewAPI records tool prices in currency per 1000 calls. The surcharge is
// independent of token pricing and is multiplied only by the effective group
// ratio before being added to the request total.
func addToolSurcharges(charge *LogCharge, total *big.Rat, raw string, group *big.Rat) error {
	if raw == "" || raw == "null" || raw == "[]" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var items []toolSurchargeItem
	if err := decoder.Decode(&items); err != nil {
		return fmt.Errorf("invalid tool_surcharges: %w", err)
	}
	surcharge := new(big.Rat)
	for _, item := range items {
		if item.Count <= 0 {
			continue
		}
		price, err := decimalRat(item.Price.String())
		if err != nil || price.Sign() <= 0 {
			return fmt.Errorf("invalid tool surcharge price for %s", item.Name)
		}
		amount := new(big.Rat).Mul(price, big.NewRat(item.Count, 1000))
		amount.Mul(amount, group)
		surcharge.Add(surcharge, amount)
	}
	if surcharge.Sign() > 0 {
		charge.ToolSurchargeAmount = FormatAmount(surcharge, 6)
		total.Add(total, surcharge)
	}
	return nil
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
	difference := result.Difference
	if difference < 0 {
		difference = -difference
	}
	result.Verified = difference <= quotaVerificationTolerance || cacheOverflowExplainsLoggedQuota(log, charge, quotaPerUnit, result.LoggedQuota)
	return result, nil
}

// cacheOverflowExplainsLoggedQuota recognizes rows settled by older new-api
// versions before rc23 added max(baseTokens, 0). CT still bills with rc23's
// clamped base-input lane, but this fully explained legacy difference is not a
// reconciliation failure and the order remains in the normal statement.
func cacheOverflowExplainsLoggedQuota(log PagedLogRecord, charge LogCharge, quotaPerUnit string, loggedQuota int64) bool {
	if charge.Mode != "token" || !log.SourcePromptTokens.Valid || log.UsageSemantic == "anthropic" ||
		log.ImageInputTokens > 0 || log.ImageOutputTokens > 0 || log.AudioInputTokens > 0 || log.AudioOutputTokens > 0 ||
		nullableInt64(log.PromptTokens) != 0 {
		return false
	}
	overflowedBase := log.SourcePromptTokens.Int64 - log.CacheTokens - log.CacheWriteTokens
	if overflowedBase >= 0 {
		return false
	}
	inputPrice, err := decimalRat(charge.InputPrice)
	if err != nil {
		return false
	}
	total, err := decimalRat(charge.Total)
	if err != nil {
		return false
	}
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		return false
	}
	legacyAdjustment := new(big.Rat).Mul(big.NewRat(overflowedBase, 1), inputPrice)
	legacyAdjustment.Quo(legacyAdjustment, big.NewRat(tokensPerMillion, 1))
	legacyQuota := new(big.Rat).Mul(new(big.Rat).Add(total, legacyAdjustment), qpu)
	difference := quotaFromRat(legacyQuota) - loggedQuota
	if difference < 0 {
		difference = -difference
	}
	return difference <= quotaVerificationTolerance
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

// FallbackLogCharge keeps a successfully charged source order in the bill when
// historical ratios are incomplete. The logged quota is the authoritative
// amount; the order is separately marked for reconciliation.
func FallbackLogCharge(log PagedLogRecord, quotaPerUnit string) LogChargeVerification {
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		qpu, _ = decimalRat(defaultQuotaPerUnit)
	}
	amount := new(big.Rat).Quo(big.NewRat(log.Quota, 1), qpu)
	return LogChargeVerification{
		Charge:          LogCharge{Mode: "logged_quota_fallback", Total: FormatAmount(amount, 6)},
		CalculatedQuota: log.Quota, LoggedQuota: log.Quota, Verified: false,
	}
}

func NewReconciliationOrder(job Job, log PagedLogRecord, result LogChargeVerification, reason, quotaPerUnit string) ReconciliationOrder {
	qpu, err := decimalRat(quotaPerUnit)
	if err != nil || qpu.Sign() <= 0 {
		qpu, _ = decimalRat(defaultQuotaPerUnit)
	}
	logged := new(big.Rat).Quo(big.NewRat(log.Quota, 1), qpu)
	calculated, _ := decimalRat(result.Charge.Total)
	if calculated == nil {
		calculated = new(big.Rat)
	}
	return ReconciliationOrder{
		JobID: job.ID, InstanceID: job.InstanceID, SourceLogID: log.ID, CreatedUnix: log.CreatedUnix,
		RequestID: log.RequestID, UpstreamRequestID: log.UpstreamRequestID, UserID: log.UserID, Username: log.Username,
		TokenID: log.TokenID, TokenName: log.TokenName, ChannelID: log.ChannelID, ChannelName: log.ChannelName, ModelName: log.ModelName,
		PromptTokens: nullableInt64(log.PromptTokens), CompletionTokens: nullableInt64(log.CompletionTokens), CacheReadTokens: log.CacheTokens, CacheWriteTokens: log.CacheWriteTokens,
		CalculatedQuota: result.CalculatedQuota, LoggedQuota: log.Quota, QuotaDifference: result.CalculatedQuota - log.Quota,
		CalculatedAmount: FormatAmount(calculated, 6), LoggedAmount: FormatAmount(logged, 6), AmountDifference: FormatAmount(new(big.Rat).Sub(calculated, logged), 6),
		Reason: reason, DetectedAt: time.Now().UTC(),
	}
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
