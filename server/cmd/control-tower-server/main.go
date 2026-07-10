package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/config"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/httpapi"
	"controltower/server/internal/mysqlstore"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("control tower server failed: %v", err)
	}
}

func run() error {
	cfg, err := config.Load(envValues(config.Keys()))
	if err != nil {
		return err
	}

	db, err := mysqlstore.Open(cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping mysql: %w", err)
	}

	migration, err := os.ReadFile(cfg.MigrationPath)
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	if err := mysqlstore.ApplySQL(ctx, db, string(migration)); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}

	store := mysqlstore.New(db)
	startAggregationRunner(store, time.Duration(cfg.AggregationIntervalSeconds)*time.Second)
	startNotificationRunner(store, time.Duration(cfg.NotificationIntervalSeconds)*time.Second)
	startChannelSnapshotRetentionRunner(store, cfg.ChannelSnapshotRetentionDays)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.NewMux(httpapi.Options{AgentToken: cfg.AgentToken, DashboardToken: cfg.DashboardToken, Store: store}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("control tower server listening on %s", cfg.ListenAddr)
	return server.ListenAndServe()
}

func startAggregationRunner(store mysqlstore.Store, interval time.Duration) {
	runner := aggregator.NewRunner(
		aggregator.NewScheduler(store),
		store,
		aggregator.NewMemoryLock(),
		interval,
	)
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("aggregation runner stopped: %v", err)
		}
	}()
}

func startNotificationRunner(store mysqlstore.Store, interval time.Duration) {
	runner := dashboard.NewAlertNotificationRunner(store, store, store, store, interval)
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("notification runner stopped: %v", err)
		}
	}()
}
func startChannelSnapshotRetentionRunner(store mysqlstore.Store, retentionDays int) {
	prune := func() {
		cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		if err := store.PruneChannelSnapshots(cutoff); err != nil {
			log.Printf("channel snapshot retention failed: %v", err)
		}
	}
	prune()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			prune()
		}
	}()
}

func envValues(keys []string) map[string]string {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = os.Getenv(key)
	}
	return values
}
