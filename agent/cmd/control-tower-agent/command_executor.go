package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"controltower/agent/internal/reporter"
	"controltower/internal/channelcontrol"
)

func executeCommands(ctx context.Context, controller channelController, commands []reporter.ChannelCommand) []reporter.ChannelCommandResult {
	var completedProbes []reporter.ChannelCommandResult
	if dispatcher, ok := ctx.Value(probeDispatcherKey{}).(*probeDispatcher); ok {
		ordinary := make([]reporter.ChannelCommand, 0, len(commands))
		for _, command := range commands {
			if command.Type == "channel.probe" {
				dispatcher.submit(command)
			} else {
				ordinary = append(ordinary, command)
			}
		}
		commands = ordinary
		completedProbes = dispatcher.drain()
	}
	if len(commands) == 0 {
		return completedProbes
	}
	// Keep each channel's command order; a slow probe/write must not delay
	// unrelated channels. Result positions remain identical to the request.
	results := make([]reporter.ChannelCommandResult, len(commands))
	groups := map[int64][]int{}
	var channels []int64
	for i, command := range commands {
		if _, exists := groups[command.ChannelID]; !exists {
			channels = append(channels, command.ChannelID)
		}
		groups[command.ChannelID] = append(groups[command.ChannelID], i)
	}
	jobs := make(chan int64)
	var workers sync.WaitGroup
	for i := 0; i < min(4, len(channels)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for channel := range jobs {
				for _, i := range groups[channel] {
					results[i] = executeCommand(ctx, controller, commands[i])
				}
			}
		}()
	}
	for _, channel := range channels {
		jobs <- channel
	}
	close(jobs)
	workers.Wait()
	return append(results, completedProbes...)
}

func executeCommand(ctx context.Context, controller channelController, command reporter.ChannelCommand) reporter.ChannelCommandResult {
	result := reporter.ChannelCommandResult{
		ID:        command.ID,
		ChannelID: command.ChannelID,
		AppliedAt: time.Now().UTC(),
	}
	if command.Type == "channel.reconcile" {
		result.Reconciled, result.Status = true, "unconfirmed"
		reader, ok := controller.(interface {
			Read(context.Context, int64) (channelcontrol.Result, error)
		})
		if !ok {
			result.Error = "channel read control is unavailable"
		} else if observed, err := reader.Read(ctx, command.ChannelID); err != nil {
			result.Error = err.Error()
		} else {
			result.Status = "observed"
			result.ObservedWeight, result.ObservedStatus, result.ObservedPriority, result.ObservedGroup = observed.Weight, observed.Status, observed.Priority, &observed.Group
		}
		return result
	}
	if controller == nil {
		result.Status = "skipped"
		result.Error = "channel control is disabled"
		return result
	}
	if command.Type != "channel.update" && command.Type != "channel.verify" && command.Type != "channel.probe" {
		result.Status = "failed"
		result.Error = fmt.Sprintf("unsupported command type %q", command.Type)
		return result
	}
	if command.ID == "" || command.ChannelID <= 0 {
		result.Status = "failed"
		result.Error = "command id and positive channel id are required"
		return result
	}
	if command.Type == "channel.probe" {
		round := channelcontrol.RunProbeRound(ctx, controller, command.ChannelID, command.Model, command.ProbeCount, command.ProbeIntervalSeconds, func(ctx context.Context, d time.Duration) {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
		})
		result.Attempts, result.Successes = round.Attempts, round.Successes
		result.DurationSeconds, result.ProbeSlowStreak = round.DurationSeconds, round.SlowStreak
		result.Status, result.Error = "succeeded", round.Error
		result.AppliedAt = time.Now().UTC()
		return result
	}
	// channel.verify sends no field changes. Update still performs an
	// authenticated GET and PUT, proving that new-api is writable.
	_, err := controller.Update(ctx, channelcontrol.UpdateRequest{
		ChannelID: command.ChannelID,
		Status:    command.Status,
		Weight:    command.Weight,
		Priority:  command.Priority,
		Group:     command.Group,
	})
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	} else {
		result.Status = "succeeded"
	}
	return result
}
