package main

import (
	"context"
	"testing"

	"controltower/agent/internal/channelcontrol"
	"controltower/agent/internal/reporter"
)

type probeController struct {
	results []channelcontrol.ProbeResult
	calls   int
}

func (p *probeController) Update(context.Context, channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	return channelcontrol.Result{}, nil
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
