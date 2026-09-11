package containerlogs

import (
	cl "controltower/internal/containerlog"
	"testing"
)

func TestKeywordCaseInsensitiveKeepsRequestIDExact(t *testing.T) {
	match := newMatcher(cl.Query{Keyword: "ppio [x].*", RequestID: "Req-1"})
	for _, line := range []string{"Req-1 PPIO [x].*", "Req-1 Ppio [x].*"} {
		if !match(line) {
			t.Fatalf("missing case-insensitive literal match: %s", line)
		}
	}
	for _, line := range []string{"req-1 PPIO [x].*", "Req-10 PPIO [x].*", "Req-1 PPIO xabc"} {
		if match(line) {
			t.Fatalf("incorrect match: %s", line)
		}
	}
}
