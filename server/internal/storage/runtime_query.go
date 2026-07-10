package storage

import "time"

const MaxRuntimeQueryLimit = 200

type AgentQuery struct {
	InstanceID string
	Status     string
	Limit      int
	Offset     int
}

type ServerMetricQuery struct {
	InstanceID string
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}

type HealthCheckQuery struct {
	InstanceID string
	Target     string
	Status     string
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}

type DockerStatusQuery struct {
	InstanceID    string
	ContainerName string
	Running       *bool
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
	Offset        int
}

func NormalizeRuntimePagination(limit int, offset int) (int, int) {
	if limit <= 0 || limit > MaxRuntimeQueryLimit {
		limit = MaxRuntimeQueryLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
