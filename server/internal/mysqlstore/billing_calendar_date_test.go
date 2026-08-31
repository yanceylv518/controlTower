package mysqlstore

import (
	"testing"
	"time"
)

func TestBillingCalendarDatePreservesShanghaiDayAcrossUTCBoundary(t *testing.T) {
	shanghaiMidnight := time.Date(2026, 8, 25, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	if got := billingCalendarDate(shanghaiMidnight); got != "2026-08-25" {
		t.Fatalf("billing date=%q, want 2026-08-25", got)
	}
	if got := billingCalendarDate(shanghaiMidnight.UTC()); got != "2026-08-25" {
		t.Fatalf("UTC instant billing date=%q, want 2026-08-25", got)
	}
}
