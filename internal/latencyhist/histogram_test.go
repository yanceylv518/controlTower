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

func TestQuantileInterpolatesWithinBucket(t *testing.T) {
	var b Buckets
	b[6] = 100 // bucket (5,10]
	if v := Quantile(b, 0.5); v == nil || *v != 7.5 {
		t.Fatalf("expected interpolated 7.5, got %v", v)
	}
	if v := Quantile(b, 0.95); v == nil || *v != 9.75 {
		t.Fatalf("expected interpolated 9.75, got %v", v)
	}
}

func TestQuantileMonotonicAndBounded(t *testing.T) {
	var b Buckets
	b[0], b[6], b[9] = 50, 40, 10
	p50, p95, p99 := Quantile(b, 0.5), Quantile(b, 0.95), Quantile(b, 0.99)
	if p50 == nil || p95 == nil || p99 == nil {
		t.Fatal("nil quantile")
	}
	if !(*p50 <= *p95 && *p95 <= *p99) {
		t.Fatalf("not monotonic: %v %v %v", *p50, *p95, *p99)
	}
	if *p99 > UpperBounds[BucketCount-1] {
		t.Fatalf("beyond histogram range: %v", *p99)
	}
	var single Buckets
	single[2] = 1 // bucket (0.5,1]
	for _, q := range []float64{0.5, 0.95, 0.99} {
		v := Quantile(single, q)
		if v == nil || *v <= 0.5 || *v > 1 {
			t.Fatalf("single sample q=%v out of bucket: %v", q, v)
		}
	}
	var empty Buckets
	if Quantile(empty, 0.95) != nil {
		t.Fatal("empty buckets must return nil")
	}
}
