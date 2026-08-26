package tuning

import (
	"testing"
	"time"
)

func TestFastCircuitUsesFreshAgentBatchAndConfiguredThresholds(t *testing.T) {
	now := time.Now().UTC()
	p := DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto"}
	pr := PolicyRecord{InstanceID: "i", Policy: p}
	f := &continuousFake{
		policy: &pr,
		bases:  []ChannelBaseValue{{ChannelID: 7, ChannelName: "upstream", ModelName: "m", Models: []string{"m"}, BaseWeight: 100, BasePriority: 3, CurrentWeight: 100}},
		states: map[int64]ContinuousState{7: {InstanceID: "i", ChannelID: 7, ModelName: "m", Phase: "normal"}},
	}
	e := NewEngine(f)
	e.evaluateFastCircuit(FastCircuitBatch{InstanceID: "inst", AgentID: "a", BatchID: "b", ReportedAt: now, Metrics: []FastCircuitMetric{{ChannelID: 7, RequestCount: 50, ErrorCount: 26, UserErrorCount: 1}}}, now)
	if len(f.writes) != 1 || f.writes[0].ProposedWeight != 0 || f.writes[0].Evidence["trigger"] != "agent_report_batch" {
		t.Fatalf("expected one fast circuit write: %#v", f.writes)
	}
	state := f.states[7]
	if state.Phase != "circuit" || state.LastWrittenWeight == nil || *state.LastWrittenWeight != 0 {
		t.Fatalf("unexpected fast circuit state: %#v", state)
	}
}

func TestFastCircuitHonorsMinimumSamplesRateFreshnessAndAutoMode(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name       string
		mode       string
		reportedAt time.Time
		metric     FastCircuitMetric
	}{
		{"below samples", "auto", now, FastCircuitMetric{ChannelID: 7, RequestCount: 49, ErrorCount: 49}},
		{"below rate", "auto", now, FastCircuitMetric{ChannelID: 7, RequestCount: 50, ErrorCount: 24}},
		{"observe", "observe", now, FastCircuitMetric{ChannelID: 7, RequestCount: 50, ErrorCount: 50}},
		{"stale", "auto", now.Add(-3 * time.Minute), FastCircuitMetric{ChannelID: 7, RequestCount: 50, ErrorCount: 50}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DefaultPolicy()
			p.DispatchModes = map[string]string{"m": tc.mode}
			pr := PolicyRecord{InstanceID: "i", Policy: p}
			f := &continuousFake{policy: &pr, bases: []ChannelBaseValue{{ChannelID: 7, ModelName: "m", Models: []string{"m"}, BaseWeight: 100}}, states: map[int64]ContinuousState{}}
			NewEngine(f).evaluateFastCircuit(FastCircuitBatch{InstanceID: "inst", ReportedAt: tc.reportedAt, Metrics: []FastCircuitMetric{tc.metric}}, now)
			if len(f.writes) != 0 {
				t.Fatalf("unexpected fast circuit write: %#v", f.writes)
			}
		})
	}
}
