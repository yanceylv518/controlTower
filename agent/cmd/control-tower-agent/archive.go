package main

import (
	"context"
	"controltower/agent/internal/config"
	"errors"
)

func startLogArchive(ctx context.Context, cfg config.Config) (func(), error) {
	if !cfg.LogArchiveEnabled {
		return func() {}, nil
	}
	if !cfg.LogArchiveManaged {
		return nil, errors.New("standalone legacy archive retired; enable CT_LOG_ARCHIVE_MANAGED for the new two-task executor")
	}
	return startManagedArchive(ctx, cfg), nil
}
