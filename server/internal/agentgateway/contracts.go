package agentgateway

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
	InstanceID         string                     `json:"instance_id"`
	AgentID            string                     `json:"agent_id"`
	AgentVersion       string                     `json:"agent_version"`
	ReportedAt         time.Time                  `json:"reported_at"`
	Sequence           int64                      `json:"sequence"`
	LastLogID          int64                      `json:"last_log_id"`
	SourceLatestLogID  int64                      `json:"source_latest_log_id,omitempty"`
	BacklogEstimate    int64                      `json:"backlog_estimate,omitempty"`
	MetricBatchID      string                     `json:"metric_batch_id,omitempty"`
	LogEvents          []LogEventPayload          `json:"log_events"`
	LogSamples         []LogSamplePayload         `json:"log_samples"`
	AggregatedMetrics  []AggregatedMetricPayload  `json:"aggregated_metrics"`
	ServerMetrics      []ServerMetricPayload      `json:"server_metrics"`
	DockerStatuses     []DockerStatusPayload      `json:"docker_statuses"`
	HealthChecks       []HealthCheckPayload       `json:"health_checks"`
	ChannelSnapshots   []ChannelSnapshotPayload   `json:"channel_snapshots"`
	CommandResults     []ChannelCommandResult     `json:"command_results"`
	NginxTimingBuckets []NginxTimingBucketPayload `json:"nginx_timing_buckets,omitempty"`
	NginxSlowSamples   []NginxSlowSamplePayload   `json:"nginx_slow_samples,omitempty"`
}

type NginxTimingBucketPayload struct {
	BucketAt          time.Time `json:"bucket_at"`
	RequestCount      int64     `json:"request_count"`
	UpstreamCount     int64     `json:"upstream_count"`
	Status4xx         int64     `json:"status_4xx"`
	Status5xx         int64     `json:"status_5xx"`
	Status504         int64     `json:"status_504"`
	RTP50             float64   `json:"rt_p50"`
	RTP95             float64   `json:"rt_p95"`
	RTMax             float64   `json:"rt_max"`
	UHTP50            float64   `json:"uht_p50"`
	UHTP95            float64   `json:"uht_p95"`
	UHTMax            float64   `json:"uht_max"`
	TransferP50       float64   `json:"transfer_p50"`
	TransferP95       float64   `json:"transfer_p95"`
	TransferMax       float64   `json:"transfer_max"`
	BytesTotal        int64     `json:"bytes_total"`
	SlowCount         int64     `json:"slow_count"`
	SlowTTFTCount     int64     `json:"slow_ttft_count"`
	SlowTransferCount int64     `json:"slow_transfer_count"`
}
type NginxSlowSamplePayload struct {
	OccurredAt time.Time `json:"occurred_at"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	RT         float64   `json:"rt"`
	UHT        float64   `json:"uht"`
	URT        float64   `json:"urt"`
	Bytes      int64     `json:"bytes"`
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
	P50UseTime        *float64            `json:"p50_use_time,omitempty"`
	P95UseTime        *float64            `json:"p95_use_time"`
	P99UseTime        *float64            `json:"p99_use_time,omitempty"`
	StreamRate        *float64            `json:"stream_rate"`
	CacheTokenRate    *float64            `json:"cache_token_rate"`
	UseTimeSum        float64             `json:"use_time_sum"`
	StreamCount       int64               `json:"stream_count"`
	CacheTokensTotal  int64               `json:"cache_tokens_total"`
	CachePromptTokens int64               `json:"cache_prompt_tokens"`
	BigInputCount     *int64              `json:"big_input_count,omitempty"`
	BigInputCacheHits *int64              `json:"big_input_cache_hits,omitempty"`
	TTFTCount         *int64              `json:"ttft_count,omitempty"`
	TTFTSumMS         *int64              `json:"ttft_sum_ms,omitempty"`
	TTFTP95MS         *float64            `json:"ttft_p95_ms,omitempty"`
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
	GroupName   *string   `json:"group_name,omitempty"`
	Priority    *int64    `json:"priority,omitempty"`
	CapturedAt  time.Time `json:"captured_at"`
}
