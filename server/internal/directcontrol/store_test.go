package directcontrol

import (
	"context"
	"fmt"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
)

type fakeController struct {
	updates []channelcontrol.UpdateRequest
	probes  []int64
	results []channelcontrol.ProbeResult
	err     error
}

func (f *fakeController) Update(_ context.Context, request channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	f.updates = append(f.updates, request)
	return channelcontrol.Result{}, f.err
}
func (f *fakeController) Probe(_ context.Context, channelID int64, _ string) (channelcontrol.ProbeResult, error) {
	f.probes = append(f.probes, channelID)
	if f.err != nil {
		return channelcontrol.ProbeResult{}, f.err
	}
	return f.results[(len(f.probes)-1)%len(f.results)], nil
}
func (f *fakeController) Check(context.Context) error { return f.err }

func TestExecuteWeightUpdateMapsFields(t *testing.T) {
	f := &fakeController{}
	priority := int64(7)
	v := tuning.Recommendation{ChannelID: 9, ProposedWeight: 25, ProposedPriority: &priority, Rule: "circuit_recovered"}
	if err := executeWeightUpdate(context.Background(), f, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.updates) != 1 || f.updates[0].ChannelID != 9 || f.updates[0].Weight == nil || *f.updates[0].Weight != 25 {
		t.Fatalf("weight not mapped: %#v", f.updates)
	}
	if f.updates[0].Priority == nil || *f.updates[0].Priority != 7 || f.updates[0].Status != nil {
		t.Fatalf("priority mapping wrong: %#v", f.updates[0])
	}
}

func TestExecuteWeightUpdateSkipsPriorityOutsideCircuitRules(t *testing.T) {
	f := &fakeController{}
	priority := int64(7)
	v := tuning.Recommendation{ChannelID: 9, ProposedWeight: 25, ProposedPriority: &priority, Rule: "weight_write"}
	if err := executeWeightUpdate(context.Background(), f, v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.updates[0].Priority != nil {
		t.Fatalf("priority must only ride on circuit transitions: %#v", f.updates[0])
	}
}

func TestExecuteWeightUpdatePropagatesFailure(t *testing.T) {
	f := &fakeController{err: fmt.Errorf("boom")}
	if err := executeWeightUpdate(context.Background(), f, tuning.Recommendation{ChannelID: 9}); err == nil {
		t.Fatal("controller failure must propagate so the engine treats the write as failed")
	}
}

func TestExecuteProbeRoundCountsWholeRound(t *testing.T) {
	f := &fakeController{results: []channelcontrol.ProbeResult{
		{Success: true, Duration: 1.5},
		{Success: false, Message: "upstream 500"},
	}}
	slept := 0
	attempts, successes, durationSum, lastError := executeProbeRound(context.Background(), f, 9, "m", 4, 5, func(context.Context, time.Duration) { slept++ })
	if attempts != 4 || successes != 2 || durationSum != 3 || slept != 3 {
		t.Fatalf("round accounting wrong: attempts=%d successes=%d duration=%v slept=%d", attempts, successes, durationSum, slept)
	}
	if lastError != "upstream 500" {
		t.Fatalf("last error must survive: %q", lastError)
	}
}

func TestExecuteProbeRoundStopsOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeController{results: []channelcontrol.ProbeResult{{Success: true}}}
	attempts, _, _, lastError := executeProbeRound(ctx, f, 9, "m", 5, 1, func(context.Context, time.Duration) { cancel() })
	if attempts != 1 || lastError == "" {
		t.Fatalf("canceled round must stop early and record the reason: attempts=%d err=%q", attempts, lastError)
	}
}

type fakeStateLister struct {
	calls    int
	markerAt int
	id       string
}

func (f *fakeStateLister) ListContinuousStates(string) ([]tuning.ContinuousState, error) {
	f.calls++
	if f.markerAt > 0 && f.calls >= f.markerAt {
		return []tuning.ContinuousState{{ChannelID: 9, ProbeCommandID: &f.id}}, nil
	}
	return []tuning.ContinuousState{{ChannelID: 9}}, nil
}

func TestWaitForProbeMarkerBlocksUntilPersisted(t *testing.T) {
	lister := &fakeStateLister{markerAt: 3, id: "cmd-1"}
	ok := waitForProbeMarker(context.Background(), lister, "s", 9, "cmd-1", func(context.Context, time.Duration) {})
	if !ok || lister.calls != 3 {
		t.Fatalf("marker must be awaited before probing: ok=%v calls=%d", ok, lister.calls)
	}
}

func TestWaitForProbeMarkerGivesUpWhenNeverPersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lister := &fakeStateLister{id: "cmd-1"}
	if waitForProbeMarker(ctx, lister, "s", 9, "cmd-1", func(context.Context, time.Duration) {}) {
		t.Fatal("a marker that never lands must abort the round")
	}
}

func TestWaitForProbeMarkerIgnoresForeignRounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lister := &fakeStateLister{markerAt: 1, id: "other-round"}
	if waitForProbeMarker(ctx, lister, "s", 9, "cmd-1", func(context.Context, time.Duration) {}) {
		t.Fatal("a stale marker from another round must not release the probes")
	}
}

func TestRecordExecutedWriteRetriesThenReportsSuccess(t *testing.T) {
	calls := 0
	id, err := recordExecutedWrite(func() (string, error) {
		calls++
		if calls == 1 {
			return "", fmt.Errorf("lock wait timeout")
		}
		return "cmd-9", nil
	})
	if err != nil || id != "cmd-9" || calls != 2 {
		t.Fatalf("one transient failure must be retried: id=%q err=%v calls=%d", id, err, calls)
	}
}

func TestRecordExecutedWriteNeverForksFromReality(t *testing.T) {
	calls := 0
	id, err := recordExecutedWrite(func() (string, error) { calls++; return "", fmt.Errorf("boom") })
	if err != nil || id != "" || calls != 2 {
		t.Fatalf("a write that reached new-api must be reported as written even without a paper trail: id=%q err=%v calls=%d", id, err, calls)
	}
}
