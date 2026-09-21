package config

import "testing"

func TestArchiveFoundationIdentityIsExplicitAndManaged(t *testing.T) {
	v := map[string]string{"CT_AGENT_ID": "agent", "CT_INSTANCE_ID": "instance", "CT_SERVER_URL": "http://server", "CT_AGENT_TOKEN": "token", "CT_LOG_DSN": "source", "CT_LOG_ARCHIVE_DSN": "target", "CT_LOG_ARCHIVE_ENABLED": "true", "CT_LOG_ARCHIVE_MANAGED": "true"}
	if _, err := LoadFromMap(v); err != nil {
		t.Fatal("legacy config rejected", err)
	}
	v["CT_LOG_ARCHIVE_DATASET_ID"] = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := LoadFromMap(v); err == nil {
		t.Fatal("partial identity accepted")
	}
	v["CT_LOG_ARCHIVE_GENERATION_ID"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	v["CT_LOG_ARCHIVE_SITE_ID"] = "site"
	if _, err := LoadFromMap(v); err != nil {
		t.Fatal(err)
	}
	v["CT_LOG_ARCHIVE_MANAGED"] = "false"
	if _, err := LoadFromMap(v); err == nil {
		t.Fatal("standalone v2 writer accepted")
	}
}
