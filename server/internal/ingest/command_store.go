package ingest

import (
	"errors"
	"sort"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

func (s *MemoryStore) CreateChannelCommand(v storage.ChannelCommand) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.channelCommands[v.ID]; ok {
		return errors.New("command exists")
	}
	s.channelCommands[v.ID] = v
	return nil
}
func (s *MemoryStore) ClaimPendingCommands(instanceID string, now time.Time) ([]storage.ChannelCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.ChannelCommand
	for id, v := range s.channelCommands {
		if v.InstanceID == instanceID && v.Status == "pending" {
			v.Status = "delivered"
			v.UpdatedAt = now
			s.channelCommands[id] = v
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
func (s *MemoryStore) CompleteChannelCommand(id, status, errorSummary string, now time.Time) (storage.ChannelCommand, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.channelCommands[id]
	if !ok || v.Status != "delivered" {
		return v, false, nil
	}
	v.Status = status
	v.ErrorSummary = errorSummary
	v.UpdatedAt = now
	s.channelCommands[id] = v
	return v, true, nil
}
func (s *MemoryStore) ExpireStaleCommands(before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, v := range s.channelCommands {
		if v.Status == "pending" && v.CreatedAt.Before(before) {
			v.Status = "expired"
			v.UpdatedAt = time.Now().UTC()
			s.channelCommands[id] = v
			if audit, ok := s.operationAudits[id]; ok && audit.Status == "submitted" {
				audit.Status = "expired"
				audit.ErrorSummary = "command expired before execution"
				audit.UpdatedAt = v.UpdatedAt
				s.operationAudits[id] = audit
			}
			n++
		}
	}
	return n, nil
}
func (s *MemoryStore) QueryChannelCommands(q storage.ChannelCommandQuery) ([]storage.ChannelCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []storage.ChannelCommand
	for _, v := range s.channelCommands {
		if (q.InstanceID == "" || v.InstanceID == q.InstanceID) && (q.Status == "" || v.Status == q.Status) {
			all = append(all, v)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })
	limit, offset := storage.NormalizeCommandPagination(q.Limit, q.Offset)
	if offset >= len(all) {
		return []storage.ChannelCommand{}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return append([]storage.ChannelCommand(nil), all[offset:end]...), nil
}
func (s *MemoryStore) InsertOperationAudit(v storage.OperationAudit) error {
	v = storage.NormalizeOperationAudit(v)
	if !storage.IsManualOperationAudit(v) {
		return nil
	}
	if storage.IsExcludedOperationAudit(v.OperationType) {
		return nil
	}
	if !storage.IsSupportedOperationAudit(v.OperationType) {
		return storage.ErrUnsupportedOperationAudit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.operationAudits[v.ID]; ok {
		if old.Status == "submitted" && v.Status != "submitted" {
			old.AfterSummary = v.AfterSummary
			old.ErrorSummary = v.ErrorSummary
			old.HTTPStatus = v.HTTPStatus
			old.Status = v.Status
			old.UpdatedAt = v.UpdatedAt
			s.operationAudits[v.ID] = old
		}
		return nil
	}
	s.operationAudits[v.ID] = v
	return nil
}

func (s *MemoryStore) UpdateOperationAuditHTTPStatus(requestID string, status int) error {
	if requestID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, audit := range s.operationAudits {
		if audit.RequestID == requestID {
			audit.HTTPStatus = status
			s.operationAudits[id] = audit
		}
	}
	return nil
}

func (s *MemoryStore) QueryOperationAudits(q storage.OperationAuditQuery) (storage.OperationAuditPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []storage.OperationAudit
	for _, v := range s.operationAudits {
		if !storage.IsManualOperationAudit(v) {
			continue
		}
		if q.InstanceID != "" && v.InstanceID != q.InstanceID {
			continue
		}
		if q.SiteID != "" && v.InstanceID != q.SiteID {
			instance, ok := s.instances[v.InstanceID]
			if !ok || (instance.SiteID != q.SiteID && !(instance.SiteID == "" && instance.ID == q.SiteID)) {
				continue
			}
		}
		if q.OperationType != "" && !storage.OperationAuditTypeMatchesFilter(v.OperationType, q.OperationType) {
			continue
		}
		if q.Actor != "" && !strings.Contains(strings.ToLower(v.ActorID), strings.ToLower(q.Actor)) {
			continue
		}
		if q.Actor != "" && q.ActorExact && !strings.EqualFold(v.ActorID, q.Actor) {
			continue
		}
		if q.RequestID != "" && v.RequestID != q.RequestID {
			continue
		}
		if q.CorrelationID != "" && v.CorrelationID != q.CorrelationID {
			continue
		}
		if q.Status != "" && v.Status != q.Status && !((q.Status == "success" || q.Status == "succeeded") && (v.Status == "success" || v.Status == "succeeded")) {
			continue
		}
		if q.Source != "" && v.SourceComponent != q.Source {
			continue
		}
		if q.Trigger != "" && v.TriggerType != q.Trigger {
			continue
		}
		if !q.From.IsZero() && v.CreatedAt.Before(q.From) {
			continue
		}
		if !q.To.IsZero() && !v.CreatedAt.Before(q.To) {
			continue
		}
		if q.Search != "" {
			needle := strings.ToLower(q.Search)
			if !strings.Contains(strings.ToLower(v.OperationType+" "+v.TargetType+" "+v.TargetID+" "+v.ActorID+" "+v.ErrorSummary+" "+v.RequestID+" "+v.CorrelationID), needle) {
				continue
			}
		}
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].ID > all[j].ID
		}
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})
	limit, offset := storage.NormalizeCommandPagination(q.Limit, q.Offset)
	page := storage.OperationAuditPage{Total: int64(len(all)), OperationTypes: storage.OperationAuditFilterTypes()}
	if q.ActorOptions {
		actors := map[string]bool{}
		for _, v := range all {
			if v.ActorID != "" {
				actors[v.ActorID] = true
			}
		}
		page.Actors = []string{}
		for actor := range actors {
			page.Actors = append(page.Actors, actor)
		}
		sort.Strings(page.Actors)
		if len(page.Actors) > 100 {
			page.Actors = page.Actors[:100]
		}
		return page, nil
	}
	if offset >= len(all) {
		page.Items = []storage.OperationAudit{}
		return page, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	page.Items = append([]storage.OperationAudit(nil), all[offset:end]...)
	return page, nil
}

// PruneBefore deletes rows strictly older than cutoff. Audits, alerts,
// deliveries and commands are intentionally retained for their audit value.
func (s *MemoryStore) PruneBefore(kind string, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.pruneNginx(kind, cutoff); ok {
		return n, nil
	}
	var n int64
	switch kind {
	case "alerts", "alert_events", "notification_deliveries":
		// Alert history lives in the MySQL store; the in-memory store keeps
		// none of it, so pruning is a no-op here.
		return 0, nil
	case "log_events":
		for k, v := range s.logEvents {
			if v.CreatedAt.Before(cutoff) {
				delete(s.logEvents, k)
				n++
			}
		}
	case "log_samples":
		for k, v := range s.logSamples {
			if v.CreatedAt.Before(cutoff) {
				delete(s.logSamples, k)
				n++
			}
		}
	case "server_metrics":
		var out []storage.ServerMetric
		for _, v := range s.serverMetrics {
			if v.CollectedAt.Before(cutoff) {
				n++
			} else {
				out = append(out, v)
			}
		}
		s.serverMetrics = out
	case "health_checks":
		var out []storage.HealthCheck
		for _, v := range s.healthChecks {
			if v.CheckedAt.Before(cutoff) {
				n++
			} else {
				out = append(out, v)
			}
		}
		s.healthChecks = out
	case "docker_statuses":
		var out []storage.DockerStatus
		for _, v := range s.dockerStatuses {
			if v.CollectedAt.Before(cutoff) {
				n++
			} else {
				out = append(out, v)
			}
		}
		s.dockerStatuses = out
	case "metric_1m":
		for k, v := range s.metrics1m {
			if v.BucketTime.Before(cutoff) {
				delete(s.metrics1m, k)
				n++
			}
		}
	case "metric_5m":
		for k, v := range s.metrics5m {
			if v.BucketTime.Before(cutoff) {
				delete(s.metrics5m, k)
				n++
			}
		}
	default:
		return 0, errors.New("unknown prune kind")
	}
	return n, nil
}
