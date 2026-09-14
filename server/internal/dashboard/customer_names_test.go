package dashboard

import (
	"context"
	"controltower/server/internal/aggregator"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

type customerNameFunc func(context.Context, string) (map[int64]string, error)

func (f customerNameFunc) CustomerNames(ctx context.Context, site string) (map[int64]string, error) {
	return f(ctx, site)
}

type customerInstanceSource struct{ nameSourceFake }

func (f *customerInstanceSource) InstanceByID(id string) (storage.Instance, bool, error) {
	site := "site-a"
	if id == "other" {
		site = "site-b"
	}
	if id == "missing" {
		return storage.Instance{}, false, nil
	}
	return storage.Instance{ID: id, SiteID: site}, true, nil
}

func TestCustomerDirectoryWithoutLogsAndSiteIsolation(t *testing.T) {
	source := &customerInstanceSource{nameSourceFake{calls: map[string]int{}}}
	calls := map[string]int{}
	profile := customerNameFunc(func(ctx context.Context, site string) (map[int64]string, error) {
		calls[site]++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("missing bounded deadline")
		}
		return map[int64]string{104: site + "-alice", 105: site + "-bob", 12: "current-name"}, nil
	})
	h := NewHandler(nil).WithNameSource(source).WithCustomerNameSource(profile)
	for _, id := range []string{"inst", "sibling", "other"} {
		want := "site-a-alice"
		if id == "other" {
			want = "site-b-alice"
		}
		if got := h.displayDimensionName("instance_user", id+":user:104"); got != want {
			t.Fatalf("%s: %q", id, got)
		}
	}
	if got := h.displayDimensionName("instance_user", "inst:user:105"); got != "site-a-bob" {
		t.Fatal(got)
	}
	if got := h.displayDimensionName("instance_user", "inst:user:12"); got != "current-name" {
		t.Fatal(got)
	}
	if source.calls["user_batch"] != 0 || calls["site-a"] != 1 || calls["site-b"] != 1 {
		t.Fatalf("calls: %v %v", source.calls, calls)
	}
	if got := h.names.UserName("inst", 999); got != "用户 999" {
		t.Fatal(got)
	}
	if got := h.names.UserName("missing", 104); got != "用户 104" {
		t.Fatal(got)
	}
	if calls[""] != 0 {
		t.Fatal("unknown instance must not select a source site")
	}
}

func TestCustomerDirectoryRefreshFailureRecoveryAndFallback(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	names := map[int64]string{104: "alice"}
	fail := false
	calls := 0
	h := NewHandler(nil).WithNameSource(&nameSourceFake{calls: map[string]int{}}).WithCustomerNameSource(customerNameFunc(func(context.Context, string) (map[int64]string, error) {
		calls++
		if fail {
			return nil, errors.New("offline")
		}
		return maps.Clone(names), nil
	}))
	h.names.now = func() time.Time { return now }
	if got := h.names.UserName("inst", 104); got != "alice" {
		t.Fatal(got)
	}
	names[104] = "renamed"
	now = now.Add(4 * time.Minute)
	if got := h.names.UserName("inst", 104); got != "alice" || calls != 1 {
		t.Fatalf("%s calls=%d", got, calls)
	}
	now = now.Add(time.Minute)
	if got := h.names.UserName("inst", 104); got != "renamed" || calls != 2 {
		t.Fatalf("%s calls=%d", got, calls)
	}
	fail = true
	now = now.Add(5 * time.Minute)
	for i := 0; i < 100; i++ {
		if got := h.names.UserName("inst", 104); got != "renamed" {
			t.Fatal(got)
		}
	}
	if calls != 3 {
		t.Fatalf("failure queries=%d", calls)
	}
	// Users absent from the directory retain the existing log-name fallback.
	if got := h.names.UserName("inst", 12); got != "张三" {
		t.Fatal(got)
	}
	fail = false
	names = map[int64]string{105: "new-user"}
	now = now.Add(time.Minute)
	if got := h.names.UserName("inst", 105); got != "new-user" {
		t.Fatal(got)
	}
	if got := h.names.UserName("inst", 104); got != "用户 104" {
		t.Fatal("removed profile leaked:", got)
	}
}

func TestCustomerDirectoryColdFailureIsCached(t *testing.T) {
	calls := 0
	d := customerNameDirectory{sites: map[string]*customerNameSnapshot{}, source: customerNameFunc(func(context.Context, string) (map[int64]string, error) { calls++; return nil, errors.New("offline") })}
	now := time.Now()
	for id := int64(1); id <= 100; id++ {
		if got := d.name("site", id, now); got != "" {
			t.Fatal(got)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	d.name("site", 1, now.Add(time.Minute))
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestCustomerDirectoryConcurrentLoadsCoalesceWithoutBlockingOtherSites(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	d := customerNameDirectory{sites: map[string]*customerNameSnapshot{}, source: customerNameFunc(func(ctx context.Context, site string) (map[int64]string, error) {
		if site == "slow" {
			if calls.Add(1) == 1 {
				close(entered)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return map[int64]string{104: site}, nil
	})}
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := d.name("slow", 104, now); got != "slow" {
				t.Errorf("got %q", got)
			}
		}()
	}
	<-entered
	done := make(chan string, 1)
	go func() { done <- d.name("fast", 104, now) }()
	select {
	case got := <-done:
		if got != "fast" {
			t.Error(got)
		}
	case <-time.After(time.Second):
		t.Error("one site blocked another")
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}

func TestCustomerNamesRequiresConfiguredSource(t *testing.T) {
	h := &PassthroughHandler{}
	if _, err := h.CustomerNames(context.Background(), "unconfigured"); err == nil {
		t.Fatal("missing source cannot replace a successful snapshot")
	}
}

func TestCustomerDirectoryNamesRespectViewerScope(t *testing.T) {
	store := ingest.NewMemoryStore()
	for _, instance := range []storage.Instance{{ID: "a", SiteID: "allowed"}, {ID: "b", SiteID: "other"}} {
		if err := store.CreateInstance(instance); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateUser(storage.User{ID: 1, Username: "viewer", Role: "viewer", ScopeSite: "allowed", ScopeUserIDs: []int64{104}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(storage.Session{ID: "customer-names-session", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	source := &scopedCustomerChannelSource{metricSourceStub: metricSourceStub{metrics: []aggregator.Metric{
		{InstanceID: "a", BucketTime: time.Now(), DimensionType: "instance_user", DimensionKey: "a:user:104", TPM: 10},
		{InstanceID: "a", BucketTime: time.Now(), DimensionType: "instance_user", DimensionKey: "a:user:105", TPM: 20},
		{InstanceID: "b", BucketTime: time.Now(), DimensionType: "instance_user", DimensionKey: "b:user:104", TPM: 30},
	}}}
	h := NewHandler(nil).WithMetricSource(source).WithInstanceStore(store).WithNameSource(store).WithCustomerNameSource(customerNameFunc(func(_ context.Context, site string) (map[int64]string, error) {
		if site != "allowed" {
			t.Errorf("queried unauthorized site %s", site)
		}
		return map[int64]string{104: "visible-name", 105: "hidden-name"}, nil
	}))
	for _, aggregate := range []string{"false", "true"} {
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance_user&dimension_key_prefix=a:user:&instance_id=b&site=other&aggregate="+aggregate, nil)
		req.AddCookie(&http.Cookie{Name: "ct_session", Value: "customer-names-session"})
		rr := httptest.NewRecorder()
		ctauth.RequireSessionOrToken(ctauth.NewManager(store, time.Hour), "", http.HandlerFunc(h.HandleMetricHistory)).ServeHTTP(rr, req)
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.Contains(body, `"display_name":"visible-name"`) || strings.Contains(body, "hidden-name") || strings.Contains(body, "b:user:104") {
			t.Fatalf("scope leaked: %d %s", rr.Code, body)
		}
	}
}

func TestModelCustomerHistoryUsesCustomerDirectory(t *testing.T) {
	source := &metricSourceStub{metrics: []aggregator.Metric{{InstanceID: "inst", BucketTime: time.Now(), DimensionType: "instance_model_user", DimensionKey: "inst:model:provider:user:inner:kimi:user:104", TPM: 42}}}
	h := NewHandler(nil).WithMetricSource(source).WithNameSource(&nameSourceFake{calls: map[string]int{}}).WithCustomerNameSource(customerNameFunc(func(context.Context, string) (map[int64]string, error) {
		return map[int64]string{104: "account-name"}, nil
	}))
	for _, aggregate := range []string{"true", "false"} {
		rr := httptest.NewRecorder()
		h.HandleMetricHistory(rr, httptest.NewRequest(http.MethodGet, "/api/dashboard/metric-history?dimension_type=instance_model_user&dimension_key_prefix=inst:model:provider:user:inner:kimi:user:&hours=1&aggregate="+aggregate, nil))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"display_name":"account-name"`) || !strings.Contains(rr.Body.String(), `"tpm":42`) {
			t.Fatalf("%d %s", rr.Code, rr.Body.String())
		}
	}
	if got := h.displayDimensionName("instance_model_user", "inst:model:kimi:user:999"); got != "用户 999" {
		t.Fatal(got)
	}
	for _, key := range []string{"inst:model:kimi:user:bad", "inst:model:kimi:user:0", "inst:model::user:104"} {
		if got := h.displayDimensionName("instance_model_user", key); got != key {
			t.Fatal(got)
		}
	}
	if got := h.displayDimensionName("instance_user_model", "inst:user:104:model:kimi"); got != "inst:user:104:model:kimi" {
		t.Fatal(got)
	}
}
