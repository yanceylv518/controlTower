package dashboard

import (
	"strings"
	"testing"
)

func TestBillingOtherProjectionOnlyTransfersBillingFields(t *testing.T) {
	for _, want := range []string{"usage_semantic", "cache_tokens", "cache_creation_tokens_5m", "cache_creation_tokens_1h"} {
		if !strings.Contains(billingOtherProjection, want) {
			t.Fatalf("projection missing %q", want)
		}
	}
	for _, unwanted := range []string{"request_path", "stream_status", "admin_info"} {
		if strings.Contains(billingOtherProjection, unwanted) {
			t.Fatalf("projection unexpectedly transfers %q", unwanted)
		}
	}
}

func TestBillingCacheTokensUnderstandsKnownNewAPIShapes(t *testing.T) {
	for raw, want := range map[string]int64{
		`{"cache_tokens":12}`:            12,
		`{"cached_tokens":"13"}`:         13,
		`{"cache_read_input_tokens":14}`: 14,
		`{"prompt_cache_hit_tokens":15}`: 15,
		`{"unrelated":1}`:                0,
		`not-json`:                       0,
	} {
		if got := billingCacheTokens(raw); got != want {
			t.Errorf("billingCacheTokens(%q)=%d want %d", raw, got, want)
		}
	}
}

func TestParseBillingCacheUsageDoesNotDoubleCountAliases(t *testing.T) {
	v := parseBillingCacheUsage(`{"usage_semantic":"anthropic","cache_tokens":75841,"cache_creation_tokens":753,"cache_write_tokens":753,"cache_creation_tokens_5m":753}`)
	if v.Semantic != "anthropic" || v.Read != 75841 || v.Write != 753 || v.Write5m != 753 || v.Write1h != 0 {
		t.Fatalf("unexpected: %#v", v)
	}
}

// The accepted production example (prompt=298 already non-cache, cache=8507)
// carries no usage_semantic marker on some new-api versions. OpenAI-style
// cache is a subset of prompt, so cache exceeding prompt proves the row is
// Anthropic-shaped; without this guard the input lane would be zeroed.
func TestUnmarkedAnthropicShapeKeepsInputLane(t *testing.T) {
	cache := resolveBillingCacheSemantic(parseBillingCacheUsage(`{"cache_tokens":8507}`), 298)
	if cache.Semantic != "anthropic" || cache.Read != 8507 {
		t.Fatalf("unexpected: %#v", cache)
	}
	marked := resolveBillingCacheSemantic(parseBillingCacheUsage(`{"cached_tokens":400}`), 1000)
	if marked.Semantic != "openai" {
		t.Fatalf("subset cache must stay openai: %#v", marked)
	}
}

// Verbatim production sample (2026-08-05, user-provided) — the calibration
// evidence: both anthropic markers exist, all creation is 1h here, and the
// in-log ratios (1.25 vs 2) confirm the hardcoded 1.6 one-hour multiplier.
func TestParseBillingCacheUsageOnProductionSample(t *testing.T) {
	sample := `{"admin_info":{"use_channel":["10"]},"billing_source":"wallet","cache_creation_ratio":1.25,"cache_creation_ratio_1h":2,"cache_creation_tokens":2382,"cache_creation_tokens_1h":2382,"cache_ratio":0.1,"cache_tokens":43268,"cache_write_tokens":2382,"claude":true,"completion_ratio":5,"frt":10010,"group_ratio":1,"model_price":-1,"model_ratio":2.5,"request_conversion":["OpenAI Compatible","Claude Messages"],"request_path":"/v1/chat/completions","stream_status":{"end_reason":"eof","status":"ok"},"usage_semantic":"anthropic","user_group_ratio":-1}`
	v := parseBillingCacheUsage(sample)
	if v.Semantic != "anthropic" {
		t.Fatalf("semantic = %q", v.Semantic)
	}
	if v.Read != 43268 || v.Write != 2382 || v.Write5m != 0 || v.Write1h != 2382 {
		t.Fatalf("lanes = %#v", v)
	}
}
