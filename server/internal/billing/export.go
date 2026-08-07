package billing

import "math/big"

type RequestCharge struct {
	InputPrice, OutputPrice, CachePrice, CacheWritePrice, InputAmount, OutputAmount, CacheAmount, CacheWriteAmount, Total string
	Unpriced                                                                                                              bool
}

// RequestPrice applies ratios captured by new-api on the individual log row.
// The model table remains the fallback for legacy rows that predate these
// fields, but a recorded ratio always wins because it reflects actual billing.
func RequestPrice(log PagedLogRecord, fallback Price) (Price, string) {
	price := fallback
	if log.CompletionRatio != "" {
		if value, err := multipliedDecimal(fallback.Input, log.CompletionRatio); err == nil {
			price.Output = value
		}
	}
	if log.CacheRatio != "" {
		if value, err := multipliedDecimal(fallback.Input, log.CacheRatio); err == nil {
			price.Cache = value
		}
	}
	// Cache-write price has no implicit default. Only a ratio explicitly
	// recorded by new-api may derive it from the input price.
	if log.CacheCreationRatio != "" {
		if value, err := multipliedDecimal(fallback.Input, log.CacheCreationRatio); err == nil {
			price.CacheWrite = value
		}
	}
	ratio := log.GroupRatio
	return price, ratio
}

func PriceRequest(log PagedLogRecord, price Price, ratio string) RequestCharge {
	if ratio == "" {
		ratio = "1"
	}
	in, _ := multipliedDecimal(price.Input, ratio)
	out, _ := multipliedDecimal(price.Output, ratio)
	cachePrice, _ := multipliedDecimal(price.Cache, ratio)
	cacheWritePrice, _ := multipliedDecimal(decimalOrZero(price.CacheWrite), ratio)
	prompt := int64(0)
	if log.PromptTokens.Valid {
		prompt = log.PromptTokens.Int64
	}
	completion := int64(0)
	if log.CompletionTokens.Valid {
		completion = log.CompletionTokens.Int64
	}
	zero := Price{Input: "0", Output: "0", Cache: "0", CacheWrite: "0"}
	p := zero
	p.Input = price.Input
	a, _ := Amount(Usage{PromptTokens: prompt}, p, ratio)
	p = zero
	p.Output = price.Output
	b, _ := Amount(Usage{CompletionTokens: completion}, p, ratio)
	p = zero
	p.Cache = price.Cache
	c, _ := Amount(Usage{CacheTokens: log.CacheTokens}, p, ratio)
	p = zero
	p.CacheWrite = decimalOrZero(price.CacheWrite)
	w, _ := Amount(Usage{CacheWriteTokens: log.CacheWriteTokens, CacheWrite5mTokens: log.CacheWrite5mTokens, CacheWrite1hTokens: log.CacheWrite1hTokens}, p, ratio)
	total := new(big.Rat).Add(a, b)
	total.Add(total, c)
	total.Add(total, w)
	return RequestCharge{in, out, cachePrice, cacheWritePrice, FormatAmount(a, 6), FormatAmount(b, 6), FormatAmount(c, 6), FormatAmount(w, 6), FormatAmount(total, 6), false}
}
