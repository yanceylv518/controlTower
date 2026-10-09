package mysqlstore

import (
	"controltower/server/internal/tuning"
	"errors"
	"testing"
)

func TestCircuitGateRejectsLegacyStatusBeforeDispatch(t *testing.T) {
	status := 3
	err := checkCircuitStatus(nil, tuning.Recommendation{Rule: "circuit_disabled", ModeAtCreation: "auto", ProposedChannelStatus: &status})
	if !errors.Is(err, ErrCircuitStatusSuperseded) {
		t.Fatalf("legacy status must be invalidated, got %v", err)
	}
}
