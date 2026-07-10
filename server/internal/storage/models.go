package storage

import "time"

type Instance struct {
	ID        string
	Name      string
	Env       string
	Region    string
	BaseURL   string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Agent struct {
	ID                string
	InstanceID        string
	Version           string
	LastSeenAt        time.Time
	LastSequence      int64
	LastLogID         int64
	SourceLatestLogID int64
	BacklogEstimate   int64
	Status            string
	ReportDelayMS     int64
}

type LogEvent struct {
	InstanceID        string
	SourceLogID       int64
	CreatedAt         time.Time
	LogType           string
	UserID            int64
	Username          string
	ChannelID         int64
	ModelName         string
	TokenID           int64
	TokenName         string
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	Quota             int64
	UseTime           float64
	IsStream          bool
	Group             string
	RequestID         string
	UpstreamRequestID string
	ErrorSummary      string
	CacheTokens       *int64
	CacheFieldPresent bool
}

type LogSample struct {
	InstanceID        string
	SampleKind        string
	SourceLogID       int64
	CreatedAt         time.Time
	LogType           string
	UserID            int64
	Username          string
	ChannelID         int64
	ModelName         string
	TokenID           int64
	TokenName         string
	PromptTokens      int64
	CompletionTokens  int64
	TotalTokens       int64
	Quota             int64
	UseTime           float64
	IsStream          bool
	Group             string
	RequestID         string
	UpstreamRequestID string
	ErrorSummary      string
	CacheTokens       *int64
	CacheFieldPresent bool
}
type ServerMetric struct {
	InstanceID              string
	CollectedAt             time.Time
	CPUPercent              float64
	MemoryUsedPercent       float64
	DiskUsedPercent         float64
	NetworkRxBytesPerSecond int64
	NetworkTxBytesPerSecond int64
	Load1m                  float64
}
type HealthCheck struct {
	InstanceID     string
	CheckedAt      time.Time
	Target         string
	Status         string
	HTTPStatusCode int
	LatencyMS      int64
	ErrorSummary   string
}
type DockerStatus struct {
	InstanceID    string
	CollectedAt   time.Time
	ContainerName string
	Status        string
	Running       bool
}

type ChannelSnapshot struct {
	ID          string
	InstanceID  string
	ChannelID   int64
	ChannelName string
	Status      string
	Weight      int64
	ModelsText  string
	CapturedAt  time.Time
}
type Alert struct {
	ID           string
	InstanceID   string
	RuleKey      string
	Severity     string
	Status       string
	Title        string
	Summary      string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	ResolvedAt   *time.Time
	SilenceUntil *time.Time
}

type NotificationChannel struct {
	ID          string
	ChannelType string
	Name        string
	WebhookURL  string
	SecretValue string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type NotificationDelivery struct {
	ID            string
	AlertID       string
	ChannelID     string
	Status        string
	AttemptedAt   time.Time
	NextAttemptAt time.Time
	Attempts      int
	StatusCode    int
	ErrorSummary  string
}
