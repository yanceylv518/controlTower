package directcontrol

import (
	"context"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/tuning"
	"errors"
	"fmt"
	"time"
)

type prioritySyncStore interface {
	GetPolicy(string) (tuning.PolicyRecord, bool, error)
	ListChannelBaseValues(string, string) ([]tuning.ChannelBaseValue, error)
	HasPendingPrioritySync(string, int64) (bool, error)
	CreateContinuousWeightChange(tuning.Recommendation, string, time.Time) (string, error)
}

func (s Store) ReconcilePriorities(ctx context.Context, site string) error {
	return reconcilePriorities(ctx, s, site)
}

func reconcilePriorities(ctx context.Context, s prioritySyncStore, site string) error {
	rows, err := s.ListChannelBaseValues(site, "")
	if err != nil {
		return err
	}
	var failures []error
	for _, old := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if old.CurrentPriority == old.BasePriority {
			continue
		}
		// Re-read after each preceding network write, never reuse a batch's policy/target.
		pr, ok, err := s.GetPolicy(site)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		fresh, err := s.ListChannelBaseValues(site, old.ModelName)
		if err != nil {
			return err
		}
		for _, row := range fresh {
			if row.ChannelID != old.ChannelID || pr.Policy.DispatchModes[row.ModelName] != "auto" || len(row.Models) != 1 || row.Models[0] != row.ModelName || row.CurrentPriority == row.BasePriority {
				continue
			}
			pending, err := s.HasPendingPrioritySync(site, row.ChannelID)
			if err != nil {
				return err
			}
			if pending {
				continue
			}
			now := time.Now().UTC()
			current, target := row.CurrentPriority, row.BasePriority
			rec := tuning.Recommendation{ID: tuning.NewID(now, site, row.ChannelID, "base_priority_sync"), InstanceID: site, ChannelID: row.ChannelID, ChannelName: row.ChannelName, CreatedAt: now, Rule: "base_priority_sync", CurrentWeight: row.CurrentWeight, ProposedWeight: row.CurrentWeight, CurrentPriority: &current, ProposedPriority: &target, ModeAtCreation: "auto", Status: "recorded", Evidence: map[string]any{"model": row.ModelName, "trigger": "priority_drift"}}
			if _, err = s.CreateContinuousWeightChange(rec, "system:auto", now); err != nil && !errors.Is(err, mysqlstore.ErrPrioritySyncSuperseded) {
				failures = append(failures, fmt.Errorf("channel %d priority correction: %w", row.ChannelID, err))
			}
		}
	}
	return errors.Join(failures...)
}
