package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/agentgateway"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

func TestTuningAuditCommandAndPolicyRegressions(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, ApplyDir(context.Background(), db, "../../migrations"))
	s := New(db)
	now := time.Now().UTC().Truncate(time.Second)
	fixture := func(t *testing.T) (string, tuning.Recommendation, tuning.ContinuousState, tuning.PolicyRecord) {
		site := fmt.Sprintf("audit-fix-%d", time.Now().UnixNano())
		_, err := db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
		require.NoError(t, err)
		t.Cleanup(func() {
			for _, table := range []string{"channel_commands", "tuning_recommendations", "operation_audits", "channel_current", "channel_base_values", "tuning_continuous_states", "tuning_policies", "instances"} {
				key := "instance_id"
				if table == "instances" {
					key = "id"
				}
				_, _ = db.Exec("DELETE FROM "+table+" WHERE "+key+"=?", site)
			}
		})
		require.NoError(t, s.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: 7, Name: "uncapped", Models: "m", Weight: 50, Status: 1, Group: "default"}}, now))
		policy := tuning.DefaultPolicy()
		policy.DispatchModes = map[string]string{"m": "auto"}
		require.NoError(t, s.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: policy, Mode: "observe", UpdatedAt: now}))
		pr, _, err := s.GetPolicy(site)
		require.NoError(t, err)
		bases, err := s.ListChannelBaseValues(site, "m")
		require.NoError(t, err)
		require.Len(t, bases, 1)
		evaluation := &tuning.EvaluationContext{BaseWeight: bases[0].BaseWeight, BaseUpdatedAt: bases[0].UpdatedAt, PolicyUpdatedAt: pr.UpdatedAt, Params: pr.Policy.Continuous, EvaluatedAt: now}
		state := tuning.ContinuousState{InstanceID: site, ChannelID: 7, ModelName: "m", Phase: "normal", UpdatedAt: now, Evaluation: evaluation, Capacity: tuning.CapacityControl{Initialized: true, Fresh: true, Phase: "normal", ConfirmedWeight: 50}}
		require.NoError(t, s.PutContinuousState(state))
		rec := tuning.Recommendation{ID: site + "-rec", InstanceID: site, ChannelID: 7, Rule: "weight_write", ModeAtCreation: "auto", CreatedAt: now, CurrentWeight: 50, ProposedWeight: 55, Evidence: map[string]any{"capacity_managed": true, "capacity": state.Capacity, "model": "m", "evaluation": evaluation}}
		return site, rec, state, pr
	}
	for _, change := range []string{"off", "observe", "base", "params", "model", "cap", "legacy", "mixed", "disabled", "new_snapshot"} {
		t.Run("superseded_"+change, func(t *testing.T) {
			site, rec, _, pr := fixture(t)
			id, err := s.CreateContinuousWeightChange(rec, "system:auto", now)
			require.NoError(t, err)
			switch change {
			case "off", "observe":
				pr.Policy.DispatchModes["m"] = change
				require.NoError(t, s.PutPolicy(pr))
			case "params":
				pr.Policy.Continuous.Sensitivity = 1.5
				require.NoError(t, s.PutPolicy(pr))
			case "base":
				_, err = db.Exec(`UPDATE channel_base_values SET base_weight=70 WHERE instance_id=?`, site)
			case "model":
				_, err = db.Exec(`UPDATE channel_base_values SET model_name='other' WHERE instance_id=?`, site)
			case "cap":
				_, err = db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=?`, site)
			case "legacy":
				_, err = db.Exec(`UPDATE tuning_recommendations SET evidence_json='{}' WHERE command_id=?`, id)
			case "mixed":
				_, err = db.Exec(`UPDATE channel_current SET models_text='m,other' WHERE instance_id=?`, site)
			case "disabled":
				_, err = db.Exec(`UPDATE channel_current SET status='disabled' WHERE instance_id=?`, site)
			case "new_snapshot":
				_, err = db.Exec(`UPDATE channel_current SET weight=40,captured_at=? WHERE instance_id=?`, now.Add(time.Second), site)
			}
			require.NoError(t, err)
			commands, err := s.ClaimPendingCommands(site, now.Add(time.Second))
			require.NoError(t, err)
			require.Empty(t, commands)
			status, _, err := s.ContinuousCommandResult(id)
			require.NoError(t, err)
			require.Equal(t, "superseded", status)
			events, err := s.ListRecommendations(tuning.RecommendationQuery{InstanceID: site})
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, "expired", events[0].Status)
		})
	}
	for _, matched := range []bool{true, false} {
		t.Run(fmt.Sprintf("lost_ack_matched_%v", matched), func(t *testing.T) {
			site, rec, state, pr := fixture(t)
			id, err := s.CreateContinuousWeightChange(rec, "system:auto", now)
			require.NoError(t, err)
			events, err := s.ListRecommendations(tuning.RecommendationQuery{InstanceID: site})
			require.NoError(t, err)
			require.Equal(t, "pending", events[0].Status)
			commands, err := s.ClaimPendingCommands(site, now)
			require.NoError(t, err)
			require.Len(t, commands, 1)
			events, err = s.ListRecommendations(tuning.RecommendationQuery{InstanceID: site})
			require.NoError(t, err)
			require.Equal(t, "delivered", events[0].Status)
			require.Nil(t, events[0].OutcomeAt)
			commands, err = s.CommandsToReconcile(site, now)
			require.NoError(t, err)
			require.Empty(t, commands)
			// Mode off does not hide unresolved writes; reconciliation is read-only.
			pr.Policy.DispatchModes["m"] = "off"
			require.NoError(t, s.PutPolicy(pr))
			commands, err = s.CommandsToReconcile(site, now.Add(24*time.Hour))
			require.NoError(t, err)
			require.Len(t, commands, 1)
			require.Equal(t, "channel.reconcile", commands[0].CommandType)
			weight, status, priority, group := uint(55), 1, int64(11), "default"
			if !matched {
				weight = 40
			}
			result := agentgateway.ChannelCommandResult{ID: id, ChannelID: 7, Reconciled: true, Status: "observed", ObservedWeight: &weight, ObservedStatus: &status, ObservedPriority: &priority, ObservedGroup: &group}
			_, changed, err := s.CompleteReconciledCommand("other-instance", result, now.Add(24*time.Hour))
			require.NoError(t, err)
			require.False(t, changed)
			completed, changed, err := s.CompleteReconciledCommand(site, result, now.Add(24*time.Hour))
			require.NoError(t, err)
			require.True(t, changed)
			want := "succeeded"
			if !matched {
				want = "failed"
			}
			require.Equal(t, want, completed.Status)
			_, changed, err = s.CompleteChannelCommand(id, "succeeded", "late duplicate", now.Add(25*time.Hour))
			require.NoError(t, err)
			require.False(t, changed)
			bases, err := s.ListChannelBaseValues(site, "m")
			require.NoError(t, err)
			require.Equal(t, int64(weight), bases[0].CurrentWeight)
			events, err = s.ListRecommendations(tuning.RecommendationQuery{InstanceID: site})
			require.NoError(t, err)
			require.Equal(t, want, events[0].Status)
			require.NotNil(t, events[0].OutcomeAt)
			require.Equal(t, completed.ErrorSummary, events[0].Outcome["command_error"])
			loaded, err := s.ListContinuousStates(site)
			require.NoError(t, err)
			require.Equal(t, *state.Evaluation, *loaded[0].Evaluation)
			_, err = db.Exec(`DELETE FROM channel_commands WHERE id=?`, id)
			require.NoError(t, err)
			events, err = s.ListRecommendations(tuning.RecommendationQuery{InstanceID: site})
			require.NoError(t, err)
			require.Equal(t, "unknown", events[0].Status)
		})
	}
	t.Run("policy_compare_and_swap", func(t *testing.T) {
		site, _, _, current := fixture(t)
		next := current
		next.Policy.Continuous.Sensitivity = 1.5
		ok, err := s.PutPolicyIfCurrent(next, current, true)
		require.NoError(t, err)
		require.True(t, ok)
		stale := current
		stale.Policy.Continuous.Sensitivity = 2
		ok, err = s.PutPolicyIfCurrent(stale, current, true)
		require.NoError(t, err)
		require.False(t, ok)
		actual, _, err := s.GetPolicy(site)
		require.NoError(t, err)
		require.Equal(t, 1.5, actual.Policy.Continuous.Sensitivity)
	})
	t.Run("status_recovery_keeps_capacity_interlock", func(t *testing.T) {
		site, rec, state, _ := fixture(t)
		_, err := db.Exec(`UPDATE channel_base_values SET max_tpm=1000 WHERE instance_id=?`, site)
		require.NoError(t, err)
		enable := 1
		rec.Rule, rec.ProposedChannelStatus = "circuit_recovered", &enable
		state.Capacity.Fresh, state.Capacity.SampleAt = true, now
		state.Capacity.Active = true
		require.NoError(t, s.PutContinuousState(state))
		require.ErrorIs(t, s.CheckContinuousCapacity(rec, now), ErrCapacitySuperseded)
		state.Capacity.Active = false
		require.NoError(t, s.PutContinuousState(state))
		require.NoError(t, s.CheckContinuousCapacity(rec, now))
		require.ErrorIs(t, s.CheckContinuousCapacity(rec, now.Add(91*time.Second)), ErrCapacitySuperseded)
	})
	t.Run("upgrade_recovers_optimistic_legacy_writes", func(t *testing.T) {
		migration, err := os.ReadFile("../../migrations/119_tuning_evaluation.sql")
		require.NoError(t, err)
		query := string(migration)[strings.Index(string(migration), "UPDATE tuning_continuous_states"):]
		for _, status := range []string{"pending", "delivered", "failed", "succeeded"} {
			t.Run(status, func(t *testing.T) {
				site, rec, _, _ := fixture(t)
				id, err := s.CreateContinuousWeightChange(rec, "system:auto", now)
				require.NoError(t, err)
				_, err = db.Exec(`UPDATE channel_commands SET status=? WHERE id=?`, status, id)
				require.NoError(t, err)
				_, err = db.Exec(`UPDATE tuning_continuous_states SET last_written_weight=55,last_write_at=?,capacity_control_json='{}' WHERE instance_id=?`, now, site)
				require.NoError(t, err)
				_, err = db.Exec(query)
				require.NoError(t, err)
				states, err := s.ListContinuousStates(site)
				require.NoError(t, err)
				if status == "succeeded" {
					require.NotNil(t, states[0].LastWrittenWeight)
					require.Empty(t, states[0].Capacity.PendingCommandID)
				} else {
					require.Nil(t, states[0].LastWrittenWeight)
					require.Equal(t, id, states[0].Capacity.PendingCommandID)
					require.Equal(t, int64(50), states[0].Capacity.ConfirmedWeight)
					require.True(t, states[0].Capacity.Fresh)
				}
			})
		}
	})
}
