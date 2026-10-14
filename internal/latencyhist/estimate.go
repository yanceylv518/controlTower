package latencyhist

// Estimate returns a histogram estimate and whether only a lower bound is
// known. The last bucket is open-ended (>60s / >90s), never bounded by 120s.
func Estimate(v1 Buckets, v2 *BucketsV2, q float64) (*float64, bool) {
	counts, bounds := v1[:], UpperBounds[:]
	if v2 != nil {
		counts, bounds = v2[:], UpperBoundsV2[:]
	}
	var total, below int64
	for i, n := range counts {
		if n < 0 {
			return nil, false
		}
		total += n
		if i < len(counts)-1 {
			below += n
		}
	}
	if total == 0 {
		return nil, false
	}
	if float64(below) < float64(total)*q {
		value := bounds[len(bounds)-2]
		return &value, true
	}
	return interpolate(counts, bounds, q), false
}
