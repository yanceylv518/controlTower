package main

import (
	"controltower/server/internal/channelschedule"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type input struct {
	Configs []channelschedule.Config `json:"configs"`
	Now     time.Time                `json:"now"`
}

type result struct {
	Config  *channelschedule.Config  `json:"config,omitempty"`
	Preview *channelschedule.Preview `json:"preview,omitempty"`
	Error   string                   `json:"error,omitempty"`
	Message string                   `json:"message,omitempty"`
}

func write(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	var request input
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		write(result{Error: "invalid_schedule", Message: err.Error()})
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF || len(request.Configs) < 1 || len(request.Configs) > channelschedule.MaxPerSite || request.Now.IsZero() {
		write(result{Error: "invalid_schedule", Message: "invalid preview request"})
		return
	}
	items := make([]result, 0, len(request.Configs))
	for _, config := range request.Configs {
		if err := config.Normalize(); err != nil {
			items = append(items, result{Error: "invalid_schedule", Message: err.Error()})
			continue
		}
		preview := config.Preview(request.Now.UTC())
		items = append(items, result{Config: &config, Preview: &preview})
	}
	write(struct {
		Items []result `json:"items"`
	}{Items: items})
}
