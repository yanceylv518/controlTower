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
