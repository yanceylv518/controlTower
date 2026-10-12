package tuning

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManualDisabledStopsEveryAutomaticPath(t *testing.T) {
	for _, phase := range []string{"normal", "circuit", "probing", "soft_start"} {
		t.Run(phase, func(t *testing.T) {
			f, e, base, policy := routineFixture()
			now := time.Now().UTC()
			old := f.states[1]
			old.Phase, old.PausedReason = phase, ManualDisabled
			old.ControlRevision = 3
			f.states[1] = old
			addNeutralPerformanceEvidence(f)
			require.False(t, routineEligible(base, old, policy))
			e.evaluateContinuous("i", policy, now, f)
			e.evaluateFastCircuit(FastCircuitBatch{InstanceID: "inst", ReportedAt: now, Metrics: []FastCircuitMetric{{ChannelID: 1, RequestCount: 100, ErrorCount: 100}}}, now)
			e.applyRoutineEvidence(context.Background(), routineTestProber{}, channelKey{"i", 1}, base, policy, 0, routineStreak{errors: 3}, now)
			require.Empty(t, f.writes)
			require.Empty(t, f.probes)
			require.Equal(t, old, f.states[1], "manual ownership must survive automatic evaluation")
			require.False(t, f.states[999].BaselineReady, "manual channel evidence must not form a healthy peer baseline")
		})
	}
}
