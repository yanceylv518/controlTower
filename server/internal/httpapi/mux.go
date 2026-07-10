package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"controltower/server/internal/agentgateway"
	"controltower/server/internal/dashboard"
	"controltower/server/internal/ingest"
)

type Options struct {
	AgentToken     string
	DashboardToken string
	Store          Store
	WebDir         string
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
	mux.Handle("/api/dashboard/overview", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleOverview)))
	mux.Handle("/api/dashboard/log-samples", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleLogSamples)))
	mux.Handle("/api/dashboard/logs", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleLogs)))
	mux.Handle("/api/dashboard/metrics", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleMetrics)))
	mux.Handle("/api/dashboard/channel-snapshots", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleChannelSnapshots)))
	mux.Handle("/api/dashboard/alerts", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleAlerts)))
	mux.Handle("/api/dashboard/alerts/action", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleAlertAction)))
	mux.Handle("/api/dashboard/notification-channels", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleNotificationChannels)))
	mux.Handle("/api/dashboard/notification-deliveries", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleNotificationDeliveries)))
	mux.Handle("/api/dashboard/agents", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleAgents)))
	mux.Handle("/api/dashboard/server-metrics", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleServerMetrics)))
	mux.Handle("/api/dashboard/health-checks", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleHealthChecks)))
	mux.Handle("/api/dashboard/docker-statuses", dashboard.RequireBearerToken(options.DashboardToken, http.HandlerFunc(dashboardHandler.HandleDockerStatuses)))

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
