package dashboard

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"controltower/server/internal/aggregator"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

type AlertStore interface {
	UpsertCurrentAlerts(alerts []storage.Alert, now time.Time) error
	ResolveMissingAlerts(currentIDs []string, now time.Time) error
	QueryAlerts(query storage.AlertQuery) ([]storage.Alert, error)
	UpdateAlertAction(id string, status string, silenceUntil *time.Time, now time.Time) error
	ExpireSilencedAlerts(now time.Time) error
	InsertAlertEvents([]storage.AlertEvent) error
	QueryAlertEvents(string, int) ([]storage.AlertEvent, error)
}

type AlertListResponse struct {
	Items []AlertItem `json:"items"`
}

type AlertItem struct {
	ID            string     `json:"id"`
	InstanceID    string     `json:"instance_id"`
	InstanceName  string     `json:"instance_name"`
	DisplayKey    string     `json:"display_key"`
	DimensionType string     `json:"dimension_type"`
	DimensionKey  string     `json:"dimension_key"`
	RuleKey       string     `json:"rule_key"`
	Severity      string     `json:"severity"`
	Status        string     `json:"status"`
	Title         string     `json:"title"`
	Summary       string     `json:"summary"`
	SeenAt        time.Time  `json:"seen_at"`
	FirstSeenAt   time.Time  `json:"first_seen_at"`
	LastSeenAt    time.Time  `json:"last_seen_at"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	SilenceUntil  *time.Time `json:"silence_until,omitempty"`
}

type AlertActionRequest struct {
	ID             string `json:"id"`
	Action         string `json:"action"`
	SilenceMinutes int    `json:"silence_minutes"`
	Note           string `json:"note"`
}

type AlertActionResponse struct {
	OK bool `json:"ok"`
}

func (h Handler) WithAlertStore(store AlertStore) Handler {
	h.alertStore = store
	return h
}

func (h Handler) HandleAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	computed, err := h.currentAlerts()
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if h.alertStore == nil {
		writeDashboardJSON(w, http.StatusOK, AlertListResponse{Items: computed})
		return
	}
	now := time.Now().UTC()
	if err := h.alertStore.ExpireSilencedAlerts(now); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if err := h.alertStore.UpsertCurrentAlerts(alertItemsToStorage(computed), now); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	if err := h.alertStore.ResolveMissingAlerts(alertIDs(computed), now); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	alerts, err := h.alertStore.QueryAlerts(parseAlertQuery(r))
	if err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	items := storageAlertsToItems(alerts)
	computedByID := make(map[string]AlertItem, len(computed))
	for _, item := range computed {
		computedByID[item.ID] = item
	}
	for i := range items {
		items[i].InstanceName = h.instanceName(items[i].InstanceID)
		if current, ok := computedByID[items[i].ID]; ok {
			items[i].DisplayKey = current.DisplayKey
			items[i].DimensionType = current.DimensionType
			items[i].DimensionKey = current.DimensionKey
		}
		if items[i].DisplayKey == "" {
			items[i].DisplayKey = items[i].InstanceName
		}
	}
	writeDashboardJSON(w, http.StatusOK, AlertListResponse{Items: items})
}

func (h Handler) HandleAlertAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeDashboardError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	if h.alertStore == nil {
		writeDashboardError(w, http.StatusInternalServerError, "alert_store_not_configured")
		return
	}
	var request AlertActionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeDashboardError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	status, silenceUntil, ok := alertActionStatus(request, time.Now().UTC())
	if request.ID == "" || !ok || len(request.Note) > 500 {
		writeDashboardError(w, http.StatusBadRequest, "invalid_alert_action")
		return
	}
	if err := h.alertStore.UpdateAlertAction(request.ID, status, silenceUntil, time.Now().UTC()); err != nil {
		writeDashboardError(w, http.StatusInternalServerError, "query_failed")
		return
	}
	actor := ctauth.Actor(r)
	if actor == "" {
		actor = "unknown"
	}
	eventType := status
	if request.Action == "silence" {
		eventType = "silenced"
	}
	_ = h.alertStore.InsertAlertEvents([]storage.AlertEvent{{AlertID: request.ID, EventType: eventType, Actor: actor, Note: request.Note, CreatedAt: time.Now().UTC()}})
	writeDashboardJSON(w, http.StatusOK, AlertActionResponse{OK: true})
}
func (h Handler) HandleAlertEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeDashboardError(w, 405, "method_not_allowed")
		return
	}
	if h.alertStore == nil {
		writeDashboardError(w, 500, "alert_store_not_configured")
		return
	}
	limit := parseInt(r.URL.Query().Get("limit"))
	events, e := h.alertStore.QueryAlertEvents(r.PathValue("id"), limit)
	if e != nil {
		writeDashboardError(w, 500, "query_failed")
		return
	}
	items := make([]map[string]any, 0, len(events))
	for _, v := range events {
		items = append(items, map[string]any{"event_type": v.EventType, "actor": v.Actor, "note": v.Note, "created_at": v.CreatedAt})
	}
	writeDashboardJSON(w, 200, map[string]any{"items": items})
}

func (h Handler) currentAlerts() ([]AlertItem, error) {
	metrics, err := latestOverviewMetrics(h.source)
	if h.metricSource != nil {
		metrics, err = h.metricSource.Latest1mMetrics("")
	}
	if err != nil {
		return nil, err
	}
	var serverMetrics []storage.ServerMetric
	var healthChecks []storage.HealthCheck
	var dockerStatuses []storage.DockerStatus
	var agents []storage.Agent
	if h.runtimeStore != nil {
		agents, err = h.runtimeStore.QueryAgents(storage.AgentQuery{Limit: storage.MaxRuntimeQueryLimit})
		if err != nil {
			return nil, err
		}
		serverMetrics, err = h.runtimeStore.QueryServerMetrics(storage.ServerMetricQuery{Limit: storage.MaxRuntimeQueryLimit})
		if err != nil {
			return nil, err
		}
		healthChecks, err = h.runtimeStore.QueryHealthChecks(storage.HealthCheckQuery{Limit: storage.MaxRuntimeQueryLimit})
		if err != nil {
			return nil, err
		}
		dockerStatuses, err = h.runtimeStore.QueryDockerStatuses(storage.DockerStatusQuery{Limit: storage.MaxRuntimeQueryLimit})
		if err != nil {
			return nil, err
		}
	}
	values := defaultAlertSettings()
	if h.settings != nil {
		if current, e := h.settings.Current(); e == nil {
			values = current
		}
	}
	items := BuildCurrentAlertsWithSettings(metrics, serverMetrics, healthChecks, dockerStatuses, values)
	if h.logStore != nil {
		events, err := h.logStore.QueryLogEvents(storage.LogQuery{Limit: storage.MaxLogQueryLimit})
		if err != nil {
			return nil, err
		}
		items = appendRecentErrorAlerts(items, events)
	}
	items = appendAgentBacklogAlertsWithOffline(items, agents, time.Now().UTC(), values.OfflineSeconds)
	if h.instanceStore != nil {
		instances, e := h.instanceStore.ListInstances()
		if e != nil {
			return nil, e
		}
		items = appendInstanceOfflineAlerts(items, instances, agents, time.Now().UTC(), values.OfflineSeconds)
	}
	for i := range items {
		items[i].InstanceName = h.instanceName(items[i].InstanceID)
		if items[i].DimensionKey != "" {
			items[i].DisplayKey = h.displayDimensionKey(items[i].DimensionType, items[i].DimensionKey)
		} else {
			items[i].DisplayKey = items[i].InstanceName
		}
		items[i].Title = items[i].DisplayKey + " " + items[i].Title
	}
	return items, nil
}

func appendInstanceOfflineAlerts(items []AlertItem, instances []storage.Instance, agents []storage.Agent, now time.Time, offlineSeconds int) []AlertItem {
	latest := map[string]storage.Agent{}
	for _, agent := range agents {
		current, ok := latest[agent.InstanceID]
		if !ok || agent.LastSeenAt.After(current.LastSeenAt) {
			latest[agent.InstanceID] = agent
		}
	}
	for _, instance := range instances {
		agent, seen := latest[instance.ID]
		if !instance.Enabled || !seen || agent.LastSeenAt.IsZero() {
			continue
		}
		age := now.Sub(agent.LastSeenAt)
		if age <= time.Duration(offlineSeconds)*time.Second || age > 7*24*time.Hour {
			continue
		}
		minutes := int(age.Minutes())
		if minutes < 1 {
			minutes = 1
		}
		items = append(items, AlertItem{ID: alertID(instance.ID, "instance_offline", instance.ID), InstanceID: instance.ID, RuleKey: "instance_offline", Severity: "critical", Status: "firing", Title: "实例离线", Summary: fmt.Sprintf("最后心跳于 %d 分钟前", minutes), SeenAt: agent.LastSeenAt, FirstSeenAt: agent.LastSeenAt, LastSeenAt: agent.LastSeenAt})
	}
	return items
}

func appendAgentBacklogAlerts(items []AlertItem, agents []storage.Agent, now time.Time) []AlertItem {
	return appendAgentBacklogAlertsWithOffline(items, agents, now, 120)
}
func appendAgentBacklogAlertsWithOffline(items []AlertItem, agents []storage.Agent, now time.Time, offlineSeconds int) []AlertItem {
	for _, agent := range agents {
		if agent.BacklogEstimate < 3000 || agent.LastSeenAt.IsZero() || now.Sub(agent.LastSeenAt) > time.Duration(offlineSeconds)*time.Second {
			continue
		}
		severity := "warning"
		if agent.BacklogEstimate >= 10000 {
			severity = "critical"
		}
		items = append(items, AlertItem{
			ID: alertID(agent.InstanceID, "agent_backlog", agent.ID), InstanceID: agent.InstanceID,
			RuleKey: "agent_backlog", Severity: severity, Status: "firing", Title: "Agent \u65e5\u5fd7\u79ef\u538b",
			Summary: fmt.Sprintf("Agent %s \u4f30\u7b97\u79ef\u538b %d \u6761\uff0c\u6e90\u5e93\u6700\u65b0 ID %d\uff0c\u5df2\u5904\u7406 ID %d", agent.ID, agent.BacklogEstimate, agent.SourceLatestLogID, agent.LastLogID),
			SeenAt:  agent.LastSeenAt, FirstSeenAt: agent.LastSeenAt, LastSeenAt: agent.LastSeenAt,
		})
	}
	sortAlerts(items)
	return items
}

func BuildCurrentAlerts(metrics []aggregator.Metric, serverMetrics []storage.ServerMetric, healthChecks []storage.HealthCheck, dockerStatuses []storage.DockerStatus) []AlertItem {
	return BuildCurrentAlertsWithSettings(metrics, serverMetrics, healthChecks, dockerStatuses, defaultAlertSettings())
}
func defaultAlertSettings() settings.Values {
	items := map[string]settings.Item{}
	for _, k := range settings.Keys() {
		items[k] = settings.Item{Value: map[string]string{settings.RetentionDetail: "30", settings.RetentionMetric5m: "90", settings.RetentionRuntime: "7", settings.OfflineSeconds: "120", settings.CPUWarn: "80", settings.CPUCrit: "90", settings.MemoryWarn: "80", settings.MemoryCrit: "90", settings.DiskWarn: "85", settings.DiskCrit: "95", settings.ErrorRateWarn: "20", settings.ErrorRateCrit: "50", settings.P95Warn: "5", settings.P95Crit: "10", settings.NotificationsEnabled: "true"}[k]}
	}
	v, _ := settings.Parse(items)
	return v
}
func BuildCurrentAlertsWithSettings(metrics []aggregator.Metric, serverMetrics []storage.ServerMetric, healthChecks []storage.HealthCheck, dockerStatuses []storage.DockerStatus, values settings.Values) []AlertItem {
	items := make([]AlertItem, 0)
	for _, metric := range latestDimensionMetrics(metrics) {
		items = appendMetricAlertsWithSettings(items, metric, values)
	}
	for _, metric := range latestServerMetricsByInstance(serverMetrics) {
		items = appendServerMetricAlertsWithSettings(items, metric, values)
	}
	for _, item := range latestHealthChecksByTarget(healthChecks) {
		if item.Status != "up" {
			items = append(items, AlertItem{ID: alertID(item.InstanceID, "health_down", item.Target), InstanceID: item.InstanceID, RuleKey: "health_down", Severity: "critical", Status: "firing", Title: "\u5065\u5eb7\u68c0\u67e5\u5931\u8d25", Summary: fmt.Sprintf("%s \u8fd4\u56de %s\uff0cHTTP %d\uff0c\u5ef6\u8fdf %dms", item.Target, item.Status, item.HTTPStatusCode, item.LatencyMS), SeenAt: item.CheckedAt, FirstSeenAt: item.CheckedAt, LastSeenAt: item.CheckedAt})
		}
	}
	for _, item := range latestDockerStatusesByContainer(dockerStatuses) {
		if !item.Running {
			items = append(items, AlertItem{ID: alertID(item.InstanceID, "docker_stopped", item.ContainerName), InstanceID: item.InstanceID, RuleKey: "docker_stopped", Severity: "warning", Status: "firing", Title: "\u5bb9\u5668\u672a\u8fd0\u884c", Summary: fmt.Sprintf("%s \u5f53\u524d\u72b6\u6001\uff1a%s", item.ContainerName, item.Status), SeenAt: item.CollectedAt, FirstSeenAt: item.CollectedAt, LastSeenAt: item.CollectedAt})
		}
	}
	sortAlerts(items)
	return items
}

func appendMetricAlerts(items []AlertItem, metric aggregator.Metric) []AlertItem {
	return appendMetricAlertsWithSettings(items, metric, defaultAlertSettings())
}
func appendMetricAlertsWithSettings(items []AlertItem, metric aggregator.Metric, values settings.Values) []AlertItem {
	if metric.ErrorRate != nil && metric.RequestCount >= 5 && *metric.ErrorRate >= values.ErrorRateWarn/100 {
		severity := "warning"
		if *metric.ErrorRate >= values.ErrorRateCrit/100 {
			severity = "critical"
		}
		items = append(items, AlertItem{ID: alertID(metric.InstanceID, "high_error_rate", metric.DimensionKey), InstanceID: metric.InstanceID, DimensionType: metric.DimensionType, DimensionKey: metric.DimensionKey, RuleKey: "high_error_rate", Severity: severity, Status: "firing", Title: "\u9519\u8bef\u7387\u5347\u9ad8", Summary: fmt.Sprintf("\u6700\u8fd1 1 \u5206\u949f\u9519\u8bef\u7387 %.1f%%\uff0c%d/%d \u8bf7\u6c42\u5931\u8d25", *metric.ErrorRate*100, metric.ErrorCount, metric.RequestCount), SeenAt: metric.BucketTime, FirstSeenAt: metric.BucketTime, LastSeenAt: metric.BucketTime})
	}
	if metric.P95UseTime != nil && *metric.P95UseTime >= values.P95Warn {
		severity := "warning"
		if *metric.P95UseTime >= values.P95Crit {
			severity = "critical"
		}
		summary := fmt.Sprintf("\u6700\u8fd1 1 \u5206\u949f P95 %.2fs\uff0c\u5171 %d \u6761\u8bf7\u6c42", *metric.P95UseTime, metric.RequestCount)
		if *metric.P95UseTime >= 60 {
			summary = fmt.Sprintf("\u6700\u8fd1 1 \u5206\u949f P95 \u226560s\uff08\u8d85\u51fa\u76f4\u65b9\u56fe\u91cf\u7a0b\uff09\uff0c\u5171 %d \u6761\u8bf7\u6c42", metric.RequestCount)
		}
		items = append(items, AlertItem{ID: alertID(metric.InstanceID, "high_p95_latency", metric.DimensionKey), InstanceID: metric.InstanceID, DimensionType: metric.DimensionType, DimensionKey: metric.DimensionKey, RuleKey: "high_p95_latency", Severity: severity, Status: "firing", Title: "P95 \u8017\u65f6\u504f\u9ad8", Summary: summary, SeenAt: metric.BucketTime, FirstSeenAt: metric.BucketTime, LastSeenAt: metric.BucketTime})
	}
	return items
}

func appendServerMetricAlerts(items []AlertItem, metric storage.ServerMetric) []AlertItem {
	return appendServerMetricAlertsWithSettings(items, metric, defaultAlertSettings())
}
func appendServerMetricAlertsWithSettings(items []AlertItem, metric storage.ServerMetric, values settings.Values) []AlertItem {
	thresholds := []struct {
		key      string
		title    string
		value    float64
		warning  float64
		critical float64
	}{
		{key: "high_cpu", title: "CPU \u4f7f\u7528\u7387\u504f\u9ad8", value: metric.CPUPercent, warning: values.CPUWarn, critical: values.CPUCrit},
		{key: "high_memory", title: "\u5185\u5b58\u4f7f\u7528\u7387\u504f\u9ad8", value: metric.MemoryUsedPercent, warning: values.MemoryWarn, critical: values.MemoryCrit},
		{key: "high_disk", title: "\u78c1\u76d8\u4f7f\u7528\u7387\u504f\u9ad8", value: metric.DiskUsedPercent, warning: values.DiskWarn, critical: values.DiskCrit},
	}
	for _, threshold := range thresholds {
		if threshold.value < threshold.warning {
			continue
		}
		severity := "warning"
		if threshold.value >= threshold.critical {
			severity = "critical"
		}
		items = append(items, AlertItem{ID: alertID(metric.InstanceID, threshold.key, "server"), InstanceID: metric.InstanceID, RuleKey: threshold.key, Severity: severity, Status: "firing", Title: threshold.title, Summary: fmt.Sprintf("\u5f53\u524d %.1f%%", threshold.value), SeenAt: metric.CollectedAt, FirstSeenAt: metric.CollectedAt, LastSeenAt: metric.CollectedAt})
	}
	return items
}
func latestDimensionMetrics(metrics []aggregator.Metric) []aggregator.Metric {
	latest := make(map[string]aggregator.Metric)
	for _, metric := range metrics {
		key := metric.InstanceID + "\x00" + metric.DimensionType + "\x00" + metric.DimensionKey
		current, ok := latest[key]
		if !ok || metric.BucketTime.After(current.BucketTime) {
			latest[key] = metric
		}
	}
	result := make([]aggregator.Metric, 0, len(latest))
	for _, metric := range latest {
		result = append(result, metric)
	}
	return result
}

func latestHealthChecksByTarget(items []storage.HealthCheck) []storage.HealthCheck {
	latest := make(map[string]storage.HealthCheck)
	for _, item := range items {
		key := item.InstanceID + "\x00" + item.Target
		current, ok := latest[key]
		if !ok || item.CheckedAt.After(current.CheckedAt) {
			latest[key] = item
		}
	}
	result := make([]storage.HealthCheck, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	return result
}

func latestDockerStatusesByContainer(items []storage.DockerStatus) []storage.DockerStatus {
	latest := make(map[string]storage.DockerStatus)
	for _, item := range items {
		key := item.InstanceID + "\x00" + item.ContainerName
		current, ok := latest[key]
		if !ok || item.CollectedAt.After(current.CollectedAt) {
			latest[key] = item
		}
	}
	result := make([]storage.DockerStatus, 0, len(latest))
	for _, item := range latest {
		result = append(result, item)
	}
	return result
}

func alertActionStatus(request AlertActionRequest, now time.Time) (string, *time.Time, bool) {
	switch request.Action {
	case "acknowledge":
		return "acknowledged", nil, true
	case "silence":
		minutes := request.SilenceMinutes
		if minutes <= 0 {
			minutes = 60
		}
		until := now.Add(time.Duration(minutes) * time.Minute)
		return "silenced", &until, true
	case "resolve":
		return "resolved", nil, true
	default:
		return "", nil, false
	}
}

func parseAlertQuery(r *http.Request) storage.AlertQuery {
	query := r.URL.Query()
	return storage.AlertQuery{
		InstanceID: query.Get("instance_id"),
		Status:     query.Get("status"),
		Severity:   query.Get("severity"),
		ActiveOnly: query.Get("active_only") == "true",
		Limit:      parseInt(query.Get("limit")),
		Offset:     parseInt(query.Get("offset")),
	}
}

func alertItemsToStorage(items []AlertItem) []storage.Alert {
	alerts := make([]storage.Alert, 0, len(items))
	for _, item := range items {
		alerts = append(alerts, storage.Alert{ID: item.ID, InstanceID: item.InstanceID, RuleKey: item.RuleKey, Severity: item.Severity, Status: item.Status, Title: item.Title, Summary: item.Summary, FirstSeenAt: item.FirstSeenAt, LastSeenAt: item.LastSeenAt, ResolvedAt: item.ResolvedAt, SilenceUntil: item.SilenceUntil})
	}
	return alerts
}

func storageAlertsToItems(alerts []storage.Alert) []AlertItem {
	items := make([]AlertItem, 0, len(alerts))
	for _, alert := range alerts {
		items = append(items, AlertItem{ID: alert.ID, InstanceID: alert.InstanceID, RuleKey: alert.RuleKey, Severity: alert.Severity, Status: alert.Status, Title: alert.Title, Summary: alert.Summary, SeenAt: alert.LastSeenAt, FirstSeenAt: alert.FirstSeenAt, LastSeenAt: alert.LastSeenAt, ResolvedAt: alert.ResolvedAt, SilenceUntil: alert.SilenceUntil})
	}
	sortAlerts(items)
	return items
}

func alertIDs(items []AlertItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func alertID(instanceID string, ruleKey string, target string) string {
	key := instanceID + ":" + ruleKey + ":" + target
	sum := sha1.Sum([]byte(key))
	return hex.EncodeToString(sum[:])
}

func sortAlerts(items []AlertItem) {
	weight := map[string]int{"critical": 0, "warning": 1, "info": 2}
	sort.Slice(items, func(i, j int) bool {
		if weight[items[i].Severity] != weight[items[j].Severity] {
			return weight[items[i].Severity] < weight[items[j].Severity]
		}
		return items[i].SeenAt.After(items[j].SeenAt)
	})
}
