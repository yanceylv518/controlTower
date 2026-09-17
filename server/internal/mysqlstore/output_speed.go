package mysqlstore

import (
	"controltower/internal/outputstats"
	"database/sql"
)

var outputSpeedColumns = []string{"output_speed_tokens", "output_speed_seconds", "output_speed_samples", "output_direct_tokens", "output_direct_seconds", "output_direct_samples", "output_retry_samples", "output_unknown_samples"}

func outputSpeedAssignments(merge bool) []string {
	var out []string
	for _, c := range outputSpeedColumns {
		value := "VALUES(" + c + ")"
		if merge {
			value = "IF(" + c + " IS NULL AND VALUES(" + c + ") IS NULL,NULL,COALESCE(" + c + ",0)+COALESCE(VALUES(" + c + "),0))"
		}
		out = append(out, c+" = "+value)
	}
	return out
}

func outputSpeedArgs(s *outputstats.Stats) []any {
	if s == nil {
		return make([]any, len(outputSpeedColumns))
	}
	return []any{s.Tokens, s.Seconds, s.Samples, s.DirectTokens, s.DirectSeconds, s.DirectSamples, s.RetrySamples, s.UnknownSamples}
}

type outputSpeedScan struct {
	tokens, samples, directTokens, directSamples, retries, unknown sql.NullInt64
	seconds, directSeconds                                         sql.NullFloat64
}

func (v *outputSpeedScan) args() []any {
	return []any{&v.tokens, &v.seconds, &v.samples, &v.directTokens, &v.directSeconds, &v.directSamples, &v.retries, &v.unknown}
}
func (v *outputSpeedScan) stats() *outputstats.Stats {
	if !v.tokens.Valid || !v.seconds.Valid || !v.samples.Valid || !v.directTokens.Valid || !v.directSeconds.Valid || !v.directSamples.Valid || !v.retries.Valid || !v.unknown.Valid {
		return nil
	}
	return &outputstats.Stats{Tokens: v.tokens.Int64, Seconds: v.seconds.Float64, Samples: v.samples.Int64, DirectTokens: v.directTokens.Int64, DirectSeconds: v.directSeconds.Float64, DirectSamples: v.directSamples.Int64, RetrySamples: v.retries.Int64, UnknownSamples: v.unknown.Int64}
}
