package ingest

import (
	"context"
	"controltower/server/internal/storage"
	"errors"
)

func (s *MemoryStore) LoadRequestMonitorRules(context.Context) (storage.RequestMonitorRules, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requestMonitorRules.Version == 0 {
		return storage.DefaultRequestMonitorRules(), nil
	}
	return s.requestMonitorRules, nil
}
func (s *MemoryStore) SaveRequestMonitorRules(_ context.Context, c storage.RequestMonitorRules, _ string) error {
	if !c.Valid() {
		return errors.New("invalid request monitor rules")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requestMonitorRules.Version != c.Version {
		return storage.ErrRequestRulesConflict
	}
	c.Version++
	s.requestMonitorRules = c
	return nil
}
