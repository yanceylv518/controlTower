package config

import "testing"

func validB4Config() map[string]string {
	return map[string]string{"CT_PUBLIC_BASE_URL": "x", "CT_DATABASE_DSN": "x", "CT_AGENT_TOKEN": "x", "CT_DASHBOARD_TOKEN": "x", "CT_AGENT_TOKEN_PEPPER": "x"}
}
func TestM1B4ConfigDefaultsAndBounds(t *testing.T) {
	base := validB4Config()
	cfg, e := Load(base)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.CommandExpiryMinutes != 10 || cfg.RetentionDetailDays != 30 || cfg.RetentionMetric5mDays != 90 || cfg.RetentionRuntimeDays != 7 {
		t.Fatalf("defaults=%+v", cfg)
	}
	base["CT_COMMAND_EXPIRY_MINUTES"] = "0"
	if _, e = Load(base); e == nil {
		t.Fatal("zero command expiry accepted")
	}
	base["CT_COMMAND_EXPIRY_MINUTES"] = "10"
	base["CT_RETENTION_DETAIL_DAYS"] = "-1"
	if _, e = Load(base); e == nil {
		t.Fatal("negative retention accepted")
	}
	base["CT_RETENTION_DETAIL_DAYS"] = "0"
	if _, e = Load(base); e != nil {
		t.Fatalf("zero retention rejected: %v", e)
	}
}
