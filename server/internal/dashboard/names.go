package dashboard

import (
	"fmt"
	"sync"
	"time"

	"controltower/server/internal/storage"
)

type NameSource interface {
	InstanceByID(string) (storage.Instance, bool, error)
	QueryChannelSnapshots(storage.ChannelSnapshotQuery) ([]storage.ChannelSnapshot, error)
	QueryLogEvents(storage.LogQuery) ([]storage.LogEvent, error)
}

type nameEntry struct {
	value     string
	expiresAt time.Time
}

type nameResolver struct {
	source NameSource
	ttl    time.Duration
	now    func() time.Time
	mu     sync.Mutex
	cache  map[string]nameEntry
}

func newNameResolver(source NameSource, ttl time.Duration) *nameResolver {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &nameResolver{source: source, ttl: ttl, now: time.Now, cache: make(map[string]nameEntry)}
}

func (r *nameResolver) resolve(key string, fallback string, load func() (string, error)) string {
	if r == nil || r.source == nil {
		return fallback
	}
	r.mu.Lock()
	if entry, ok := r.cache[key]; ok && r.now().Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.value
	}
	r.mu.Unlock()
	value, err := load()
	if err != nil || value == "" {
		value = fallback
	}
	r.mu.Lock()
	r.cache[key] = nameEntry{value: value, expiresAt: r.now().Add(r.ttl)}
	r.mu.Unlock()
	return value
}

func (r *nameResolver) InstanceName(instanceID string) string {
	return r.resolve("instance:"+instanceID, instanceID, func() (string, error) {
		item, ok, err := r.source.InstanceByID(instanceID)
		if err != nil || !ok {
			return "", err
		}
		return item.Name, nil
	})
}

func (r *nameResolver) ChannelName(instanceID string, channelID int64) string {
	fallback := fmt.Sprintf("渠道 %d", channelID)
	return r.resolve(fmt.Sprintf("channel:%s:%d", instanceID, channelID), fallback, func() (string, error) {
		items, err := r.source.QueryChannelSnapshots(storage.ChannelSnapshotQuery{InstanceID: instanceID, ChannelID: channelID, LatestOnly: true, Limit: 1})
		if err != nil || len(items) == 0 {
			return "", err
		}
		return items[0].ChannelName, nil
	})
}

func (r *nameResolver) UserName(instanceID string, userID int64) string {
	fallback := fmt.Sprintf("用户 %d", userID)
	return r.resolve(fmt.Sprintf("user:%s:%d", instanceID, userID), fallback, func() (string, error) {
		items, err := r.source.QueryLogEvents(storage.LogQuery{InstanceID: instanceID, UserID: userID, Limit: 1})
		if err != nil || len(items) == 0 {
			return "", err
		}
		return items[0].Username, nil
	})
}
