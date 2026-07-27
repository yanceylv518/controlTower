package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestSiteOfFallsBackToInstanceID(t *testing.T) {
	if got := siteOf(storage.Instance{ID: "inst-a"}); got != "inst-a" {
		t.Fatalf("unexpected fallback: %s", got)
	}
	if got := siteOf(storage.Instance{ID: "inst-a", SiteID: "site-a"}); got != "site-a" {
		t.Fatalf("unexpected explicit site: %s", got)
	}
}

func TestInstanceIDsForRequestUsesSiteAndInstancePrecedence(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	_ = store.CreateInstance(storage.Instance{ID: "a-1", SiteID: "site-a", CreatedAt: now, UpdatedAt: now})
	_ = store.CreateInstance(storage.Instance{ID: "a-2", SiteID: "site-a", CreatedAt: now, UpdatedAt: now})
	_ = store.CreateInstance(storage.Instance{ID: "legacy", CreatedAt: now, UpdatedAt: now})
	h := NewHandler(nil).WithInstanceStore(store)
	ids, err := h.instanceIDsForRequest("", "site-a")
	if err != nil || len(ids) != 2 {
		t.Fatalf("unexpected site expansion: %v %v", ids, err)
	}
	ids, err = h.instanceIDsForRequest("a-2", "site-a")
	if err != nil || len(ids) != 1 || ids[0] != "a-2" {
		t.Fatalf("instance_id should win: %v %v", ids, err)
	}
	ids, err = h.instanceIDsForRequest("", "missing")
	if err != nil || len(ids) != 0 {
		t.Fatalf("missing site must expand to empty: %v %v", ids, err)
	}
}
