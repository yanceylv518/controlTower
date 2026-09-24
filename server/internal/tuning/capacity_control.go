package tuning

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const capacityWindow = time.Minute

// CapacityControl is persisted separately from performance scores. All clocks
// describing evidence use the Agent's common coverage cutoff, not tick time.
type CapacityControl struct {
	Initialized      bool      `json:"initialized"`
	Active           bool      `json:"active"`
	Phase            string    `json:"phase"`
	Reason           string    `json:"reason,omitempty"`
	Fresh            bool      `json:"fresh"`
	MaxRPM           int64     `json:"max_rpm"`
	MaxTPM           int64     `json:"max_tpm"`
	Utilization      float64   `json:"utilization"`
	SampleAt         time.Time `json:"sample_at"`
	OverSince        time.Time `json:"over_since"`
	UnderSince       time.Time `json:"under_since"`
	AppliedAt        time.Time `json:"applied_at"`
	ConfirmedWeight  int64     `json:"confirmed_weight"`
	RawTarget        int64     `json:"raw_target"`
	BoundWeight      int64     `json:"bound_weight"`
	PendingCommandID string    `json:"pending_command_id,omitempty"`
	PendingWeight    int64     `json:"pending_weight"`
}

type currentChannelRateSnapshotStore interface {
	QueryCurrentChannelRateSnapshot(string, time.Time) ([]ChannelMetric, time.Time, error)
}

type continuousCommandResultStore interface {
	ContinuousCommandResult(string) (string, time.Time, error)
}

func capacityConfigured(b ChannelBaseValue) bool { return b.MaxRPM > 0 || b.MaxTPM > 0 }

func capacityUtilization(b ChannelBaseValue, rpm, tpm float64) float64 {
	u := 0.0
	if b.MaxRPM > 0 {
		u = rpm / float64(b.MaxRPM)
	}
	if b.MaxTPM > 0 {
		u = math.Max(u, tpm/float64(b.MaxTPM))
	}
	return u
}

func capacityTarget(weight int64, utilization float64) (raw, bounded int64) {
	if weight <= 1 || utilization < 1 {
		return weight, weight
	}
	raw = max(int64(1), int64(math.Floor(float64(weight)*.90/utilization)))
	// Integer weights 2 and 3 cannot fall by 25%; explicitly allow one unit.
	bounded = max(raw, weight-max(int64(1), weight/4), int64(1))
	return
}

func confirmCapacityWrite(s *ContinuousState, weight int64, now time.Time) {
	s.Capacity.ConfirmedWeight, s.Capacity.AppliedAt = weight, now
	s.Capacity.PendingCommandID = ""
	s.Capacity.UnderSince = time.Time{}
	s.LastWrittenWeight, s.LastWriteAt = &weight, &now
}

func (e *Engine) settleCapacityWrite(cs ContinuousStore, site string, b ChannelBaseValue, s *ContinuousState, now time.Time) {
	if s.Capacity.PendingCommandID == "" {
		return
	}
	var status string
	var err error
	appliedAt := now
	if resultStore, ok := cs.(continuousCommandResultStore); ok {
		var completedAt time.Time
		status, completedAt, err = resultStore.ContinuousCommandResult(s.Capacity.PendingCommandID)
		if completedAt.After(appliedAt) {
			appliedAt = completedAt
		}
	} else {
		status, err = cs.ContinuousCommandStatus(s.Capacity.PendingCommandID)
	}
	if err != nil || status == "pending" || status == "delivered" {
		return
	}
	if status == "succeeded" {
		// Observing the acknowledgement is a conservative upper bound on the
		// actual apply time, including across a server restart.
		confirmCapacityWrite(s, s.Capacity.PendingWeight, appliedAt)
		e.noteWriteSuccess(s)
	} else if status == "superseded" {
		s.Capacity.PendingCommandID = ""
		e.resetUnappliedCapacityTransition(site, s, now)
	} else {
		s.Capacity.PendingCommandID = ""
		e.resetUnappliedCapacityTransition(site, s, now)
		e.noteWriteFailure(site, b, s, "auto", fmt.Errorf("capacity-managed command %s", status), now)
		s.Capacity.Phase = "write_failed"
	}
}

func (e *Engine) resetUnappliedCapacityTransition(site string, s *ContinuousState, now time.Time) {
	if s.Phase == "soft_start" {
		p := DefaultPolicy().Continuous
		if policy, found, err := e.store.GetPolicy(site); err == nil && found {
			p = policy.Policy.Continuous
		}
		resetFailedCircuitEnable(s, p, now)
	} else if s.Phase == "circuit" && s.Capacity.PendingWeight == 0 {
		s.Phase = "normal"
		s.CircuitOpenedAt, s.NextProbeAt, s.OriginalPriority = nil, nil, nil
	}
}

// updateCapacity never interprets absent coverage as zero traffic. A gap resets
// confirmation, but not the latch or a confirmed write's feedback deadline.
func updateCapacity(b ChannelBaseValue, s *ContinuousState, asOf, now time.Time, available bool) {
	c := &s.Capacity
	if !capacityConfigured(b) && c.PendingCommandID == "" {
		*c = CapacityControl{}
		s.CapacityLimited = false
		return
	}
	if !c.Initialized {
		c.Initialized, c.ConfirmedWeight = true, effectiveCurrentWeight(b, *s)
	}
	if c.MaxRPM != b.MaxRPM || c.MaxTPM != b.MaxTPM {
		c.Active, c.OverSince, c.UnderSince = false, time.Time{}, time.Time{}
		c.MaxRPM, c.MaxTPM = b.MaxRPM, b.MaxTPM
	}
	// A newly confirmed external write or circuit transition invalidates the
	// old traffic response. Stale ten-minute channel snapshots do not.
	if c.PendingCommandID == "" {
		w := effectiveCurrentWeight(b, *s)
		if w != c.ConfirmedWeight {
			c.ConfirmedWeight, c.AppliedAt, c.UnderSince = w, now, time.Time{}
		}
	}
	c.BoundWeight, c.RawTarget, c.Reason = c.ConfirmedWeight, c.ConfirmedWeight, ""
	c.Fresh = available && !asOf.IsZero() && !asOf.After(now) && now.Sub(asOf) <= 90*time.Second && !asOf.Before(c.SampleAt)
	if !c.Fresh {
		c.OverSince, c.UnderSince, c.Phase = time.Time{}, time.Time{}, "unavailable"
		s.CapacityLimited = capacityConfigured(b) || c.PendingCommandID != ""
		return
	}
	c.Utilization = capacityUtilization(b, s.MetricRPM, s.MetricTPM)
	// Latch the increase guard on the first crossing. Only the decrease needs
	// 60s confirmation; otherwise a 100% -> 95% oscillation could keep raising
	// the weight and never reach sustained-overload confirmation.
	if c.Utilization >= 1 {
		c.Active = true
	}
	newSample := asOf.After(c.SampleAt)
	if newSample {
		if !c.SampleAt.IsZero() && asOf.Sub(c.SampleAt) > capacityWindow {
			c.OverSince, c.UnderSince = time.Time{}, time.Time{}
		}
		c.SampleAt = asOf
		if c.Utilization >= 1 {
			if c.OverSince.IsZero() {
				c.OverSince = asOf
			}
		} else {
			c.OverSince = time.Time{}
		}
		feedbackReady := c.AppliedAt.IsZero() || !asOf.Add(-capacityWindow).Before(c.AppliedAt)
		if c.Active && c.Utilization < .85 && feedbackReady && c.PendingCommandID == "" {
			if c.UnderSince.IsZero() {
				c.UnderSince = asOf
			}
			if asOf.Sub(c.UnderSince) >= capacityWindow {
				c.Active = false
			}
		} else {
			c.UnderSince = time.Time{}
		}
	}
	s.CapacityLimited = c.Active || c.Utilization >= 1 || c.PendingCommandID != ""
	switch {
	case c.PendingCommandID != "":
		c.Phase = "awaiting_write"
	case !c.AppliedAt.IsZero() && asOf.Add(-capacityWindow).Before(c.AppliedAt):
		c.Phase = "waiting_feedback"
		// Even after a performance decrease, do not immediately cancel it.
		s.CapacityLimited = true
	case c.Active && !c.UnderSince.IsZero():
		c.Phase = "recovering"
	case c.Active && c.Utilization < 1:
		c.Phase = "holding"
	case c.Utilization >= 1 && (c.OverSince.IsZero() || asOf.Sub(c.OverSince) < capacityWindow):
		c.Phase = "observing"
	case c.Utilization >= 1 && c.ConfirmedWeight <= 1:
		c.Phase = "minimum_weight"
	case c.Utilization >= 1:
		c.Phase = "reducing"
		c.RawTarget, c.BoundWeight = capacityTarget(c.ConfirmedWeight, c.Utilization)
	default:
		c.Phase = "normal"
	}
}

// A weight only redistributes within an eligible model/group/priority pool.
// Require an alternative in every group, rather than claiming a partial pool
// can solve a channel-wide cap. Unconfigured peers have unknown headroom.
func capacityDiversion(b ChannelBaseValue, rows []ChannelBaseValue, states map[int64]ContinuousState, rates map[int64]ChannelMetric, p ContinuousDispatchParams) (bool, string) {
	groups := strings.Split(b.GroupName, ",")
	unknown := false
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			return false, "unknown_group"
		}
		found := false
		for _, peer := range rows {
			state := states[peer.ChannelID]
			if peer.ChannelID == b.ChannelID || peer.CurrentPriority != b.CurrentPriority || peer.ModelName != b.ModelName ||
				peer.BaseWeight <= 0 || effectiveCurrentWeight(peer, state) <= 0 || state.CircuitDisabled ||
				(state.Phase != "" && state.Phase != "normal") || state.PausedReason != "" || state.Capacity.Active || state.Capacity.PendingCommandID != "" || state.SmoothedErrorRate >= p.ErrorDegradedRate {
				continue
			}
			m := rates[peer.ChannelID]
			if capacityUtilization(peer, float64(m.RequestCount), float64(m.TPM)) >= .85 {
				continue
			}
			for _, pg := range strings.Split(peer.GroupName, ",") {
				if strings.TrimSpace(pg) == group {
					found = true
					unknown = unknown || !capacityConfigured(peer)
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false, "no_eligible_headroom"
		}
	}
	if unknown {
		return true, "peer_capacity_unconfigured"
	}
	return true, ""
}

func applyCapacityTarget(s *ContinuousState) bool {
	if s.Capacity.PendingCommandID != "" {
		// Display the in-flight target; it is not yet the confirmed baseline
		// and normal writes cannot replace it until its result arrives.
		s.ProposedWeight = s.Capacity.PendingWeight
		return false
	}
	if !s.CapacityLimited {
		return false
	}
	bound := s.Capacity.ConfirmedWeight
	if s.Capacity.Phase == "reducing" {
		bound = s.Capacity.BoundWeight
	}
	before := s.ProposedWeight
	s.ProposedWeight = min(s.ProposedWeight, bound)
	return s.Capacity.Phase == "reducing" && s.ProposedWeight < before
}

func capacityRecoveryBlocked(b ChannelBaseValue, s ContinuousState) bool {
	return capacityConfigured(b) && (s.CapacityLimited || s.Capacity.PendingCommandID != "")
}

// Use this for weight-only circuit transitions too: their acknowledgements
// establish the same feedback baseline as normal capacity-managed writes.
func (e *Engine) createTrackedWeightChange(cs ContinuousStore, rec Recommendation, b ChannelBaseValue, s *ContinuousState, now time.Time) (string, error) {
	if s.Capacity.Initialized {
		s.UpdatedAt = now
		if err := cs.PutContinuousState(*s); err != nil {
			return "", err
		}
	}
	id, err := cs.CreateContinuousWeightChange(rec, "system:auto", now)
	if err != nil || !s.Capacity.Initialized {
		return id, err
	}
	s.Capacity.PendingCommandID, s.Capacity.PendingWeight = id, rec.ProposedWeight
	s.Capacity.Phase = "awaiting_write"
	if id == "" {
		confirmCapacityWrite(s, rec.ProposedWeight, time.Now().UTC())
		e.noteWriteSuccess(s)
	} else {
		e.settleCapacityWrite(cs, rec.InstanceID, b, s, now)
	}
	if s.Capacity.PendingCommandID == "" && s.LastWriteAt != nil && s.Capacity.ConfirmedWeight == rec.ProposedWeight {
		s.Capacity.Phase = "waiting_feedback"
	}
	return id, nil
}
