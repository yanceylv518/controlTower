package tuning

import (
	"context"
	"crypto/rand"
	"log"
	"sync"
	"time"
)

const ProbeRecordRetention = 7 * 24 * time.Hour
const probeRecordBufferLimit = 65536

type ProbeRecord struct {
	RoundID         string     `json:"round_id,omitempty"`
	RoundIndex      int        `json:"round_index,omitempty"`
	ID              string     `json:"id"`
	InstanceID      string     `json:"instance_id"`
	ChannelID       int64      `json:"channel_id"`
	ChannelName     string     `json:"channel_name"`
	ModelName       string     `json:"model_name"`
	Kind            string     `json:"kind"`
	StartedAt       time.Time  `json:"started_at"`
	DeadlineAt      time.Time  `json:"deadline_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Outcome         string     `json:"outcome"`
	Reason          string     `json:"reason"`
	DurationSeconds *float64   `json:"duration_seconds,omitempty"`
}

type ProbeRecorderHealth struct {
	InstanceID     string     `json:"-"`
	RunID          string     `json:"-"`
	StartedAt      time.Time  `json:"started_at"`
	LastSeenAt     time.Time  `json:"last_seen_at"`
	LastLossAt     *time.Time `json:"last_loss_at,omitempty"`
	DroppedRecords int64      `json:"dropped_records"`
	QueuedRecords  int64      `json:"queued_records"`
}

type ProbeRecordQuery struct {
	RoundID, Dispatch                          string
	Site, Model, Kind, Search, Outcome, Cursor string
	ChannelID                                  int64
	From, To, Now                              time.Time
	Limit                                      int
}

type ProbeMetrics struct {
	Due                int64      `json:"due"`
	Sent               int64      `json:"sent"`
	Success            int64      `json:"success"`
	Slow               int64      `json:"slow"`
	Errors             int64      `json:"errors"`
	Unknown            int64      `json:"unknown"`
	Canceled           int64      `json:"canceled"`
	Pending            int64      `json:"pending"`
	Unconfirmed        int64      `json:"unconfirmed"`
	Skipped            int64      `json:"skipped"`
	GlobalLimited      int64      `json:"global_limited"`
	ChannelLimited     int64      `json:"channel_limited"`
	EvidenceLimited    int64      `json:"evidence_limited"`
	SuccessRate        *float64   `json:"success_rate"`
	MeanSeconds        *float64   `json:"mean_seconds"`
	MeanSuccessSeconds *float64   `json:"mean_success_seconds"`
	MaxSeconds         *float64   `json:"max_seconds"`
	LastStartedAt      *time.Time `json:"last_started_at,omitempty"`
}
type ProbeChannelStats struct {
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ModelName   string `json:"model_name"`
	ProbeMetrics
}
type ProbeTrendPoint struct {
	At time.Time `json:"at"`
	ProbeMetrics
}
type ProbeOverview struct {
	RetentionDays int                  `json:"retention_days"`
	From          time.Time            `json:"from"`
	To            time.Time            `json:"to"`
	QueriedAt     time.Time            `json:"queried_at"`
	BucketSeconds int                  `json:"bucket_seconds"`
	Total         ProbeMetrics         `json:"total"`
	Channels      []ProbeChannelStats  `json:"channels"`
	Trend         []ProbeTrendPoint    `json:"trend"`
	Recorder      *ProbeRecorderHealth `json:"recorder,omitempty"`
}
type ProbeRecordPage struct {
	Items      []ProbeRecord `json:"items"`
	Total      int64         `json:"total"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
type ProbeRecordsReader interface {
	QueryProbeOverview(context.Context, ProbeRecordQuery) (ProbeOverview, error)
	QueryProbeRecords(context.Context, ProbeRecordQuery) (ProbeRecordPage, error)
}
type probeRecordsStore interface {
	StoreProbeRecords(context.Context, []ProbeRecord, []ProbeRecorderHealth) error
	PruneProbeRecords(context.Context, time.Time) error
}

// A pending snapshot can be replaced by its terminal snapshot during a write.
// Acknowledgement removes only the exact stage written; retries are idempotent.
type probeRecordBuffer struct {
	mu     sync.Mutex
	runID  string
	rows   map[string]ProbeRecord
	health map[string]ProbeRecorderHealth
}

func newProbeRecordBuffer() *probeRecordBuffer {
	return &probeRecordBuffer{runID: rand.Text(), rows: map[string]ProbeRecord{}, health: map[string]ProbeRecorderHealth{}}
}
func (b *probeRecordBuffer) put(v ProbeRecord) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	h := b.health[v.InstanceID]
	if h.RunID == "" {
		h = ProbeRecorderHealth{InstanceID: v.InstanceID, RunID: b.runID, StartedAt: now}
	}
	if old, exists := b.rows[v.ID]; exists && old.Outcome != "pending" {
		return
	}
	if _, exists := b.rows[v.ID]; !exists && len(b.rows) >= probeRecordBufferLimit {
		h.DroppedRecords++
		h.LastLossAt = &now
		b.health[v.InstanceID] = h
		return
	}
	b.rows[v.ID] = v
	b.health[v.InstanceID] = h
}
func (b *probeRecordBuffer) snapshot() ([]ProbeRecord, []ProbeRecorderHealth) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	rows := make([]ProbeRecord, 0, len(b.rows))
	counts := map[string]int64{}
	for id, v := range b.rows {
		if v.StartedAt.Before(now.Add(-ProbeRecordRetention)) {
			h := b.health[v.InstanceID]
			h.DroppedRecords++
			h.LastLossAt = &now
			b.health[v.InstanceID] = h
			delete(b.rows, id)
			continue
		}
		rows = append(rows, v)
		counts[v.InstanceID]++
	}
	health := make([]ProbeRecorderHealth, 0, len(b.health))
	for _, h := range b.health {
		h.LastSeenAt = now
		h.QueuedRecords = counts[h.InstanceID]
		health = append(health, h)
	}
	return rows, health
}
func (b *probeRecordBuffer) acknowledge(rows []ProbeRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range rows {
		if current, ok := b.rows[v.ID]; ok && current.Outcome == v.Outcome {
			delete(b.rows, v.ID)
		}
	}
}
func (e *Engine) flushProbeRecords(ctx context.Context) {
	s, ok := e.store.(probeRecordsStore)
	if !ok || e.probeRecords == nil {
		return
	}
	rows, health := e.probeRecords.snapshot()
	if len(health) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.StoreProbeRecords(writeCtx, rows, health); err != nil {
		log.Printf("probe request records persistence failed; retained for retry")
		return
	}
	e.probeRecords.acknowledge(rows)
}
func (e *Engine) runProbeRecordsWriter(ctx context.Context) {
	s, ok := e.store.(probeRecordsStore)
	if !ok {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	cleanup := time.NewTicker(time.Minute)
	defer tick.Stop()
	defer cleanup.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.flushProbeRuntime(ctx)
			e.flushProbeRecords(ctx)
		case <-cleanup.C:
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.PruneProbeRecords(c, time.Now().UTC())
			cancel()
			if err != nil {
				log.Printf("probe request record cleanup failed")
			}
		}
	}
}

func probeRecord(base ChannelBaseValue, site, id string, started time.Time, scheduled bool, timeout time.Duration) ProbeRecord {
	kind := "accelerated"
	if scheduled {
		kind = "scheduled"
	}
	return ProbeRecord{ID: id, InstanceID: site, ChannelID: base.ChannelID, ChannelName: base.ChannelName, ModelName: base.ModelName, Kind: kind, StartedAt: started.UTC(), DeadlineAt: started.Add(timeout).UTC(), Outcome: "pending"}
}
func probeRecordResult(v ProbeRecord, r routineResult) ProbeRecord {
	at := time.Now().UTC()
	v.FinishedAt = &at
	duration := r.duration
	v.DurationSeconds = &duration
	switch r.outcome {
	case routineHealthy:
		v.Outcome = "success"
	case routineSlow:
		v.Outcome = "slow"
		v.Reason = "slow_threshold"
	case routineError:
		v.Outcome = "error"
		v.Reason = "channel_error"
	case routineCanceled:
		v.Outcome = "canceled"
		v.Reason = "scheduler_canceled"
	default:
		v.Outcome = "unknown"
		v.Reason = "control_unavailable"
	}
	return v
}
