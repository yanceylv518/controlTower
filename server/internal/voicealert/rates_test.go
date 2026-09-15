package voicealert

import (
	"testing"
	"time"
)

func TestThresholds(t *testing.T) {
	c := DefaultConfig()
	for _, tc := range []struct {
		low, high int64
		want      bool
	}{{12000000, 24000000, true}, {20000000, 30000000, false}, {30000000, 41000000, false}, {0, 10000001, true}, {0, 10000000, false}, {0, 0, false}} {
		values := make([]int64, 11)
		for i := range values {
			values[i] = tc.low
		}
		values[10] = tc.high
		got, _, _ := Trigger(values, c)
		if got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
	if hit, _, _ := Trigger([]int64{0, 99999999}, c); hit {
		t.Fatal("partial history triggered")
	}
}

func TestEvaluateDirectionUsesExtremaOrder(t *testing.T) {
	c := DefaultConfig()
	up := []int64{20000000, 15000000, 15000000, 15000000, 15000000, 15000000, 15000000, 15000000, 15000000, 15000000, 26000000}
	if hit, _, _, direction := Evaluate(up, c); !hit || direction != "上涨" {
		t.Fatalf("up hit=%v direction=%q", hit, direction)
	}
	down := []int64{26000000, 26000000, 26000000, 26000000, 26000000, 26000000, 26000000, 26000000, 26000000, 26000000, 15000000}
	if hit, _, _, direction := Evaluate(down, c); !hit || direction != "下降" {
		t.Fatalf("down hit=%v direction=%q", hit, direction)
	}
}

func TestRecipientScope(t *testing.T) {
	target := Target{Site: "site-a", UserID: 7}
	if !(Recipient{Phone: "13800000000"}).Matches(target) {
		t.Fatal("empty scope must match every customer")
	}
	if !(Recipient{Targets: []string{"site-a/7"}}).Matches(target) {
		t.Fatal("selected customer did not match")
	}
	if (Recipient{Targets: []string{"site-a/8"}}).Matches(target) {
		t.Fatal("unselected customer matched")
	}
}
func TestRollingBoundaryAndCoverage(t *testing.T) {
	end := time.Unix(10000, 0)
	data := map[int64]int64{end.Unix() - 360: 3, end.Unix() - 301: 5, end.Unix() - 300: 100, end.Unix() - 60: 7, end.Unix() - 1: 11, end.Unix(): 999}
	got := rolling(data, end)
	if got[0] != 8 || got[10] != 18 {
		t.Fatal(got)
	}
	stamps := []time.Time{}
	for i := 0; i <= 12; i++ {
		stamps = append(stamps, end.Add(time.Duration(i-12)*30*time.Second))
	}
	if !continuous(stamps, end.Add(-6*time.Minute), end) {
		t.Fatal("complete coverage rejected")
	}
	if continuous(stamps[1:], end.Add(-6*time.Minute), end) {
		t.Fatal("partial startup accepted")
	}
	broken := append([]time.Time{}, stamps[:3]...)
	broken = append(broken, stamps[6:]...)
	if continuous(broken, end.Add(-6*time.Minute), end) {
		t.Fatal("collection gap accepted")
	}
}
