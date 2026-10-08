package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"controltower/agent/internal/reporter"
	"controltower/internal/channelcontrol"
)

type backgroundProbeController struct {
	started chan int64
	release chan struct{}
	calls   atomic.Int32
}

func (p *backgroundProbeController) Probe(ctx context.Context, id int64, _ string) (channelcontrol.ProbeResult, error) {
	p.calls.Add(1)
	p.started <- id
	select {
	case <-ctx.Done():
		return channelcontrol.ProbeResult{}, ctx.Err()
	case <-p.release:
		return channelcontrol.ProbeResult{Success: true, Duration: 1}, nil
	}
}
func (p *backgroundProbeController) Update(context.Context, channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	return channelcontrol.Result{}, nil
}

func TestBackgroundProbesDoNotWaitForResponseOrCollectorDeadline(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &backgroundProbeController{started: make(chan int64, 8), release: make(chan struct{})}
	defer close(p.release)
	ctx := withProbeDispatcher(root, p)
	pass, endPass := context.WithCancel(ctx)
	command := reporter.ChannelCommand{ID: "probe", Type: "channel.probe", ChannelID: 1, ProbeCount: 1}
	if results := executeCommands(pass, p, []reporter.ChannelCommand{command}); len(results) != 0 {
		t.Fatal("in-flight probe presented as complete")
	}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	endPass()
	// A canceled collection pass must neither cancel the probe nor resubmit it.
	executeCommands(ctx, p, []reporter.ChannelCommand{command, {ID: "write", Type: "channel.update", ChannelID: 2}})
	if p.calls.Load() != 1 {
		t.Fatal("duplicate probe started")
	}
	d := ctx.Value(probeDispatcherKey{}).(*probeDispatcher)
	if results := d.drain(); len(results) != 0 {
		t.Fatalf("collector cancellation completed probe: %+v", results)
	}
	p.release <- struct{}{}
	deadline := time.After(time.Second)
	for {
		results := executeCommands(ctx, p, nil)
		if len(results) > 0 {
			if len(results) != 1 || results[0].Status != "succeeded" || results[0].Attempts != 1 {
				t.Fatalf("result not returned through report path: %+v", results)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("completed probe was lost")
		case <-time.After(time.Millisecond):
		}
	}
	if len(executeCommands(ctx, p, []reporter.ChannelCommand{command})) != 0 || p.calls.Load() != 1 {
		t.Fatal("completed command replayed")
	}
}

func TestBackgroundProbeConcurrencyIsBounded(t *testing.T) {
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &backgroundProbeController{started: make(chan int64, 8), release: make(chan struct{})}
	defer close(p.release)
	ctx := withProbeDispatcher(root, p)
	var commands []reporter.ChannelCommand
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		commands = append(commands, reporter.ChannelCommand{ID: id, Type: "channel.probe", ChannelID: int64(len(commands) + 1), ProbeCount: 1})
	}
	executeCommands(ctx, p, commands)
	for i := 0; i < 4; i++ {
		select {
		case <-p.started:
		case <-time.After(time.Second):
			t.Fatal("independent channels did not start concurrently")
		}
	}
	select {
	case <-p.started:
		t.Fatal("more than four active probes")
	default:
	}
	p.release <- struct{}{}
	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("queued fifth channel did not start after a slot freed")
	}
}
