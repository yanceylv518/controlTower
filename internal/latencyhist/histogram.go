package latencyhist

import "math"

const BucketCount = 10
const BucketCountV2 = 15

type Buckets [BucketCount]int64

// BucketsV2 densifies the tail and is shared by use_time and TTFT.
// V1 bounds are a strict subset, so a V1 histogram can be derived exactly.
type BucketsV2 [BucketCountV2]int64

var UpperBounds = [BucketCount]float64{0.25, 0.5, 1, 2, 3, 5, 10, 30, 60, 120}
var UpperBoundsV2 = [BucketCountV2]float64{0.25, 0.5, 1, 2, 3, 5, 8, 10, 12, 20, 30, 45, 60, 90, 120}

func Index(seconds float64) int {
	for i := 0; i < BucketCount-1; i++ {
		if seconds <= UpperBounds[i] {
			return i
		}
	}
	return BucketCount - 1
}

func IndexV2(seconds float64) int {
	for i := 0; i < BucketCountV2-1; i++ {
		if seconds <= UpperBoundsV2[i] {
			return i
		}
	}
	return BucketCountV2 - 1
}

func Add(left, right Buckets) Buckets {
	var result Buckets
	for i := range result {
		result[i] = left[i] + right[i]
	}
	return result
}

func AddV2(left, right BucketsV2) BucketsV2 {
	var result BucketsV2
	for i := range result {
		result[i] = left[i] + right[i]
	}
	return result
}

// DeriveV1 coarsens a V2 histogram into the legacy V1 buckets. The mapping is
// exact because every V1 upper bound is also a V2 upper bound.
func DeriveV1(v2 BucketsV2) Buckets {
	var v1 Buckets
	j := 0
	for i := 0; i < BucketCount; i++ {
		for j < BucketCountV2 && UpperBoundsV2[j] <= UpperBounds[i] {
			v1[i] += v2[j]
			j++
		}
	}
	return v1
}

func Quantile(buckets Buckets, quantile float64) *float64 {
	return interpolate(buckets[:], UpperBounds[:], quantile)
}

func QuantileV2(buckets BucketsV2, quantile float64) *float64 {
	return interpolate(buckets[:], UpperBoundsV2[:], quantile)
}

// interpolate performs the histogram_quantile-style linear interpolation
// within the target bucket. Returns nil for an empty histogram.
func interpolate(counts []int64, bounds []float64, quantile float64) *float64 {
	var total int64
	for _, count := range counts {
		total += count
	}
	if total == 0 {
		return nil
	}
	if quantile <= 0 {
		quantile = 0
	}
	if quantile > 1 {
		quantile = 1
	}
	target := float64(total) * quantile
	if target <= 0 {
		target = 1
	}
	var cumulative int64
	for i, count := range counts {
		previous := cumulative
		cumulative += count
		if float64(cumulative) >= target {
			lower := 0.0
			if i > 0 {
				lower = bounds[i-1]
			}
			fraction := 1.0
			if count > 0 {
				fraction = (target - float64(previous)) / float64(count)
			}
			fraction = math.Max(0, math.Min(1, fraction))
			value := lower + fraction*(bounds[i]-lower)
			return &value
		}
	}
	value := bounds[len(bounds)-1]
	return &value
}
