package outputstats

import (
	"encoding/json"
	"math"
	"testing"
)

func TestTotalTimeSpeedMergeAndEligibility(t *testing.T) {
	a, b := &Stats{}, &Stats{}
	a.Add(684, 15, 1, true)
	b.Add(100, 5, 2, true)
	b.Add(16, 10, 0, true)
	b.Add(20, 0, 1, true)
	b.Add(0, 30, 1, true)
	b.Add(20, math.Inf(1), 1, true)
	b.Add(20, math.NaN(), 1, true)
	got := Merge(a, b)
	if !got.Valid(3, true) || *got.Rate() != 800.0/30 || got.DirectTokens != 684 || got.DirectSeconds != 15 || got.DirectSamples != 1 || got.RetrySamples != 1 || got.UnknownSamples != 1 {
		t.Fatalf("%+v", got)
	}
	if a.Tokens != 684 || b.Tokens != 116 {
		t.Fatal("merge mutated input")
	}
	if Merge(nil, nil).Rate() != nil || *Merge(nil, got).Rate() != *got.Rate() {
		t.Fatal("legacy merge")
	}
	public := &Stats{}
	public.Add(684, 15, 2, false)
	if !public.Valid(1, false) || public.DirectTokens != 0 {
		t.Fatal("non-channel evidence")
	}
	if math.Round(*public.Rate()) != 46 {
		t.Fatal("NewAPI 684/15 should display 46 t/s")
	}
}

func TestInvalidOptionalOutputEvidence(t *testing.T) {
	for _, wire := range []string{`"bad"`, `{"tokens":"bad"}`, `{"tokens":10,"samples":1,"seconds":0}`, `{"tokens":10,"samples":1,"seconds":1,"direct_tokens":11,"direct_seconds":1,"direct_samples":1}`, `{"tokens":10,"samples":1,"seconds":1,"retry_samples":2}`} {
		var got Stats
		if err := json.Unmarshal([]byte(wire), &got); err != nil {
			t.Fatal("optional payload rejected report", err)
		}
		if got.Valid(1, true) {
			t.Fatalf("accepted %s", wire)
		}
	}
}
