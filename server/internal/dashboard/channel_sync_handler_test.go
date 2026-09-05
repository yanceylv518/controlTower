package dashboard

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"controltower/server/internal/channelupdates"
)

type refreshingTuningStore struct {
	tuningStub
	site string
}

func (s *refreshingTuningStore) RefreshChannels(_ context.Context, site, actor string) error {
	s.site = site
	return nil
}

func TestRefreshChannelsCallsLiveSource(t *testing.T) {
	s := &refreshingTuningStore{}
	h := NewHandler(nil).WithTuningStore(s)
	w := httptest.NewRecorder()
	h.HandleRefreshTuningChannels(w, httptest.NewRequest("POST", "/api/dashboard/tuning/channels/refresh?site_id=site", nil))
	if w.Code != 200 || s.site != "site" {
		t.Fatalf("refresh did not reach live source: %d %s", w.Code, w.Body)
	}
}

func TestChannelChangesWakeAfterWrite(t *testing.T) {
	after, _ := channelupdates.Listen()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(nil).HandleTuningChannelChanges(w, httptest.NewRequest("GET", "/api/dashboard/tuning/channels/changes?site_id=site&after="+after, nil).WithContext(ctx))
		close(done)
	}()
	// Notification before or after the handler starts must both be delivered.
	channelupdates.Notify()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("write notification was lost")
	}
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("missing notification: %d %s", w.Code, w.Body)
	}
}
