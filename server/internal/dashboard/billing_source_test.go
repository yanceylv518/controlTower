package dashboard

import "testing"

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
