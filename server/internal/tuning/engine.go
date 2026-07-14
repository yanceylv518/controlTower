package tuning

import (
	"context"
	"log"
	"math"
	"time"
)

type Store interface {
	GetPolicy(string) (PolicyRecord, bool, error)
	PutPolicy(PolicyRecord) error
	ListEnabledInstances() ([]string, error)
	QueryMetrics(string, time.Time, time.Time) ([]ChannelMetric, error)
	LatestChannels(string) ([]Channel, error)
	InsertRecommendation(Recommendation) error
	OriginalDegrade(string, int64) (int64, bool, error)
	HasUnrecoveredDegrade(string, int64) (bool, error)
	PendingOutcomes(time.Time, int) ([]Recommendation, error)
	UpdateOutcome(string, map[string]any, time.Time, *bool) error
	ListRecommendations(RecommendationQuery) ([]Recommendation, error)
	RecommendationReport(RecommendationQuery) (Report, error)
}
type channelState struct {
	degrade, recover int
	cooldown         time.Time
}
type Engine struct {
	store Store
	// Consecutive-window and cooldown state is intentionally in memory for B1;
	// restart resets it. Persisting action state belongs to the real-action batch.
	states map[string]*channelState
	last   map[string]time.Time
}

func NewEngine(s Store) *Engine {
	return &Engine{store: s, states: map[string]*channelState{}, last: map[string]time.Time{}}
}
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
	ids, err := e.store.ListEnabledInstances()
	if err != nil {
		log.Printf("tuning list instances failed: %v", err)
		return
	}
	for _, id := range ids {
		p, ok, err := e.store.GetPolicy(id)
		if err != nil {
			continue
		}
		if !ok {
			p = PolicyRecord{InstanceID: id, Policy: DefaultPolicy(), Mode: "observe"}
		}
		if now.Sub(e.last[id]) >= time.Duration(p.Policy.EvaluationWindowMinutes)*time.Minute {
			n, c := e.evaluate(id, p, now)
			e.last[id] = now
			log.Printf("tuning evaluation instance=%s channels=%d recommendations=%d", id, c, n)
		}
	}
	e.fillOutcomes(now)
}
func (e *Engine) evaluate(id string, pr PolicyRecord, now time.Time) (int, int) {
	metrics, err := e.store.QueryMetrics(id, now.Add(-time.Duration(pr.Policy.EvaluationWindowMinutes)*time.Minute), now)
	if err != nil {
		return 0, 0
	}
	channels, err := e.store.LatestChannels(id)
	if err != nil {
		return 0, 0
	}
	mm := map[int64]ChannelMetric{}
	for _, m := range metrics {
		mm[m.ChannelID] = m
	}
	made := 0
	evaluated := 0
	for _, ch := range channels {
		if ch.Status != "enabled" && ch.Status != "1" {
			continue
		}
		m, ok := mm[ch.ID]
		if !ok {
			continue
		}
		evaluated++
		key := id + ":" + fmtInt(ch.ID)
		s := e.states[key]
		if s == nil {
			s = &channelState{}
			e.states[key] = s
		}
		rate := m.ErrorRate()
		if m.RequestCount < pr.Policy.MinSamples {
			s.degrade = 0
			s.recover = 0
			continue
		}
		if rate >= pr.Policy.Degrade.ErrorRateThreshold {
			s.degrade++
		} else {
			s.degrade = 0
		}
		if s.degrade >= pr.Policy.Degrade.SustainedWindows && !now.Before(s.cooldown) {
			w := int64(math.Floor(float64(ch.Weight) * pr.Policy.Degrade.WeightStepRatio))
			if w < pr.Policy.Degrade.WeightFloor {
				w = pr.Policy.Degrade.WeightFloor
			}
			if err := e.store.InsertRecommendation(Recommendation{ID: NewID(now, id, ch.ID, "degrade"), InstanceID: id, ChannelID: ch.ID, ChannelName: ch.Name, CreatedAt: now, Rule: "degrade", Evidence: evidence(m, s.degrade, now, pr.Policy, false), CurrentWeight: ch.Weight, ProposedWeight: w, ModeAtCreation: "observe", Status: "recorded"}); err != nil {
				continue
			}
			s.cooldown = now.Add(time.Duration(pr.Policy.CooldownMinutes) * time.Minute)
			s.degrade = 0
			made++
			continue
		}
		prior, _ := e.store.HasUnrecoveredDegrade(id, ch.ID)
		if !prior {
			s.recover = 0
			continue
		}
		if rate <= pr.Policy.Recover.ErrorRateThreshold {
			s.recover++
		} else {
			s.recover = 0
		}
		if s.recover >= pr.Policy.Recover.SustainedWindows && !now.Before(s.cooldown) {
			original, ok, _ := e.store.OriginalDegrade(id, ch.ID)
			if !ok {
				continue
			}
			w := int64(math.Floor(float64(ch.Weight) * pr.Policy.Recover.WeightStepRatio))
			if w > original {
				w = original
			}
			if err := e.store.InsertRecommendation(Recommendation{ID: NewID(now, id, ch.ID, "recover"), InstanceID: id, ChannelID: ch.ID, ChannelName: ch.Name, CreatedAt: now, Rule: "recover", Evidence: evidence(m, s.recover, now, pr.Policy, true), CurrentWeight: ch.Weight, ProposedWeight: w, ModeAtCreation: "observe", Status: "recorded"}); err != nil {
				continue
			}
			s.cooldown = now.Add(time.Duration(pr.Policy.CooldownMinutes) * time.Minute)
			s.recover = 0
			made++
		}
	}
	return made, evaluated
}
func evidence(m ChannelMetric, n int, now time.Time, p Policy, sim bool) map[string]any {
	threshold := p.Degrade.ErrorRateThreshold
	if sim {
		threshold = p.Recover.ErrorRateThreshold
	}
	return map[string]any{"error_rate": m.ErrorRate(), "threshold": threshold, "samples": m.RequestCount, "p95": m.P95, "sustained_windows": n, "window_end": now, "window_start": now.Add(-time.Duration(p.EvaluationWindowMinutes) * time.Minute), "simulated": sim}
}
func (e *Engine) fillOutcomes(now time.Time) {
	items, err := e.store.PendingOutcomes(now.Add(-30*time.Minute), 100)
	if err != nil {
		return
	}
	for _, r := range items {
		ms, err := e.store.QueryMetrics(r.InstanceID, r.CreatedAt, r.CreatedAt.Add(30*time.Minute))
		if err != nil {
			continue
		}
		var m ChannelMetric
		for _, x := range ms {
			if x.ChannelID == r.ChannelID {
				m = x
			}
		}
		out := map[string]any{"error_rate": m.ErrorRate(), "samples": m.RequestCount, "p95": m.P95}
		var hit *bool
		if m.RequestCount < 5 {
			out["insufficient_samples"] = true
		} else {
			threshold, ok := r.Evidence["threshold"].(float64)
			if !ok {
				p := DefaultPolicy()
				if r.Rule == "degrade" {
					threshold = p.Degrade.ErrorRateThreshold
				} else {
					threshold = p.Recover.ErrorRateThreshold
				}
			}
			v := (r.Rule == "degrade" && m.ErrorRate() >= threshold) || (r.Rule == "recover" && m.ErrorRate() <= threshold)
			hit = &v
		}
		_ = e.store.UpdateOutcome(r.ID, out, now, hit)
	}
}
func fmtInt(v int64) string {
	const d = "0123456789"
	if v == 0 {
		return "0"
	}
	b := make([]byte, 0, 20)
	for v > 0 {
		b = append([]byte{d[v%10]}, b...)
		v /= 10
	}
	return string(b)
}
