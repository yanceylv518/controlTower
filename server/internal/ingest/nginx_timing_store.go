package ingest

import (
	"fmt"
	"sort"
	"time"

	"controltower/server/internal/storage"
)

func (s *MemoryStore) UpsertNginxTimingBucket(v storage.NginxTimingBucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nginxTimingBuckets[fmt.Sprintf("%s:%d", v.InstanceID, v.BucketAt.Unix())] = v
	return nil
}
func (s *MemoryStore) InsertNginxSlowSample(v storage.NginxSlowSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.nginxSlowSamples {
		if old.InstanceID == v.InstanceID && old.OccurredAt.Equal(v.OccurredAt) && old.Path == v.Path && old.Status == v.Status {
			v.ID = old.ID
			s.nginxSlowSamples[i] = v
			return nil
		}
	}
	v.ID = int64(len(s.nginxSlowSamples) + 1)
	s.nginxSlowSamples = append(s.nginxSlowSamples, v)
	return nil
}
func (s *MemoryStore) QueryNginxTiming(q storage.NginxTimingQuery) ([]storage.NginxTimingBucket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.NginxTimingBucket
	for _, v := range s.nginxTimingBuckets {
		if v.InstanceID == q.InstanceID && !v.BucketAt.Before(q.Since) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BucketAt.Before(out[j].BucketAt) })
	return out, nil
}
func (s *MemoryStore) QueryNginxSlowSamples(q storage.NginxSlowSampleQuery) ([]storage.NginxSlowSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []storage.NginxSlowSample
	for _, v := range s.nginxSlowSamples {
		if v.InstanceID == q.InstanceID && !v.OccurredAt.Before(q.Since) && (q.RequestID == "" || v.RequestID == q.RequestID) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OccurredAt.After(out[j].OccurredAt) })
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

func (s *MemoryStore) QueryRequestDimensions(instanceID string, requestIDs []string) ([]storage.RequestDimension, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wanted := make(map[string]struct{}, len(requestIDs))
	for _, id := range requestIDs {
		wanted[id] = struct{}{}
	}
	var out []storage.RequestDimension
	for _, v := range s.logSamples {
		if v.InstanceID == instanceID {
			if _, ok := wanted[v.RequestID]; ok {
				out = append(out, storage.RequestDimension{Source: "sample", InstanceID: instanceID, RequestID: v.RequestID, SourceLogID: v.SourceLogID, UserID: v.UserID, Username: v.Username, ChannelID: v.ChannelID, ModelName: v.ModelName, TokenName: v.TokenName})
			}
		}
	}
	for _, v := range s.logEvents {
		if v.InstanceID == instanceID {
			if _, ok := wanted[v.RequestID]; ok {
				out = append(out, storage.RequestDimension{Source: "event", InstanceID: instanceID, RequestID: v.RequestID, SourceLogID: v.SourceLogID, UserID: v.UserID, Username: v.Username, ChannelID: v.ChannelID, ModelName: v.ModelName, TokenName: v.TokenName})
			}
		}
	}
	return out, nil
}
func (s *MemoryStore) pruneNginx(kind string, cutoff time.Time) (int64, bool) {
	var n int64
	if kind == "nginx_timing_1m" {
		for k, v := range s.nginxTimingBuckets {
			if v.BucketAt.Before(cutoff) {
				delete(s.nginxTimingBuckets, k)
				n++
			}
		}
		return n, true
	}
	if kind == "nginx_slow_samples" {
		var out []storage.NginxSlowSample
		for _, v := range s.nginxSlowSamples {
			if v.OccurredAt.Before(cutoff) {
				n++
			} else {
				out = append(out, v)
			}
		}
		s.nginxSlowSamples = out
		return n, true
	}
	return 0, false
}
