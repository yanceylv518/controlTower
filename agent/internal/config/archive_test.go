package config

import "testing"

func TestManagedArchiveRequiresServerAndContinuousMode(t *testing.T) {
	base := map[string]string{"CT_AGENT_ID": "a", "CT_INSTANCE_ID": "i", "CT_SERVER_URL": "http://server", "CT_AGENT_TOKEN": "t", "CT_LOG_DSN": "source", "CT_LOG_ARCHIVE_MANAGED": "true"}
	if c, e := LoadFromMap(base); e != nil || !c.LogArchiveManaged {
		t.Fatal(c, e)
	}
	base["CT_AGENT_RUN_ONCE"] = "true"
	if _, e := LoadFromMap(base); e == nil {
		t.Fatal("managed one-shot accepted")
	}
}

func TestArchiveConfig(t *testing.T) {
	base := map[string]string{"CT_AGENT_ID": "a", "CT_INSTANCE_ID": "i", "CT_SERVER_URL": "http://server", "CT_AGENT_TOKEN": "t", "CT_LOG_DSN": "source"}
	cfg, err := LoadFromMap(base)
	if err != nil || cfg.LogArchiveEnabled || cfg.LogArchiveBatchSize != 500 || cfg.LogArchiveIntervalSeconds != 30 {
		t.Fatalf("archive defaults: %v", err)
	}
	base["CT_LOG_ARCHIVE_ENABLED"] = "true"
	if _, err := LoadFromMap(base); err == nil {
		t.Fatal("missing target accepted")
	}
	base["CT_LOG_ARCHIVE_DSN"] = "target"
	if _, err := LoadFromMap(base); err != nil {
		t.Fatal(err)
	}
	base["CT_LOG_ARCHIVE_BATCH_SIZE"] = "5001"
	if _, err := LoadFromMap(base); err == nil {
		t.Fatal("oversized batch accepted")
	}
	base["CT_LOG_ARCHIVE_BATCH_SIZE"] = "500"
	base["CT_LOG_ARCHIVE_INTERVAL_SECONDS"] = "0"
	if _, err := LoadFromMap(base); err == nil {
		t.Fatal("zero interval accepted")
	}
}

func TestArchiveEnvironment(t *testing.T) {
	t.Setenv("CT_LOG_ARCHIVE_ENABLED", "true")
	t.Setenv("CT_LOG_ARCHIVE_DSN", "target")
	t.Setenv("CT_LOG_ARCHIVE_BATCH_SIZE", "100")
	t.Setenv("CT_LOG_ARCHIVE_INTERVAL_SECONDS", "15")
	values := envMap()
	if values["CT_LOG_ARCHIVE_ENABLED"] != "true" || values["CT_LOG_ARCHIVE_DSN"] != "target" || values["CT_LOG_ARCHIVE_BATCH_SIZE"] != "100" || values["CT_LOG_ARCHIVE_INTERVAL_SECONDS"] != "15" {
		t.Fatal("archive environment not loaded")
	}
}
