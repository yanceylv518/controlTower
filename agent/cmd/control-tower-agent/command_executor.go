package main

import (
	"context"
	"fmt"
	"time"

	"controltower/agent/internal/reporter"
	"controltower/internal/channelcontrol"
)

func executeCommands(ctx context.Context, controller channelController, commands []reporter.ChannelCommand) []reporter.ChannelCommandResult {
	if len(commands) == 0 {
		return nil
	}
	results := make([]reporter.ChannelCommandResult, 0, len(commands))
	for _, command := range commands {
		result := reporter.ChannelCommandResult{
			ID:        command.ID,
			ChannelID: command.ChannelID,
			AppliedAt: time.Now().UTC(),
		}
		if controller == nil {
			result.Status = "skipped"
			result.Error = "channel control is disabled"
			results = append(results, result)
			continue
		}
		if command.Type != "channel.update" && command.Type != "channel.verify" && command.Type != "channel.probe" {
			result.Status = "failed"
			result.Error = fmt.Sprintf("unsupported command type %q", command.Type)
			results = append(results, result)
			continue
		}
		if command.ID == "" || command.ChannelID <= 0 {
			result.Status = "failed"
			result.Error = "command id and positive channel id are required"
			results = append(results, result)
			continue
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
			results = append(results, result)
			continue
		}
		// channel.verify sends no field changes. Update still performs an
		// authenticated GET and PUT, proving that new-api is writable.
		_, err := controller.Update(ctx, channelcontrol.UpdateRequest{
			ChannelID: command.ChannelID,
			Status:    command.Status,
			Weight:    command.Weight,
			Priority:  command.Priority,
		})
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
		} else {
			result.Status = "succeeded"
		}
		results = append(results, result)
	}
	return results
}
