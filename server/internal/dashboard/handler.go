package dashboard

import (
	"encoding/json"
	"net/http"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type OverviewSource interface {
	Recent1mMetrics() ([]aggregator.Metric, error)
}

type Handler struct {
	source                  OverviewSource
	logStore                LogStore
	logSampleStore          LogSampleStore
	runtimeStore            RuntimeStore
	metricSource            MetricSource
	alertStore              AlertStore
	notificationStore       NotificationStore
	channelSnapshotStore    ChannelSnapshotStore
	nginxTimingStore        NginxTimingStore
	tuningStore             TuningStore
	notificationMaxAttempts int
	names                   *nameResolver
}

func (h Handler) WithNotificationMaxAttempts(v int) Handler { h.notificationMaxAttempts = v; return h }

func NewHandler(source OverviewSource) Handler {
	return Handler{source: source}
}

func (h Handler) WithNameSource(source NameSource) Handler {
	h.names = newNameResolver(source, time.Minute)
	return h
}

func (h Handler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	metrics, err := latestOverviewMetrics(h.source)
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	instanceID := r.URL.Query().Get("instance_id")
	if instanceID != "" {
		filtered := metrics[:0]
		for _, m := range metrics {
			if m.InstanceID == instanceID {
				filtered = append(filtered, m)
			}
		}
		metrics = filtered
	}
	if h.runtimeStore == nil {
		writeDashboardJSON(w, http.StatusOK, BuildOverview(metrics))
		return
	}
	serverMetrics, err := h.runtimeStore.QueryServerMetrics(storage.ServerMetricQuery{InstanceID: instanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	healthChecks, err := h.runtimeStore.QueryHealthChecks(storage.HealthCheckQuery{InstanceID: instanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	dockerStatuses, err := h.runtimeStore.QueryDockerStatuses(storage.DockerStatusQuery{InstanceID: instanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, BuildOverviewWithRuntime(metrics, serverMetrics, healthChecks, dockerStatuses))
}

type latest1mOverviewSource interface {
	Latest1mMetrics() ([]aggregator.Metric, error)
}

func latestOverviewMetrics(source OverviewSource) ([]aggregator.Metric, error) {
	if latest, ok := source.(latest1mOverviewSource); ok {
		return latest.Latest1mMetrics()
	}
	return source.Recent1mMetrics()
}

func writeDashboardJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeDashboardError(w http.ResponseWriter, status int, code string) {
	writeDashboardJSON(w, status, map[string]string{"error": code})
}
