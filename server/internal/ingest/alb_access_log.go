package ingest

import (
	"context"
	"controltower/server/internal/alblog"
)

func (s *MemoryStore) LoadALBLogConfig(context.Context) (alblog.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.albLogConfig.Version == 0 {
		return alblog.Config{}, alblog.ErrMissing
	}
	return cloneALBConfig(s.albLogConfig), nil
}
func (s *MemoryStore) SaveALBLogConfig(_ context.Context, c alblog.Config, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.albLogConfig.Version != c.Version {
		return alblog.ErrConflict
	}
	c.Version++
	s.albLogConfig = cloneALBConfig(c)
	return nil
}
func cloneALBConfig(c alblog.Config) alblog.Config {
	if c.LastTest != nil {
		r := *c.LastTest
		c.LastTest = &r
	}
	return c
}
