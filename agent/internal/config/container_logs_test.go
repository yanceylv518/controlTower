package config

import "testing"

func TestContainerLogSocketFromEnvironment(t *testing.T) {
	t.Setenv("CT_CONTAINER_LOG_SOCKET", "/run/control-tower-log-reader/reader.sock")
	if envMap()["CT_CONTAINER_LOG_SOCKET"] != "/run/control-tower-log-reader/reader.sock" {
		t.Fatal("log socket missing from environment")
	}
	cfg, err := LoadFromMap(map[string]string{"CT_SERVER_URL": "http://127.0.0.1", "CT_AGENT_TOKEN": "test", "CT_AGENT_ID": "test", "CT_INSTANCE_ID": "test", "CT_LOG_COLLECT_ENABLED": "false", "CT_CONTAINER_LOG_SOCKET": "/run/reader.sock"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ContainerLogSocket != "/run/reader.sock" {
		t.Fatal("log socket not loaded into config")
	}
}
