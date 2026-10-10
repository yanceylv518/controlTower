package billing

import "testing"

func TestMatchUpstreamPriority(t *testing.T) {
	prefixes := map[string]int64{"qujing": 2, "qujing_vip": 3}
	review := map[string]bool{"qujing_vip": true}
	for _, tc := range []struct {
		name    string
		urls    map[int64]bool
		owner   int64
		blocked bool
	}{
		{"qujing_x", map[int64]bool{1: true}, 1, false},
		{"qujing_x", nil, 2, false},
		{"qujing_x", map[int64]bool{1: true, 2: true}, 2, false},
		{"unknown", map[int64]bool{1: true, 2: true}, 0, true},
		{"qujing_x", map[int64]bool{1: true, 3: true}, 0, true},
		{"qujing_vip_x", nil, 0, true},
		{"qujing_vip_x", map[int64]bool{1: true}, 1, false},
		{"unknown", nil, 0, false},
	} {
		owner, _, blocked := MatchUpstream(tc.name, tc.urls, prefixes, review)
		if owner != tc.owner || blocked != tc.blocked {
			t.Fatalf("%+v: owner=%d blocked=%v", tc, owner, blocked)
		}
	}
}
