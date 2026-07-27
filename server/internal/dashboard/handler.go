package dashboard

import (
	"encoding/json"
	"net/http"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/settings"
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
	settings                *settings.Provider
	instanceStore           InstanceStore
}

func (h Handler) WithNotificationMaxAttempts(v int) Handler         { h.notificationMaxAttempts = v; return h }
func (h Handler) WithSettingsProvider(v *settings.Provider) Handler { h.settings = v; return h }
func (h Handler) WithInstanceStore(v InstanceStore) Handler         { h.instanceStore = v; return h }

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
	instanceIDs, err := h.instanceIDsForRequest(instanceID, r.URL.Query().Get("site"))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	allowed := instanceIDSet(instanceIDs)
	if allowed != nil {
		filtered := metrics[:0]
		for _, m := range metrics {
			if allowed[m.InstanceID] {
				filtered = append(filtered, m)
			}
		}
		metrics = filtered
	}
	if h.runtimeStore == nil {
		writeDashboardJSON(w, http.StatusOK, BuildOverview(metrics))
		return
	}
	runtimeInstanceID := instanceID
	serverMetrics, err := h.runtimeStore.QueryServerMetrics(storage.ServerMetricQuery{InstanceID: runtimeInstanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	healthChecks, err := h.runtimeStore.QueryHealthChecks(storage.HealthCheckQuery{InstanceID: runtimeInstanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	dockerStatuses, err := h.runtimeStore.QueryDockerStatuses(storage.DockerStatusQuery{InstanceID: runtimeInstanceID, Limit: storage.MaxRuntimeQueryLimit})
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if allowed != nil && runtimeInstanceID == "" {
		serverMetrics = filterServerMetricsByInstance(serverMetrics, allowed)
		healthChecks = filterHealthChecksByInstance(healthChecks, allowed)
		dockerStatuses = filterDockerStatusesByInstance(dockerStatuses, allowed)
	}
	writeDashboardJSON(w, http.StatusOK, BuildOverviewWithRuntime(metrics, serverMetrics, healthChecks, dockerStatuses))
}

func filterServerMetricsByInstance(items []storage.ServerMetric, allowed map[string]bool) []storage.ServerMetric {
	out := items[:0]
	for _, item := range items {
		if allowed[item.InstanceID] {
			out = append(out, item)
		}
	}
	return out
}

func filterHealthChecksByInstance(items []storage.HealthCheck, allowed map[string]bool) []storage.HealthCheck {
	out := items[:0]
	for _, item := range items {
		if allowed[item.InstanceID] {
			out = append(out, item)
		}
	}
	return out
}

func filterDockerStatusesByInstance(items []storage.DockerStatus, allowed map[string]bool) []storage.DockerStatus {
	out := items[:0]
	for _, item := range items {
		if allowed[item.InstanceID] {
			out = append(out, item)
		}
	}
	return out
}

type latest1mOverviewSource interface {
	Latest1mMetrics(dimensionType string) ([]aggregator.Metric, error)
}

func latestOverviewMetrics(source OverviewSource) ([]aggregator.Metric, error) {
	if latest, ok := source.(latest1mOverviewSource); ok {
		return latest.Latest1mMetrics("instance")
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
