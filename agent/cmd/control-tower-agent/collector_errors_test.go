package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"controltower/agent/internal/config"
	"controltower/agent/internal/reporter"
)

func TestCollectorLoopLogsSafeFailureStage(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cause := &reporter.HTTPStatusError{Operation: "secret", StatusCode: 413}
	failure := collectorFailure("buffer_flush", cause)
	if !errors.Is(failure, cause) {
		t.Fatal("failure lost original cause")
	}
	err := runCollectorLoop(ctx, config.Config{ReportTimeoutSeconds: 1, LogQueryTimeoutSeconds: 1}, func(context.Context) error {
		cancel()
		return failure
	})
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "stage=buffer_flush http_status=413; failures=1") || strings.Contains(got, "secret") {
		t.Fatalf("unexpected diagnostic: %s", got)
	}
}
