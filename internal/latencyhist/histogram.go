package latencyhist

import "math"

const BucketCount = 10

type Buckets [BucketCount]int64

var UpperBounds = [BucketCount]float64{0.25, 0.5, 1, 2, 3, 5, 10, 30, 60, 120}

func Index(seconds float64) int {
	for i := 0; i < BucketCount-1; i++ {
		if seconds <= UpperBounds[i] {
			return i
		}
	}
	return BucketCount - 1
}

func Add(left, right Buckets) Buckets {
	var result Buckets
	for i := range result {
		result[i] = left[i] + right[i]
	}
	return result
}

func Quantile(buckets Buckets, quantile float64) *float64 {
	var total int64
	for _, count := range buckets {
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
	target := int64(math.Ceil(float64(total) * quantile))
	if target < 1 {
		target = 1
	}
	var cumulative int64
	for i, count := range buckets {
		cumulative += count
		if cumulative >= target {
			value := UpperBounds[i]
			return &value
		}
	}
	value := UpperBounds[BucketCount-1]
	return &value
}
