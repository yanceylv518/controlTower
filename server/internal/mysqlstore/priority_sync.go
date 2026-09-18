package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"database/sql"
	"errors"
)

var ErrPrioritySyncSuperseded = errors.New("priority sync superseded by current target or mode")

type priorityQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkPrioritySync(q priorityQuery, v tuning.Recommendation) error {
	if v.Rule != "base_priority_sync" {
		return nil
	}
	if v.ProposedPriority == nil {
		return ErrPrioritySyncSuperseded
	}
	var target int64
	var model string
	err := q.QueryRowContext(context.Background(), `SELECT base_priority,model_name FROM channel_base_values WHERE instance_id=? AND channel_id=?`, v.InstanceID, v.ChannelID).Scan(&target, &model)
	if err == sql.ErrNoRows {
		return ErrPrioritySyncSuperseded
	}
	if err != nil {
		return err
	}
	if target != *v.ProposedPriority {
		return ErrPrioritySyncSuperseded
	}
	if v.ModeAtCreation != "auto" {
		return nil
	}
	var raw string
	err = q.QueryRowContext(context.Background(), `SELECT policy_json FROM tuning_policies WHERE instance_id=?`, v.InstanceID).Scan(&raw)
	if err == sql.ErrNoRows {
		return ErrPrioritySyncSuperseded
	}
	if err != nil {
		return err
	}
	policy, err := tuning.DecodePolicyJSON([]byte(raw))
	if err != nil {
		return err
	}
	if policy.DispatchModes[model] != "auto" {
		return ErrPrioritySyncSuperseded
	}
	return nil
}

func (s Store) CheckPrioritySync(v tuning.Recommendation) error { return checkPrioritySync(s.db, v) }

func (s Store) HasPendingPrioritySync(site string, channel int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM channel_commands c JOIN tuning_recommendations r ON r.command_id=c.id WHERE r.instance_id=? AND r.channel_id=? AND r.rule='base_priority_sync' AND c.status IN ('pending','delivered') AND c.updated_at > DATE_SUB(UTC_TIMESTAMP(), INTERVAL 2 MINUTE)`, site, channel).Scan(&count)
	return count > 0, err
}
