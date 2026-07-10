package reporter

import (
	"time"

	"controltower/internal/latencyhist"
)

type AgentHeartbeatRequest struct {
	InstanceID   string    `json:"instance_id"`
	AgentID      string    `json:"agent_id"`
	AgentVersion string    `json:"agent_version"`
	ReportedAt   time.Time `json:"reported_at"`
	Sequence     int64     `json:"sequence"`
	LastLogID    int64     `json:"last_log_id"`
}

type AgentHeartbeatResponse struct {
	Accepted        bool             `json:"accepted"`
	ServerLastLogID int64            `json:"server_last_log_id"`
	Commands        []ChannelCommand `json:"commands"`
}

type ChannelCommand struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	ChannelID int64  `json:"channel_id"`
	Status    *int   `json:"status,omitempty"`
	Weight    *uint  `json:"weight,omitempty"`
	Priority  *int64 `json:"priority,omitempty"`
}

type ChannelCommandResult struct {
	ID        string    `json:"id"`
	ChannelID int64     `json:"channel_id"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	AppliedAt time.Time `json:"applied_at"`
}
type AgentReportRequest struct {
	InstanceID        string                    `json:"instance_id"`
	AgentID           string                    `json:"agent_id"`
	AgentVersion      string                    `json:"agent_version"`
	ReportedAt        time.Time                 `json:"reported_at"`
	Sequence          int64                     `json:"sequence"`
	LastLogID         int64                     `json:"last_log_id"`
	SourceLatestLogID int64                     `json:"source_latest_log_id,omitempty"`
	BacklogEstimate   int64                     `json:"backlog_estimate,omitempty"`
	MetricBatchID     string                    `json:"metric_batch_id,omitempty"`
	LogEvents         []LogEventPayload         `json:"log_events"`
	LogSamples        []LogSamplePayload        `json:"log_samples"`
	AggregatedMetrics []AggregatedMetricPayload `json:"aggregated_metrics"`
	ServerMetrics     []ServerMetricPayload     `json:"server_metrics"`
	DockerStatuses    []DockerStatusPayload     `json:"docker_statuses"`
	HealthChecks      []HealthCheckPayload      `json:"health_checks"`
	ChannelSnapshots  []ChannelSnapshotPayload  `json:"channel_snapshots"`
	CommandResults    []ChannelCommandResult    `json:"command_results"`
}

type AggregatedMetricPayload struct {
	BucketTime        time.Time           `json:"bucket_time"`
	WindowSeconds     int64               `json:"window_seconds"`
	DimensionType     string              `json:"dimension_type"`
	DimensionKey      string              `json:"dimension_key"`
	RequestCount      int64               `json:"request_count"`
	SuccessCount      int64               `json:"success_count"`
	ErrorCount        int64               `json:"error_count"`
	SuccessRate       *float64            `json:"success_rate"`
	ErrorRate         *float64            `json:"error_rate"`
	TPM               int64               `json:"tpm"`
	PromptTokens      int64               `json:"prompt_tokens"`
	CompletionTokens  int64               `json:"completion_tokens"`
	Quota             int64               `json:"quota"`
	AvgUseTime        *float64            `json:"avg_use_time"`
	P95UseTime        *float64            `json:"p95_use_time"`
	StreamRate        *float64            `json:"stream_rate"`
	CacheTokenRate    *float64            `json:"cache_token_rate"`
	UseTimeSum        float64             `json:"use_time_sum"`
	StreamCount       int64               `json:"stream_count"`
	CacheTokensTotal  int64               `json:"cache_tokens_total"`
	CachePromptTokens int64               `json:"cache_prompt_tokens"`
	LatencyBuckets    latencyhist.Buckets `json:"latency_buckets"`
}

type LogSamplePayload struct {
	SampleKind        string    `json:"sample_kind"`
	SourceLogID       int64     `json:"source_log_id"`
	CreatedAt         time.Time `json:"created_at"`
	LogType           string    `json:"log_type"`
	UserID            int64     `json:"user_id"`
	Username          string    `json:"username"`
	ChannelID         int64     `json:"channel_id"`
	ModelName         string    `json:"model_name"`
	TokenID           int64     `json:"token_id"`
	TokenName         string    `json:"token_name"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	Quota             int64     `json:"quota"`
	UseTime           float64   `json:"use_time"`
	IsStream          bool      `json:"is_stream"`
	Group             string    `json:"group"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id"`
	ErrorSummary      string    `json:"error_summary"`
	CacheTokens       *int64    `json:"cache_tokens"`
	CacheFieldPresent bool      `json:"cache_field_present"`
}
type LogEventPayload struct {
	SourceLogID       int64     `json:"source_log_id"`
	CreatedAt         time.Time `json:"created_at"`
	LogType           string    `json:"log_type"`
	UserID            int64     `json:"user_id"`
	Username          string    `json:"username"`
	ChannelID         int64     `json:"channel_id"`
	ModelName         string    `json:"model_name"`
	TokenID           int64     `json:"token_id"`
	TokenName         string    `json:"token_name"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	TotalTokens       int64     `json:"total_tokens"`
	Quota             int64     `json:"quota"`
	UseTime           float64   `json:"use_time"`
	IsStream          bool      `json:"is_stream"`
	Group             string    `json:"group"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id"`
	ErrorSummary      string    `json:"error_summary"`
	CacheTokens       *int64    `json:"cache_tokens"`
	CacheFieldPresent bool      `json:"cache_field_present"`
}

type ServerMetricPayload struct {
	CollectedAt             time.Time `json:"collected_at"`
	CPUPercent              float64   `json:"cpu_percent"`
	MemoryUsedPercent       float64   `json:"memory_used_percent"`
	DiskUsedPercent         float64   `json:"disk_used_percent"`
	NetworkRxBytesPerSecond int64     `json:"network_rx_bytes_per_second"`
	NetworkTxBytesPerSecond int64     `json:"network_tx_bytes_per_second"`
	Load1m                  float64   `json:"load_1m"`
}

type DockerStatusPayload struct {
	CollectedAt   time.Time `json:"collected_at"`
	ContainerName string    `json:"container_name"`
	Status        string    `json:"status"`
	Running       bool      `json:"running"`
}

type HealthCheckPayload struct {
	CheckedAt      time.Time `json:"checked_at"`
	Target         string    `json:"target"`
	Status         string    `json:"status"`
	HTTPStatusCode int       `json:"http_status_code"`
	LatencyMS      int64     `json:"latency_ms"`
	ErrorSummary   string    `json:"error_summary"`
}

type ChannelSnapshotPayload struct {
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	Status      string    `json:"status"`
	Weight      int64     `json:"weight"`
	ModelsText  string    `json:"models_text"`
	CapturedAt  time.Time `json:"captured_at"`
}
