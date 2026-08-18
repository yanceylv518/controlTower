package billing

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"
)

const tokensPerMillion int64 = 1_000_000

// Usage contains already-normalized billing lanes. PromptTokens is ordinary
// (non-cache) input; cache reads and writes are never included in it.
type Usage struct {
	PromptTokens, CompletionTokens, CacheTokens              int64
	CacheWriteTokens, CacheWrite5mTokens, CacheWrite1hTokens int64
}
type Price struct {
	EffectiveFrom time.Time `json:"effective_from"`
	TierFrom      int64     `json:"tier_from"`
	Input         string    `json:"input_price"`
	Output        string    `json:"output_price"`
	Cache         string    `json:"cache_price"`
	CacheWrite    string    `json:"cache_write_price"`
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
	for _, value := range []string{price.Input, price.Output, price.Cache, decimalOrZero(price.CacheWrite)} {
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
	InstanceID         string
	UserID             int64
	Username           string
	ModelName          string
	GroupName          string
	TierFrom           int64
	Day                time.Time
	RequestCount       int64
	PromptTokens       int64
	CompletionTokens   int64
	CacheTokens        int64
	CacheWriteTokens   int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	Quota              int64
	UpdatedAt          time.Time
}

type TokenDailyRow struct {
	InstanceID, Username, TokenName, ModelName, GroupName string
	UserID, TokenID, TierFrom                             int64
	Day                                                   time.Time
	RequestCount, PromptTokens, CompletionTokens          int64
	CacheTokens, CacheWriteTokens                         int64
	CacheWrite5mTokens, CacheWrite1hTokens, Quota         int64
	UpdatedAt                                             time.Time
}

// SelectPrice uses the most recently saved price schedule and then selects its
// tier. Billing is priced when it is generated; the log date is intentionally
// ignored and remains in the signature only for compatibility with callers.
func SelectPrice(prices []Price, _ time.Time, promptTokens int64) (Price, bool) {
	var current time.Time
	for _, price := range prices {
		if current.IsZero() || price.EffectiveFrom.After(current) {
			current = price.EffectiveFrom
		}
	}
	if current.IsZero() {
		return Price{}, false
	}
	eligible := make([]Price, 0, len(prices))
	for _, price := range prices {
		if price.EffectiveFrom.Equal(current) && price.TierFrom <= promptTokens {
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

// Amount mirrors new-api's normalized lanes. The configured cache-write price
// is the 5-minute/default price; 1-hour writes cost 1.6 times that price.
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
	cacheWritePrice, err := decimalRat(decimalOrZero(price.CacheWrite))
	if err != nil {
		return nil, err
	}
	groupRatio, err := decimalRat(ratio)
	if err != nil {
		return nil, err
	}
	write5m, write1h := usage.CacheWrite5mTokens, usage.CacheWrite1hTokens
	remainingWrite := usage.CacheWriteTokens - write5m - write1h
	if remainingWrite < 0 {
		remainingWrite = 0
	}
	total := new(big.Rat)
	total.Add(total, tokenCost(usage.PromptTokens, inputPrice))
	total.Add(total, tokenCost(usage.CacheTokens, cachePrice))
	total.Add(total, tokenCost(write5m+remainingWrite, cacheWritePrice))
	oneHourPrice := new(big.Rat).Mul(cacheWritePrice, big.NewRat(8, 5))
	total.Add(total, tokenCost(write1h, oneHourPrice))
	total.Add(total, tokenCost(usage.CompletionTokens, outputPrice))
	return total.Mul(total, groupRatio), nil
}

func decimalOrZero(value string) string {
	if strings.TrimSpace(value) == "" {
		return "0"
	}
	return value
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
