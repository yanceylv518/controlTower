package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestALBAccessLogAuditRedactsSecret(t *testing.T) {
	raw, _ := json.Marshal(redactAuditValue("", map[string]any{"endpoint": "cn-hangzhou.log.aliyuncs.com", "access_key_secret": "synthetic-secret"}))
	if strings.Contains(string(raw), "synthetic-secret") || !strings.Contains(string(raw), "[redacted]") {
		t.Fatal(string(raw))
	}
}
