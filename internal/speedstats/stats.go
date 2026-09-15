// Package speedstats carries the direct-attempt TTFT evidence used only by tuning.
package speedstats

import (
	"controltower/internal/latencyhist"
	"encoding/json"
)

type Stats struct {
	Buckets      []int64 `json:"buckets"`
	RetryCount   int64   `json:"retry_count"`
	UnknownCount int64   `json:"unknown_count"`
}

// Speed evidence is optional: an incompatible value must not reject the public
// monitoring report. Leave invalid evidence empty so ingest can discard it.
func (s *Stats) UnmarshalJSON(data []byte) error {
	type wireStats Stats
	var decoded wireStats
	if err := json.Unmarshal(data, &decoded); err != nil {
		*s = Stats{}
		return nil
	}
	*s = Stats(decoded)
	return nil
}

func New() *Stats { return &Stats{Buckets: make([]int64, latencyhist.BucketCountV2)} }

func (s *Stats) Samples() int64 {
	if s == nil {
		return 0
	}
	var n int64
	for _, v := range s.Buckets {
		n += v
	}
	return n
}

// Valid checks the optional evidence against the unfiltered TTFT count. Unknown
// includes valid TTFT records that cannot safely qualify as successful direct attempts.
func (s *Stats) Valid(total int64) bool {
	if s == nil || len(s.Buckets) != latencyhist.BucketCountV2 || total < 0 {
		return false
	}
	remaining := total
	for _, v := range s.Buckets {
		if v < 0 || v > remaining {
			return false
		}
		remaining -= v
	}
	for _, v := range [...]int64{s.RetryCount, s.UnknownCount} {
		if v < 0 || v > remaining {
			return false
		}
		remaining -= v
	}
	return remaining == 0
}

func Clone(s *Stats) *Stats {
	if s == nil {
		return nil
	}
	v := *s
	v.Buckets = append([]int64(nil), s.Buckets...)
	return &v
}

// Merge retains only reported new evidence; legacy records never become direct
// samples. Their missing coverage is derived from the public TTFT count.
func Merge(a, b *Stats) *Stats {
	if a == nil {
		return Clone(b)
	}
	if b == nil {
		return Clone(a)
	}
	v := Clone(a)
	for i := range v.Buckets {
		v.Buckets[i] += b.Buckets[i]
	}
	v.RetryCount += b.RetryCount
	v.UnknownCount += b.UnknownCount
	return v
}
