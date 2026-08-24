package storage

type ChannelCommandQuery struct {
	InstanceID string
	Status     string
	Limit      int
	Offset     int
}

type OperationAuditQuery struct {
	InstanceID string
	Limit      int
	Offset     int
}

// IsConfigurationAuditOperation keeps operation history focused on explicit
// configuration changes. Runtime activity, reads, probes and logins are not
// configuration changes and must not grow the audit table.
func IsConfigurationAuditOperation(operation string) bool {
	switch operation {
	case "settings.update",
		"billing.price_update",
		"billing.group_ratio_update",
		"billing.model_metadata_update",
		"billing.models_sync",
		"channel.update":
		return true
	default:
		return false
	}
}

func NormalizeCommandPagination(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
