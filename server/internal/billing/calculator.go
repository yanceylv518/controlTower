package billing

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"
)

const tokensPerMillion int64 = 1_000_000

type Usage struct{ PromptTokens, CompletionTokens, CacheTokens int64 }
type Price struct {
	EffectiveFrom time.Time `json:"effective_from"`
	TierFrom      int64     `json:"tier_from"`
	Input         string    `json:"input_price"`
	Output        string    `json:"output_price"`
	Cache         string    `json:"cache_price"`
}

func ValidateRatio(value string) error {
	ratio, err := decimalRat(value)
	if err != nil {
		return err
	}
	if ratio.Cmp(big.NewRat(10000, 1)) > 0 {
		return errors.New("ratio must not exceed 10000")
	}
	return nil
}

func ValidatePrice(price Price) error {
	for _, value := range []string{price.Input, price.Output, price.Cache} {
		if _, err := decimalRat(value); err != nil {
			return err
		}
	}
	return nil
}

type PriceRecord struct {
	InstanceID string `json:"instance_id"`
	ModelName  string `json:"model_name"`
	Price
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

type GroupRatio struct {
	InstanceID string    `json:"instance_id"`
	GroupName  string    `json:"group_name"`
	Ratio      string    `json:"ratio"`
	UpdatedAt  time.Time `json:"updated_at"`
	UpdatedBy  string    `json:"updated_by"`
}

type DailyRow struct {
	InstanceID       string
	UserID           int64
	Username         string
	ModelName        string
	GroupName        string
	TierFrom         int64
	Day              time.Time
	RequestCount     int64
	PromptTokens     int64
	CompletionTokens int64
	CacheTokens      int64
	Quota            int64
	UpdatedAt        time.Time
}

// SelectPrice pins one effective price schedule before selecting its tier.
func SelectPrice(prices []Price, day time.Time, promptTokens int64) (Price, bool) {
	var effective time.Time
	for _, price := range prices {
		if !price.EffectiveFrom.After(day) && (effective.IsZero() || price.EffectiveFrom.After(effective)) {
			effective = price.EffectiveFrom
		}
	}
	if effective.IsZero() {
		return Price{}, false
	}
	eligible := make([]Price, 0, len(prices))
	for _, price := range prices {
		if price.EffectiveFrom.Equal(effective) && price.TierFrom <= promptTokens {
			eligible = append(eligible, price)
		}
	}
	if len(eligible) == 0 {
		return Price{}, false
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].TierFrom > eligible[j].TierFrom })
	return eligible[0], true
}

func ValidateTierSchedule(prices []Price) error {
	if len(prices) == 0 {
		return errors.New("at least one tier is required")
	}
	tiers := make([]int64, len(prices))
	for i := range prices {
		tiers[i] = prices[i].TierFrom
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i] < tiers[j] })
	if tiers[0] != 0 {
		return errors.New("first tier must start at 0")
	}
	for i := 1; i < len(tiers); i++ {
		if tiers[i] <= tiers[i-1] {
			return errors.New("tier boundaries must be strictly increasing")
		}
	}
	return nil
}

// Amount returns an exact rational amount. When cache tokens exceed prompt
// tokens (a shape emitted by some new-api versions), prompt is already the
// non-cache component and must not be erased by subtraction.
func Amount(usage Usage, price Price, ratio string) (*big.Rat, error) {
	inputPrice, err := decimalRat(price.Input)
	if err != nil {
		return nil, err
	}
	outputPrice, err := decimalRat(price.Output)
	if err != nil {
		return nil, err
	}
	cachePrice, err := decimalRat(price.Cache)
	if err != nil {
		return nil, err
	}
	groupRatio, err := decimalRat(ratio)
	if err != nil {
		return nil, err
	}
	nonCache := usage.PromptTokens
	if usage.CacheTokens <= usage.PromptTokens {
		nonCache -= usage.CacheTokens
	}
	if nonCache < 0 {
		nonCache = 0
	}
	total := new(big.Rat)
	total.Add(total, tokenCost(nonCache, inputPrice))
	total.Add(total, tokenCost(usage.CacheTokens, cachePrice))
	total.Add(total, tokenCost(usage.CompletionTokens, outputPrice))
	return total.Mul(total, groupRatio), nil
}

func FormatAmount(amount *big.Rat, places int) string {
	if amount == nil {
		return ""
	}
	return amount.FloatString(places)
}

func tokenCost(tokens int64, price *big.Rat) *big.Rat {
	if tokens < 0 {
		tokens = 0
	}
	result := new(big.Rat).Mul(new(big.Rat).SetInt64(tokens), price)
	return result.Quo(result, new(big.Rat).SetInt64(tokensPerMillion))
}

func decimalRat(value string) (*big.Rat, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("empty decimal")
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok || result.Sign() < 0 {
		return nil, errors.New("invalid non-negative decimal: " + value)
	}
	return result, nil
}
