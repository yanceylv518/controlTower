package dashboard

import (
	"testing"

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
