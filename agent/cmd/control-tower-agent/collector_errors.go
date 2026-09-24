package main

import (
	"controltower/agent/internal/reporter"
	"errors"
)

type collectorStageError struct {
	stage string
	cause error
}

func (e *collectorStageError) Error() string { return e.stage + ": " + e.cause.Error() }
func (e *collectorStageError) Unwrap() error { return e.cause }
func collectorFailure(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &collectorStageError{stage: stage, cause: err}
}
func collectorFailureSummary(err error) string {
	var failure *collectorStageError
	if errors.As(err, &failure) {
		return "stage=" + failure.stage + " " + reporter.SafeErrorSummary(failure.cause)
	}
	return "stage=collect " + reporter.SafeErrorSummary(err)
}
