package tuning

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLifelineRequiresFreshMatchingWorker(t *testing.T) {
	now := time.Now().UTC()
	c := Channel{ID: 7, Status: "1", Models: []string{"m"}}
	b := ChannelBaseValue{ChannelID: 7, BaseWeight: 100, Models: []string{"m"}, ModelName: "m", UpdatedAt: now}
	p := PolicyRecord{Policy: DefaultPolicy(), UpdatedAt: now}
	p.Policy.DispatchModes = map[string]string{"m": "auto"}
	s := ContinuousState{ControlRevision: 3}
	r := ProbeRuntime{Running: true, Model: "m", Revision: 3, BaseAt: now, PolicyAt: now, ConfirmedAt: now}
	describe := func() LifelineChannel { return DescribeLifelineChannel(c, b, s, p, r, now, nil) }
	require.Equal(t, "routine", describe().State)
	r.Pending = 1
	r.Accelerated = 1
	require.Contains(t, describe().Reason, "加速")
	r.ConfirmedAt = now.Add(-21 * time.Second)
	require.Equal(t, "unknown", describe().State)
	r.ConfirmedAt = now
	r.Revision = 2
	require.Equal(t, "unknown", describe().State)
	r.Revision = 3
	r.PolicyAt = now.Add(-time.Second)
	require.Equal(t, "unknown", describe().State)
	r.PolicyAt = now
	r.BaseAt = now.Add(-time.Second)
	require.Equal(t, "unknown", describe().State)
	r.BaseAt = now
	r.Running = false
	require.Equal(t, "unknown", describe().State)
}
func TestLifelineManualProtectionAndRecoveryIndependentOfRoutineSelection(t *testing.T) {
	now := time.Now().UTC()
	c := Channel{ID: 7, Status: "2", Models: []string{"m"}}
	b := ChannelBaseValue{ChannelID: 7, BaseWeight: 100}
	p := PolicyRecord{Policy: DefaultPolicy()}
	p.Policy.DispatchModes = map[string]string{"m": "auto"}
	p.Policy.Continuous.RoutineProbeEnabled = false
	s := ContinuousState{Phase: "circuit", CircuitDisabled: true, UpdatedAt: now, NextProbeAt: &now}
	r := ProbeRuntime{}
	describe := func() LifelineChannel { return DescribeLifelineChannel(c, b, s, p, r, now, nil) }
	require.Equal(t, "waiting", describe().State)
	require.Equal(t, "auto_disabled", describe().Switch)
	s.Phase = "circuit"
	require.Equal(t, "waiting", describe().State)
	s.UpdatedAt = now.Add(-4 * time.Minute)
	require.Equal(t, "stopped", describe().State)
	s.UpdatedAt = now
	s.PausedReason = ManualDisabled
	require.Equal(t, "stopped", describe().State)
	require.Equal(t, "manual_disabled", describe().Switch)
	require.Nil(t, describe().NextAt)
	r.Running = true
	r.Pending = 1
	r.ConfirmedAt = now
	require.Equal(t, "stopping", describe().State)
	r.ConfirmedAt = now.Add(-time.Minute)
	require.Equal(t, "unknown", describe().State)
	r.Running = false
	c.Status = "1"
	require.Equal(t, "manual_hold", describe().Switch)
}
func TestLifelineMultiModelCountAndExpiredLastRecord(t *testing.T) {
	now := time.Now().UTC()
	record := ProbeRecord{Outcome: "pending", DeadlineAt: now.Add(-time.Second)}
	v := DescribeLifelineChannel(Channel{ID: 7, Status: "1", Models: []string{"a", "b"}}, ChannelBaseValue{}, ContinuousState{}, PolicyRecord{Policy: DefaultPolicy()}, ProbeRuntime{Last: &record}, now, nil)
	require.Equal(t, "stopped", v.State)
	require.Len(t, v.Models, 2)
	require.Equal(t, "unconfirmed", v.Last.Outcome)
	require.Equal(t, "pending", record.Outcome)
}

func TestClosedLifelineUsesTasksInsteadOfOldRecoveryPhase(t *testing.T) {
	now := time.Now().UTC()
	c := Channel{ID: 7, Status: "2", Models: []string{"m"}}
	p := PolicyRecord{Policy: DefaultPolicy()}
	p.Policy.DispatchModes = map[string]string{"m": "auto"}
	p.Policy.Continuous.RoutineProbeEnabled = false
	for _, phase := range []string{"circuit", "probing", "soft_start", "normal"} {
		for _, age := range []time.Duration{0, 24 * time.Hour} {
			for _, status := range []string{"", "succeeded", "failed", "expired", "canceled", "pending", "delivered", "executing"} {
				t.Run(phase+"/"+age.String()+"/"+status, func(t *testing.T) {
					state := ContinuousState{Phase: phase, UpdatedAt: now.Add(-age), NextProbeAt: &now}
					var round *ProbeRound
					if status != "" {
						round = &ProbeRound{ID: "batch", Status: status, UpdatedAt: now}
					}
					v := DescribeLifelineChannel(c, ChannelBaseValue{}, state, p, ProbeRuntime{}, now, round)
					want := "stopped"
					if phase == "circuit" && age == 0 {
						want = "waiting"
					}
					if status == "pending" || status == "delivered" {
						want = "waiting"
					}
					if status == "executing" {
						want = "recovery"
					}
					require.Equal(t, want, v.State)
					if want == "stopped" {
						require.Nil(t, v.NextAt)
						require.Nil(t, v.RoundID)
						require.Contains(t, v.Reason, "渠道已关闭")
					}
				})
			}
		}
	}
	state := ContinuousState{PausedReason: ManualDisabled}
	round := &ProbeRound{ID: "inflight", Status: "executing", UpdatedAt: now}
	v := DescribeLifelineChannel(c, ChannelBaseValue{}, state, p, ProbeRuntime{}, now, round)
	require.Equal(t, "stopping", v.State)
	require.Nil(t, v.NextAt)
	state = ContinuousState{}
	round.Status = "failed"
	round.Metrics = &ProbeMetrics{Pending: 1}
	require.Equal(t, "recovery", DescribeLifelineChannel(c, ChannelBaseValue{}, state, p, ProbeRuntime{}, now, round).State)
	round.Metrics = nil
	round.Status = "executing"
	round.UpdatedAt = now.Add(-time.Hour)
	require.Equal(t, "unknown", DescribeLifelineChannel(c, ChannelBaseValue{}, state, p, ProbeRuntime{}, now, round).State)
}
