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

func TestV2BoundsSupersetAndDeriveV1(t *testing.T) {
	v1set := map[float64]bool{}
	for _, b := range UpperBoundsV2 {
		v1set[b] = true
	}
	for _, b := range UpperBounds {
		if !v1set[b] {
			t.Fatalf("V1 bound %v missing from V2", b)
		}
	}
	var v2 BucketsV2
	for i := range v2 {
		v2[i] = int64(i + 1)
	}
	v1 := DeriveV1(v2)
	var totalV1, totalV2 int64
	for _, c := range v1 {
		totalV1 += c
	}
	for _, c := range v2 {
		totalV2 += c
	}
	if totalV1 != totalV2 {
		t.Fatalf("derive lost counts: %d != %d", totalV1, totalV2)
	}
	// bucket (5,10] in V1 must absorb V2 buckets (5,8] and (8,10]
	if v1[6] != v2[6]+v2[7] {
		t.Fatalf("coarsening mapping wrong: %d != %d+%d", v1[6], v2[6], v2[7])
	}
}

func TestQuantileV2Interpolates(t *testing.T) {
	var b BucketsV2
	b[IndexV2(9)] = 100 // bucket (8,10]
	if v := QuantileV2(b, 0.5); v == nil || *v != 9 {
		t.Fatalf("expected 9.0, got %v", v)
	}
	var empty BucketsV2
	if QuantileV2(empty, 0.9) != nil {
		t.Fatal("empty must be nil")
	}
}
