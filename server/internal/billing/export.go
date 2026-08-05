package billing

import "math/big"

type RequestCharge struct {
	InputPrice, OutputPrice, CachePrice, CacheWritePrice, InputAmount, OutputAmount, CacheAmount, CacheWriteAmount, Total string
	Unpriced                                                                                                              bool
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
