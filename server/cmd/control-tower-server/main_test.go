package main

import (
	"context"
	"testing"
	"time"
)

func TestWorkerGroupWaitsForCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	workers := newWorkerGroup(ctx)
	started := make(chan struct{})
	workers.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
	})
	<-started
	cancel()
	if err := workers.Wait(time.Second); err != nil {
		t.Fatalf("wait for canceled worker: %v", err)
	}
}

func TestWorkerGroupWaitReportsTimeout(t *testing.T) {
	release := make(chan struct{})
	workers := newWorkerGroup(context.Background())
	workers.Go(func(context.Context) {
		<-release
	})
	if err := workers.Wait(time.Millisecond); err == nil {
		t.Fatal("expected worker wait timeout")
	}
	close(release)
}
