package ingest

import (
	"sort"

	"controltower/server/internal/storage"
)

func (s *MemoryStore) QueryAgents(query storage.AgentQuery) ([]storage.Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []storage.Agent
	for _, item := range s.agents {
		if query.InstanceID != "" && item.InstanceID != query.InstanceID {
			continue
		}
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].LastSeenAt.Equal(filtered[j].LastSeenAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].LastSeenAt.After(filtered[j].LastSeenAt)
	})
	return paginateRuntime(filtered, query.Limit, query.Offset), nil
}
func (s *MemoryStore) QueryServerMetrics(query storage.ServerMetricQuery) ([]storage.ServerMetric, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []storage.ServerMetric
	for _, item := range s.serverMetrics {
		if query.InstanceID != "" && item.InstanceID != query.InstanceID {
			continue
		}
		if !query.StartTime.IsZero() && item.CollectedAt.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && item.CollectedAt.After(query.EndTime) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CollectedAt.After(filtered[j].CollectedAt)
	})
	return paginateRuntime(filtered, query.Limit, query.Offset), nil
}

func (s *MemoryStore) QueryHealthChecks(query storage.HealthCheckQuery) ([]storage.HealthCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []storage.HealthCheck
	for _, item := range s.healthChecks {
		if query.InstanceID != "" && item.InstanceID != query.InstanceID {
			continue
		}
		if query.Target != "" && item.Target != query.Target {
			continue
		}
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		if !query.StartTime.IsZero() && item.CheckedAt.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && item.CheckedAt.After(query.EndTime) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CheckedAt.Equal(filtered[j].CheckedAt) {
			return filtered[i].Target < filtered[j].Target
		}
		return filtered[i].CheckedAt.After(filtered[j].CheckedAt)
	})
	return paginateRuntime(filtered, query.Limit, query.Offset), nil
}

func (s *MemoryStore) QueryDockerStatuses(query storage.DockerStatusQuery) ([]storage.DockerStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var filtered []storage.DockerStatus
	for _, item := range s.dockerStatuses {
		if query.InstanceID != "" && item.InstanceID != query.InstanceID {
			continue
		}
		if query.ContainerName != "" && item.ContainerName != query.ContainerName {
			continue
		}
		if query.Running != nil && item.Running != *query.Running {
			continue
		}
		if !query.StartTime.IsZero() && item.CollectedAt.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && item.CollectedAt.After(query.EndTime) {
			continue
		}
		filtered = append(filtered, item)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CollectedAt.Equal(filtered[j].CollectedAt) {
			return filtered[i].ContainerName < filtered[j].ContainerName
		}
		return filtered[i].CollectedAt.After(filtered[j].CollectedAt)
	})
	return paginateRuntime(filtered, query.Limit, query.Offset), nil
}

func paginateRuntime[T any](items []T, limit int, offset int) []T {
	limit, offset = storage.NormalizeRuntimePagination(limit, offset)
	if offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return append([]T(nil), items[offset:end]...)
}
