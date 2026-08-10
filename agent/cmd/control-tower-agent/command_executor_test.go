package main

import (
	"context"
	"testing"

	"controltower/internal/channelcontrol"
	"controltower/agent/internal/reporter"
)

type probeController struct {
	results []channelcontrol.ProbeResult
	calls   int
	updates []channelcontrol.UpdateRequest
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
