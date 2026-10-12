package directcontrol

import (
	"context"
	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type memoryProbeJournal struct {
	rows []tuning.ProbeRecord
	err  error
}

func (j *memoryProbeJournal) StoreProbeRecords(_ context.Context, rows []tuning.ProbeRecord, _ []tuning.ProbeRecorderHealth) error {
	j.rows = append(j.rows, rows...)
	return j.err
}
func TestRecoveryProbeJournalRecordsEachResultWithoutChangingRoundOutcome(t *testing.T) {
	for _, tc := range []struct {
		result  channelcontrol.ProbeResult
		outcome string
	}{{channelcontrol.ProbeResult{Success: true}, "success"}, {channelcontrol.ProbeResult{Slow: true}, "slow"}, {channelcontrol.ProbeResult{}, "error"}} {
		f := &fakeController{results: []channelcontrol.ProbeResult{tc.result}}
		j := &memoryProbeJournal{}
		p := recoveryProbeJournal{Prober: f, store: j, site: "site"}
		result, err := p.ProbeWithTimeout(context.Background(), 7, "m", time.Second)
		require.NoError(t, err)
		require.Equal(t, tc.result, result)
		require.Len(t, j.rows, 2)
		require.Equal(t, "pending", j.rows[0].Outcome)
		require.Equal(t, tc.outcome, j.rows[1].Outcome)
		require.Equal(t, j.rows[0].ID, j.rows[1].ID)
		require.Equal(t, "recovery", j.rows[1].Kind)
		if tc.outcome == "slow" {
			require.Equal(t, 1.0, *j.rows[1].DurationSeconds)
		}
	}
	f := &fakeController{}
	j := &memoryProbeJournal{err: errors.New("database offline")}
	p := recoveryProbeJournal{Prober: f, store: j}
	_, err := p.Probe(context.Background(), 7, "m")
	require.Error(t, err)
	require.Empty(t, f.probes)
}

type memoryJournal struct {
	controller          *fakeController
	begun, finished     []tuning.ChannelControlRequest
	beginErr, finishErr error
}

func (j *memoryJournal) BeginChannelControlRequest(_ context.Context, v tuning.ChannelControlRequest) error {
	if len(j.controller.updates) > 0 {
		panic("intent must precede write")
	}
	j.begun = append(j.begun, v)
	return j.beginErr
}
func (j *memoryJournal) FinishChannelControlRequest(_ context.Context, v tuning.ChannelControlRequest) error {
	j.finished = append(j.finished, v)
	return j.finishErr
}
func (j *memoryJournal) LinkChannelControlRequest(context.Context, string, string) error { return nil }
func TestChannelJournalPersistsIntentAndSafeUncertainOutcome(t *testing.T) {
	for _, tc := range []struct {
		name            string
		err             error
		outcome, reason string
	}{{"success", nil, "succeeded", ""}, {"timeout", context.DeadlineExceeded, "unconfirmed", "upstream_timeout"}, {"unknown", errors.New("token=secret body=private"), "unconfirmed", "upstream_request_failed"}, {"missing", channelcontrol.ErrChannelNotFound, "failed", "channel_not_found"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeController{err: tc.err}
			j := &memoryJournal{controller: f}
			c := &journalController{Controller: f, journal: j, site: "a", actor: "test"}
			weight := uint(0)
			_, err := c.Update(context.Background(), channelcontrol.UpdateRequest{ChannelID: 7, Weight: &weight})
			require.Equal(t, tc.err, err)
			require.Len(t, j.begun, 1)
			require.Len(t, j.finished, 1)
			require.Equal(t, tc.outcome, j.finished[0].Outcome)
			require.Equal(t, tc.reason, j.finished[0].Reason)
			require.Equal(t, uint(0), j.begun[0].Payload["target"].(map[string]any)["weight"])
		})
	}
	f := &fakeController{}
	j := &memoryJournal{controller: f, beginErr: errors.New("db offline")}
	c := &journalController{Controller: f, journal: j}
	_, err := c.Update(context.Background(), channelcontrol.UpdateRequest{ChannelID: 7})
	require.Error(t, err)
	require.Empty(t, f.updates)
	j.beginErr = nil
	j.finishErr = errors.New("db offline after write")
	_, err = c.Update(context.Background(), channelcontrol.UpdateRequest{ChannelID: 7})
	require.NoError(t, err)
	require.Len(t, f.updates, 1) // known applied write must not become retryable failure.
}
