package config

import (
	"errors"
	"strconv"
)

type Config struct {
	ListenAddr                   string
	PublicBaseURL                string
	DatabaseDriver               string
	DatabaseDSN                  string
	MigrationPath                string
	RedisAddr                    string
	RedisPassword                string
	AgentToken                   string
	DashboardToken               string
	AgentTokenPepper             string
	AggregationIntervalSeconds   int
	NotificationIntervalSeconds  int
	ChannelSnapshotRetentionDays int
	AdminUsername                string
	AdminInitialPassword         string
	SessionTTLHours              int
	NotificationMaxAttempts      int
}

func Load(values map[string]string) (Config, error) {
	cfg := Config{
		ListenAddr:                   valueOrDefault(values, "CT_SERVER_LISTEN_ADDR", "0.0.0.0:8080"),
		PublicBaseURL:                values["CT_PUBLIC_BASE_URL"],
		DatabaseDriver:               valueOrDefault(values, "CT_DATABASE_DRIVER", "mysql"),
		DatabaseDSN:                  values["CT_DATABASE_DSN"],
		MigrationPath:                valueOrDefault(values, "CT_MIGRATION_PATH", "server/migrations/001_init.sql"),
		RedisAddr:                    values["CT_REDIS_ADDR"],
		RedisPassword:                values["CT_REDIS_PASSWORD"],
		AgentToken:                   values["CT_AGENT_TOKEN"],
		DashboardToken:               values["CT_DASHBOARD_TOKEN"],
		AgentTokenPepper:             values["CT_AGENT_TOKEN_PEPPER"],
		AggregationIntervalSeconds:   intOrDefault(values, "CT_AGGREGATION_INTERVAL_SECONDS", 60),
		NotificationIntervalSeconds:  intOrDefault(values, "CT_NOTIFICATION_INTERVAL_SECONDS", 30),
		ChannelSnapshotRetentionDays: intOrDefault(values, "CT_CHANNEL_SNAPSHOT_RETENTION_DAYS", 30),
		AdminUsername:                values["CT_ADMIN_USERNAME"], AdminInitialPassword: values["CT_ADMIN_INITIAL_PASSWORD"], SessionTTLHours: intOrDefault(values, "CT_SESSION_TTL_HOURS", 720),
		NotificationMaxAttempts: intOrDefault(values, "CT_NOTIFICATION_MAX_ATTEMPTS", 8),
	}
	if cfg.PublicBaseURL == "" || cfg.DatabaseDSN == "" || cfg.AgentToken == "" || cfg.DashboardToken == "" || cfg.AgentTokenPepper == "" {
		return Config{}, errors.New("missing required control tower server config")
	}
	if cfg.DatabaseDriver != "mysql" {
		return Config{}, errors.New("CT_DATABASE_DRIVER must be mysql")
	}
	if cfg.NotificationIntervalSeconds <= 0 {
		return Config{}, errors.New("CT_NOTIFICATION_INTERVAL_SECONDS must be positive")
	}
	if cfg.AggregationIntervalSeconds <= 0 {
		return Config{}, errors.New("CT_AGGREGATION_INTERVAL_SECONDS must be positive")
	}
	if cfg.ChannelSnapshotRetentionDays < 1 || cfg.ChannelSnapshotRetentionDays > 3650 {
		return Config{}, errors.New("CT_CHANNEL_SNAPSHOT_RETENTION_DAYS must be between 1 and 3650")
	}
	if cfg.SessionTTLHours < 1 || cfg.SessionTTLHours > 8760 {
		return Config{}, errors.New("CT_SESSION_TTL_HOURS must be between 1 and 8760")
	}
	if (cfg.AdminUsername == "") != (cfg.AdminInitialPassword == "") {
		return Config{}, errors.New("CT_ADMIN_USERNAME and CT_ADMIN_INITIAL_PASSWORD must be configured together")
	}
	if cfg.NotificationMaxAttempts < 1 || cfg.NotificationMaxAttempts > 100 {
		return Config{}, errors.New("CT_NOTIFICATION_MAX_ATTEMPTS must be between 1 and 100")
	}
	return cfg, nil
}

func Keys() []string {
	return []string{
		"CT_SERVER_LISTEN_ADDR",
		"CT_PUBLIC_BASE_URL",
		"CT_DATABASE_DRIVER",
		"CT_DATABASE_DSN",
		"CT_MIGRATION_PATH",
		"CT_REDIS_ADDR",
		"CT_REDIS_PASSWORD",
		"CT_AGENT_TOKEN",
		"CT_DASHBOARD_TOKEN",
		"CT_AGENT_TOKEN_PEPPER",
		"CT_AGGREGATION_INTERVAL_SECONDS",
		"CT_NOTIFICATION_INTERVAL_SECONDS",
		"CT_CHANNEL_SNAPSHOT_RETENTION_DAYS",
		"CT_ADMIN_USERNAME", "CT_ADMIN_INITIAL_PASSWORD", "CT_SESSION_TTL_HOURS", "CT_NOTIFICATION_MAX_ATTEMPTS",
	}
}

func valueOrDefault(values map[string]string, key, fallback string) string {
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
		return 0
	}
	return parsed
}
