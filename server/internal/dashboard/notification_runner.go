package dashboard

import (
	"context"
	"errors"
	"log"
	"time"

	"controltower/server/internal/storage"
)

type AlertNotificationRunner struct {
	handler  Handler
	interval time.Duration
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
		return err
	}
	now := time.Now().UTC()
	if err := r.handler.alertStore.ExpireSilencedAlerts(now); err != nil {
		return err
	}
	if err := r.handler.alertStore.UpsertCurrentAlerts(alertItemsToStorage(computed), now); err != nil {
		return err
	}
	if err := r.handler.alertStore.ResolveMissingAlerts(alertIDs(computed), now); err != nil {
		return err
	}
	alerts, err := r.handler.alertStore.QueryAlerts(storage.AlertQuery{Status: "firing", Limit: storage.MaxAlertQueryLimit})
	if err != nil {
		return err
	}
	return r.handler.dispatchAlertNotifications(alerts)
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
