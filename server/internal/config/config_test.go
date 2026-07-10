package config

import "testing"

func TestLoadServerConfig(t *testing.T) {
	cfg, err := Load(map[string]string{
		"CT_PUBLIC_BASE_URL":    "https://control.example.com",
		"CT_DATABASE_DSN":       "controltower:password@tcp(mysql:3306)/control_tower?parseTime=true&loc=UTC",
		"CT_AGENT_TOKEN":        "agent-token",
		"CT_DASHBOARD_TOKEN":    "dashboard-token",
		"CT_AGENT_TOKEN_PEPPER": "pepper",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8080" {
		t.Fatalf("ListenAddr = %q", cfg.ListenAddr)
	}
	if cfg.DatabaseDriver != "mysql" {
		t.Fatalf("DatabaseDriver = %q", cfg.DatabaseDriver)
	}
	if cfg.MigrationPath != "server/migrations/001_init.sql" {
		t.Fatalf("MigrationPath = %q", cfg.MigrationPath)
	}
	if cfg.AggregationIntervalSeconds != 60 {
		t.Fatalf("AggregationIntervalSeconds = %d", cfg.AggregationIntervalSeconds)
	}
}

func TestLoadServerConfigAllowsAggregationIntervalOverride(t *testing.T) {
	cfg, err := Load(map[string]string{
		"CT_PUBLIC_BASE_URL":              "https://control.example.com",
		"CT_DATABASE_DSN":                 "dsn",
		"CT_AGENT_TOKEN":                  "agent-token",
		"CT_DASHBOARD_TOKEN":              "dashboard-token",
		"CT_AGENT_TOKEN_PEPPER":           "pepper",
		"CT_AGGREGATION_INTERVAL_SECONDS": "15",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.AggregationIntervalSeconds != 15 {
		t.Fatalf("AggregationIntervalSeconds = %d", cfg.AggregationIntervalSeconds)
	}
}

func TestLoadServerConfigRejectsMissingRequiredValues(t *testing.T) {
	_, err := Load(map[string]string{
		"CT_PUBLIC_BASE_URL": "https://control.example.com",
	})
	if err == nil {
		t.Fatal("expected missing config error")
	}
}

func TestLoadServerConfigRejectsUnsupportedDatabaseDriver(t *testing.T) {
	_, err := Load(map[string]string{
		"CT_PUBLIC_BASE_URL":    "https://control.example.com",
		"CT_DATABASE_DRIVER":    "postgres",
		"CT_DATABASE_DSN":       "dsn",
		"CT_AGENT_TOKEN":        "agent-token",
		"CT_DASHBOARD_TOKEN":    "dashboard-token",
		"CT_AGENT_TOKEN_PEPPER": "pepper",
	})
	if err == nil {
		t.Fatal("expected unsupported database driver error")
	}
}

func TestLoadServerConfigRejectsInvalidAggregationInterval(t *testing.T) {
	_, err := Load(map[string]string{
		"CT_PUBLIC_BASE_URL":              "https://control.example.com",
		"CT_DATABASE_DSN":                 "dsn",
		"CT_AGENT_TOKEN":                  "agent-token",
		"CT_DASHBOARD_TOKEN":              "dashboard-token",
		"CT_AGENT_TOKEN_PEPPER":           "pepper",
		"CT_AGGREGATION_INTERVAL_SECONDS": "0",
	})
	if err == nil {
		t.Fatal("expected invalid aggregation interval error")
	}
}

func TestServerConfigKeysIncludesRequiredSecrets(t *testing.T) {
	keys := Keys()
	for _, want := range []string{"CT_DATABASE_DRIVER", "CT_DATABASE_DSN", "CT_AGENT_TOKEN", "CT_DASHBOARD_TOKEN", "CT_AGENT_TOKEN_PEPPER", "CT_AGGREGATION_INTERVAL_SECONDS"} {
		found := false
		for _, key := range keys {
			if key == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing key %s in %#v", want, keys)
		}
	}
}
