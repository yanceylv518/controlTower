package storage

import (
	"strings"
	"testing"
)

func TestIsConfigurationAuditOperation(t *testing.T) {
	for _, operation := range []string{"auth.account_create", "auth.account_update", "auth.password_reset", "settings.update", "instance.create", "instance.update", "instance.delete", "instance.token_rotate", "tuning.policy_update", "tuning.group_presets_update", "billing.discount.create", "billing.discount.update", "billing.discount.delete", "billing.upstream.create", "billing.upstream.update", "billing.upstream.delete", "billing.channel_setting.update", "billing.user_setting.update", "billing.price_update", "billing.group_ratio_update", "billing.model_metadata_update", "billing.models_sync", "channel.update"} {
		if !IsConfigurationAuditOperation(operation) {
			t.Fatalf("configuration operation rejected: %s", operation)
		}
	}
	for _, operation := range []string{"auth.viewer_login", "channel.probe", "channel.verify", "passthrough.logs", "tuning.auto_execute"} {
		if IsConfigurationAuditOperation(operation) {
			t.Fatalf("runtime operation accepted: %s", operation)
		}
	}
	if !IsSupportedOperationAudit("http.billing.upstreams.put") || !IsConfigurationAuditOperation("billing.backfill") || !IsConfigurationAuditOperation("tuning.base_priority_sync") || !IsConfigurationAuditOperation("auth.password_change") {
		t.Fatal("supported Control Tower mutation audits must be accepted")
	}
	if IsSupportedOperationAudit("unknown.operation") || !IsExcludedOperationAudit("passthrough.logs") {
		t.Fatal("unknown writes must be rejected and read-only passthrough operations excluded")
	}
}

func TestIsManualOperationAuditExcludesAutomaticActorsAndTriggers(t *testing.T) {
	for _, audit := range []OperationAudit{
		{ActorID: "system:auto", ActorType: "system", TriggerType: "automatic"},
		{ActorID: "agent:collector", TriggerType: "manual"},
		{ActorID: "admin", ActorType: "human", TriggerType: "automatic"},
		{ActorID: "system", ActorType: "human", TriggerType: "manual"},
		{ActorID: "agent", ActorType: "human", TriggerType: "manual"},
		{ActorID: "admin", ActorType: "human", OperationType: "tuning.auto_execute", TriggerType: "manual"},
	} {
		if IsManualOperationAudit(audit) {
			t.Fatalf("automatic audit was classified as manual: %+v", audit)
		}
	}
	for _, audit := range []OperationAudit{
		{ActorID: "admin", ActorType: "human", TriggerType: "manual"},
		{ActorID: "system", ActorType: "human", ActorRole: "admin", AuthMethod: "session", TriggerType: "manual"},
		{ActorID: "agent:admin", ActorType: "human", ActorRole: "admin", AuthMethod: "session", TriggerType: "manual"},
		{ActorID: "token", ActorType: "service_token", TriggerType: "manual"},
	} {
		if !IsManualOperationAudit(audit) {
			t.Fatalf("manual audit was rejected: %+v", audit)
		}
	}
}

func TestNormalizeOperationAuditInfersAuthenticationMethod(t *testing.T) {
	for _, test := range []struct {
		actorID, actorType, want string
	}{
		{actorID: "admin", actorType: "human", want: "session"},
		{actorID: "token", actorType: "service_token", want: "bearer"},
	} {
		audit := NormalizeOperationAudit(OperationAudit{ActorID: test.actorID, ActorType: test.actorType})
		if audit.AuthMethod != test.want {
			t.Fatalf("auth method for %q = %q, want %q", test.actorID, audit.AuthMethod, test.want)
		}
	}
}

func TestOperationAuditFilterCatalogIsFixedAndGroupsHTTPTypes(t *testing.T) {
	filters := OperationAuditFilterTypes()
	if len(filters) == 0 {
		t.Fatalf("invalid operation audit filter catalog: %v", filters)
	}
	seen := make(map[string]struct{}, len(filters))
	for _, filter := range filters {
		if _, duplicate := seen[filter]; duplicate {
			t.Fatalf("duplicate operation audit filter: %q", filter)
		}
		seen[filter] = struct{}{}
	}
	if _, ok := seen["http.system.*"]; !ok {
		t.Fatal("fixed module filters should include system API mutations")
	}
	if _, ok := seen["tuning.auto_execute"]; ok {
		t.Fatal("automatic tuning must not be available as a manual operation filter")
	}
	if _, ok := seen["http.system.settings.put"]; ok {
		t.Fatal("route-specific values must not enter the fixed filter catalog")
	}
	filters[0] = "mutated"
	if OperationAuditFilterTypes()[0] == "mutated" {
		t.Fatal("operation audit filter catalog must not expose mutable package state")
	}

	if !OperationAuditTypeMatchesFilter("http.archive.logs.put", "http.archive.*") {
		t.Fatal("module filter should include legacy route-specific operation types")
	}
	if OperationAuditTypeMatchesFilter("http.archivex.logs.put", "http.archive.*") {
		t.Fatal("module filter must treat underscores literally")
	}
	if !OperationAuditTypeMatchesFilter("billing.price_update", "billing.price_update") {
		t.Fatal("semantic operation filters should remain exact")
	}
	if _, ok := OperationAuditTypeFilterPrefix("http.unknown.*"); ok {
		t.Fatal("unknown module filters must not be accepted as categories")
	}
}

func TestRedactAuditErrorRemovesCredentialsAndBoundsLength(t *testing.T) {
	value := RedactAuditError(`failed Authorization: Bearer abc.def token=secret-value dsn=mysql://user:pass@db/control`)
	for _, secret := range []string{"abc.def", "secret-value", "user:pass"} {
		if strings.Contains(value, secret) {
			t.Fatalf("audit error leaked %q: %s", secret, value)
		}
	}
	if !strings.Contains(value, "[redacted]") {
		t.Fatalf("redacted placeholders missing: %s", value)
	}
	if got := len(RedactAuditError(strings.Repeat("x", 1200))); got > 1000 {
		t.Fatalf("audit error length=%d, want <= 1000 bytes", got)
	}
}
