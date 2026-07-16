package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"controltower/server/internal/agentgateway"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/ingest"
	"controltower/server/internal/settings"
	"controltower/server/internal/tuning"
)

type Options struct {
	AgentToken              string
	DashboardToken          string
	Store                   Store
	WebDir                  string
	WebAppDir               string
	NextWebDir              string
	AuthManager             *ctauth.Manager
	AgentTokenPepper        string
	NotificationMaxAttempts int
	CommandExpiry           time.Duration
	TuningStore             tuning.Store
	SettingsProvider        *settings.Provider
}

type Store interface {
	ingest.Store
	dashboard.OverviewSource
	dashboard.LogStore
	dashboard.LogSampleStore
	dashboard.RuntimeStore
	dashboard.MetricSource
	dashboard.AlertStore
	dashboard.NotificationStore
	dashboard.ChannelSnapshotStore
	dashboard.NginxTimingStore
	dashboard.InstanceStore
	dashboard.CommandStore
	dashboard.NameSource
	agentgateway.TokenLookup
}

func NewMux(options Options) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)

	ingestService := ingest.NewServiceWithCommandExpiry(options.Store, options.CommandExpiry)
	agentHandler := agentgateway.NewHandlerWithTokens(options.AgentToken, ingestService, options.Store, options.AgentTokenPepper)
	mux.HandleFunc("/api/agent/heartbeat", agentHandler.HandleHeartbeat)
	mux.HandleFunc("/api/agent/report", agentHandler.HandleReport)

	dashboardHandler := dashboard.NewHandler(options.Store).WithNameSource(options.Store).WithLogStore(options.Store).WithLogSampleStore(options.Store).WithRuntimeStore(options.Store).WithMetricSource(options.Store).WithAlertStore(options.Store).WithNotificationStore(options.Store).WithChannelSnapshotStore(options.Store).WithNginxTimingStore(options.Store).WithNotificationMaxAttempts(options.NotificationMaxAttempts).WithSettingsProvider(options.SettingsProvider).WithInstanceStore(options.Store)
	tuningStore := options.TuningStore
	if tuningStore == nil {
		tuningStore, _ = any(options.Store).(tuning.Store)
	}
	if tuningStore != nil {
		dashboardHandler = dashboardHandler.WithTuningStore(tuningStore)
	}
	protect := func(h http.Handler) http.Handler {
		h = gzipJSON(h)
		if options.AuthManager != nil {
			return ctauth.RequireSessionOrToken(options.AuthManager, options.DashboardToken, h)
		}
		return dashboard.RequireBearerToken(options.DashboardToken, h)
	}
	a := ctauth.Handlers{M: options.AuthManager, Limiter: ctauth.NewIPLimiter()}
	mux.HandleFunc("/api/auth/login", a.Login)
	mux.HandleFunc("/api/auth/logout", a.Logout)
	mux.HandleFunc("/api/auth/me", a.Me)
	mux.HandleFunc("/api/auth/password", a.Password)
	mux.Handle("/api/dashboard/overview", protect(http.HandlerFunc(dashboardHandler.HandleOverview)))
	mux.Handle("/api/dashboard/log-samples", protect(http.HandlerFunc(dashboardHandler.HandleLogSamples)))
	mux.Handle("/api/dashboard/logs", protect(http.HandlerFunc(dashboardHandler.HandleLogs)))
	mux.Handle("/api/dashboard/metrics", protect(http.HandlerFunc(dashboardHandler.HandleMetrics)))
	mux.Handle("/api/dashboard/metric-history", protect(http.HandlerFunc(dashboardHandler.HandleMetricHistory)))
	mux.Handle("/api/dashboard/usage", protect(http.HandlerFunc(dashboardHandler.HandleUsage)))
	mux.Handle("/api/dashboard/channel-snapshots", protect(http.HandlerFunc(dashboardHandler.HandleChannelSnapshots)))
	mux.Handle("/api/dashboard/alerts", protect(http.HandlerFunc(dashboardHandler.HandleAlerts)))
	mux.Handle("/api/dashboard/alerts/action", protect(http.HandlerFunc(dashboardHandler.HandleAlertAction)))
	mux.Handle("GET /api/dashboard/alerts/{id}/events", protect(http.HandlerFunc(dashboardHandler.HandleAlertEvents)))
	mux.Handle("/api/dashboard/notification-channels", protect(http.HandlerFunc(dashboardHandler.HandleNotificationChannels)))
	mux.Handle("/api/dashboard/notification-deliveries", protect(http.HandlerFunc(dashboardHandler.HandleNotificationDeliveries)))
	mux.Handle("POST /api/dashboard/notification-deliveries/{id}/resend", protect(http.HandlerFunc(dashboardHandler.HandleNotificationResend)))
	mux.Handle("/api/dashboard/agents", protect(http.HandlerFunc(dashboardHandler.HandleAgents)))
	mux.Handle("/api/dashboard/server-metrics", protect(http.HandlerFunc(dashboardHandler.HandleServerMetrics)))
	mux.Handle("/api/dashboard/health-checks", protect(http.HandlerFunc(dashboardHandler.HandleHealthChecks)))
	mux.Handle("/api/dashboard/docker-statuses", protect(http.HandlerFunc(dashboardHandler.HandleDockerStatuses)))
	mux.Handle("GET /api/dashboard/nginx-timing", protect(http.HandlerFunc(dashboardHandler.HandleNginxTiming)))
	mux.Handle("GET /api/dashboard/nginx-timing/slow-samples", protect(http.HandlerFunc(dashboardHandler.HandleNginxSlowSamples)))
	if tuningStore != nil {
		mux.Handle("/api/dashboard/tuning/policy", protect(http.HandlerFunc(dashboardHandler.HandleTuningPolicy)))
		mux.Handle("GET /api/dashboard/tuning/recommendations", protect(http.HandlerFunc(dashboardHandler.HandleTuningRecommendations)))
		mux.Handle("GET /api/dashboard/tuning/report", protect(http.HandlerFunc(dashboardHandler.HandleTuningReport)))
	}
	instances := dashboard.InstanceHandler{Store: options.Store, Runtime: options.Store, Pepper: options.AgentTokenPepper, Settings: options.SettingsProvider}
	commands := (dashboard.CommandHandler{Store: options.Store, Instances: options.Store}).WithNameSource(options.Store)
	mux.Handle("POST /api/dashboard/channels/{channelID}/commands", protect(http.HandlerFunc(commands.Create)))
	mux.Handle("GET /api/dashboard/channel-commands", protect(http.HandlerFunc(commands.List)))
	mux.Handle("GET /api/dashboard/operation-audits", protect(http.HandlerFunc(commands.Audits)))
	if settingsStore, ok := any(options.Store).(dashboard.SettingsStore); ok && options.SettingsProvider != nil {
		mux.Handle("/api/dashboard/settings", protect(dashboard.SettingsHandler{Store: settingsStore, Provider: options.SettingsProvider}))
	}
	mux.Handle("GET /api/dashboard/instances", protect(http.HandlerFunc(instances.List)))
	mux.Handle("POST /api/dashboard/instances", protect(http.HandlerFunc(instances.Create)))
	mux.Handle("PUT /api/dashboard/instances/{id}", protect(http.HandlerFunc(instances.Update)))
	mux.Handle("POST /api/dashboard/instances/{id}/rotate-token", protect(http.HandlerFunc(instances.Rotate)))

	if options.WebAppDir == "" {
		options.WebAppDir = options.NextWebDir // Backward-compatible option name for one release cycle.
	}
	if options.WebAppDir == "" {
		if options.WebDir == "" {
			options.WebDir = "web"
		}
		options.WebAppDir = filepath.Join(options.WebDir, "dist", "desktop")
	}
	mux.HandleFunc("/next", redirectNext)
	mux.HandleFunc("/next/", redirectNext)
	mux.HandleFunc("/", handleWebApp(options.WebAppDir))
	return mux
}

func redirectNext(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(r.URL.Path, "/next")
	if target == "" {
		target = "/"
	}
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func handleWebApp(webDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
			return
		}
		indexPath := filepath.Join(webDir, "index.html")
		if info, err := os.Stat(indexPath); err != nil || info.IsDir() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"webapp_not_built","hint":"cd webapp && pnpm install && pnpm build"}`))
			return
		}
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}
		path := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if path == "." || path == "" {
			path = "index.html"
		}
		fullPath := filepath.Join(webDir, path)
		if relative, err := filepath.Rel(webDir, fullPath); err != nil || strings.HasPrefix(relative, "..") {
			fullPath = indexPath
		} else if info, err := os.Stat(fullPath); err != nil || info.IsDir() {
			fullPath = indexPath
		}
		http.ServeFile(w, r, fullPath)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}
