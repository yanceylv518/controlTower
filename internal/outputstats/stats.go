// Package outputstats keeps total-request-time output speed separate from the
// legacy streaming generation-time metric. Nil means no new-format evidence.
package outputstats

import (
	"encoding/json"
	"math"
)

type Stats struct {
	Tokens         int64   `json:"tokens"`
	Seconds        float64 `json:"seconds"`
	Samples        int64   `json:"samples"`
	DirectTokens   int64   `json:"direct_tokens,omitempty"`
	DirectSeconds  float64 `json:"direct_seconds,omitempty"`
	DirectSamples  int64   `json:"direct_samples,omitempty"`
	RetrySamples   int64   `json:"retry_samples,omitempty"`
	UnknownSamples int64   `json:"unknown_samples,omitempty"`
	invalid        bool
}

// Optional malformed evidence must not reject the rest of an Agent report.
func (s *Stats) UnmarshalJSON(data []byte) error {
	type wire Stats
	var v wire
	if err := json.Unmarshal(data, &v); err != nil {
		*s = Stats{invalid: true}
		return nil
	}
	*s = Stats(v)
	return nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func validPart(tokens, samples int64, seconds float64) bool {
	if tokens < 0 || samples < 0 || seconds < 0 || !finite(seconds) {
		return false
	}
	if samples == 0 {
		return tokens == 0 && seconds == 0
	}
	return tokens >= samples && seconds > 0
}

func (s *Stats) Valid(requests int64, channel bool) bool {
	if s == nil || s.invalid || requests < 0 || s.Samples > requests || !validPart(s.Tokens, s.Samples, s.Seconds) || !validPart(s.DirectTokens, s.DirectSamples, s.DirectSeconds) {
		return false
	}
	if s.DirectTokens > s.Tokens || s.DirectSeconds > s.Seconds || s.DirectSamples > s.Samples || s.RetrySamples < 0 || s.UnknownSamples < 0 {
		return false
	}
	if !channel {
		return s.DirectSamples == 0 && s.RetrySamples == 0 && s.UnknownSamples == 0
	}
	remaining := s.Samples - s.DirectSamples
	return s.RetrySamples <= remaining && s.UnknownSamples == remaining-s.RetrySamples
}

func (s *Stats) Add(tokens int64, seconds float64, attempts int, channel bool) {
	if tokens <= 0 || seconds <= 0 || !finite(seconds) {
		return
	}
	s.Tokens += tokens
	s.Seconds += seconds
	s.Samples++
	if !channel {
		return
	}
	switch {
	case attempts == 1:
		s.DirectTokens += tokens
		s.DirectSeconds += seconds
		s.DirectSamples++
	case attempts > 1:
		s.RetrySamples++
	default:
		s.UnknownSamples++
	}
}

func Clone(s *Stats) *Stats {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}
func Merge(a, b *Stats) *Stats {
	if a == nil {
		return Clone(b)
	}
	if b == nil {
		return Clone(a)
	}
	v := *a
	v.Tokens += b.Tokens
	v.Seconds += b.Seconds
	v.Samples += b.Samples
	v.DirectTokens += b.DirectTokens
	v.DirectSeconds += b.DirectSeconds
	v.DirectSamples += b.DirectSamples
	v.RetrySamples += b.RetrySamples
	v.UnknownSamples += b.UnknownSamples
	v.invalid = v.invalid || b.invalid
	return &v
}

func (s *Stats) Rate() *float64 {
	if s == nil || s.Tokens <= 0 || s.Seconds <= 0 {
		return nil
	}
	v := float64(s.Tokens) / s.Seconds
	return &v
}

func (s *Stats) SampleTokens() int64 {
	if s == nil {
		return 0
	}
	return s.Tokens
}
func (s *Stats) Duration() float64 {
	if s == nil {
		return 0
	}
	return s.Seconds
}
