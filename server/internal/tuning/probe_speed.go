package tuning

import (
	"context"
	"log"
	"math"
	"time"
)

// Routine speed evidence is independent of NewAPI consumption logs and TTFT.
type RoutineProbeSample struct {
	InstanceID, ModelName      string
	ChannelID, ControlRevision int64
	BaseUpdatedAt, StartedAt   time.Time
	DurationSeconds            float64
}

type ProbeSpeedStats struct {
	Samples         int64     `json:"samples"`
	MeanSeconds     float64   `json:"mean_seconds"`
	BaselineSeconds float64   `json:"baseline_seconds"`
	LastStartedAt   time.Time `json:"last_started_at"`
	Factor          float64   `json:"factor"`
	Ready           bool      `json:"ready"`
}

type routineSpeedStore interface {
	RecordRoutineProbeSample(context.Context, RoutineProbeSample) error
	QueryRoutineProbeSpeeds(context.Context, string, time.Time, time.Time) (map[int64]ProbeSpeedStats, error)
	PruneRoutineProbeSamples(context.Context, time.Time) error
}

func (e *Engine) runProbeSampleWriter(ctx context.Context) {
	s, ok := e.store.(routineSpeedStore)
	if !ok {
		return
	}
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case sample := <-e.routineSamples:
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.RecordRoutineProbeSample(writeCtx, sample)
			cancel()
			if err != nil {
				log.Printf("routine probe speed persistence failed site=%s channel=%d", sample.InstanceID, sample.ChannelID)
			}
		case <-tick.C:
			cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.PruneRoutineProbeSamples(cleanupCtx, time.Now().UTC().Add(-48*time.Hour))
			cancel()
			if err != nil {
				log.Printf("routine probe speed cleanup failed")
			}
		}
	}
}

func probeSpeedEvidenceReady(s ProbeSpeedStats, minSamples int64, now time.Time, policy ...ContinuousDispatchParams) bool {
	return optionalRules(policy).RoutineProbeEnabled && s.Samples >= minSamples && s.MeanSeconds > 0 && !math.IsNaN(s.MeanSeconds) && !math.IsInf(s.MeanSeconds, 0) &&
		!s.LastStartedAt.IsZero() && !s.LastStartedAt.After(now) && now.Sub(s.LastStartedAt) <= time.Duration(optionalRules(policy).ProbeMaxAgeSeconds)*time.Second
}

func probeSpeedBaseline(rows []ChannelBaseValue, stats map[int64]ProbeSpeedStats, states map[int64]ContinuousState, minSamples int64, now time.Time, policy ...ContinuousDispatchParams) float64 {
	var means []float64
	for _, b := range rows {
		s := states[b.ChannelID]
		if b.BaseWeight <= 0 || len(b.Models) != 1 || s.PausedReason == ManualDisabled || s.CircuitDisabled || s.RoutineCircuitLatched || (s.Phase != "" && s.Phase != "normal" && s.Phase != "soft_start") {
			continue
		}
		if m := stats[b.ChannelID]; probeSpeedEvidenceReady(m, minSamples, now, policy...) {
			means = append(means, m.MeanSeconds)
		}
	}
	if len(means) < optionalRules(policy).BaselineMinChannels {
		return 0
	}
	return average(means)
}

func evaluateProbeSpeed(s ProbeSpeedStats, baseline float64, p ContinuousDispatchParams, now time.Time) ProbeSpeedStats {
	s.BaselineSeconds, s.Factor, s.Ready = baseline, 1, false
	if p.ProbeSpeedExponent <= 0 || baseline <= 0 || !probeSpeedEvidenceReady(s, p.MinSamples, now, p) {
		return s
	}
	s.Ready = true
	s.Factor = clamp(math.Pow(baseline/s.MeanSeconds, p.ProbeSpeedExponent*p.Sensitivity), p.ProbeSpeedMinFactor, p.ProbeSpeedMaxFactor)
	return s
}
