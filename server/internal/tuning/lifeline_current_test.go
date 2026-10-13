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
	require.Equal(t, "stopped", describe().State)
	require.Equal(t, 1, describe().Pending)
	r.ConfirmedAt = now.Add(-time.Minute)
	require.Equal(t, "stopped", describe().State)
	require.Zero(t, describe().Pending)
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
	require.Equal(t, "stopped", v.State)
	require.NotNil(t, v.CurrentRound)
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

func TestLifelineDisabledEligibilityNeverBecomesPersistentStopping(t *testing.T) {
	now := time.Date(2026, 10, 13, 0, 20, 22, 0, time.UTC)
	for _, cause := range []string{"model_disabled", "manual_disabled", "multiple_models"} {
		for _, status := range []string{"pending", "delivered", "executing"} {
			for _, age := range []time.Duration{0, 3 * time.Minute, 3*time.Minute + time.Nanosecond, 32 * 24 * time.Hour, -time.Minute} {
				t.Run(cause+"/"+status+"/"+age.String(), func(t *testing.T) {
					c := Channel{ID: 214, Status: "2", Models: []string{"deepseek-v4-flash"}}
					p := PolicyRecord{Policy: DefaultPolicy()}
					s := ContinuousState{UpdatedAt: now} // fresh state is not proof an old command is running.
					if cause == "manual_disabled" {
						s.PausedReason = ManualDisabled
					}
					if cause == "multiple_models" {
						c.Models = append(c.Models, "other")
					}
					round := &ProbeRound{ID: "old-batch", Status: status, CreatedAt: now.Add(-32 * 24 * time.Hour), UpdatedAt: now.Add(-age), Planned: 10}
					v := DescribeLifelineChannel(c, ChannelBaseValue{}, s, p, ProbeRuntime{}, now, round)
					require.Equal(t, "stopped", v.State)
					require.Zero(t, v.Pending)
					require.Nil(t, v.NextAt)
					if age >= 0 && age <= 3*time.Minute {
						require.NotNil(t, v.CurrentRound)
						require.Contains(t, v.Reason, "先前的恢复任务尚未结案")
					} else {
						require.Nil(t, v.CurrentRound)
						require.Nil(t, v.RoundID)
						require.NotContains(t, v.Reason, "退出确认")
					}
					// Actual unexpired requests remain visible even with an old or
					// terminal command; the state does not claim they were canceled.
					round.Status = "succeeded"
					round.Metrics = &ProbeMetrics{Pending: 2}
					v = DescribeLifelineChannel(c, ChannelBaseValue{}, s, p, ProbeRuntime{}, now, round)
					require.Equal(t, "stopped", v.State)
					require.Equal(t, 2, v.Pending)
					require.NotNil(t, v.CurrentRound)
					require.Contains(t, v.Reason, "先前发出的请求待结果")
				})
			}
		}
	}
}

func TestLifelineStoppedWorkerRequestsRemainSupplementary(t *testing.T) {
	now := time.Now().UTC()
	c := Channel{ID: 7, Status: "2", Models: []string{"m"}}
	p := PolicyRecord{Policy: DefaultPolicy()}
	p.Policy.DispatchModes = map[string]string{"m": "auto"}
	r := ProbeRuntime{Running: true, Pending: 1, ConfirmedAt: now}
	v := DescribeLifelineChannel(c, ChannelBaseValue{}, ContinuousState{}, p, r, now, nil)
	require.Equal(t, "stopped", v.State)
	require.Equal(t, 1, v.Pending)
	require.NotContains(t, v.Reason, "当前没有探测任务")
	r.ConfirmedAt = now.Add(-time.Minute)
	v = DescribeLifelineChannel(c, ChannelBaseValue{}, ContinuousState{}, p, r, now, nil)
	require.Equal(t, "stopped", v.State)
	require.Zero(t, v.Pending)
	require.Nil(t, v.CurrentRound)
	require.Contains(t, v.Reason, "当前没有探测任务")
}
