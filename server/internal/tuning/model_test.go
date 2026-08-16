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

func TestDecodePolicyJSONDefaultsWriteHysteresisForOldPolicies(t *testing.T) {
	p, err := DecodePolicyJSON([]byte(`{"scheduling":{"window_minutes":15,"min_samples":20,"sparse_min_samples":10,"sparse_lookback_minutes":360},"dispatch_modes":{},"continuous":{"sensitivity":1,"otps_cap":1.5,"circuit_threshold":0.1,"recovery_threshold":0.2,"circuit_error_rate":0.3,"recovery_error_rate":0.1,"silent_minutes":5,"probe_interval_seconds":5,"probe_count":10,"soft_start_multiplier":0.2,"window_minutes":15,"min_samples":20,"sparse_lookback_minutes":360}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Continuous.WriteDeadbandPercent != 5 || p.Continuous.MinWriteIntervalMinutes != 5 {
		t.Fatalf("old policy must receive hysteresis defaults: %#v", p.Continuous)
	}
	if p.Continuous.SpeedExponent != .35 || p.Continuous.CacheExponent != .15 || p.Continuous.OTPSExponent != .25 ||
		p.Continuous.ErrorHealthyRate != .01 || p.Continuous.ErrorMinFactor != .20 ||
		p.Continuous.CombinedMinFactor != .50 || p.Continuous.CombinedMaxFactor != 1.50 {
		t.Fatalf("old policy must receive evaluation curve defaults: %#v", p.Continuous)
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

func TestPolicyValidationCoversWriteHysteresisRanges(t *testing.T) {
	p := DefaultPolicy()
	p.Continuous.WriteDeadbandPercent = -1
	p.Continuous.MinWriteIntervalMinutes = 0
	fields := p.Validate()
	if fields["continuous.write_deadband_percent"] == "" || fields["continuous.min_write_interval_minutes"] == "" {
		t.Fatalf("missing lower-bound validation: %#v", fields)
	}
	p.Continuous.WriteDeadbandPercent = 51
	p.Continuous.MinWriteIntervalMinutes = 61
	fields = p.Validate()
	if fields["continuous.write_deadband_percent"] == "" || fields["continuous.min_write_interval_minutes"] == "" {
		t.Fatalf("missing upper-bound validation: %#v", fields)
	}
}
