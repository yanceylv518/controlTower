package main

import (
	"context"
	"log"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
)

func startLogArchive(ctx context.Context, cfg config.Config) (func(), error) {
	if cfg.LogArchiveManaged {
		return startManagedArchive(ctx, cfg), nil
	}
	if !cfg.LogArchiveEnabled {
		return func() {}, nil
	}
	w, err := logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, cfg.LogArchiveBatchSize)
	if err != nil {
		return nil, err
	}
	w.WithDelay(time.Duration(cfg.LogArchiveDelaySeconds) * time.Second)
	pass := func() {
		passCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		n, err := w.Pass(passCtx)
		if err != nil {
			log.Printf("log archive: %v", err)
		} else if n > 0 {
			log.Printf("log archive: committed %d rows", n)
		}
	}
	if cfg.RunOnce {
		pass()
		w.Close()
		return func() {}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer w.Close()
		pass()
		ticker := time.NewTicker(time.Duration(cfg.LogArchiveIntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pass()
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
