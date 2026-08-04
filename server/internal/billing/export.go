package billing

import "math/big"

type RequestCharge struct {
	InputPrice, OutputPrice, CachePrice, InputAmount, OutputAmount, CacheAmount, Total string
	Unpriced                                                                           bool
}

func PriceRequest(log PagedLogRecord, price Price, ratio string) RequestCharge {
	if ratio == "" {
		ratio = "1"
	}
	in, _ := multipliedDecimal(price.Input, ratio)
	out, _ := multipliedDecimal(price.Output, ratio)
	cachePrice, _ := multipliedDecimal(price.Cache, ratio)
	prompt := int64(0)
	if log.PromptTokens.Valid {
		prompt = log.PromptTokens.Int64
	}
	completion := int64(0)
	if log.CompletionTokens.Valid {
		completion = log.CompletionTokens.Int64
	}
	nonCache := prompt - log.CacheTokens
	if nonCache < 0 {
		nonCache = prompt
	}
	a, _ := Amount(Usage{PromptTokens: nonCache}, Price{Input: price.Input, Output: "0", Cache: "0"}, ratio)
	b, _ := Amount(Usage{CompletionTokens: completion}, Price{Input: "0", Output: price.Output, Cache: "0"}, ratio)
	c, _ := Amount(Usage{PromptTokens: log.CacheTokens, CacheTokens: log.CacheTokens}, Price{Input: "0", Output: "0", Cache: price.Cache}, ratio)
	total := new(big.Rat).Add(a, b)
	total.Add(total, c)
	return RequestCharge{in, out, cachePrice, FormatAmount(a, 6), FormatAmount(b, 6), FormatAmount(c, 6), FormatAmount(total, 6), false}
}
