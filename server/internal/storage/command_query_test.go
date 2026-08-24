package storage

import "testing"

func TestIsConfigurationAuditOperation(t *testing.T) {
	for _, operation := range []string{"settings.update", "billing.price_update", "billing.group_ratio_update", "billing.model_metadata_update", "billing.models_sync", "channel.update"} {
		if !IsConfigurationAuditOperation(operation) {
			t.Fatalf("configuration operation rejected: %s", operation)
		}
	}
	for _, operation := range []string{"auth.viewer_login", "channel.probe", "channel.verify", "billing.backfill", "passthrough.logs"} {
		if IsConfigurationAuditOperation(operation) {
			t.Fatalf("runtime operation accepted: %s", operation)
		}
	}
}
