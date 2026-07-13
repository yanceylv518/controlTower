package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"controltower/server/internal/aggregator"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/config"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/httpapi"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/storage"
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

	if err := mysqlstore.ApplyDir(ctx, db, filepath.Dir(cfg.MigrationPath)); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}

	store := mysqlstore.New(db)
	authManager := ctauth.NewManager(store, time.Duration(cfg.SessionTTLHours)*time.Hour)
	count, err := store.CountUsers()
	if err != nil {
		return err
	}
	if count == 0 && cfg.AdminUsername != "" {
		hash, e := ctauth.HashPassword(cfg.AdminInitialPassword)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if e = store.CreateUser(storage.User{Username: cfg.AdminUsername, PasswordHash: hash, Role: "admin", CreatedAt: now, UpdatedAt: now}); e != nil {
			return e
		}
		log.Printf("initial admin created; change the password after first login")
	} else if count == 0 {
		log.Printf("no users configured; legacy dashboard token authentication only")
	}
	go authManager.CleanupLoop(context.Background())
	startAggregationRunner(store, time.Duration(cfg.AggregationIntervalSeconds)*time.Second)
	startNotificationRunner(store, time.Duration(cfg.NotificationIntervalSeconds)*time.Second)
	startChannelSnapshotRetentionRunner(store, cfg.ChannelSnapshotRetentionDays)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.NewMux(httpapi.Options{AgentToken: cfg.AgentToken, DashboardToken: cfg.DashboardToken, Store: store, AuthManager: authManager}),
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
	runner := dashboard.NewAlertNotificationRunner(store, store, store, store, store, interval)
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
