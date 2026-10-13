package directcontrol

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
)

type channelRequestJournal interface {
	BeginChannelControlRequest(context.Context, tuning.ChannelControlRequest) error
	FinishChannelControlRequest(context.Context, tuning.ChannelControlRequest) error
	LinkChannelControlRequest(context.Context, string, string) error
}
type journalController struct {
	Controller
	journal                                        channelRequestJournal
	site, actor, recommendation, operation, lastID string
	before                                         map[string]any
}

func (c *journalController) Update(ctx context.Context, r channelcontrol.UpdateRequest) (channelcontrol.Result, error) {
	target := map[string]any{}
	if r.Weight != nil {
		target["weight"] = *r.Weight
	}
	if r.Priority != nil {
		target["priority"] = *r.Priority
	}
	if r.Status != nil {
		target["status"] = *r.Status
	}
	if r.Group != nil {
		target["group"] = *r.Group
	}
	v := tuning.ChannelControlRequest{ID: rand.Text(), InstanceID: c.site, ChannelID: r.ChannelID, Operation: c.operation, Actor: c.actor, RecommendationID: c.recommendation, StartedAt: time.Now().UTC(), Payload: map[string]any{"target": target, "before": c.before}}
	if v.Operation == "" {
		v.Operation = "channel.update"
	}
	if err := c.journal.BeginChannelControlRequest(ctx, v); err != nil {
		return channelcontrol.Result{}, fmt.Errorf("channel request journal unavailable: %w", err)
	}
	c.lastID = v.ID
	result, err := c.Controller.Update(ctx, r)
	finished := time.Now().UTC()
	duration := finished.Sub(v.StartedAt).Seconds()
	v.FinishedAt = &finished
	v.DurationSeconds = &duration
	v.Outcome = "succeeded"
	if err != nil {
		// A transport error does not prove that the upstream write was rejected.
		v.Outcome, v.Reason = "unconfirmed", "upstream_request_failed"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			v.Reason = "upstream_timeout"
		}
		if errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled {
			v.Reason = "request_canceled"
		}
		if errors.Is(err, channelcontrol.ErrChannelNotFound) {
			v.Outcome, v.Reason = "failed", "channel_not_found"
		} else if errors.Is(err, channelcontrol.ErrStatusRejected) {
			v.Outcome, v.Reason = "failed", "upstream_rejected"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if saveErr := c.journal.FinishChannelControlRequest(finishCtx, v); saveErr != nil {
		log.Printf("direct channel request result persistence failed request=%s site=%s channel=%d", v.ID, c.site, r.ChannelID)
	}
	return result, err
}
func (c *journalController) link(command string) {
	if c.lastID == "" || command == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.journal.LinkChannelControlRequest(ctx, c.lastID, command); err != nil {
		log.Printf("direct channel request command link failed request=%s command=%s", c.lastID, command)
	}
}
