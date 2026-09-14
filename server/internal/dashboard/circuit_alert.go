package dashboard

import (
	"controltower/server/internal/storage"
	"time"
)

// Circuit events own their lifecycle; a missing metric is not a recovery.
type CircuitAlertSource interface {
	SyncCircuitAlerts(time.Time) error
	CircuitNotificationAlerts(time.Time) ([]storage.Alert, error)
}

func (r AlertNotificationRunner) WithCircuitAlerts(source CircuitAlertSource) AlertNotificationRunner {
	r.circuitAlerts = source
	return r
}

func isCircuitAlert(rule string) bool {
	return rule == "channel_circuit_opened" || rule == "channel_circuit_recovered"
}
