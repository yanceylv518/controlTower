package directcontrol

import (
	"context"
	"fmt"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

func (s Store) UpdateChannelStatus(ctx context.Context, site string, channel int64, target int, actor string, now time.Time) (storage.ChannelCommand, error) {
	controller, direct, err := s.controllerForSite(site)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	if !direct {
		return storage.ChannelCommand{}, tuning.ErrManualStatusUnavailable
	}
	unlock, err := s.LockChannelWrite(ctx, site, channel)
	if err != nil {
		return storage.ChannelCommand{}, err
	}
	defer unlock()
	cmd, err := s.BeginManualChannelStatus(ctx, site, channel, target, actor, now)
	if err != nil {
		return cmd, err
	}
	op := "channel.manual_disable"
	if target == 1 {
		op = "channel.manual_enable"
	}
	tracked := &journalController{Controller: controller, journal: s.Store, site: site, actor: actor, operation: op}
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	_, writeErr := tracked.Update(writeCtx, channelcontrol.UpdateRequest{ChannelID: channel, Status: &target})
	cancel()
	writtenAt := time.Now().UTC()
	recordErr := s.FinishManualChannelStatus(site, cmd, writeErr == nil, writtenAt)
	if recordErr != nil {
		recordErr = s.FinishManualChannelStatus(site, cmd, writeErr == nil, writtenAt)
	}
	tracked.link(cmd.ID)
	if recordErr != nil {
		return cmd, fmt.Errorf("manual status result persistence: %w", recordErr)
	}
	if writeErr != nil {
		return cmd, fmt.Errorf("manual status request: %w", writeErr)
	}
	cmd.Status, cmd.UpdatedAt = "succeeded", writtenAt
	return cmd, nil
}

// A running recovery round must not send its next request after ownership has
// changed. Its in-flight result is also discarded by the command marker guard.
type ownedRecoveryProber struct {
	Controller
	active func() bool
	cancel context.CancelFunc
}

func (p ownedRecoveryProber) Probe(ctx context.Context, channel int64, model string) (channelcontrol.ProbeResult, error) {
	if !p.active() {
		p.cancel()
		return channelcontrol.ProbeResult{}, context.Canceled
	}
	result, err := p.Controller.Probe(ctx, channel, model)
	if !p.active() {
		p.cancel()
		return channelcontrol.ProbeResult{}, context.Canceled
	}
	return result, err
}
