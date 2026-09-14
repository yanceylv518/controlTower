package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/aggregator"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
)

func TestCustomerChannelHistoryNames(t *testing.T) {
	source := &metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "inst", BucketTime: time.Now().UTC(), DimensionType: "instance_user_channel", DimensionKey: "inst:user:12:channel:5", TPM: 1234},
	}}
	h := NewHandler(nil).WithMetricSource(source).WithNameSource(&nameSourceFake{calls: map[string]int{}})
	rr := httptest.NewRecorder()
	h.HandleMetricHistory(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance_user_channel&dimension_key_prefix=inst:user:12:channel:&hours=1", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"display_name":"主渠道"`) || !strings.Contains(rr.Body.String(), `"tpm":1234`) {
		t.Fatalf("unexpected history: %d %s", rr.Code, rr.Body.String())
	}
	if got := h.displayDimensionName("instance_user_channel", "inst:user:12:channel:99"); got != "渠道 99" {
		t.Fatalf("fallback=%q", got)
	}
	if id, ok := metricUserID("inst:user:12:channel:5"); !ok || id != 12 {
		t.Fatalf("invalid scoped user: %d %v", id, ok)
	}
}

type scopedCustomerChannelSource struct {
	metricSourceStub
	instances []string
}

func (s *scopedCustomerChannelSource) QueryMetricHistoryPrefixForInstances(_ string, _ string, _ string, ids []string, _ time.Time) ([]aggregator.Metric, error) {
	s.instances = ids
	var rows []aggregator.Metric
	for _, row := range s.metrics {
		for _, id := range ids {
			if row.InstanceID == id {
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

func TestCustomerChannelViewerCannotReadOtherUsersOrSites(t *testing.T) {
	store := ingest.NewMemoryStore()
	if err := store.CreateInstance(storage.Instance{ID: "a", SiteID: "allowed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(storage.Instance{ID: "b", SiteID: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(storage.User{ID: 1, Username: "viewer", Role: "viewer", ScopeSite: "allowed", ScopeUserIDs: []int64{7}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(storage.Session{ID: "customer-channel-session", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	source := &scopedCustomerChannelSource{metricSourceStub: metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "a", BucketTime: time.Now(), DimensionType: "instance_user_channel", DimensionKey: "a:user:7:channel:5", TPM: 10},
		{InstanceID: "a", BucketTime: time.Now(), DimensionType: "instance_user_channel", DimensionKey: "a:user:8:channel:5", TPM: 20},
		{InstanceID: "b", BucketTime: time.Now(), DimensionType: "instance_user_channel", DimensionKey: "b:user:7:channel:5", TPM: 30},
	}}}
	h := NewHandler(nil).WithMetricSource(source).WithInstanceStore(store)
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance_user_channel&dimension_key_prefix=a:user:&instance_id=b&site=other", nil)
	req.AddCookie(&http.Cookie{Name: "ct_session", Value: "customer-channel-session"})
	rr := httptest.NewRecorder()
	ctauth.RequireSessionOrToken(ctauth.NewManager(store, time.Hour), "", http.HandlerFunc(h.HandleMetricHistory)).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || len(source.instances) != 1 || source.instances[0] != "a" || !strings.Contains(rr.Body.String(), "a:user:7:channel:5") || strings.Contains(rr.Body.String(), "a:user:8") || strings.Contains(rr.Body.String(), "b:user:7") {
		t.Fatalf("scope leaked: instances=%v status=%d body=%s", source.instances, rr.Code, rr.Body.String())
	}
}
