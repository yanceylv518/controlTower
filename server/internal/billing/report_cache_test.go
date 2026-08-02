package billing

import "testing"

func TestSummaryCacheScopesAndInvalidatesByInstance(t *testing.T) {
	cache := NewSummaryCache()
	keyA := SummaryCacheKey("site-a", "2026-08", []int64{9, 7})
	keyAReordered := SummaryCacheKey("site-a", "2026-08", []int64{7, 9})
	keyB := SummaryCacheKey("site-b", "2026-08", nil)
	cache.Put(keyA, []UserSummary{{UserID: 7, UnpricedModels: []string{"m"}}}, SummaryTotal{Users: 1})
	cache.Put(keyB, []UserSummary{{UserID: 8}}, SummaryTotal{Users: 1})
	items, _, ok := cache.Get(keyAReordered)
	if !ok || len(items) != 1 {
		t.Fatal("scoped cache miss")
	}
	items[0].UnpricedModels[0] = "changed"
	again, _, _ := cache.Get(keyA)
	if again[0].UnpricedModels[0] != "m" {
		t.Fatal("cache returned mutable shared data")
	}
	cache.InvalidateInstance("site-a")
	if _, _, ok = cache.Get(keyA); ok {
		t.Fatal("site-a entry was not invalidated")
	}
	if _, _, ok = cache.Get(keyB); !ok {
		t.Fatal("site-b entry must remain cached")
	}
}
