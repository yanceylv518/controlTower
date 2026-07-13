package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"controltower/server/internal/agentgateway"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/ingest"
)

type Options struct {
	AgentToken     string
	DashboardToken string
	Store          Store
	WebDir         string
	AuthManager    *ctauth.Manager
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
}

func NewMux(options Options) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)

	ingestService := ingest.NewService(options.Store)
	agentHandler := agentgateway.NewHandler(options.AgentToken, ingestService)
	mux.HandleFunc("/api/agent/heartbeat", agentHandler.HandleHeartbeat)
	mux.HandleFunc("/api/agent/report", agentHandler.HandleReport)

	dashboardHandler := dashboard.NewHandler(options.Store).WithLogStore(options.Store).WithLogSampleStore(options.Store).WithRuntimeStore(options.Store).WithMetricSource(options.Store).WithAlertStore(options.Store).WithNotificationStore(options.Store).WithChannelSnapshotStore(options.Store)
	protect := func(h http.Handler) http.Handler {
		if options.AuthManager != nil {
			return ctauth.RequireSessionOrToken(options.AuthManager, options.DashboardToken, h)
		}
		return dashboard.RequireBearerToken(options.DashboardToken, h)
	}
	a := ctauth.Handlers{M: options.AuthManager}
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
	mux.Handle("/api/dashboard/notification-channels", protect(http.HandlerFunc(dashboardHandler.HandleNotificationChannels)))
	mux.Handle("/api/dashboard/notification-deliveries", protect(http.HandlerFunc(dashboardHandler.HandleNotificationDeliveries)))
	mux.Handle("/api/dashboard/agents", protect(http.HandlerFunc(dashboardHandler.HandleAgents)))
	mux.Handle("/api/dashboard/server-metrics", protect(http.HandlerFunc(dashboardHandler.HandleServerMetrics)))
	mux.Handle("/api/dashboard/health-checks", protect(http.HandlerFunc(dashboardHandler.HandleHealthChecks)))
	mux.Handle("/api/dashboard/docker-statuses", protect(http.HandlerFunc(dashboardHandler.HandleDockerStatuses)))

	if options.WebDir == "" {
		options.WebDir = "web"
	}
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(options.WebDir, "assets")))))
	mux.HandleFunc("/", handleWeb(options.WebDir))
	return mux
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

func handleWeb(webDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			http.NotFound(w, r)
			return
		}
		path := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if path == "." || path == "" {
			path = "index.html"
		}
		fullPath := filepath.Join(webDir, path)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			fullPath = filepath.Join(webDir, "index.html")
		}
		http.ServeFile(w, r, fullPath)
	}
}
