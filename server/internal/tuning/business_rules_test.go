package tuning

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	cacheEvidenceTokens       = int64(DefaultContinuousRules().CacheMinTokens)
	otpsEvidenceTokens        = int64(DefaultContinuousRules().OutputMinTokens)
	writeFailureRetryInterval = time.Duration(DefaultContinuousRules().WriteRetrySeconds) * time.Second
)

func TestBusinessRulesLegacyDefaultsAndExplicitZero(t *testing.T) {
	var p ContinuousDispatchParams
	require.NoError(t, json.Unmarshal([]byte(`{"min_samples":5}`), &p))
	require.Equal(t, DefaultContinuousRules(), p.Rules())
	require.NoError(t, json.Unmarshal([]byte(`{"capacity_min_weight":0,"external_write_grace_seconds":0,"fast_report_future_seconds":0,"observe_deadband_percent":0,"recovery_speed_exponent":0}`), &p))
	require.Zero(t, p.CapacityMinWeight)
	require.Zero(t, p.ExternalWriteGraceSeconds)
	require.Zero(t, p.FastReportFutureSeconds)
	require.Zero(t, p.ObserveDeadbandPercent)
	require.Zero(t, p.RecoverySpeedExponent)
	encoded, err := json.Marshal(p)
	require.NoError(t, err)
	var restored ContinuousDispatchParams
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, p, restored)
}

func TestBusinessRulesValidateRelations(t *testing.T) {
	r := DefaultContinuousRules()
	errs := map[string]string{}
	validateContinuousRules(r, errs)
	require.Empty(t, errs)
	r.RoutineIntervalSeconds = 0
	r.WriteMaxAttempts = r.WritePauseAttempts - 1
	r.RecoveryLostSeconds = r.RecoveryRoundSeconds
	r.CapacityReleaseUtilization = .95
	validateContinuousRules(r, errs)
	for _, key := range []string{"routine_interval_seconds", "write_max_attempts", "recovery_lost_seconds", "capacity_release_utilization"} {
		require.Contains(t, errs, "continuous."+key)
	}
}

func TestChannelHistoryRetentionConfiguration(t *testing.T) {
	for _, days := range []int{0, 1, 7, 365, 366} {
		r := DefaultContinuousRules()
		r.ChannelHistoryRetentionDays = days
		errs := map[string]string{}
		validateContinuousRules(r, errs)
		if days < 1 || days > 365 {
			require.Contains(t, errs, "continuous.channel_history_retention_days")
		} else {
			require.Empty(t, errs)
		}
	}
}

func TestBusinessRulesChangeEvidenceAndCircuitThresholds(t *testing.T) {
	p := DefaultPolicy().Continuous
	p.BaselineMinChannels, p.ProbeMaxAgeSeconds, p.OutputMinTokens = 1, 200, 500
	now := time.Now()
	rows := []ChannelBaseValue{{ChannelID: 1, Models: []string{"m"}, BaseWeight: 100}}
	stats := map[int64]ProbeSpeedStats{1: {Samples: 5, MeanSeconds: 2, LastStartedAt: now.Add(-100 * time.Second)}}
	require.Equal(t, 2.0, probeSpeedBaseline(rows, stats, nil, 5, now, p))
	require.Zero(t, probeSpeedBaseline(rows, stats, nil, 5, now))
	m := ChannelMetric{OTPSStatsVersion: 1, OTPSSamples: 5, OTPSSampleTokens: 200, OTPS: 10}
	require.True(t, outputEvidenceReady(m, 5))
	require.False(t, outputEvidenceReady(m, 5, p))
	p.ProbeSlowLimit, p.RoutineErrorLimit = 3, 4
	var streak routineStreak
	for i := 0; i < 3; i++ {
		require.Equal(t, i == 2, streak.add(routineSlow, p))
	}
	streak.add(routineHealthy, p)
	for i := 0; i < 4; i++ {
		require.Equal(t, i == 3, streak.add(routineError, p))
	}
}

func TestBusinessRulesChangeCapacityRetryAndRecoveryPayload(t *testing.T) {
	p := DefaultPolicy().Continuous
	p.CapacityTargetUtilization, p.CapacityMinWeight = .6, 0
	raw, bound := capacityTarget(100, 2, 100, p)
	require.Equal(t, int64(30), raw)
	require.Equal(t, int64(30), bound)
	_, bound = capacityTarget(1, 2, 100, p)
	require.Zero(t, bound)
	p.WriteRetrySeconds, p.WriteMaxAttempts = 20, 4
	now := time.Now()
	s := ContinuousState{PausedReason: "write_failed", WriteFailureStreak: 3, LastWriteFailureAt: &now, Evaluation: &EvaluationContext{Params: p}}
	require.False(t, writeAttemptAllowed(s, now.Add(19*time.Second)))
	require.True(t, writeAttemptAllowed(s, now.Add(20*time.Second)))
	s.WriteFailureStreak = 4
	require.False(t, writeAttemptAllowed(s, now.Add(time.Hour)))
	p.ProbeTimeoutSeconds, p.ProbeSlowLimit, p.RecoveryRoundSeconds = 45, 3, 900
	options := RecoveryProbeOptions(Recommendation{Evidence: map[string]any{"evaluation": EvaluationContext{Params: p}}})
	require.Equal(t, 45, options.TimeoutSeconds)
	require.Equal(t, 3, options.SlowLimit)
	require.Equal(t, 900, options.RoundSeconds)
}
