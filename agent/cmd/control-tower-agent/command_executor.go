package main

import (
	"context"
	"fmt"
	"time"

	"controltower/agent/internal/channelcontrol"
	"controltower/agent/internal/reporter"
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
		if command.Type != "channel.update" {
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
