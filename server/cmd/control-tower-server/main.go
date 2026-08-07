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
	"controltower/server/internal/billing"
	"controltower/server/internal/config"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/httpapi"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
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

	pingCtx, cancelPing := context.WithTimeout(context.Background(), 10*time.Second)
	if err := db.PingContext(pingCtx); err != nil {
		cancelPing()
		return fmt.Errorf("ping mysql: %w", err)
	}
	cancelPing()

	// rc20 performs a one-time indexed scan of the legacy channel history to
	// preserve each channel's newest state before asynchronous cleanup starts.
	// Keep that migration bounded, but do not reuse the short ping timeout.
	migrationCtx, cancelMigration := context.WithTimeout(context.Background(), 30*time.Minute)
	err = mysqlstore.ApplyDir(migrationCtx, db, filepath.Dir(cfg.MigrationPath))
	cancelMigration()
	if err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}

	store := mysqlstore.New(db)
	settingsProvider := settings.NewProvider(store, 60*time.Second)
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
		if e = store.CreateUser(storage.User{Username: cfg.AdminUsername, PasswordHash: hash, Role: "admin", Enabled: true, CreatedAt: now, UpdatedAt: now}); e != nil {
			return e
		}
		log.Printf("initial admin created; change the password after first login")
	} else if count == 0 {
		log.Printf("no users configured; legacy dashboard token authentication only")
	}
	if cfg.APIOnly {
		log.Printf("API-only mode enabled; operational runners are disabled (billing job worker remains enabled)")
	} else {
		go authManager.CleanupLoop(context.Background())
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for now := range ticker.C {
				_, _ = store.DeleteExpiredInstanceTokens(now.UTC())
			}
		}()
		startAggregationRunner(store, time.Duration(cfg.AggregationIntervalSeconds)*time.Second)
		startNotificationRunner(store, settingsProvider, time.Duration(cfg.NotificationIntervalSeconds)*time.Second)
		startChannelSnapshotHistoryCleanup(store)
		startRetentionRunner(store, settingsProvider)
		startTuningRunner(store)
	}
	startBillingJobRunner(store, cfg.SecretKey, time.Duration(cfg.BillingPagePauseMilliseconds)*time.Millisecond)
	startReadonlyLogRollupRunner(store, cfg.SecretKey)

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpapi.NewMux(httpapi.Options{AgentToken: cfg.AgentToken, DashboardToken: cfg.DashboardToken, Store: store, TuningStore: store, AuthManager: authManager, AgentTokenPepper: cfg.AgentTokenPepper, SecretKey: cfg.SecretKey, NotificationMaxAttempts: cfg.NotificationMaxAttempts, CommandExpiry: time.Duration(cfg.CommandExpiryMinutes) * time.Minute, SettingsProvider: settingsProvider, BillingPagePause: time.Duration(cfg.BillingPagePauseMilliseconds) * time.Millisecond}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("control tower server listening on %s", cfg.ListenAddr)
	return server.ListenAndServe()
}

func startBillingJobRunner(store mysqlstore.Store, secretKey string, pagePause time.Duration) {
	readonly := &dashboard.PassthroughHandler{Config: store, SecretKey: secretKey}
	runner := billing.JobRunner{Source: dashboard.BillingReadonlySource{Handler: readonly}, Store: store, PagePause: pagePause}
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("billing job runner stopped: %v", err)
		}
	}()
}

func startReadonlyLogRollupRunner(store mysqlstore.Store, secretKey string) {
	source := &dashboard.PassthroughHandler{Config: store, SecretKey: secretKey}
	runner := dashboard.ReadonlyLogRollupRunner{Source: source, Store: store, Interval: 30 * time.Second}
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("readonly log rollup runner stopped: %v", err)
		}
	}()
}

func startTuningRunner(store mysqlstore.Store) {
	runner := tuning.NewEngine(store)
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("tuning runner stopped: %v", err)
		}
	}()
}

type retentionStore interface {
	PruneBefore(string, time.Time) (int64, error)
}

func startRetentionRunner(store retentionStore, provider *settings.Provider) {
	prune := func() {
		values, err := provider.Current()
		if err != nil {
			log.Printf("retention settings failed: %v", err)
			return
		}
		pruneRetention(store, values.RetentionDetailDays, values.RetentionMetric5mDays, values.RetentionRuntimeDays, values.RetentionHealthHours, values.RetentionAlertsDays, time.Now().UTC())
	}
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		<-timer.C
		prune()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			prune()
		}
	}()
}
func pruneRetention(store retentionStore, detailDays, metric5mDays, runtimeDays, healthHours, alertsDays int, now time.Time) {
	groups := []struct {
		days  int
		kinds []string
	}{{detailDays, []string{"log_events", "log_samples", "metric_1m", "nginx_timing_1m", "nginx_slow_samples"}}, {metric5mDays, []string{"metric_5m"}}, {runtimeDays, []string{"server_metrics", "docker_statuses"}}, {alertsDays, []string{"alerts", "alert_events", "notification_deliveries"}}}
	for _, g := range groups {
		if g.days == 0 {
			continue
		}
		cutoff := now.Add(-time.Duration(g.days) * 24 * time.Hour)
		for _, kind := range g.kinds {
			n, e := store.PruneBefore(kind, cutoff)
			if e != nil {
				log.Printf("retention prune %s failed: %v", kind, e)
			} else {
				log.Printf("retention prune %s rows=%d", kind, n)
			}
		}
	}
	if healthHours > 0 {
		cutoff := now.Add(-time.Duration(healthHours) * time.Hour)
		n, err := store.PruneBefore("health_checks", cutoff)
		if err != nil {
			log.Printf("retention prune health_checks failed: %v", err)
		} else {
			log.Printf("retention prune health_checks rows=%d", n)
		}
	}
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

func startNotificationRunner(store mysqlstore.Store, provider *settings.Provider, interval time.Duration) {
	runner := dashboard.NewAlertNotificationRunner(store, store, store, store, store, interval).WithSettingsProvider(provider)
	go func() {
		if err := runner.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("notification runner stopped: %v", err)
		}
	}()
}
func startChannelSnapshotHistoryCleanup(store mysqlstore.Store) {
	const batchSize = 5000
	const batchPause = 200 * time.Millisecond
	go func() {
		var deleted int64
		for {
			rows, err := store.DeleteChannelSnapshotHistoryBatch(batchSize)
			if err != nil {
				log.Printf("channel snapshot history cleanup stopped after rows=%d: %v", deleted, err)
				return
			}
			deleted += rows
			if rows == 0 {
				log.Printf("channel snapshot history cleanup complete rows=%d", deleted)
				return
			}
			if deleted%100000 == 0 {
				log.Printf("channel snapshot history cleanup progress rows=%d", deleted)
			}
			time.Sleep(batchPause)
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
