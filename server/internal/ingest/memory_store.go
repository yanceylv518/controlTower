package ingest

import (
	"sync"
	"time"

	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type MemoryStore struct {
	mu                     sync.Mutex
	agents                 map[string]storage.Agent
	logEvents              map[string]storage.LogEvent
	logSamples             map[string]storage.LogSample
	serverMetrics          []storage.ServerMetric
	dockerStatuses         []storage.DockerStatus
	healthChecks           []storage.HealthCheck
	channelSnapshots       []storage.ChannelSnapshot
	offsets                map[string]int64
	alerts                 map[string]storage.Alert
	notificationChannels   map[string]storage.NotificationChannel
	notificationDeliveries map[string]storage.NotificationDelivery
	metrics1m              map[string]aggregator.Metric
	metrics5m              map[string]aggregator.Metric
	metricBatches          map[string]struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		agents:                 make(map[string]storage.Agent),
		logEvents:              make(map[string]storage.LogEvent),
		logSamples:             make(map[string]storage.LogSample),
		offsets:                make(map[string]int64),
		alerts:                 make(map[string]storage.Alert),
		notificationChannels:   make(map[string]storage.NotificationChannel),
		notificationDeliveries: make(map[string]storage.NotificationDelivery),
		metrics1m:              make(map[string]aggregator.Metric),
		metrics5m:              make(map[string]aggregator.Metric),
		metricBatches:          make(map[string]struct{}),
	}
}

func (s *MemoryStore) Upsert1m(metrics []aggregator.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, metric := range metrics {
		key := metric.InstanceID + ":" + metric.BucketTime.Format(time.RFC3339Nano) + ":" + metric.DimensionType + ":" + metric.DimensionKey
		s.metrics1m[key] = metric
	}
	return nil
}

func (s *MemoryStore) ApplyMetricBatch(instanceID string, batchID string, metrics []aggregator.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	batchKey := instanceID + ":" + batchID
	if _, exists := s.metricBatches[batchKey]; exists {
		return nil
	}
	for _, metric := range metrics {
		key := metric.InstanceID + ":" + metric.BucketTime.Format(time.RFC3339Nano) + ":" + metric.DimensionType + ":" + metric.DimensionKey
		s.metrics1m[key] = aggregator.MergeMetric(s.metrics1m[key], metric)
	}
	for _, metric := range aggregator.Rollup5m(metrics) {
		key := metric.InstanceID + ":" + metric.BucketTime.Format(time.RFC3339Nano) + ":" + metric.DimensionType + ":" + metric.DimensionKey
		s.metrics5m[key] = aggregator.MergeMetric(s.metrics5m[key], metric)
	}
	s.metricBatches[batchKey] = struct{}{}
	return nil
}

func (s *MemoryStore) Recent1mMetrics() ([]aggregator.Metric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]aggregator.Metric, 0, len(s.metrics1m))
	for _, metric := range s.metrics1m {
		items = append(items, metric)
	}
	return items, nil
}

func (s *MemoryStore) Latest1mMetrics() ([]aggregator.Metric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest time.Time
	for _, metric := range s.metrics1m {
		if metric.BucketTime.After(latest) {
			latest = metric.BucketTime
		}
	}
	items := make([]aggregator.Metric, 0)
	for _, metric := range s.metrics1m {
		if metric.BucketTime.Equal(latest) {
			items = append(items, metric)
		}
	}
	return items, nil
}

func (s *MemoryStore) Recent5mMetrics() ([]aggregator.Metric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]aggregator.Metric, 0, len(s.metrics5m))
	for _, metric := range s.metrics5m {
		items = append(items, metric)
	}
	return items, nil
}
func (s *MemoryStore) UpsertAgent(agent storage.Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current, ok := s.agents[agent.ID]; ok && current.LastLogID > agent.LastLogID {
		agent.LastLogID = current.LastLogID
	}
	if current, ok := s.agents[agent.ID]; ok && agent.SourceLatestLogID == 0 {
		agent.SourceLatestLogID = current.SourceLatestLogID
		agent.BacklogEstimate = current.BacklogEstimate
	}
	s.agents[agent.ID] = agent
	return nil
}

func (s *MemoryStore) InsertLogEvent(event storage.LogEvent) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := event.InstanceID + ":" + int64Key(event.SourceLogID)
	if _, exists := s.logEvents[key]; exists {
		return false, nil
	}
	s.logEvents[key] = event
	return true, nil
}

func (s *MemoryStore) InsertLogSample(sample storage.LogSample) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sample.InstanceID + ":" + sample.SampleKind + ":" + int64Key(sample.SourceLogID)
	if _, exists := s.logSamples[key]; exists {
		return false, nil
	}
	s.logSamples[key] = sample
	return true, nil
}

func (s *MemoryStore) LogSampleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logSamples)
}
func (s *MemoryStore) QueryLogSamples(query storage.LogSampleQuery) ([]storage.LogSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	samples := make([]storage.LogSample, 0, len(s.logSamples))
	for _, sample := range s.logSamples {
		samples = append(samples, sample)
	}
	return storage.FilterLogSamples(samples, query), nil
}
func (s *MemoryStore) QueryLogEvents(query storage.LogQuery) ([]storage.LogEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]storage.LogEvent, 0, len(s.logEvents))
	for _, event := range s.logEvents {
		events = append(events, event)
	}
	return storage.FilterLogEvents(events, query), nil
}

func (s *MemoryStore) InsertServerMetric(metric storage.ServerMetric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverMetrics = append(s.serverMetrics, metric)
	return nil
}

func (s *MemoryStore) InsertDockerStatus(status storage.DockerStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dockerStatuses = append(s.dockerStatuses, status)
	return nil
}

func (s *MemoryStore) InsertHealthCheck(check storage.HealthCheck) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.healthChecks = append(s.healthChecks, check)
	return nil
}

func (s *MemoryStore) UpdateLogOffset(instanceID string, lastLogID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lastLogID > s.offsets[instanceID] {
		s.offsets[instanceID] = lastLogID
	}
	return nil
}

func (s *MemoryStore) CurrentLogOffset(instanceID string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offsets[instanceID], nil
}

func (s *MemoryStore) LogEventCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logEvents)
}

func (s *MemoryStore) DockerStatusCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.dockerStatuses)
}

func (s *MemoryStore) HealthCheckCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.healthChecks)
}

func (s *MemoryStore) Offset(instanceID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offsets[instanceID]
}

func (s *MemoryStore) Agent(agentID string) (storage.Agent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent, ok := s.agents[agentID]
	return agent, ok
}

func int64Key(value int64) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}

func (s *MemoryStore) UpsertCurrentAlerts(alerts []storage.Alert, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, alert := range alerts {
		current, ok := s.alerts[alert.ID]
		if ok {
			if current.Status == "resolved" {
				current.Status = "firing"
			}
			current.InstanceID = alert.InstanceID
			current.RuleKey = alert.RuleKey
			current.Severity = alert.Severity
			current.Title = alert.Title
			current.Summary = alert.Summary
			current.LastSeenAt = alert.LastSeenAt
			current.ResolvedAt = nil
			s.alerts[alert.ID] = current
			continue
		}
		if alert.Status == "" {
			alert.Status = "firing"
		}
		if alert.FirstSeenAt.IsZero() {
			alert.FirstSeenAt = now
		}
		if alert.LastSeenAt.IsZero() {
			alert.LastSeenAt = now
		}
		s.alerts[alert.ID] = alert
	}
	return nil
}

func (s *MemoryStore) ResolveMissingAlerts(currentIDs []string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := make(map[string]struct{}, len(currentIDs))
	for _, id := range currentIDs {
		current[id] = struct{}{}
	}
	for id, alert := range s.alerts {
		if alert.Status == "resolved" {
			continue
		}
		if _, ok := current[id]; ok {
			continue
		}
		alert.Status = "resolved"
		alert.ResolvedAt = &now
		s.alerts[id] = alert
	}
	return nil
}

func (s *MemoryStore) ExpireSilencedAlerts(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, alert := range s.alerts {
		if alert.Status == "silenced" && alert.SilenceUntil != nil && !alert.SilenceUntil.After(now) {
			alert.Status = "firing"
			alert.SilenceUntil = nil
			s.alerts[id] = alert
		}
	}
	return nil
}

func (s *MemoryStore) QueryAlerts(query storage.AlertQuery) ([]storage.Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]storage.Alert, 0, len(s.alerts))
	for _, alert := range s.alerts {
		if query.InstanceID != "" && alert.InstanceID != query.InstanceID {
			continue
		}
		if query.Status != "" && alert.Status != query.Status {
			continue
		}
		if query.Severity != "" && alert.Severity != query.Severity {
			continue
		}
		if query.ActiveOnly && alert.Status == "resolved" {
			continue
		}
		items = append(items, alert)
	}
	return items, nil
}

func (s *MemoryStore) UpdateAlertAction(id string, status string, silenceUntil *time.Time, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	alert, ok := s.alerts[id]
	if !ok {
		return nil
	}
	alert.Status = status
	alert.SilenceUntil = silenceUntil
	if status == "resolved" {
		alert.ResolvedAt = &now
	} else {
		alert.ResolvedAt = nil
	}
	s.alerts[id] = alert
	return nil
}

func (s *MemoryStore) UpsertNotificationChannel(channel storage.NotificationChannel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notificationChannels[channel.ID] = channel
	return nil
}

func (s *MemoryStore) QueryNotificationChannels(enabledOnly bool) ([]storage.NotificationChannel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	channels := make([]storage.NotificationChannel, 0, len(s.notificationChannels))
	for _, channel := range s.notificationChannels {
		if enabledOnly && !channel.Enabled {
			continue
		}
		channels = append(channels, channel)
	}
	return channels, nil
}

func (s *MemoryStore) InsertNotificationDelivery(delivery storage.NotificationDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notificationDeliveries[delivery.AlertID+":"+delivery.ChannelID] = delivery
	return nil
}

func (s *MemoryStore) NotificationDeliveryDue(alertID string, channelID string, now time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, ok := s.notificationDeliveries[alertID+":"+channelID]
	if !ok {
		return true, nil
	}
	if delivery.Status == "sent" {
		return false, nil
	}
	return !delivery.NextAttemptAt.After(now), nil
}

func (s *MemoryStore) QueryNotificationDeliveries(query storage.NotificationDeliveryQuery) ([]storage.NotificationDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deliveries := make([]storage.NotificationDelivery, 0, len(s.notificationDeliveries))
	for _, delivery := range s.notificationDeliveries {
		if query.AlertID != "" && delivery.AlertID != query.AlertID {
			continue
		}
		if query.ChannelID != "" && delivery.ChannelID != query.ChannelID {
			continue
		}
		if query.Status != "" && delivery.Status != query.Status {
			continue
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, nil
}

func (s *MemoryStore) InsertChannelSnapshot(snapshot storage.ChannelSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelSnapshots = append(s.channelSnapshots, snapshot)
	return nil
}

func (s *MemoryStore) QueryChannelSnapshots(query storage.ChannelSnapshotQuery) ([]storage.ChannelSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]storage.ChannelSnapshot, 0, len(s.channelSnapshots))
	for _, item := range s.channelSnapshots {
		if query.InstanceID != "" && item.InstanceID != query.InstanceID {
			continue
		}
		if query.ChannelID > 0 && item.ChannelID != query.ChannelID {
			continue
		}
		if !query.StartTime.IsZero() && item.CapturedAt.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && item.CapturedAt.After(query.EndTime) {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}
