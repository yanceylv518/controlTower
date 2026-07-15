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

type bulkNameSource interface {
	ChannelNames(instanceID string) (map[int64]string, error)
	UserNames(instanceID string, since time.Time) (map[int64]string, error)
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
	if bulk, ok := r.source.(bulkNameSource); ok {
		r.preloadChannels(instanceID, bulk)
		return r.cachedValue(fmt.Sprintf("channel:%s:%d", instanceID, channelID), fallback)
	}
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
	if bulk, ok := r.source.(bulkNameSource); ok {
		r.preloadUsers(instanceID, bulk)
		return r.cachedValue(fmt.Sprintf("user:%s:%d", instanceID, userID), fallback)
	}
	return r.resolve(fmt.Sprintf("user:%s:%d", instanceID, userID), fallback, func() (string, error) {
		items, err := r.source.QueryLogEvents(storage.LogQuery{InstanceID: instanceID, UserID: userID, Limit: 1})
		if err != nil || len(items) == 0 {
			return "", err
		}
		return items[0].Username, nil
	})
}

func (r *nameResolver) cachedValue(key, fallback string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.cache[key]; ok && r.now().Before(entry.expiresAt) {
		return entry.value
	}
	return fallback
}

func (r *nameResolver) preloadChannels(instanceID string, source bulkNameSource) {
	r.preload("channels:"+instanceID, func() (map[string]string, error) {
		items, err := source.ChannelNames(instanceID)
		values := make(map[string]string, len(items))
		for id, name := range items {
			values[fmt.Sprintf("channel:%s:%d", instanceID, id)] = name
		}
		return values, err
	})
}

func (r *nameResolver) preloadUsers(instanceID string, source bulkNameSource) {
	r.preload("users:"+instanceID, func() (map[string]string, error) {
		items, err := source.UserNames(instanceID, r.now().Add(-24*time.Hour))
		values := make(map[string]string, len(items))
		for id, name := range items {
			values[fmt.Sprintf("user:%s:%d", instanceID, id)] = name
		}
		return values, err
	})
}

func (r *nameResolver) preload(marker string, load func() (map[string]string, error)) {
	if r == nil || r.source == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.cache[marker]; ok && r.now().Before(entry.expiresAt) {
		return
	}
	values, err := load()
	expires := r.now().Add(r.ttl)
	if err == nil {
		for key, value := range values {
			if value != "" {
				r.cache[key] = nameEntry{value: value, expiresAt: expires}
			}
		}
	}
	r.cache[marker] = nameEntry{value: "loaded", expiresAt: expires}
}
