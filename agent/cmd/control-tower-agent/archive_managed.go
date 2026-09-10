package main

import (
	"bytes"
	"context"
	"controltower/agent/internal/config"

	ac "controltower/internal/archivecontrol"

	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

func archivePoll(ctx context.Context, client *http.Client, cfg config.Config, st ac.Status) (ac.Response, error) {
	var out ac.Response
	b, _ := json.Marshal(st)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(cfg.ServerURL, "/")+"/api/agent/log-archive/poll", bytes.NewReader(b))
	if err != nil {
		return out, errors.New("archive control request failed")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AgentToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return out, errors.New("archive control unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return out, errors.New("archive control rejected; check instance token and server version")
	}
	if json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&out) != nil {
		return out, errors.New("invalid archive control response")
	}
	return out, nil
}
