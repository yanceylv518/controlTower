package tuning

import (
	"math"
	"reflect"
)

// ContinuousRules contains operator-adjustable business rules. Internal queue
// bounds and RPM/TPM's one-minute measurement definition are not policy knobs.
type ContinuousRules struct {
	ChannelHistoryRetentionDays int     `json:"channel_history_retention_days"`
	RecoverySpeedExponent       float64 `json:"recovery_speed_exponent"`
	RecoveryIntervalSeconds     int     `json:"recovery_interval_seconds"`
	EvaluationIntervalSeconds   int     `json:"evaluation_interval_seconds"`
	RoutineProbeEnabled         bool    `json:"routine_probe_enabled"`
	RoutineIntervalSeconds      int     `json:"routine_interval_seconds"`
	ProbeTimeoutSeconds         int     `json:"probe_timeout_seconds"`
	ProbeSlowLimit              int     `json:"probe_slow_limit"`
	RoutineErrorLimit           int     `json:"routine_error_limit"`
	ProbeMaxAgeSeconds          int     `json:"probe_max_age_seconds"`
	BaselineMinChannels         int     `json:"baseline_min_channels"`
	OutputMinTokens             int     `json:"output_min_tokens"`
	CacheMinTokens              int     `json:"cache_min_tokens"`
	ErrorAlpha                  float64 `json:"error_alpha"`
	ErrorSettleSeconds          int     `json:"error_settle_seconds"`
	ErrorLookbackMinutes        int     `json:"error_lookback_minutes"`
	CapacityTargetUtilization   float64 `json:"capacity_target_utilization"`
	CapacityReleaseUtilization  float64 `json:"capacity_release_utilization"`
	CapacityPeerUtilization     float64 `json:"capacity_peer_utilization"`
	CapacityOverloadSeconds     int     `json:"capacity_overload_seconds"`
	CapacityRecoverySeconds     int     `json:"capacity_recovery_seconds"`
	CapacityFeedbackSeconds     int     `json:"capacity_feedback_seconds"`
	CapacityMaxAgeSeconds       int     `json:"capacity_max_age_seconds"`
	CapacitySampleGapSeconds    int     `json:"capacity_sample_gap_seconds"`
	CapacityMinWeight           int     `json:"capacity_min_weight"`
	WritePauseAttempts          int     `json:"write_pause_attempts"`
	WriteMaxAttempts            int     `json:"write_max_attempts"`
	WriteRetrySeconds           int     `json:"write_retry_seconds"`
	ExternalWriteGraceSeconds   int     `json:"external_write_grace_seconds"`
	RecoveryRoundSeconds        int     `json:"recovery_round_seconds"`
	RecoveryLostSeconds         int     `json:"recovery_lost_seconds"`
	RecoverySpeedMinFactor      float64 `json:"recovery_speed_min_factor"`
	RecoveryMinWeight           int     `json:"recovery_min_weight"`
	FastReportMaxAgeSeconds     int     `json:"fast_report_max_age_seconds"`
	FastReportFutureSeconds     int     `json:"fast_report_future_seconds"`
	ObserveDeadbandPercent      float64 `json:"observe_deadband_percent"`
}

func DefaultContinuousRules() ContinuousRules {
	return ContinuousRules{
		ChannelHistoryRetentionDays: 7,
		RecoverySpeedExponent:       .5,
		RecoveryIntervalSeconds:     0,
		EvaluationIntervalSeconds:   30,
		RoutineProbeEnabled:         true,
		RoutineIntervalSeconds:      10,
		ProbeTimeoutSeconds:         30,
		ProbeSlowLimit:              2,
		RoutineErrorLimit:           3,
		ProbeMaxAgeSeconds:          90,
		BaselineMinChannels:         2,
		OutputMinTokens:             100,
		CacheMinTokens:              10000,
		ErrorAlpha:                  0.3,
		ErrorSettleSeconds:          90,
		ErrorLookbackMinutes:        15,
		CapacityTargetUtilization:   0.9,
		CapacityReleaseUtilization:  0.85,
		CapacityPeerUtilization:     0.85,
		CapacityOverloadSeconds:     60,
		CapacityRecoverySeconds:     60,
		CapacityFeedbackSeconds:     60,
		CapacityMaxAgeSeconds:       90,
		CapacitySampleGapSeconds:    60,
		CapacityMinWeight:           1,
		WritePauseAttempts:          3,
		WriteMaxAttempts:            5,
		WriteRetrySeconds:           600,
		ExternalWriteGraceSeconds:   120,
		RecoveryRoundSeconds:        480,
		RecoveryLostSeconds:         600,
		RecoverySpeedMinFactor:      0.1,
		RecoveryMinWeight:           1,
		FastReportMaxAgeSeconds:     120,
		FastReportFutureSeconds:     60,
		ObserveDeadbandPercent:      5,
	}
}
func (p ContinuousDispatchParams) Rules() ContinuousRules {
	if p.ContinuousRules == (ContinuousRules{}) {
		return DefaultContinuousRules()
	}
	return p.ContinuousRules
}
func stateRules(s ContinuousState) ContinuousRules {
	if s.Evaluation != nil {
		return s.Evaluation.Params.Rules()
	}
	return DefaultContinuousRules()
}
func optionalRules(p []ContinuousDispatchParams) ContinuousRules {
	if len(p) > 0 {
		return p[0].Rules()
	}
	return DefaultContinuousRules()
}
func validateContinuousRules(r ContinuousRules, errors map[string]string) {
	values := reflect.ValueOf(r)
	for _, rule := range []struct {
		name, key string
		min, max  float64
	}{
		{"RecoverySpeedExponent", "recovery_speed_exponent", 0, 2},
		{"ChannelHistoryRetentionDays", "channel_history_retention_days", 1, 365},
		{"RecoveryIntervalSeconds", "recovery_interval_seconds", 0, 3600},
		{"EvaluationIntervalSeconds", "evaluation_interval_seconds", 1, 3600},
		{"RoutineIntervalSeconds", "routine_interval_seconds", 1, 3600},
		{"ProbeTimeoutSeconds", "probe_timeout_seconds", 1, 300},
		{"ProbeSlowLimit", "probe_slow_limit", 1, 100},
		{"RoutineErrorLimit", "routine_error_limit", 1, 100},
		{"ProbeMaxAgeSeconds", "probe_max_age_seconds", 1, 86400},
		{"BaselineMinChannels", "baseline_min_channels", 1, 100},
		{"OutputMinTokens", "output_min_tokens", 1, 1000000000},
		{"CacheMinTokens", "cache_min_tokens", 1, 1000000000},
		{"ErrorAlpha", "error_alpha", 0.01, 1},
		{"ErrorSettleSeconds", "error_settle_seconds", 0, 3600},
		{"ErrorLookbackMinutes", "error_lookback_minutes", 1, 2880},
		{"CapacityTargetUtilization", "capacity_target_utilization", 0.01, 1},
		{"CapacityReleaseUtilization", "capacity_release_utilization", 0.01, 1},
		{"CapacityPeerUtilization", "capacity_peer_utilization", 0.01, 1},
		{"CapacityOverloadSeconds", "capacity_overload_seconds", 1, 3600},
		{"CapacityRecoverySeconds", "capacity_recovery_seconds", 1, 3600},
		{"CapacityFeedbackSeconds", "capacity_feedback_seconds", 60, 3600},
		{"CapacityMaxAgeSeconds", "capacity_max_age_seconds", 1, 3600},
		{"CapacitySampleGapSeconds", "capacity_sample_gap_seconds", 1, 3600},
		{"CapacityMinWeight", "capacity_min_weight", 0, 1000000},
		{"WritePauseAttempts", "write_pause_attempts", 1, 100},
		{"WriteMaxAttempts", "write_max_attempts", 1, 100},
		{"WriteRetrySeconds", "write_retry_seconds", 1, 86400},
		{"ExternalWriteGraceSeconds", "external_write_grace_seconds", 0, 3600},
		{"RecoveryRoundSeconds", "recovery_round_seconds", 1, 86400},
		{"RecoveryLostSeconds", "recovery_lost_seconds", 1, 172800},
		{"RecoverySpeedMinFactor", "recovery_speed_min_factor", 0.01, 1},
		{"RecoveryMinWeight", "recovery_min_weight", 1, 1000000},
		{"FastReportMaxAgeSeconds", "fast_report_max_age_seconds", 1, 3600},
		{"FastReportFutureSeconds", "fast_report_future_seconds", 0, 3600},
		{"ObserveDeadbandPercent", "observe_deadband_percent", 0, 100},
	} {
		field := values.FieldByName(rule.name)
		var v float64
		if field.Kind() == reflect.Int {
			v = float64(field.Int())
		} else {
			v = field.Float()
		}
		if math.IsNaN(v) || math.IsInf(v, 0) || v < rule.min || v > rule.max {
			errors["continuous."+rule.key] = "outside_allowed_range"
		}
	}
	if r.WriteMaxAttempts < r.WritePauseAttempts {
		errors["continuous.write_max_attempts"] = "must_be_at_least_pause_attempts"
	}
	if r.RecoveryLostSeconds <= r.RecoveryRoundSeconds {
		errors["continuous.recovery_lost_seconds"] = "must_exceed_round_timeout"
	}
	if r.CapacityReleaseUtilization > r.CapacityTargetUtilization {
		errors["continuous.capacity_release_utilization"] = "must_not_exceed_target_utilization"
	}
}
