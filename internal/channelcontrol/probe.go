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
	Aborted                         bool
}

type ProbeOptions struct {
	TimeoutSeconds int `json:"probe_timeout_seconds,omitempty"`
	SlowLimit      int `json:"probe_slow_limit,omitempty"`
	RoundSeconds   int `json:"probe_round_seconds,omitempty"`
}

func (o ProbeOptions) Defaults() ProbeOptions {
	if o.TimeoutSeconds <= 0 {
		o.TimeoutSeconds = int(ProbeRequestTimeout / time.Second)
	}
	if o.SlowLimit <= 0 {
		o.SlowLimit = ProbeSlowLimit
	}
	if o.RoundSeconds <= 0 {
		o.RoundSeconds = 480
	}
	return o
}

func ProbeWithBudget(ctx context.Context, p Prober, id int64, model string, timeout time.Duration) (ProbeResult, error) {
	if timed, ok := p.(interface {
		ProbeWithTimeout(context.Context, int64, string, time.Duration) (ProbeResult, error)
	}); ok {
		return timed.ProbeWithTimeout(ctx, id, model, timeout)
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, err := p.Probe(requestCtx, id, model)
	if ctx.Err() == nil && requestCtx.Err() == context.DeadlineExceeded {
		r.Slow = true
	}
	return r, err
}

// RunProbeRound shares the latency rule between Server and Agent. Slow
// responses are failures even when NewAPI reports success. Parent cancellation
// stops the round without creating slow-channel evidence.
// The legacy interval arguments remain readable for older policies/commands;
// explicit options enable the configured interval; legacy calls ignore it.
func RunProbeRound(ctx context.Context, controller Prober, channelID int64, model string, count, interval int, sleep func(context.Context, time.Duration), options ...ProbeOptions) ProbeRoundResult {
	o := ProbeOptions{}.Defaults()
	if len(options) > 0 {
		o = options[0].Defaults()
	}
	var result ProbeRoundResult
	for attempt := 0; attempt < max(count, 1); attempt++ {
		if ctx.Err() != nil {
			result.Aborted = true
			result.Error = ctx.Err().Error()
			break
		}
		if attempt > 0 && interval > 0 && len(options) > 0 {
			if sleep != nil {
				sleep(ctx, time.Duration(interval)*time.Second)
			} else {
				timer := time.NewTimer(time.Duration(interval) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		}
		if ctx.Err() != nil {
			result.Aborted = true
			result.Error = ctx.Err().Error()
			break
		}
		probe, err := ProbeWithBudget(ctx, controller, channelID, model, time.Duration(o.TimeoutSeconds)*time.Second)
		if ctx.Err() != nil {
			result.Aborted = true
			result.Error = ctx.Err().Error()
			break
		}
		// Only settled requests count toward a complete failed round. A parent
		// cancellation on the last request must not satisfy that disable rule.
		result.Attempts++
		if probe.Slow || probe.Duration > float64(o.TimeoutSeconds) {
			result.SlowStreak++
			result.Error = fmt.Sprintf("probe exceeded %ds (%d slow results)", o.TimeoutSeconds, result.SlowStreak)
			if result.SlowStreak >= o.SlowLimit {
				break
			}
			continue
		}
		if err == nil && probe.Success {
			result.SlowStreak = 0
			result.Successes++
			result.DurationSeconds += probe.Duration
		} else if err != nil {
			// An unknown control/transport result breaks evidence coverage.
			result.SlowStreak = 0
			result.Error = err.Error()
		} else {
			result.Error = probe.Message
		}
	}
	return result
}
