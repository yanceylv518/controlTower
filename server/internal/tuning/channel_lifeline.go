package tuning

import (
	"context"
	"time"
)

type ChannelControlRequest struct {
	ID, InstanceID, Operation, Actor, RecommendationID, CommandID, Outcome, Reason string
	ChannelID                                                                      int64
	Payload                                                                        map[string]any
	StartedAt                                                                      time.Time
	FinishedAt                                                                     *time.Time
	DurationSeconds                                                                *float64
}
type ChannelLifeEvent struct {
	ID               string         `json:"id"`
	Source           string         `json:"source"`
	At               time.Time      `json:"at"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
	Operation        string         `json:"operation"`
	Outcome          string         `json:"outcome"`
	Actor            string         `json:"actor"`
	CommandID        string         `json:"command_id,omitempty"`
	RecommendationID string         `json:"recommendation_id,omitempty"`
	Details          map[string]any `json:"details"`
}
type ChannelLifePage struct {
	Items      []ChannelLifeEvent `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
type ChannelLifelineReader interface {
	QueryChannelLifeline(context.Context, ProbeRecordQuery, string) (ChannelLifePage, error)
}
