package voicealert

import (
	"encoding/json"
	"testing"
	"time"
)

func TestThresholds(t *testing.T) {
	c := DefaultConfig()
	c.Percent = 50
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

func TestOptionalPercent(t *testing.T) {
	c := DefaultConfig()
	if c.Percent != 20 || !c.UsePercent {
		t.Fatal(c)
	}
	for _, tc := range []struct {
		low, high int64
		on, off   bool
	}{
		{80000000, 95000000, false, true}, {65000000, 80000000, true, true},
		{100000, 200000, false, false}, {80000000, 90000000, false, false},
		{100000000, 120000000, false, true}, {0, 10000001, true, true},
	} {
		values := make([]int64, 11)
		for i := range values {
			values[i] = tc.low
		}
		values[10] = tc.high
		for _, enabled := range []bool{true, false} {
			c.UsePercent = enabled
			want := tc.off
			if enabled {
				want = tc.on
			}
			if hit, _, _ := Trigger(values, c); hit != want {
				t.Fatalf("%+v enabled=%v got=%v", tc, enabled, hit)
			}
		}
	}
}
func TestPercentConfigCompatibility(t *testing.T) {
	c := DefaultConfig()
	if err := json.Unmarshal([]byte(`{"percent":50}`), &c); err != nil {
		t.Fatal(err)
	}
	if !c.UsePercent || c.Percent != 50 {
		t.Fatal(c)
	}
	c.UsePercent = false
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	restored := DefaultConfig()
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.UsePercent || restored.Percent != 50 {
		t.Fatal(restored)
	}
}

func TestDirectionalPercentBaseline(t *testing.T) {
	for _, tc := range []struct {
		name             string
		start, end       int64
		percent          float64
		usePercent, want bool
		direction        string
	}{
		{"drop 36.7 percent", 30000000, 19000000, 50, true, false, "下降"},
		{"rise 57.9 percent", 19000000, 30000000, 50, true, true, "上涨"},
		{"drop exactly 50", 30000000, 15000000, 50, true, false, "下降"},
		{"drop over 50", 30000000, 14000000, 50, true, true, "下降"},
		{"rise exactly 50", 30000000, 45000000, 50, true, false, "上涨"},
		{"rise over 50", 30000000, 45000001, 50, true, true, "上涨"},
		{"fall to zero is exactly 100", 30000000, 0, 100, true, false, "下降"},
		{"fall to zero exceeds 50", 30000000, 0, 50, true, true, "下降"},
		{"rise from zero", 0, 11000000, 100, true, true, "上涨"},
		{"absolute gate from zero", 0, 10000000, 50, true, false, "上涨"},
		{"percent disabled", 30000000, 19000000, 50, false, true, "下降"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			c.Percent = tc.percent
			c.UsePercent = tc.usePercent
			values := make([]int64, 11)
			for i := range values {
				values[i] = tc.start
			}
			values[10] = tc.end
			hit, _, _, direction := Evaluate(values, c)
			if hit != tc.want || direction != tc.direction {
				t.Fatalf("hit=%v direction=%s", hit, direction)
			}
		})
	}
	c := DefaultConfig()
	c.Percent = 50
	values := []int64{30000000, 19000000, 30000000, 30000000, 30000000, 30000000, 30000000, 30000000, 30000000, 30000000, 19000000}
	if hit, _, _, dir := Evaluate(values, c); hit || dir != "下降" {
		t.Fatal("latest extrema must use decline baseline")
	}
	values[10] = 30000000
	if hit, _, _, dir := Evaluate(values, c); !hit || dir != "上涨" {
		t.Fatal("latest extrema must use rise baseline")
	}
}
