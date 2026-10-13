package directcontrol

import (
	"context"
	"crypto/rand"
	"log"
	"sync/atomic"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
)

type probeJournalStore interface {
	StoreProbeRecords(context.Context, []tuning.ProbeRecord, []tuning.ProbeRecorderHealth) error
}
type recoveryProbeJournal struct {
	channelcontrol.Prober
	store    probeJournalStore
	site     string
	round    string
	sequence *atomic.Int64
}

func (p recoveryProbeJournal) Probe(ctx context.Context, id int64, model string) (channelcontrol.ProbeResult, error) {
	return p.ProbeWithTimeout(ctx, id, model, channelcontrol.ProbeRequestTimeout)
}
func (p recoveryProbeJournal) ProbeWithTimeout(ctx context.Context, id int64, model string, timeout time.Duration) (channelcontrol.ProbeResult, error) {
	at := time.Now().UTC()
	v := tuning.ProbeRecord{ID: rand.Text(), InstanceID: p.site, ChannelID: id, ModelName: model, Kind: "recovery", StartedAt: at, DeadlineAt: at.Add(timeout), Outcome: "pending"}
	v.RoundID = p.round
	if p.sequence != nil {
		v.RoundIndex = int(p.sequence.Add(1))
	}
	// Recovery rounds are infrequent and already asynchronous; persist intent
	// before IO. Database failure is control uncertainty, never channel failure.
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := p.store.StoreProbeRecords(writeCtx, []tuning.ProbeRecord{v}, nil)
	cancel()
	if err != nil {
		return channelcontrol.ProbeResult{}, err
	}
	started := time.Now()
	result, err := channelcontrol.ProbeWithBudget(ctx, p.Prober, id, model, timeout)
	finished := time.Now().UTC()
	duration := time.Since(started).Seconds()
	v.FinishedAt = &finished
	v.DurationSeconds = &duration
	switch {
	case ctx.Err() != nil:
		v.Outcome, v.Reason = "canceled", "scheduler_canceled"
	case result.Slow || result.Duration > timeout.Seconds():
		v.Outcome, v.Reason = "slow", "slow_threshold"
		duration = timeout.Seconds()
	case err != nil:
		v.Outcome, v.Reason = "unknown", "control_unavailable"
	case result.Success:
		v.Outcome = "success"
	default:
		v.Outcome, v.Reason = "error", "channel_error"
	}
	writeCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if saveErr := p.store.StoreProbeRecords(writeCtx, []tuning.ProbeRecord{v}, nil); saveErr != nil {
		log.Printf("recovery probe result persistence failed site=%s channel=%d request=%s", p.site, id, v.ID)
	}
	return result, err
}
