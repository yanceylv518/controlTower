package channelcontrol

import (
	"context"
	"fmt"
	"time"
)

const ProbeRequestTimeout = 30 * time.Second
const ProbeSlowLimit = 2

type Prober interface {
	Probe(context.Context, int64, string) (ProbeResult, error)
}

type ProbeRoundResult struct {
	Attempts, Successes, SlowStreak int
	DurationSeconds                 float64
	Error                           string
}

// RunProbeRound shares the latency rule between Server and Agent. Slow
// responses are failures even when NewAPI reports success. Parent cancellation
// stops the round without creating slow-channel evidence.
// The legacy interval arguments remain readable for older policies/commands;
// new rounds are sequential with no delay between requests.
func RunProbeRound(ctx context.Context, controller Prober, channelID int64, model string, count, _ int, _ func(context.Context, time.Duration)) ProbeRoundResult {
	var result ProbeRoundResult
	for attempt := 0; attempt < max(count, 1); attempt++ {
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			break
		}
		probe, err := controller.Probe(ctx, channelID, model)
		if ctx.Err() != nil {
			result.Error = ctx.Err().Error()
			break
		}
		// Only settled requests count toward a complete failed round. A parent
		// cancellation on the last request must not satisfy that disable rule.
		result.Attempts++
		if probe.Slow || probe.Duration > ProbeRequestTimeout.Seconds() {
			result.SlowStreak++
			result.Error = fmt.Sprintf("probe exceeded 30s (%d consecutive)", result.SlowStreak)
			if result.SlowStreak >= ProbeSlowLimit {
				break
			}
			continue
		}
		result.SlowStreak = 0
		if err == nil && probe.Success {
			result.Successes++
			result.DurationSeconds += probe.Duration
		} else if err != nil {
			result.Error = err.Error()
		} else {
			result.Error = probe.Message
		}
	}
	return result
}
