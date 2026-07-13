package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AgentID                        string
	InstanceID                     string
	ServerURL                      string
	AgentToken                     string
	LogDSN                         string
	DataDir                        string
	LogPollIntervalSeconds         int
	LogBatchSize                   int
	LogQueryTimeoutSeconds         int
	ReportTimeoutSeconds           int
	MaxLocalBufferEvents           int
	LogEventMode                   string
	LogSampleLimit                 int
	SlowLogThresholdSeconds        float64
	NewAPIStatusURL                string
	NewAPIAdminAPIURL              string
	NewAPIAdminUsername            string
	NewAPIAdminPassword            string
	NewAPIAdminAccessToken         string
	NewAPIAdminUserID              int64
	NewAPIControlEnabled           bool
	DockerEnabled                  bool
	ChannelSnapshotEnabled         bool
	ChannelSnapshotLimit           int
	ChannelSnapshotIntervalSeconds int
	RunOnce                        bool
	DingTalkWebhookURL             string
	AlertErrorWindow               int
	AlertErrorThreshold            int
	AlertWindowMaxAgeMinutes       int
	AlertRemindMinutes             int
	AlertSlowEnabled               bool
	AlertSlowSeconds               float64
	AlertSlowWindow                int
	AlertSlowThreshold             int
	AlertSlowStreamSeconds         float64
}

func Load() (Config, error) {
	return LoadFromPath(os.Getenv("CT_AGENT_CONFIG"))
}

func LoadFromPath(path string) (Config, error) {
	return LoadFromFileAndMap(path, envMap())
}

func LoadFromFileAndMap(path string, envValues map[string]string) (Config, error) {
	values := map[string]string{}
	if path != "" {
		fileValues, err := readConfigFile(path)
		if err != nil {
			return Config{}, err
		}
		for key, value := range fileValues {
			values[key] = value
		}
	}
	for key, value := range envValues {
		if value != "" {
			values[key] = value
		}
	}
	return LoadFromMap(values)
}

func LoadFromMap(values map[string]string) (Config, error) {
	cfg := Config{
		AgentID:                        values["CT_AGENT_ID"],
		InstanceID:                     values["CT_INSTANCE_ID"],
		ServerURL:                      values["CT_SERVER_URL"],
		AgentToken:                     values["CT_AGENT_TOKEN"],
		LogDSN:                         values["CT_LOG_DSN"],
		DataDir:                        valueOrDefault(values, "CT_DATA_DIR", "data"),
		LogPollIntervalSeconds:         intOrDefault(values, "CT_LOG_POLL_INTERVAL_SECONDS", 30),
		LogBatchSize:                   intOrDefault(values, "CT_LOG_BATCH_SIZE", 1000),
		LogQueryTimeoutSeconds:         intOrDefault(values, "CT_LOG_QUERY_TIMEOUT_SECONDS", 2),
		ReportTimeoutSeconds:           intOrDefault(values, "CT_REPORT_TIMEOUT_SECONDS", 3),
		MaxLocalBufferEvents:           intOrDefault(values, "CT_MAX_LOCAL_BUFFER_EVENTS", 5000),
		LogEventMode:                   valueOrDefault(values, "CT_LOG_EVENT_MODE", "aggregate_with_samples"),
		LogSampleLimit:                 intOrDefault(values, "CT_LOG_SAMPLE_LIMIT", 50),
		SlowLogThresholdSeconds:        floatOrDefault(values, "CT_SLOW_LOG_THRESHOLD_SECONDS", 10),
		NewAPIStatusURL:                valueOrDefault(values, "CT_NEW_API_STATUS_URL", "http://127.0.0.1:3000/api/status"),
		NewAPIAdminAPIURL:              valueOrDefault(values, "CT_NEW_API_ADMIN_API_URL", "http://127.0.0.1:3000"),
		NewAPIAdminUsername:            values["CT_NEW_API_ADMIN_USERNAME"],
		NewAPIAdminPassword:            values["CT_NEW_API_ADMIN_PASSWORD"],
		NewAPIAdminAccessToken:         values["CT_NEW_API_ADMIN_ACCESS_TOKEN"],
		NewAPIAdminUserID:              int64(intOrDefault(values, "CT_NEW_API_ADMIN_USER_ID", 0)),
		NewAPIControlEnabled:           boolOrDefault(values, "CT_NEW_API_CONTROL_ENABLED", false),
		DockerEnabled:                  boolOrDefault(values, "CT_DOCKER_ENABLED", true),
		ChannelSnapshotEnabled:         boolOrDefault(values, "CT_CHANNEL_SNAPSHOT_ENABLED", true),
		ChannelSnapshotLimit:           intOrDefault(values, "CT_CHANNEL_SNAPSHOT_LIMIT", 1000),
		ChannelSnapshotIntervalSeconds: intOrDefault(values, "CT_CHANNEL_SNAPSHOT_INTERVAL_SECONDS", 600),
		RunOnce:                        boolOrDefault(values, "CT_AGENT_RUN_ONCE", false),
		DingTalkWebhookURL:             values["CT_DINGTALK_WEBHOOK_URL"],
		AlertErrorWindow:               intOrDefault(values, "CT_ALERT_ERROR_WINDOW", 10),
		AlertErrorThreshold:            intOrDefault(values, "CT_ALERT_ERROR_THRESHOLD", 3),
		AlertWindowMaxAgeMinutes:       intOrDefault(values, "CT_ALERT_WINDOW_MAX_AGE_MINUTES", 60),
		AlertRemindMinutes:             intOrDefault(values, "CT_ALERT_REMIND_MINUTES", 60),
		AlertSlowEnabled:               boolOrDefault(values, "CT_ALERT_SLOW_ENABLED", true),
		AlertSlowSeconds:               floatOrDefault(values, "CT_ALERT_SLOW_SECONDS", 120),
		AlertSlowWindow:                intOrDefault(values, "CT_ALERT_SLOW_WINDOW", 10),
		AlertSlowThreshold:             intOrDefault(values, "CT_ALERT_SLOW_THRESHOLD", 3),
		AlertSlowStreamSeconds:         floatOrDefault(values, "CT_ALERT_SLOW_STREAM_SECONDS", 300),
	}

	if cfg.AgentID == "" || cfg.InstanceID == "" || cfg.LogDSN == "" {
		return Config{}, errors.New("missing required control tower agent config")
	}
	// Standalone alert-only mode: with a DingTalk webhook configured the
	// server connection becomes optional; otherwise it stays required.
	if cfg.ServerURL == "" && cfg.DingTalkWebhookURL == "" {
		return Config{}, errors.New("missing required control tower agent config")
	}
	if cfg.ServerURL != "" && cfg.AgentToken == "" {
		return Config{}, errors.New("missing required control tower agent config")
	}
	if cfg.DingTalkWebhookURL != "" && !strings.HasPrefix(cfg.DingTalkWebhookURL, "http://") && !strings.HasPrefix(cfg.DingTalkWebhookURL, "https://") {
		return Config{}, errors.New("CT_DINGTALK_WEBHOOK_URL must be an http or https URL")
	}
	if cfg.AlertErrorWindow < 1 || cfg.AlertErrorWindow > 1000 {
		return Config{}, errors.New("CT_ALERT_ERROR_WINDOW must be between 1 and 1000")
	}
	if cfg.AlertErrorThreshold < 1 || cfg.AlertErrorThreshold > cfg.AlertErrorWindow {
		return Config{}, errors.New("CT_ALERT_ERROR_THRESHOLD must be between 1 and CT_ALERT_ERROR_WINDOW")
	}
	if cfg.AlertWindowMaxAgeMinutes < 0 {
		return Config{}, errors.New("CT_ALERT_WINDOW_MAX_AGE_MINUTES must be >= 0 (0 disables time decay)")
	}
	if cfg.AlertRemindMinutes < 0 {
		return Config{}, errors.New("CT_ALERT_REMIND_MINUTES must be >= 0 (0 disables reminders)")
	}
	if cfg.AlertSlowSeconds <= 0 {
		return Config{}, errors.New("CT_ALERT_SLOW_SECONDS must be > 0")
	}
	if cfg.AlertSlowWindow < 1 || cfg.AlertSlowWindow > 1000 {
		return Config{}, errors.New("CT_ALERT_SLOW_WINDOW must be between 1 and 1000")
	}
	if cfg.AlertSlowThreshold < 1 || cfg.AlertSlowThreshold > cfg.AlertSlowWindow {
		return Config{}, errors.New("CT_ALERT_SLOW_THRESHOLD must be between 1 and CT_ALERT_SLOW_WINDOW")
	}
	if cfg.AlertSlowStreamSeconds < 0 {
		return Config{}, errors.New("CT_ALERT_SLOW_STREAM_SECONDS must be >= 0 (0 excludes streams)")
	}
	if cfg.LogPollIntervalSeconds < 1 || cfg.LogPollIntervalSeconds > 3600 {
		return Config{}, errors.New("CT_LOG_POLL_INTERVAL_SECONDS must be between 1 and 3600")
	}
	if cfg.LogBatchSize < 1 || cfg.LogBatchSize > 5000 {
		return Config{}, errors.New("CT_LOG_BATCH_SIZE must be between 1 and 5000")
	}
	if cfg.ChannelSnapshotLimit < 1 || cfg.ChannelSnapshotLimit > 5000 {
		return Config{}, errors.New("CT_CHANNEL_SNAPSHOT_LIMIT must be between 1 and 5000")
	}
	if cfg.ChannelSnapshotIntervalSeconds < 30 || cfg.ChannelSnapshotIntervalSeconds > 86400 {
		return Config{}, errors.New("CT_CHANNEL_SNAPSHOT_INTERVAL_SECONDS must be between 30 and 86400")
	}
	if cfg.LogEventMode != "aggregate_only" && cfg.LogEventMode != "aggregate_with_samples" && cfg.LogEventMode != "full_debug" {
		return Config{}, errors.New("CT_LOG_EVENT_MODE must be aggregate_only, aggregate_with_samples, or full_debug")
	}
	if cfg.LogSampleLimit < 0 || cfg.LogSampleLimit > 1000 {
		return Config{}, errors.New("CT_LOG_SAMPLE_LIMIT must be between 0 and 1000")
	}
	if cfg.NewAPIControlEnabled && cfg.NewAPIAdminUserID <= 0 {
		return Config{}, errors.New("CT_NEW_API_ADMIN_USER_ID must be positive when channel control is enabled")
	}
	if cfg.NewAPIControlEnabled && cfg.NewAPIAdminAccessToken == "" && (cfg.NewAPIAdminUsername == "" || cfg.NewAPIAdminPassword == "") {
		return Config{}, errors.New("configure CT_NEW_API_ADMIN_ACCESS_TOKEN or both CT_NEW_API_ADMIN_USERNAME and CT_NEW_API_ADMIN_PASSWORD")
	}
	if cfg.SlowLogThresholdSeconds < 0 {
		return Config{}, errors.New("CT_SLOW_LOG_THRESHOLD_SECONDS must be >= 0")
	}
	return cfg, nil
}

func envMap() map[string]string {
	keys := []string{
		"CT_AGENT_ID",
		"CT_INSTANCE_ID",
		"CT_SERVER_URL",
		"CT_AGENT_TOKEN",
		"CT_LOG_DSN",
		"CT_DATA_DIR",
		"CT_LOG_POLL_INTERVAL_SECONDS",
		"CT_LOG_BATCH_SIZE",
		"CT_LOG_QUERY_TIMEOUT_SECONDS",
		"CT_REPORT_TIMEOUT_SECONDS",
		"CT_MAX_LOCAL_BUFFER_EVENTS",
		"CT_LOG_EVENT_MODE",
		"CT_LOG_SAMPLE_LIMIT",
		"CT_SLOW_LOG_THRESHOLD_SECONDS",
		"CT_NEW_API_STATUS_URL",
		"CT_NEW_API_ADMIN_API_URL",
		"CT_NEW_API_ADMIN_USERNAME",
		"CT_NEW_API_ADMIN_PASSWORD",
		"CT_NEW_API_ADMIN_ACCESS_TOKEN",
		"CT_NEW_API_ADMIN_USER_ID",
		"CT_NEW_API_CONTROL_ENABLED",
		"CT_DOCKER_ENABLED",
		"CT_CHANNEL_SNAPSHOT_ENABLED",
		"CT_CHANNEL_SNAPSHOT_LIMIT",
		"CT_CHANNEL_SNAPSHOT_INTERVAL_SECONDS",
		"CT_AGENT_RUN_ONCE",
		"CT_DINGTALK_WEBHOOK_URL",
		"CT_ALERT_ERROR_WINDOW",
		"CT_ALERT_ERROR_THRESHOLD",
		"CT_ALERT_WINDOW_MAX_AGE_MINUTES",
		"CT_ALERT_REMIND_MINUTES",
		"CT_ALERT_SLOW_ENABLED",
		"CT_ALERT_SLOW_SECONDS",
		"CT_ALERT_SLOW_WINDOW",
		"CT_ALERT_SLOW_THRESHOLD",
		"CT_ALERT_SLOW_STREAM_SECONDS",
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = os.Getenv(key)
	}
	return values
}

func readConfigFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "\ufeff")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid config line %d", lineNumber)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return nil, fmt.Errorf("invalid empty config key on line %d", lineNumber)
		}
		values[key] = stripQuotes(value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func stripQuotes(value string) string {
	if len(value) < 2 {
		return value
	}
	first := value[0]
	last := value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}

func valueOrDefault(values map[string]string, key string, fallback string) string {
	if values[key] == "" {
		return fallback
	}
	return values[key]
}

func intOrDefault(values map[string]string, key string, fallback int) int {
	if values[key] == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(values[key])
	if err != nil {
		return fallback
	}
	return parsed
}

func boolOrDefault(values map[string]string, key string, fallback bool) bool {
	if values[key] == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(values[key])
	if err != nil {
		return fallback
	}
	return parsed
}

func floatOrDefault(values map[string]string, key string, fallback float64) float64 {
	if values[key] == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(values[key], 64)
	if err != nil {
		return fallback
	}
	return parsed
}
