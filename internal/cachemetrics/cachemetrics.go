// Package cachemetrics holds the shared cache-hit accounting defaults so the
// agent aggregator and the server-side fallback aggregator cannot drift.
package cachemetrics

// MinPromptTokens is the single prompt-size floor for cache-token accounting:
// prompts at or below it cannot be cache hits and would dilute the ratio.
// Both agent-side aggregation and the server fallback path must use this
// value so the metric cannot vary with deployment-local configuration.
const MinPromptTokens = int64(512)

// PromptTotal returns the denominator used for cache-hit accounting. Some
// providers (notably Anthropic) report uncached prompt tokens separately from
// cache-read tokens, while OpenAI-compatible providers usually include cached
// tokens in prompt_tokens. normalizedTotal disambiguates those semantics when
// the source log provides it. The cache-token floor also prevents malformed or
// legacy payloads from producing a ratio above one.
func PromptTotal(promptTokens, cacheTokens int64, normalizedTotal *int64) int64 {
	total := promptTokens
	if normalizedTotal != nil && *normalizedTotal > 0 {
		total = *normalizedTotal
	}
	if total < cacheTokens {
		total = cacheTokens
	}
	return total
}

// ClampRate keeps externally supplied or historical cache ratios within their
// mathematical range. New aggregation should already satisfy this invariant.
func ClampRate(rate float64) float64 {
	if rate < 0 {
		return 0
	}
	if rate > 1 {
		return 1
	}
	return rate
}
