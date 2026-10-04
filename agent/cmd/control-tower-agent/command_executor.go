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
	if len(commands) == 0 {
		return nil
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
	return results
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
		count := command.ProbeCount
		if count < 1 {
			count = 1
		}
		interval := time.Duration(command.ProbeIntervalSeconds) * time.Second
		var lastError string
		for attempt := 0; attempt < count; attempt++ {
			if attempt > 0 && interval > 0 {
				select {
				case <-ctx.Done():
					lastError = ctx.Err().Error()
					attempt = count
					continue
				case <-time.After(interval):
				}
			}
			probe, err := controller.Probe(ctx, command.ChannelID, command.Model)
			result.Attempts++
			if err == nil && probe.Success {
				result.Successes++
				result.DurationSeconds += probe.Duration
			} else if err != nil {
				lastError = err.Error()
			} else {
				lastError = probe.Message
			}
		}
		// A probe command reports the whole round even when individual probes
		// fail; the server decides recovery from attempts/successes.
		result.Status = "succeeded"
		result.Error = lastError
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
