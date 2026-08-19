package tuning

import (
	"encoding/json"
	"testing"
)

func TestDecodePolicyJSONIgnoresRetiredDutyFields(t *testing.T) {
	raw := []byte(`{
		"scheduling":{"window_minutes":20,"min_samples":30,"sparse_min_samples":10,"sparse_lookback_minutes":360,"trial_initial_minutes":1},
		"dynamic_weighting":{"enabled":true},
		"criteria":[{"name":"legacy"}],
		"assignments":{"m":"legacy"},
		"dispatch_modes":{"m":"auto"},
		"continuous":{"sensitivity":1,"otps_cap":1.5,"circuit_threshold":0.1,"recovery_threshold":0.2,"silent_minutes":5,"probe_interval_seconds":5,"probe_count":10,"soft_start_multiplier":0.2,"window_minutes":15,"min_samples":20,"sparse_lookback_minutes":360}
	}`)
	p, err := DecodePolicyJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheduling.WindowMinutes != 20 || p.DispatchModes["m"] != "auto" {
		t.Fatalf("retained fields lost: %#v", p)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(out, &shape)
	for _, retired := range []string{"dynamic_weighting", "criteria", "assignments"} {
		if shape[retired] != nil {
			t.Fatalf("retired field %s was emitted: %s", retired, out)
		}
	}
}

func TestPolicyValidationCoversContinuousAndRetainedScheduling(t *testing.T) {
	p := DefaultPolicy()
	if fields := p.Validate(); len(fields) != 0 {
		t.Fatalf("default invalid: %#v", fields)
	}
	p.Continuous.CircuitThreshold = p.Continuous.RecoveryThreshold
	p.Scheduling.SparseMinSamples = p.Scheduling.MinSamples + 1
	fields := p.Validate()
	if fields["continuous.circuit_threshold"] == "" || fields["scheduling.sparse_min_samples"] == "" {
		t.Fatalf("missing validation: %#v", fields)
	}
}

func TestPolicyValidationCoversEvaluationCurveRanges(t *testing.T) {
	p := DefaultPolicy()
	p.Continuous.CacheMinFactor = 1.1
	p.Continuous.ErrorPoorRate = p.Continuous.ErrorDegradedRate
	p.Continuous.CombinedMaxFactor = p.Continuous.CombinedMinFactor
	fields := p.Validate()
	for _, field := range []string{"continuous.cache_factor_range", "continuous.error_rate_breakpoints", "continuous.combined_factor_range"} {
		if fields[field] == "" {
			t.Fatalf("missing validation for %s: %#v", field, fields)
		}
	}
}

func TestPolicyValidationRequiresSpeedPercentileWeightsToSumToOne(t *testing.T) {
	p := DefaultPolicy()
	p.Continuous.SpeedP50Weight = .6
	if fields := p.Validate(); fields["continuous.speed_percentile_weights"] == "" {
		t.Fatalf("missing percentile weight validation: %#v", fields)
	}
	p.Continuous.SpeedP50Weight, p.Continuous.SpeedP90Weight, p.Continuous.SpeedP95Weight = 0, 0, 1
	if fields := p.Validate(); fields["continuous.speed_percentile_weights"] != "" {
		t.Fatalf("valid percentile weights rejected: %#v", fields)
	}
}

// Policies persisted by earlier releases carry retired knobs (otps_cap,
// write_deadband_percent, min_write_interval_minutes); decoding must ignore
// them instead of failing or resurrecting semantics.
func TestDecodePolicyJSONIgnoresRetiredWriteKnobs(t *testing.T) {
	p, err := DecodePolicyJSON([]byte(`{"scheduling":{"window_minutes":15,"min_samples":20,"sparse_min_samples":10,"sparse_lookback_minutes":360},"dispatch_modes":{},"continuous":{"sensitivity":1,"otps_cap":2.5,"write_deadband_percent":30,"min_write_interval_minutes":45}}`))
	if err != nil {
		t.Fatal(err)
	}
	if fields := p.Validate(); len(fields) != 0 {
		t.Fatalf("retired knobs must not fail validation: %#v", fields)
	}
}
