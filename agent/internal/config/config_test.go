package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromMapAppliesDefaults(t *testing.T) {
	cfg, err := LoadFromMap(map[string]string{
		"CT_AGENT_ID":    "agent-1",
		"CT_INSTANCE_ID": "inst-1",
		"CT_SERVER_URL":  "https://control.example.com",
		"CT_AGENT_TOKEN": "token",
		"CT_LOG_DSN":     "readonly-dsn",
		"CT_DATA_DIR":    "data",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.LogPollIntervalSeconds != 30 {
		t.Fatalf("unexpected poll interval: %d", cfg.LogPollIntervalSeconds)
	}
	if cfg.LogBatchSize != 1000 {
		t.Fatalf("unexpected batch size: %d", cfg.LogBatchSize)
	}
	if cfg.NewAPIStatusURL != "http://127.0.0.1:3000/api/status" {
		t.Fatalf("unexpected status url: %s", cfg.NewAPIStatusURL)
	}
	if !cfg.DockerEnabled {
		t.Fatalf("docker should default to enabled")
	}
	if cfg.RunOnce {
		t.Fatalf("agent should default to loop mode")
	}
	if cfg.LogEventMode != "aggregate_with_samples" || cfg.LogSampleLimit != 50 || cfg.SlowLogThresholdSeconds != 10 {
		t.Fatalf("unexpected log event defaults: %#v", cfg)
	}
	if cfg.NewAPIAdminAPIURL != "http://127.0.0.1:3000" || cfg.NewAPIControlEnabled {
		t.Fatalf("unexpected new-api control defaults: %#v", cfg)
	}
	if cfg.ChannelSnapshotIntervalSeconds != 600 {
		t.Fatalf("unexpected channel snapshot interval: %d", cfg.ChannelSnapshotIntervalSeconds)
	}
}

func TestLoadFromMapAcceptsRunOnce(t *testing.T) {
	cfg, err := LoadFromMap(map[string]string{
		"CT_AGENT_ID":       "agent-1",
		"CT_INSTANCE_ID":    "inst-1",
		"CT_SERVER_URL":     "https://control.example.com",
		"CT_AGENT_TOKEN":    "token",
		"CT_LOG_DSN":        "readonly-dsn",
		"CT_AGENT_RUN_ONCE": "true",
		"CT_LOG_BATCH_SIZE": "25",
		"CT_DOCKER_ENABLED": "false",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.RunOnce {
		t.Fatalf("expected run once mode")
	}
	if cfg.LogBatchSize != 25 {
		t.Fatalf("unexpected batch size: %d", cfg.LogBatchSize)
	}
	if cfg.DockerEnabled {
		t.Fatalf("docker should be disabled")
	}
}

func TestLoadFromFileAndMapLoadsEnvStyleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.env")
	contents := "# local config\n" +
		"CT_AGENT_ID=agent-file\n" +
		"CT_INSTANCE_ID='inst-file'\n" +
		"CT_SERVER_URL=\"https://control.example.com\"\n" +
		"CT_AGENT_TOKEN=token-file\n" +
		"CT_LOG_DSN=user:pass@tcp(127.0.0.1:3306)/newapi?parseTime=true\n" +
		"CT_LOG_POLL_INTERVAL_SECONDS=15\n" +
		"CT_DOCKER_ENABLED=false\n" +
		"CT_LOG_EVENT_MODE=full_debug\n" +
		"CT_LOG_SAMPLE_LIMIT=5\n" +
		"CT_SLOW_LOG_THRESHOLD_SECONDS=3.5\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := LoadFromFileAndMap(path, map[string]string{})
	if err != nil {
		t.Fatalf("load config file: %v", err)
	}
	if cfg.AgentID != "agent-file" || cfg.InstanceID != "inst-file" {
		t.Fatalf("unexpected ids: %+v", cfg)
	}
	if cfg.LogPollIntervalSeconds != 15 {
		t.Fatalf("unexpected poll interval: %d", cfg.LogPollIntervalSeconds)
	}
	if cfg.DockerEnabled {
		t.Fatalf("expected docker disabled from config file")
	}
	if cfg.LogEventMode != "full_debug" || cfg.LogSampleLimit != 5 || cfg.SlowLogThresholdSeconds != 3.5 {
		t.Fatalf("unexpected log event config: %#v", cfg)
	}
}

func TestLoadFromFileAndMapLetsEnvOverrideFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.env")
	contents := "CT_AGENT_ID=agent-file\n" +
		"CT_INSTANCE_ID=inst-file\n" +
		"CT_SERVER_URL=https://control.example.com\n" +
		"CT_AGENT_TOKEN=token-file\n" +
		"CT_LOG_DSN=dsn-file\n" +
		"CT_LOG_BATCH_SIZE=10\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := LoadFromFileAndMap(path, map[string]string{
		"CT_AGENT_ID":       "agent-env",
		"CT_LOG_BATCH_SIZE": "20",
	})
	if err != nil {
		t.Fatalf("load config file: %v", err)
	}
	if cfg.AgentID != "agent-env" {
		t.Fatalf("expected env override, got %s", cfg.AgentID)
	}
	if cfg.LogBatchSize != 20 {
		t.Fatalf("expected env batch override, got %d", cfg.LogBatchSize)
	}
}

func TestLoadFromMapAcceptsNewAPIControlCredentials(t *testing.T) {
	cfg, err := LoadFromMap(map[string]string{"CT_AGENT_ID": "agent-1", "CT_INSTANCE_ID": "inst-1", "CT_SERVER_URL": "https://control.example.com", "CT_AGENT_TOKEN": "token", "CT_LOG_DSN": "readonly-dsn", "CT_NEW_API_CONTROL_ENABLED": "true", "CT_NEW_API_ADMIN_ACCESS_TOKEN": "admin-token", "CT_NEW_API_ADMIN_USER_ID": "7"})
	if err != nil || !cfg.NewAPIControlEnabled || cfg.NewAPIAdminUserID != 7 || cfg.NewAPIAdminAccessToken != "admin-token" {
		t.Fatalf("unexpected new-api control config: %#v err=%v", cfg, err)
	}
}

func TestLoadFromMapRejectsMissingRequiredFields(t *testing.T) {
	_, err := LoadFromMap(map[string]string{
		"CT_AGENT_ID":    "agent-1",
		"CT_INSTANCE_ID": "inst-1",
	})
	if err == nil {
		t.Fatalf("expected missing required field error")
	}
}

func TestLoadFromMapRejectsInvalidPollInterval(t *testing.T) {
	_, err := LoadFromMap(map[string]string{
		"CT_AGENT_ID":                  "agent-1",
		"CT_INSTANCE_ID":               "inst-1",
		"CT_SERVER_URL":                "https://control.example.com",
		"CT_AGENT_TOKEN":               "token",
		"CT_LOG_DSN":                   "readonly-dsn",
		"CT_LOG_POLL_INTERVAL_SECONDS": "0",
	})
	if err == nil {
		t.Fatalf("expected invalid poll interval error")
	}
}
func TestLoadFromMapRejectsInvalidLogEventMode(t *testing.T) {
	_, err := LoadFromMap(map[string]string{
		"CT_AGENT_ID":       "agent-1",
		"CT_INSTANCE_ID":    "inst-1",
		"CT_SERVER_URL":     "https://control.example.com",
		"CT_AGENT_TOKEN":    "token",
		"CT_LOG_DSN":        "readonly-dsn",
		"CT_LOG_EVENT_MODE": "everything",
	})
	if err == nil {
		t.Fatalf("expected invalid log event mode error")
	}
}
