package tuning

import (
	"context"
	"crypto/rand"
	"log"
	"sync"
	"time"
)

const (
	probeSkipGlobal        = "global_concurrency"
	probeSkipChannel       = "channel_concurrency"
	probeSkipEvidence      = "pending_evidence"
	probeCoverageRetention = 48 * time.Hour
)

type routineDispatch struct {
	id         string
	at         time.Time
	scheduled  bool
	skipReason string // Empty means admitted to the physical request pool.
}

// Due counts actual scheduler opportunities, not guessed elapsed-time quotas.
// Every opportunity is either sent or skipped for exactly one CT capacity reason.
type ProbeDispatchCounts struct {
	Due             int64      `json:"due"`
	Sent            int64      `json:"sent"`
	GlobalLimited   int64      `json:"global_limited"`
	ChannelLimited  int64      `json:"channel_limited"`
	EvidenceLimited int64      `json:"evidence_limited"`
	LastDueAt       time.Time  `json:"last_due_at"`
	LastSentAt      *time.Time `json:"last_sent_at,omitempty"`
	LastSkippedAt   *time.Time `json:"last_skipped_at,omitempty"`
}

type RoutineProbeCoverage struct {
	Scheduled   ProbeDispatchCounts `json:"scheduled"`
	Accelerated ProbeDispatchCounts `json:"accelerated"`
}

type ProbeCoverageWindow struct {
	From  time.Time                      `json:"from"`
	To    time.Time                      `json:"to"`
	AsOf  time.Time                      `json:"as_of"`
	Items map[int64]RoutineProbeCoverage `json:"items"`
	Error string                         `json:"error,omitempty"`
}

// Absolute per-process snapshots make retries safe even after an ambiguous
// database commit. Restarting a channel keeps the same process generation.
type ProbeCoverageBucket struct {
	InstanceID string
	ChannelID  int64
	RunID      string
	BucketTime time.Time
	Scheduled  bool
	ProbeDispatchCounts
}

type RoutineProbeCoverageReader interface {
	QueryRoutineProbeCoverage(context.Context, string, time.Time, time.Time) (map[int64]RoutineProbeCoverage, time.Time, error)
}
type routineProbeCoverageStore interface {
	StoreRoutineProbeCoverage(context.Context, []ProbeCoverageBucket) error
	PruneRoutineProbeCoverage(context.Context, time.Time) error
}

type probeCoverageKey struct {
	channelKey
	minute    int64
	scheduled bool
}
type probeCoverageEntry struct {
	ProbeCoverageBucket
	saved int64
}
type probeCoverageBuffer struct {
	mu      sync.Mutex
	runID   string
	entries map[probeCoverageKey]probeCoverageEntry
}

func newProbeCoverageBuffer() *probeCoverageBuffer {
	return &probeCoverageBuffer{runID: rand.Text(), entries: make(map[probeCoverageKey]probeCoverageEntry)}
}

func (b *probeCoverageBuffer) record(channel channelKey, d routineDispatch) {
	if b == nil {
		return
	}
	at := d.at.UTC()
	minute := at.Truncate(time.Minute)
	key := probeCoverageKey{channel, minute.Unix(), d.scheduled}
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.entries[key]
	v.InstanceID, v.ChannelID, v.RunID, v.BucketTime, v.Scheduled = channel.site, channel.channel, b.runID, minute, d.scheduled
	v.Due++
	v.LastDueAt = at
	switch d.skipReason {
	case probeSkipGlobal:
		v.GlobalLimited++
	case probeSkipChannel:
		v.ChannelLimited++
	case probeSkipEvidence:
		v.EvidenceLimited++
	default:
		v.Sent++
	}
	if d.skipReason == "" {
		v.LastSentAt = &at
	} else {
		v.LastSkippedAt = &at
	}
	b.entries[key] = v
}

func (b *probeCoverageBuffer) snapshot(now time.Time) []ProbeCoverageBucket {
	b.mu.Lock()
	defer b.mu.Unlock()
	values := make([]ProbeCoverageBucket, 0, len(b.entries))
	// Keep the latest absolute snapshot even across a minute boundary until
	// this channel has advanced. A dispatch waiting for this lock may still
	// carry a timestamp from the previous minute.
	latest := map[probeCoverageKey]int64{}
	for key := range b.entries {
		stream := key
		stream.minute = 0
		latest[stream] = max(latest[stream], key.minute)
	}
	for key, v := range b.entries {
		stream := key
		stream.minute = 0
		if v.BucketTime.Before(now.Add(-probeCoverageRetention)) {
			if v.saved != v.Due {
				log.Printf("routine probe coverage expired before persistence site=%s channel=%d", v.InstanceID, v.ChannelID)
			}
			delete(b.entries, key)
		} else if v.saved != v.Due {
			values = append(values, v.ProbeCoverageBucket)
		} else if key.minute < latest[stream] {
			delete(b.entries, key)
		}
	}
	return values
}

func (b *probeCoverageBuffer) acknowledge(values []ProbeCoverageBucket) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, v := range values {
		key := probeCoverageKey{channelKey{v.InstanceID, v.ChannelID}, v.BucketTime.Unix(), v.Scheduled}
		entry, ok := b.entries[key]
		if ok {
			entry.saved = max(entry.saved, v.Due)
			b.entries[key] = entry
		}
	}
}

func (e *Engine) flushProbeCoverage(ctx context.Context) {
	s, ok := e.store.(routineProbeCoverageStore)
	if !ok || e.probeCoverage == nil {
		return
	}
	values := e.probeCoverage.snapshot(time.Now().UTC())
	if len(values) == 0 {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.StoreRoutineProbeCoverage(writeCtx, values); err != nil {
		log.Printf("routine probe coverage persistence failed; retaining pending counters")
		return
	}
	e.probeCoverage.acknowledge(values)
}

func (e *Engine) runProbeCoverageWriter(ctx context.Context) {
	s, ok := e.store.(routineProbeCoverageStore)
	if !ok {
		return
	}
	flush := time.NewTicker(5 * time.Second)
	prune := time.NewTicker(time.Minute)
	defer flush.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			e.flushProbeCoverage(ctx)
		case <-prune.C:
			cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := s.PruneRoutineProbeCoverage(cleanupCtx, time.Now().UTC().Add(-probeCoverageRetention))
			cancel()
			if err != nil {
				log.Printf("routine probe coverage cleanup failed")
			}
		}
	}
}
