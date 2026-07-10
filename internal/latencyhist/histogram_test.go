package latencyhist

import "testing"

func TestQuantileMergesBucketCounts(t *testing.T) {
	left := Buckets{}
	left[Index(1)] = 95
	left[Index(30)] = 5
	right := Buckets{}
	right[Index(2)] = 95
	right[Index(60)] = 5

	merged := Add(left, right)
	p95 := Quantile(merged, 0.95)
	if p95 == nil || *p95 != 2 {
		t.Fatalf("merged p95 = %#v, want 2", p95)
	}
}

func TestIndexUsesStableUpperBounds(t *testing.T) {
	cases := []struct {
		seconds float64
		index   int
	}{
		{0.1, 0},
		{0.25, 0},
		{0.3, 1},
		{3, 4},
		{8, 6},
		{90, 9},
		{500, 9},
	}
	for _, item := range cases {
		if got := Index(item.seconds); got != item.index {
			t.Fatalf("Index(%v) = %d, want %d", item.seconds, got, item.index)
		}
	}
}
