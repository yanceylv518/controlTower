package dashboard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

type AlertNotificationRunner struct {
	circuitAlerts CircuitAlertSource
	handler       Handler
	interval      time.Duration
}

func (r AlertNotificationRunner) WithSettingsProvider(v *settings.Provider) AlertNotificationRunner {
	r.handler = r.handler.WithSettingsProvider(v)
	return r
}

func (r AlertNotificationRunner) WithMetricSource(v MetricSource) AlertNotificationRunner {
	r.handler = r.handler.WithMetricSource(v)
	return r
}
func (r AlertNotificationRunner) WithInstanceStore(v InstanceStore) AlertNotificationRunner {
	r.handler = r.handler.WithInstanceStore(v)
	return r
}
func (r AlertNotificationRunner) WithBalanceAlerts(source BalanceSource, usage BalanceUsageSource) AlertNotificationRunner {
	r.handler = r.handler.WithBalanceAlerts(source, usage)
	return r
}
func (r AlertNotificationRunner) WithBalanceSettings(v BalanceAlertSettingsStore) AlertNotificationRunner {
	r.handler = r.handler.WithBalanceSettings(v)
	return r
}

func NewAlertNotificationRunner(source OverviewSource, logStore LogStore, runtimeStore RuntimeStore, alertStore AlertStore, notificationStore NotificationStore, interval time.Duration) AlertNotificationRunner {
	handler := NewHandler(source).WithLogStore(logStore).WithRuntimeStore(runtimeStore).WithAlertStore(alertStore).WithNotificationStore(notificationStore)
	return AlertNotificationRunner{handler: handler, interval: interval}
}

func (r AlertNotificationRunner) RunOnce() error {
	if r.handler.alertStore == nil || r.handler.notificationStore == nil {
		return errors.New("alert notification stores not configured")
	}
	computed, err := r.handler.currentAlerts()
	if err != nil {
		return fmt.Errorf("evaluate current alerts: %w", err)
	}
	now := time.Now().UTC()
	var circuitErr error
	if r.circuitAlerts != nil {
		if err := r.circuitAlerts.SyncCircuitAlerts(now); err != nil {
			circuitErr = fmt.Errorf("import circuit events: %w", err)
		}
	}
	if err := r.handler.alertStore.ExpireSilencedAlerts(now); err != nil {
		return err
	}
	if err := r.handler.alertStore.UpsertCurrentAlerts(alertItemsToStorage(computed), now); err != nil {
		return err
	}
	if err := r.handler.alertStore.ResolveMissingAlerts(alertIDs(computed), now); err != nil {
		return err
	}
	if store, ok := r.handler.alertStore.(interface{ MarkOverviewAlertEvaluation(time.Time) error }); ok {
		if err := store.MarkOverviewAlertEvaluation(now); err != nil {
			return err
		}
	}
	alerts, err := r.handler.alertStore.QueryAlerts(storage.AlertQuery{Status: "firing", Limit: storage.MaxAlertQueryLimit})
	if err != nil {
		return err
	}
	if err := r.handler.dispatchAlertNotifications(alerts); err != nil {
		return errors.Join(circuitErr, fmt.Errorf("dispatch current alerts: %w", err))
	}
	if r.circuitAlerts != nil {
		events, err := r.circuitAlerts.CircuitNotificationAlerts(now)
		if err != nil {
			return errors.Join(circuitErr, fmt.Errorf("query circuit deliveries: %w", err))
		}
		return errors.Join(circuitErr, r.handler.dispatchAlertNotifications(events))
	}
	return circuitErr
}

func (r AlertNotificationRunner) Run(ctx context.Context) error {
	if r.interval <= 0 {
		return errors.New("alert notification interval must be positive")
	}
	for {
		if err := r.RunOnce(); err != nil {
			log.Printf("alert notification pass failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.interval):
		}
	}
}
