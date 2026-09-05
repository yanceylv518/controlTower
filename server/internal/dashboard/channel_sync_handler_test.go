package dashboard

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/channelupdates"
	"controltower/server/internal/tuning"
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
	after, _ := channelupdates.Listen("site")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(nil).HandleTuningChannelChanges(w, httptest.NewRequest("GET", "/api/dashboard/tuning/channels/changes?site_id=site&after="+after, nil).WithContext(ctx))
		close(done)
	}()
	// Notification before or after the handler starts must both be delivered.
	channelupdates.Notify("site")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("write notification was lost")
	}
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("missing notification: %d %s", w.Code, w.Body)
	}
}

type agentOnlyTuningStore struct{ tuningStub }

func (*agentOnlyTuningStore) RefreshChannels(context.Context, string, string) error {
	return tuning.ErrDirectControlNotConfigured
}

// Agent-managed sites are a valid configuration: the page must be able to tell
// "nothing to refresh" apart from a failed new-api call.
func TestRefreshChannelsReportsAgentOnlySiteAsNotConfigured(t *testing.T) {
	h := NewHandler(nil).WithTuningStore(&agentOnlyTuningStore{})
	w := httptest.NewRecorder()
	h.HandleRefreshTuningChannels(w, httptest.NewRequest("POST", "/api/dashboard/tuning/channels/refresh?site_id=site", nil))
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"direct_control_not_configured"`) {
		t.Fatalf("agent-only site reported as failure: %d %s", w.Code, w.Body)
	}
}
