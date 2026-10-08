package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"controltower/agent/internal/reporter"
	"controltower/internal/channelcontrol"
)

type blockedChannelController struct {
	started chan int64
	release chan struct{}
	mu      sync.Mutex
	weights []uint
}

func (c *blockedChannelController) Update(ctx context.Context, r channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	c.started <- r.ChannelID
	if r.ChannelID == 1 && *r.Weight == 55 {
		select {
		case <-c.release:
		case <-ctx.Done():
			return channelcontrol.Result{}, ctx.Err()
		}
	}
	c.mu.Lock()
	if r.ChannelID == 1 {
		c.weights = append(c.weights, *r.Weight)
	}
	c.mu.Unlock()
	return channelcontrol.Result{}, nil
}
func (c *blockedChannelController) Probe(context.Context, int64, string) (channelcontrol.ProbeResult, error) {
	return channelcontrol.ProbeResult{}, nil
}

func TestCommandWorkersIsolateSlowChannelsAndKeepSameChannelOrder(t *testing.T) {
	c := &blockedChannelController{started: make(chan int64, 4), release: make(chan struct{})}
	weight, zero := uint(55), uint(0)
	done := make(chan []reporter.ChannelCommandResult, 1)
	go func() {
		done <- executeCommands(context.Background(), c, []reporter.ChannelCommand{
			{ID: "slow", Type: "channel.update", ChannelID: 1, Weight: &weight},
			{ID: "zero", Type: "channel.update", ChannelID: 1, Weight: &zero},
			{ID: "peer", Type: "channel.update", ChannelID: 2, Weight: &weight},
		})
	}()
	defer close(c.release)
	for i := 0; i < 2; i++ {
		select {
		case <-c.started:
		case <-time.After(2 * time.Second):
			t.Fatal("peer blocked by slow channel")
		}
	}
	select {
	case channel := <-c.started:
		t.Fatalf("same-channel write overtook slow write: %d", channel)
	default:
	}
	// Use the same release channel twice safely by releasing the waiter with
	// one send here and closing it in cleanup if an assertion fails.
	c.release <- struct{}{}
	select {
	case results := <-done:
		if results[0].ID != "slow" || results[1].ID != "zero" || results[2].ID != "peer" {
			t.Fatal("result ordering changed")
		}
		for _, r := range results {
			if r.Status != "succeeded" {
				t.Fatalf("failed command: %+v", r)
			}
		}
		if c.weights[len(c.weights)-1] != 0 {
			t.Fatal("old increase executed after zero")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not finish")
	}
}

type probeController struct {
	results []channelcontrol.ProbeResult
	calls   int
	updates []channelcontrol.UpdateRequest
}

type readbackController struct {
	probeController
	readErr error
}

func (c *readbackController) Read(context.Context, int64) (channelcontrol.Result, error) {
	w, status, priority := uint(55), 1, int64(11)
	return channelcontrol.Result{Weight: &w, Status: &status, Priority: &priority, Group: "default"}, c.readErr
}

func TestReconcileNeverReplaysAWrite(t *testing.T) {
	for _, readErr := range []error{nil, errors.New("connection unavailable")} {
		c := &readbackController{readErr: readErr}
		oldTarget := uint(90)
		results := executeCommands(context.Background(), c, []reporter.ChannelCommand{{ID: "old", Type: "channel.reconcile", ChannelID: 9, Weight: &oldTarget}})
		if len(c.updates) != 0 || len(results) != 1 || !results[0].Reconciled {
			t.Fatalf("readback replayed update: %+v", results)
		}
		if readErr == nil && (results[0].Status != "observed" || *results[0].ObservedWeight != 55) {
			t.Fatalf("did not report actual value: %+v", results)
		}
		if readErr != nil && results[0].Status != "unconfirmed" {
			t.Fatalf("read failure is not proof of write failure: %+v", results)
		}
	}
}

func (p *probeController) Update(_ context.Context, request channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	p.updates = append(p.updates, request)
	return channelcontrol.Result{}, nil
}

func TestExecuteVerifyCommandUsesNoChangeUpdate(t *testing.T) {
	c := &probeController{}
	results := executeCommands(context.Background(), c, []reporter.ChannelCommand{{ID: "v", Type: "channel.verify", ChannelID: 9}})
	if len(results) != 1 || results[0].Status != "succeeded" || len(c.updates) != 1 || c.updates[0].ChannelID != 9 || c.updates[0].Weight != nil || c.updates[0].Priority != nil || c.updates[0].Status != nil {
		t.Fatalf("unexpected verify result: results=%#v updates=%#v", results, c.updates)
	}
}

func TestExecuteGroupCommandForwardsNormalizedGroup(t *testing.T) {
	c := &probeController{}
	group := "default,vip"
	results := executeCommands(context.Background(), c, []reporter.ChannelCommand{{ID: "g", Type: "channel.update", ChannelID: 9, Group: &group}})
	if len(results) != 1 || results[0].Status != "succeeded" || len(c.updates) != 1 {
		t.Fatalf("unexpected group result: results=%#v updates=%#v", results, c.updates)
	}
	if c.updates[0].Group == nil || *c.updates[0].Group != group || c.updates[0].Weight != nil || c.updates[0].Priority != nil || c.updates[0].Status != nil {
		t.Fatalf("group update fields were not forwarded exactly: %#v", c.updates[0])
	}
}

func (p *probeController) Probe(context.Context, int64, string) (channelcontrol.ProbeResult, error) {
	r := p.results[p.calls]
	p.calls++
	return r, nil
}

func TestExecuteProbeCommandAggregatesRound(t *testing.T) {
	c := &probeController{results: []channelcontrol.ProbeResult{{Success: true, Duration: 1}, {Success: false, Message: "upstream error"}, {Success: true, Duration: 3}}}
	results := executeCommands(context.Background(), c, []reporter.ChannelCommand{{ID: "p", Type: "channel.probe", ChannelID: 7, Model: "m", ProbeCount: 3}})
	if len(results) != 1 || results[0].Status != "succeeded" || results[0].Attempts != 3 || results[0].Successes != 2 || results[0].DurationSeconds != 4 {
		t.Fatalf("unexpected probe result: %#v", results)
	}
}

func TestExecuteProbeCommandReportsConsecutiveSlowRequests(t *testing.T) {
	c := &probeController{results: []channelcontrol.ProbeResult{{Success: true, Duration: 1}, {Slow: true}, {Success: true, Duration: 31}, {Success: true}}}
	results := executeCommands(context.Background(), c, []reporter.ChannelCommand{{ID: "slow", Type: "channel.probe", ChannelID: 7, Model: "m", ProbeCount: 10}})
	if len(results) != 1 || results[0].Attempts != 3 || results[0].Successes != 1 || results[0].ProbeSlowStreak != 2 || c.calls != 3 {
		t.Fatalf("slow round: %+v calls=%d", results, c.calls)
	}
}
