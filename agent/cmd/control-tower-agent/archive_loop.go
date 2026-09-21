package main

import (
	"context"
	"controltower/agent/internal/config"
	"controltower/agent/internal/controlpoll"
	ac "controltower/internal/archivecontrol"
	"net/http"
	"sync"
	"time"
)

type archiveControlState struct {
	sync.Mutex
	status       ac.Status
	response     ac.Response
	expires      time.Time
	err          error
	pollInterval time.Duration
}

func (s *archiveControlState) poll(ctx context.Context, cfg config.Config) {
	client := &http.Client{Timeout: 15 * time.Second, Transport: controlpoll.Transport(ctx)}
	interval := s.pollInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	for {
		s.Lock()
		st := cloneArchiveStatus(s.status)
		s.Unlock()
		started := time.Now()
		out, err := archivePoll(ctx, client, cfg, st)
		s.Lock()
		s.response = out
		s.err = err
		s.expires = started.Add(time.Duration(out.LeaseSeconds) * time.Second)
		s.Unlock()
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func startManagedArchive(parent context.Context, cfg config.Config) func() {
	return startAutomaticManagedArchive(parent, cfg)
}
