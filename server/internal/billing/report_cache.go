package billing

import (
	"sort"
	"strconv"
	"strings"
	"sync"
)

type summaryCacheEntry struct {
	items []UserSummary
	total SummaryTotal
}

type SummaryCache struct {
	mu      sync.RWMutex
	entries map[string]summaryCacheEntry
}

func NewSummaryCache() *SummaryCache {
	return &SummaryCache{entries: map[string]summaryCacheEntry{}}
}

var MonthlySummaryCache = NewSummaryCache()

func SummaryCacheKey(instanceID, month string, userIDs []int64) string {
	ids := append([]int64(nil), userIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return instanceID + "\x00" + month + "\x00" + strings.Join(parts, ",")
}

func (c *SummaryCache) Get(key string) ([]UserSummary, SummaryTotal, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil, SummaryTotal{}, false
	}
	return cloneSummaries(entry.items), entry.total, true
}

func (c *SummaryCache) Put(key string, items []UserSummary, total SummaryTotal) {
	c.mu.Lock()
	c.entries[key] = summaryCacheEntry{items: cloneSummaries(items), total: total}
	c.mu.Unlock()
}

func (c *SummaryCache) InvalidateInstance(instanceID string) {
	prefix := instanceID + "\x00"
	c.mu.Lock()
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

func cloneSummaries(items []UserSummary) []UserSummary {
	out := make([]UserSummary, len(items))
	copy(out, items)
	for i := range out {
		out[i].UnpricedModels = append([]string(nil), out[i].UnpricedModels...)
		out[i].PriceSources = append([]string(nil), out[i].PriceSources...)
	}
	return out
}
