package mysqlstore

import (
	"context"
	"controltower/server/internal/tuning"
	"encoding/json"
)

func (s Store) BeginChannelControlRequest(ctx context.Context, v tuning.ChannelControlRequest) error {
	payload, err := json.Marshal(v.Payload)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO channel_control_requests (id,instance_id,channel_id,operation,payload_json,actor,recommendation_id,started_at,outcome) VALUES(?,?,?,?,?,?,?,?,'pending')`, v.ID, v.InstanceID, v.ChannelID, v.Operation, payload, v.Actor, v.RecommendationID, v.StartedAt)
	return err
}
func (s Store) FinishChannelControlRequest(ctx context.Context, v tuning.ChannelControlRequest) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channel_control_requests SET finished_at=?,outcome=?,reason=?,duration_seconds=? WHERE id=? AND outcome='pending'`, v.FinishedAt, v.Outcome, v.Reason, v.DurationSeconds, v.ID)
	return err
}
func (s Store) LinkChannelControlRequest(ctx context.Context, id, command string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channel_control_requests SET command_id=? WHERE id=?`, command, id)
	return err
}
