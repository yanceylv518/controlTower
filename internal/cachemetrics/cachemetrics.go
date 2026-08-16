// Package cachemetrics holds the shared cache-hit accounting defaults so the
// agent aggregator and the server-side fallback aggregator cannot drift.
package cachemetrics

// MinPromptTokens is the single prompt-size floor for cache-token accounting:
// prompts at or below it cannot be cache hits and would dilute the ratio.
// Both agent-side aggregation and the server fallback path must use this
// value so the metric cannot vary with deployment-local configuration.
const MinPromptTokens = int64(512)
