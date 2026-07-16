package dashboard

import (
	"net/http"
	"time"

	"controltower/server/internal/storage"
)

type RuntimeStore interface {
	QueryAgents(query storage.AgentQuery) ([]storage.Agent, error)
	QueryServerMetrics(query storage.ServerMetricQuery) ([]storage.ServerMetric, error)
	QueryHealthChecks(query storage.HealthCheckQuery) ([]storage.HealthCheck, error)
	QueryDockerStatuses(query storage.DockerStatusQuery) ([]storage.DockerStatus, error)
}

type AgentListResponse struct {
	Items []AgentSummary `json:"items"`
}

type AgentSummary struct {
	ID                string    `json:"id"`
	InstanceID        string    `json:"instance_id"`
	Version           string    `json:"version"`
	LastSeenAt        time.Time `json:"last_seen_at"`
	LastSequence      int64     `json:"last_sequence"`
	LastLogID         int64     `json:"last_log_id"`
	SourceLatestLogID int64     `json:"source_latest_log_id"`
	BacklogEstimate   int64     `json:"backlog_estimate"`
	Status            string    `json:"status"`
	ReportDelayMS     int64     `json:"report_delay_ms"`
	Online            bool      `json:"online"`
	SecondsSinceSeen  int64     `json:"seconds_since_seen"`
}

type ServerMetricListResponse struct {
	Items []ServerMetricSummary `json:"items"`
}

type ServerMetricSummary struct {
	InstanceID              string    `json:"instance_id"`
	CollectedAt             time.Time `json:"collected_at"`
	CPUPercent              float64   `json:"cpu_percent"`
	MemoryUsedPercent       float64   `json:"memory_used_percent"`
	DiskUsedPercent         float64   `json:"disk_used_percent"`
	NetworkRxBytesPerSecond int64     `json:"network_rx_bytes_per_second"`
	NetworkTxBytesPerSecond int64     `json:"network_tx_bytes_per_second"`
	Load1m                  float64   `json:"load_1m"`
}

type HealthCheckListResponse struct {
	Items []HealthCheckSummary `json:"items"`
}

type HealthCheckSummary struct {
	InstanceID     string    `json:"instance_id"`
	CheckedAt      time.Time `json:"checked_at"`
	Target         string    `json:"target"`
	Status         string    `json:"status"`
	HTTPStatusCode int       `json:"http_status_code"`
	LatencyMS      int64     `json:"latency_ms"`
	ErrorSummary   string    `json:"error_summary"`
}

type DockerStatusListResponse struct {
	Items []DockerStatusSummary `json:"items"`
}

type DockerStatusSummary struct {
	InstanceID    string    `json:"instance_id"`
	CollectedAt   time.Time `json:"collected_at"`
	ContainerName string    `json:"container_name"`
	Status        string    `json:"status"`
	Running       bool      `json:"running"`
}

func (h Handler) WithRuntimeStore(store RuntimeStore) Handler {
	h.runtimeStore = store
	return h
}

func (h Handler) HandleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.runtimeStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "runtime_store_not_configured")
		return
	}
	items, err := h.runtimeStore.QueryAgents(parseAgentQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	offlineSeconds := 120
	if h.settings != nil {
		if current, e := h.settings.Current(); e == nil {
			offlineSeconds = current.OfflineSeconds
		}
	}
	writeDashboardJSON(w, http.StatusOK, AgentListResponse{Items: summarizeAgentsWithThreshold(items, time.Now().UTC(), offlineSeconds)})
}

func (h Handler) HandleServerMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.runtimeStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "runtime_store_not_configured")
		return
	}
	items, err := h.runtimeStore.QueryServerMetrics(parseServerMetricQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, ServerMetricListResponse{Items: summarizeServerMetrics(items)})
}

func (h Handler) HandleHealthChecks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.runtimeStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "runtime_store_not_configured")
		return
	}
	items, err := h.runtimeStore.QueryHealthChecks(parseHealthCheckQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, HealthCheckListResponse{Items: summarizeHealthChecks(items)})
}

func (h Handler) HandleDockerStatuses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.runtimeStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "runtime_store_not_configured")
		return
	}
	items, err := h.runtimeStore.QueryDockerStatuses(parseDockerStatusQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, DockerStatusListResponse{Items: summarizeDockerStatuses(items)})
}

func parseAgentQuery(r *http.Request) storage.AgentQuery {
	query := r.URL.Query()
	return storage.AgentQuery{
		InstanceID: query.Get("instance_id"),
		Status:     query.Get("status"),
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func parseServerMetricQuery(r *http.Request) storage.ServerMetricQuery {
	query := r.URL.Query()
	return storage.ServerMetricQuery{
		InstanceID: query.Get("instance_id"),
		StartTime:  parseTime(query.Get("start_time")),
		EndTime:    parseTime(query.Get("end_time")),
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func parseHealthCheckQuery(r *http.Request) storage.HealthCheckQuery {
	query := r.URL.Query()
	return storage.HealthCheckQuery{
		InstanceID: query.Get("instance_id"),
		Target:     query.Get("target"),
		Status:     query.Get("status"),
		StartTime:  parseTime(query.Get("start_time")),
		EndTime:    parseTime(query.Get("end_time")),
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func parseDockerStatusQuery(r *http.Request) storage.DockerStatusQuery {
	query := r.URL.Query()
	var running *bool
	if value := query.Get("running"); value != "" {
		parsed, ok := parseBool(value)
		if ok {
			running = &parsed
		}
	}
	return storage.DockerStatusQuery{
		InstanceID:    query.Get("instance_id"),
		ContainerName: query.Get("container_name"),
		Running:       running,
		StartTime:     parseTime(query.Get("start_time")),
		EndTime:       parseTime(query.Get("end_time")),
		Limit:         parseInt(query.Get("limit")),
		Offset:        parseInt(query.Get("offset")),
	}
}

func summarizeAgents(items []storage.Agent, now time.Time) []AgentSummary {
	return summarizeAgentsWithThreshold(items, now, 120)
}
func summarizeAgentsWithThreshold(items []storage.Agent, now time.Time, offlineSeconds int) []AgentSummary {
	summaries := make([]AgentSummary, 0, len(items))
	for _, item := range items {
		secondsSinceSeen := int64(0)
		if !item.LastSeenAt.IsZero() {
			secondsSinceSeen = int64(now.Sub(item.LastSeenAt).Seconds())
			if secondsSinceSeen < 0 {
				secondsSinceSeen = 0
			}
		}
		summaries = append(summaries, AgentSummary{ID: item.ID, InstanceID: item.InstanceID, Version: item.Version, LastSeenAt: item.LastSeenAt, LastSequence: item.LastSequence, LastLogID: item.LastLogID, SourceLatestLogID: item.SourceLatestLogID, BacklogEstimate: item.BacklogEstimate, Status: item.Status, ReportDelayMS: item.ReportDelayMS, Online: secondsSinceSeen <= int64(offlineSeconds), SecondsSinceSeen: secondsSinceSeen})
	}
	return summaries
}

func summarizeServerMetrics(items []storage.ServerMetric) []ServerMetricSummary {
	summaries := make([]ServerMetricSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, ServerMetricSummary{InstanceID: item.InstanceID, CollectedAt: item.CollectedAt, CPUPercent: item.CPUPercent, MemoryUsedPercent: item.MemoryUsedPercent, DiskUsedPercent: item.DiskUsedPercent, NetworkRxBytesPerSecond: item.NetworkRxBytesPerSecond, NetworkTxBytesPerSecond: item.NetworkTxBytesPerSecond, Load1m: item.Load1m})
	}
	return summaries
}

func summarizeHealthChecks(items []storage.HealthCheck) []HealthCheckSummary {
	summaries := make([]HealthCheckSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, HealthCheckSummary{InstanceID: item.InstanceID, CheckedAt: item.CheckedAt, Target: item.Target, Status: item.Status, HTTPStatusCode: item.HTTPStatusCode, LatencyMS: item.LatencyMS, ErrorSummary: item.ErrorSummary})
	}
	return summaries
}

func summarizeDockerStatuses(items []storage.DockerStatus) []DockerStatusSummary {
	summaries := make([]DockerStatusSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, DockerStatusSummary{InstanceID: item.InstanceID, CollectedAt: item.CollectedAt, ContainerName: item.ContainerName, Status: item.Status, Running: item.Running})
	}
	return summaries
}
