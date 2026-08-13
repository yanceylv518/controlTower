package tuning

import (
	"context"
	"log"
	"math"
	"time"
)

// Store is the reduced persistence contract for continuous dispatch.
type Store interface {
	GetPolicy(string) (PolicyRecord, bool, error)
	PutPolicy(PolicyRecord) error
	ListEnabledSites() ([]string, error)
	QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error)
	QueryRecentChannelBuckets(string, int64, time.Time, int) ([]RecentChannelBucket, error)
	InsertRecommendation(Recommendation) error
	ListRecommendations(RecommendationQuery) ([]Recommendation, error)
	RecommendationReport(RecommendationQuery) (Report, error)
}

type Engine struct{ store Store }

func NewEngine(s Store) *Engine { return &Engine{store: s} }

func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			e.Tick(now.UTC())
		}
	}
}

func (e *Engine) Tick(now time.Time) {
	ids, err := e.store.ListEnabledSites()
	if err != nil {
		log.Printf("tuning list sites failed: %v", err)
		return
	}
	cs, ok := e.store.(ContinuousStore)
	if !ok {
		log.Printf("tuning continuous store unavailable")
		return
	}
	for _, id := range ids {
		siteStarted := time.Now()
		p, found, err := e.store.GetPolicy(id)
		if err != nil {
			log.Printf("tuning continuous evaluation site=%s stage=policy failed duration=%s error=%v", id, time.Since(siteStarted), err)
			continue
		}
		if !found {
			p = PolicyRecord{InstanceID: id, Policy: DefaultPolicy(), Mode: "observe"}
		}
		e.runAutoSentinel(&p, now)
		n, c := e.evaluateContinuous(id, p, now, cs)
		log.Printf("tuning continuous evaluation site=%s active_channels=%d writes=%d duration=%s", id, c, n, time.Since(siteStarted))
	}
}

// runAutoSentinel pauses only per-model continuous auto modes. The global
// policy mode remains accepted for rolling API compatibility only.
func (e *Engine) runAutoSentinel(p *PolicyRecord, now time.Time) {
	hasAuto := false
	for _, mode := range p.Policy.DispatchModes {
		if mode == "auto" {
			hasAuto = true
			break
		}
	}
	if !hasAuto {
		return
	}
	checker, ok := e.store.(interface {
		HasExpiredAutoCommands(string, time.Time) (bool, error)
	})
	if !ok {
		return
	}
	since := p.UpdatedAt
	if floor := now.Add(-24 * time.Hour); since.Before(floor) {
		since = floor
	}
	expired, err := checker.HasExpiredAutoCommands(p.InstanceID, since)
	if err != nil || !expired {
		return
	}
	for model, mode := range p.Policy.DispatchModes {
		if mode == "auto" {
			p.Policy.DispatchModes[model] = "observe"
		}
	}
	p.UpdatedAt, p.UpdatedBy = now, "system:sentinel"
	if e.store.PutPolicy(*p) != nil {
		return
	}
	_ = e.store.InsertRecommendation(Recommendation{
		ID: NewID(now, p.InstanceID, 0, "auto_paused-continuous_dispatch"), InstanceID: p.InstanceID,
		ChannelName: "-", CreatedAt: now, Rule: "auto_paused", ModeAtCreation: "auto", Status: "recorded",
		Evidence: map[string]any{"reason": "auto command expired unexecuted; check the channel-control agent", "since": since, "target": "continuous_dispatch", "fallback": "observe"},
	})
}

func clamp(value, low, high float64) float64 { return math.Max(low, math.Min(high, value)) }
