package preflight

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controltower/agent/internal/config"
	"controltower/agent/internal/logcollector"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type Check struct {
	Name    string
	Status  Status
	Message string
}

type Result struct {
	Checks []Check
}

func (r Result) OK() bool {
	for _, check := range r.Checks {
		if check.Status == StatusFail {
			return false
		}
	}
	return true
}

func (r Result) Err() error {
	if r.OK() {
		return nil
	}
	return errors.New("preflight checks failed")
}

func Run(ctx context.Context, cfg config.Config) Result {
	r := Result{}
	r.add(StatusPass, "config", "required configuration loaded")
	r.checkDataDir(cfg.DataDir)
	r.checkServer(ctx, cfg)
	r.checkMySQL(ctx, cfg)
	return r
}

func (r *Result) add(status Status, name string, message string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Message: message})
}

func (r *Result) checkDataDir(dataDir string) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		r.add(StatusFail, "data_dir", err.Error())
		return
	}
	file, err := os.CreateTemp(dataDir, ".ct-agent-write-check-*")
	if err != nil {
		r.add(StatusFail, "data_dir", err.Error())
		return
	}
	name := file.Name()
	_ = file.Close()
	if err := os.Remove(name); err != nil {
		r.add(StatusWarn, "data_dir_cleanup", err.Error())
	}
	r.add(StatusPass, "data_dir", fmt.Sprintf("writable: %s", filepath.Clean(dataDir)))
}

func (r *Result) checkServer(ctx context.Context, cfg config.Config) {
	url := strings.TrimRight(cfg.ServerURL, "/") + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.add(StatusFail, "control_tower_server", err.Error())
		return
	}
	client := &http.Client{Timeout: time.Duration(cfg.ReportTimeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		r.add(StatusFail, "control_tower_server", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		r.add(StatusFail, "control_tower_server", fmt.Sprintf("healthz returned HTTP %d", resp.StatusCode))
		return
	}
	r.add(StatusPass, "control_tower_server", "healthz reachable")
}

func (r *Result) checkMySQL(ctx context.Context, cfg config.Config) {
	db, err := logcollector.OpenMySQL(cfg.LogDSN)
	if err != nil {
		r.add(StatusFail, "mysql_open", err.Error())
		return
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		r.add(StatusFail, "mysql_ping", err.Error())
		return
	}
	r.add(StatusPass, "mysql_ping", "connected")

	r.checkLogsTable(ctx, db)
	if cfg.ChannelSnapshotEnabled {
		r.checkChannelsTable(ctx, db)
	} else {
		r.add(StatusWarn, "channels_table", "skipped because CT_CHANNEL_SNAPSHOT_ENABLED=false")
	}
	r.checkLogsIDIndex(ctx, db)
}

func (r *Result) checkLogsTable(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM logs WHERE id > ? AND type IN (2, 5) ORDER BY id ASC LIMIT 1`, 0)
	if err != nil {
		r.add(StatusFail, "logs_table", err.Error())
		return
	}
	_ = rows.Close()
	r.add(StatusPass, "logs_table", "queryable")
}

func (r *Result) checkChannelsTable(ctx context.Context, db *sql.DB) {
	rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(name, ''), CAST(status AS CHAR), COALESCE(weight, 0), COALESCE(models, '') FROM channels ORDER BY id ASC LIMIT 1`)
	if err != nil {
		r.add(StatusFail, "channels_table", err.Error())
		return
	}
	_ = rows.Close()
	r.add(StatusPass, "channels_table", "queryable")
}

func (r *Result) checkLogsIDIndex(ctx context.Context, db *sql.DB) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'logs' AND column_name = 'id'`).Scan(&count)
	if err != nil {
		r.add(StatusWarn, "logs_id_index", err.Error())
		return
	}
	if count == 0 {
		r.add(StatusWarn, "logs_id_index", "no index found for logs.id")
		return
	}
	r.add(StatusPass, "logs_id_index", "logs.id index found")
}
