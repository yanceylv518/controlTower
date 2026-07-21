package settings

import (
	"os"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

type memoryStore struct{ values map[string]string }

func (s *memoryStore) ListSystemSettings() ([]storage.SystemSetting, error) {
	out := []storage.SystemSetting{}
	for k, v := range s.values {
		out = append(out, storage.SystemSetting{Key: k, Value: v})
	}
	return out, nil
}
func (s *memoryStore) ReplaceSystemSettings(values map[string]string, actor string, now time.Time) error {
	s.values = values
	return nil
}

func TestProviderDBEnvDefaultAndInvalidate(t *testing.T) {
	t.Setenv(P95Warn, "8")
	store := &memoryStore{values: map[string]string{P95Warn: "12"}}
	p := NewProvider(store, time.Hour)
	items, err := p.Items()
	if err != nil {
		t.Fatal(err)
	}
	if items[P95Warn].Value != "12" || items[P95Warn].Source != "db" {
		t.Fatalf("db override missing: %#v", items[P95Warn])
	}
	delete(store.values, P95Warn)
	p.Invalidate()
	items, _ = p.Items()
	if items[P95Warn].Value != "8" || items[P95Warn].Source != "env" {
		t.Fatalf("env fallback missing: %#v", items[P95Warn])
	}
	os.Unsetenv(P95Warn)
	p.Invalidate()
	items, _ = p.Items()
	if items[P95Warn].Value != "5" || items[P95Warn].Source != "default" {
		t.Fatalf("default fallback missing: %#v", items[P95Warn])
	}
}

func TestValidateSettings(t *testing.T) {
	bad := Validate(map[string]string{CPUWarn: "95", CPUCrit: "90", RetentionDetail: "0", RetentionHealthHours: "169", P95Warn: "0.1", TTFTP50Threshold: "30", TTFTP90Threshold: "20"})
	for _, key := range []string{CPUWarn, RetentionDetail, RetentionHealthHours, P95Warn, TTFTP50Threshold} {
		if bad[key] == "" {
			t.Fatalf("missing validation for %s", key)
		}
	}
}

func TestValidateDefaultInstanceID(t *testing.T) {
	if got := Validate(map[string]string{DefaultInstanceID: "inst-prod"}); len(got) != 0 {
		t.Fatalf("valid default instance rejected: %#v", got)
	}
	if got := Validate(map[string]string{DefaultInstanceID: strings.Repeat("x", 129)}); got[DefaultInstanceID] == "" {
		t.Fatal("missing default instance length validation")
	}
}
