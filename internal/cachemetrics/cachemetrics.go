// Package cachemetrics holds the shared cache-hit accounting defaults so the
// agent aggregator and the server-side fallback aggregator cannot drift.
package cachemetrics

// DefaultCacheHitMinPromptTokens is the prompt-size floor for cache-token
// accounting: prompts at or below it cannot be cache hits and would dilute
// the ratio. The agent may override its copy via
// CT_CACHE_HIT_MIN_PROMPT_TOKENS; the server fallback aggregator (used only
// for agents that do not pre-aggregate) always uses this default and will
// diverge from a non-default agent override — documented, accepted.
const DefaultCacheHitMinPromptTokens = int64(512)
