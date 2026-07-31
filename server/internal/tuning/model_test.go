package tuning

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDecodePolicyJSONLegacyFlatFallsBackToDefaults(t *testing.T) {
	legacy := []byte(`{"window_minutes":1,"min_samples":999,"error_rate_threshold":0.01}`)
	got, err := DecodePolicyJSON(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultPolicy()) {
		t.Fatalf("legacy policy must fall back entirely to defaults: %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	if shape["scheduling"] == nil || shape["criteria"] == nil || shape["assignments"] == nil {
		t.Fatalf("policy must write the structured representation: %s", encoded)
	}
}

func TestCriteriaForAssignmentAndFallback(t *testing.T) {
	p := DefaultPolicy()
	special := p.Criteria[0]
	special.Name = "slow-model"
	special.LatencyFloorSeconds = 30
	p.Criteria = append(p.Criteria, special)
	p.Assignments["model-a"] = "slow-model"
	p.Assignments["model-missing"] = "removed"

	if got := criteriaFor(p, "model-a"); got.Name != "slow-model" || got.LatencyFloorSeconds != 30 {
		t.Fatalf("assignment not selected: %#v", got)
	}
	if got := criteriaFor(p, "model-b"); got.Name != "default" {
		t.Fatalf("unassigned model must use default: %#v", got)
	}
	if got := criteriaFor(p, "model-missing"); got.Name != "default" {
		t.Fatalf("missing assignment target must fall back to default: %#v", got)
	}
}

func TestDecodePolicyJSONLegacyDynamicWeightingEnabled(t *testing.T) {
	base := DefaultPolicy()
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	dynamic := shape["dynamic_weighting"].(map[string]any)
	delete(dynamic, "mode")
	dynamic["enabled"] = false
	raw, _ = json.Marshal(shape)
	got, err := DecodePolicyJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.DynamicWeighting.Mode != "off" {
		t.Fatalf("legacy enabled=false must become off: %#v", got.DynamicWeighting)
	}
	encoded, _ := json.Marshal(got)
	if bytes := string(encoded); !strings.Contains(bytes, `"mode":"off"`) || strings.Contains(bytes, `"enabled"`) {
		t.Fatalf("policy must write only the new mode field: %s", bytes)
	}

	dynamic["enabled"] = true
	raw, _ = json.Marshal(shape)
	got, err = DecodePolicyJSON(raw)
	if err != nil || got.DynamicWeighting.Mode != "observe" {
		t.Fatalf("legacy enabled=true must become observe: %#v err=%v", got.DynamicWeighting, err)
	}
}

func TestPolicyValidationUsesGroupedPaths(t *testing.T) {
	p := DefaultPolicy()
	p.Scheduling.MinSamples = 0
	p.Criteria[0].LatencyFloorSeconds = 0
	fields := p.Validate()
	if fields["scheduling.min_samples"] == "" {
		t.Fatalf("missing scheduling path: %#v", fields)
	}
	if fields["criteria[default].latency_floor_seconds"] == "" {
		t.Fatalf("missing criteria path: %#v", fields)
	}
	p = DefaultPolicy()
	p.DynamicWeighting.Mode = "confirm"
	if p.Validate()["dynamic_weighting.mode"] == "" {
		t.Fatalf("invalid weighting mode must be rejected: %#v", p.Validate())
	}
}
