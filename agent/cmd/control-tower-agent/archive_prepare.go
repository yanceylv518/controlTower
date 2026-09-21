package main

import (
	"context"
	"controltower/agent/internal/config"
	"controltower/agent/internal/logarchive"
	"encoding/json"
	"errors"
	"io"
)

func prepareArchiveFoundation(ctx context.Context, cfg config.Config, out io.Writer) error {
	if cfg.LogArchiveIdentity.Validate() != nil {
		return errors.New("archive preparation requires explicit site/dataset/generation identity")
	}
	w, err := logarchive.Open(cfg.LogDSN, cfg.LogArchiveDSN, cfg.InstanceID, cfg.DataDir, cfg.LogArchiveBatchSize)
	if err != nil {
		return errors.New("archive preparation connection configuration invalid")
	}
	defer w.Close()
	info, err := w.PrepareFoundation(ctx, cfg.LogArchiveIdentity)
	if err != nil {
		return errors.New("archive preparation failed; verify paused writers, identities, schema and database permissions")
	}
	return json.NewEncoder(out).Encode(info)
}
