package dashboard

import (
	"testing"
	"time"
)

func TestParseBillingInputRangeUsesInclusiveSecond(t *testing.T) {
	from, to, err := parseBillingInputRange("2026-08-04 00:00:00", "2026-08-04 19:16:21")
	if err != nil {
		t.Fatal(err)
	}
	if got := to.Sub(from); got != 19*time.Hour+16*time.Minute+22*time.Second {
		t.Fatalf("range duration=%s", got)
	}
}

func TestBillingRequestKeyIncludesExactTime(t *testing.T) {
	from := time.Date(2026, 8, 4, 0, 0, 0, 0, time.Local)
	first := billingRequestKey("site-a", "channel", from, from.Add(time.Hour))
	second := billingRequestKey("site-a", "channel", from, from.Add(2*time.Hour))
	if first == second {
		t.Fatal("different time ranges must not reuse the same billing task")
	}
}
