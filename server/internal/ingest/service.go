package ingest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"controltower/server/internal/agentgateway"
	"controltower/server/internal/aggregator"
	"controltower/server/internal/storage"
)

type Store interface {
	UpsertAgent(agent storage.Agent) error
	InsertLogEvent(event storage.LogEvent) (bool, error)
	InsertLogSample(sample storage.LogSample) (bool, error)
	InsertServerMetric(metric storage.ServerMetric) error
	InsertDockerStatus(status storage.DockerStatus) error
	InsertHealthCheck(check storage.HealthCheck) error
	InsertChannelSnapshot(snapshot storage.ChannelSnapshot) error
	UpdateLogOffset(instanceID string, lastLogID int64) error
	CurrentLogOffset(instanceID string) (int64, error)
	ApplyMetricBatch(instanceID string, batchID string, metrics []aggregator.Metric) error
	ClaimPendingCommands(string, time.Time) ([]storage.ChannelCommand, error)
	CompleteChannelCommand(string, string, string, time.Time) (storage.ChannelCommand, bool, error)
	ExpireStaleCommands(time.Time) (int, error)
	InsertOperationAudit(storage.OperationAudit) error
}

type Service struct {
	store         Store
	commandExpiry time.Duration
}

func NewService(store Store) Service {
	return Service{store: store, commandExpiry: 10 * time.Minute}
}

func NewServiceWithCommandExpiry(store Store, expiry time.Duration) Service {
	if expiry <= 0 {
		expiry = 10 * time.Minute
	}
	return Service{store: store, commandExpiry: expiry}
}

func (s Service) SaveHeartbeat(req agentgateway.AgentHeartbeatRequest) (int64, error) {
	if req.InstanceID == "" || req.AgentID == "" {
		return 0, errors.New("missing agent identity")
	}
	if err := s.store.UpsertAgent(storage.Agent{
		ID:           req.AgentID,
		InstanceID:   req.InstanceID,
		Version:      req.AgentVersion,
		LastSeenAt:   req.ReportedAt,
		LastSequence: req.Sequence,
		LastLogID:    req.LastLogID,
		Status:       "online",
	}); err != nil {
		return 0, err
	}
	return s.store.CurrentLogOffset(req.InstanceID)
}

func (s Service) SaveHeartbeatWithCommands(req agentgateway.AgentHeartbeatRequest) (int64, []agentgateway.ChannelCommand, error) {
	offset, err := s.SaveHeartbeat(req)
	if err != nil {
		return 0, nil, err
	}
	now := time.Now().UTC()
	if _, err := s.store.ExpireStaleCommands(now.Add(-s.commandExpiry)); err != nil {
		return 0, nil, err
	}
	stored, err := s.store.ClaimPendingCommands(req.InstanceID, now)
	if err != nil {
		return 0, nil, err
	}
	commands := make([]agentgateway.ChannelCommand, 0, len(stored))
	for _, v := range stored {
		var p struct {
			Status   *int   `json:"status"`
			Weight   *uint  `json:"weight"`
			Priority *int64 `json:"priority"`
		}
		if json.Unmarshal([]byte(v.PayloadJSON), &p) != nil {
			continue
		}
		commands = append(commands, agentgateway.ChannelCommand{ID: v.ID, Type: v.CommandType, ChannelID: v.ChannelID, Status: p.Status, Weight: p.Weight, Priority: p.Priority})
	}
	return offset, commands, err
}

func (s Service) SaveReport(req agentgateway.AgentReportRequest) error {
	if req.InstanceID == "" || req.AgentID == "" {
		return errors.New("missing agent identity")
	}
	reportedLastLogID := req.LastLogID
	if reportedLastLogID == 0 {
		reportedLastLogID = maxPayloadLogID(req.LogEvents)
	}
	if err := s.store.UpsertAgent(storage.Agent{
		ID:                req.AgentID,
		InstanceID:        req.InstanceID,
		Version:           req.AgentVersion,
		LastSeenAt:        req.ReportedAt,
		LastSequence:      req.Sequence,
		LastLogID:         reportedLastLogID,
		SourceLatestLogID: req.SourceLatestLogID,
		BacklogEstimate:   req.BacklogEstimate,
		Status:            "online",
	}); err != nil {
		return err
	}
	for _, result := range req.CommandResults {
		status := "failed"
		if result.Status == "succeeded" {
			status = "succeeded"
		}
		command, changed, err := s.store.CompleteChannelCommand(result.ID, status, result.Error, time.Now().UTC())
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		summary, _ := json.Marshal(map[string]any{"payload": json.RawMessage(command.PayloadJSON), "result": map[string]any{"status": status, "error": result.Error, "applied_at": result.AppliedAt}})
		if err = s.store.InsertOperationAudit(storage.OperationAudit{ID: command.ID, InstanceID: command.InstanceID, OperationType: "channel.update", TargetType: "channel", TargetID: strconv.FormatInt(command.ChannelID, 10), ActorID: command.CreatedBy, BeforeSummary: "", AfterSummary: string(summary), Status: status, CreatedAt: time.Now().UTC()}); err != nil {
			return err
		}
	}

	for _, payload := range req.LogEvents {
		event := storage.LogEvent{
			InstanceID:        req.InstanceID,
			SourceLogID:       payload.SourceLogID,
			CreatedAt:         payload.CreatedAt,
			LogType:           payload.LogType,
			UserID:            payload.UserID,
			Username:          payload.Username,
			ChannelID:         payload.ChannelID,
			ModelName:         payload.ModelName,
			TokenID:           payload.TokenID,
			TokenName:         payload.TokenName,
			PromptTokens:      payload.PromptTokens,
			CompletionTokens:  payload.CompletionTokens,
			TotalTokens:       payload.TotalTokens,
			Quota:             payload.Quota,
			UseTime:           payload.UseTime,
			IsStream:          payload.IsStream,
			Group:             payload.Group,
			RequestID:         payload.RequestID,
			UpstreamRequestID: payload.UpstreamRequestID,
			ErrorSummary:      payload.ErrorSummary,
			CacheTokens:       payload.CacheTokens,
			CacheFieldPresent: payload.CacheFieldPresent,
		}
		if _, err := s.store.InsertLogEvent(event); err != nil {
			return err
		}
	}

	for _, payload := range req.LogSamples {
		sample := storage.LogSample{
			InstanceID:        req.InstanceID,
			SampleKind:        payload.SampleKind,
			SourceLogID:       payload.SourceLogID,
			CreatedAt:         payload.CreatedAt,
			LogType:           payload.LogType,
			UserID:            payload.UserID,
			Username:          payload.Username,
			ChannelID:         payload.ChannelID,
			ModelName:         payload.ModelName,
			TokenID:           payload.TokenID,
			TokenName:         payload.TokenName,
			PromptTokens:      payload.PromptTokens,
			CompletionTokens:  payload.CompletionTokens,
			TotalTokens:       payload.TotalTokens,
			Quota:             payload.Quota,
			UseTime:           payload.UseTime,
			IsStream:          payload.IsStream,
			Group:             payload.Group,
			RequestID:         payload.RequestID,
			UpstreamRequestID: payload.UpstreamRequestID,
			ErrorSummary:      payload.ErrorSummary,
			CacheTokens:       payload.CacheTokens,
			CacheFieldPresent: payload.CacheFieldPresent,
		}
		if _, err := s.store.InsertLogSample(sample); err != nil {
			return err
		}
	}
	if reportedLastLogID == 0 {
		reportedLastLogID = maxPayloadLogID(req.LogEvents)
		if sampleMax := maxSamplePayloadLogID(req.LogSamples); sampleMax > reportedLastLogID {
			reportedLastLogID = sampleMax
		}
	}
	if len(req.AggregatedMetrics) > 0 {
		if req.MetricBatchID == "" {
			return errors.New("missing metric batch id")
		}
		if err := s.store.ApplyMetricBatch(req.InstanceID, req.MetricBatchID, toAggregatorMetrics(req.InstanceID, req.AggregatedMetrics)); err != nil {
			return err
		}
	}
	for _, payload := range req.ServerMetrics {
		if err := s.store.InsertServerMetric(storage.ServerMetric{
			InstanceID:              req.InstanceID,
			CollectedAt:             payload.CollectedAt,
			CPUPercent:              payload.CPUPercent,
			MemoryUsedPercent:       payload.MemoryUsedPercent,
			DiskUsedPercent:         payload.DiskUsedPercent,
			NetworkRxBytesPerSecond: payload.NetworkRxBytesPerSecond,
			NetworkTxBytesPerSecond: payload.NetworkTxBytesPerSecond,
			Load1m:                  payload.Load1m,
		}); err != nil {
			return err
		}
	}

	for _, payload := range req.DockerStatuses {
		if err := s.store.InsertDockerStatus(storage.DockerStatus{
			InstanceID:    req.InstanceID,
			CollectedAt:   payload.CollectedAt,
			ContainerName: payload.ContainerName,
			Status:        payload.Status,
			Running:       payload.Running,
		}); err != nil {
			return err
		}
	}

	for _, payload := range req.HealthChecks {
		if err := s.store.InsertHealthCheck(storage.HealthCheck{
			InstanceID:     req.InstanceID,
			CheckedAt:      payload.CheckedAt,
			Target:         payload.Target,
			Status:         payload.Status,
			HTTPStatusCode: payload.HTTPStatusCode,
			LatencyMS:      payload.LatencyMS,
			ErrorSummary:   payload.ErrorSummary,
		}); err != nil {
			return err
		}
	}

	for _, payload := range req.ChannelSnapshots {
		capturedAt := payload.CapturedAt
		if capturedAt.IsZero() {
			capturedAt = req.ReportedAt
		}
		if err := s.store.InsertChannelSnapshot(storage.ChannelSnapshot{
			ID:          channelSnapshotID(req.InstanceID, payload.ChannelID, capturedAt),
			InstanceID:  req.InstanceID,
			ChannelID:   payload.ChannelID,
			ChannelName: payload.ChannelName,
			Status:      payload.Status,
			Weight:      payload.Weight,
			ModelsText:  payload.ModelsText,
			CapturedAt:  capturedAt,
		}); err != nil {
			return err
		}
	}

	if reportedLastLogID > 0 {
		return s.store.UpdateLogOffset(req.InstanceID, reportedLastLogID)
	}
	return nil
}

func maxSamplePayloadLogID(payloads []agentgateway.LogSamplePayload) int64 {
	var maxLogID int64
	for _, payload := range payloads {
		if payload.SourceLogID > maxLogID {
			maxLogID = payload.SourceLogID
		}
	}
	return maxLogID
}

func maxPayloadLogID(payloads []agentgateway.LogEventPayload) int64 {
	var maxLogID int64
	for _, payload := range payloads {
		if payload.SourceLogID > maxLogID {
			maxLogID = payload.SourceLogID
		}
	}
	return maxLogID
}
func toAggregatorMetrics(instanceID string, payloads []agentgateway.AggregatedMetricPayload) []aggregator.Metric {
	metrics := make([]aggregator.Metric, 0, len(payloads))
	for _, payload := range payloads {
		if payload.BucketTime.IsZero() || payload.DimensionType == "" || payload.DimensionKey == "" {
			continue
		}
		metrics = append(metrics, aggregator.Metric{
			InstanceID:        instanceID,
			BucketTime:        payload.BucketTime,
			DimensionType:     payload.DimensionType,
			DimensionKey:      payload.DimensionKey,
			RequestCount:      payload.RequestCount,
			SuccessCount:      payload.SuccessCount,
			ErrorCount:        payload.ErrorCount,
			SuccessRate:       payload.SuccessRate,
			ErrorRate:         payload.ErrorRate,
			TPM:               payload.TPM,
			PromptTokens:      payload.PromptTokens,
			CompletionTokens:  payload.CompletionTokens,
			Quota:             payload.Quota,
			AvgUseTime:        payload.AvgUseTime,
			P95UseTime:        payload.P95UseTime,
			StreamRate:        payload.StreamRate,
			CacheTokenRate:    payload.CacheTokenRate,
			UseTimeSum:        payload.UseTimeSum,
			StreamCount:       payload.StreamCount,
			CacheTokensTotal:  payload.CacheTokensTotal,
			CachePromptTokens: payload.CachePromptTokens,
			LatencyBuckets:    payload.LatencyBuckets,
		})
	}
	return metrics
}
func channelSnapshotID(instanceID string, channelID int64, capturedAt time.Time) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%d:%d", instanceID, channelID, capturedAt.UnixNano())))
	return hex.EncodeToString(sum[:])
}
