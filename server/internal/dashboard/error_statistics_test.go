package dashboard

import (
	"context"
	es "controltower/internal/errorstats"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/ingest"
	"controltower/server/internal/storage"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type errorStatisticsSourceStub struct {
	metricSourceStub
	calls int
	query es.Query
}

func (s *errorStatisticsSourceStub) QueryErrorStatistics(_ context.Context, q es.Query) (es.Result, error) {
	s.calls++
	s.query = q
	return es.Result{}, nil
}
func TestErrorStatisticsViewerIsolation(t *testing.T) {
	store := ingest.NewMemoryStore()
	for _, i := range []storage.Instance{{ID: "a", SiteID: "allowed"}, {ID: "b", SiteID: "other"}} {
		if err := store.CreateInstance(i); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateUser(storage.User{ID: 1, Username: "viewer", Role: "viewer", ScopeSite: "allowed", ScopeUserIDs: []int64{7}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(storage.Session{ID: "stats-viewer", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	source := &errorStatisticsSourceStub{}
	h := NewHandler(nil).WithMetricSource(source).WithInstanceStore(store)
	protected := ctauth.RequireSessionOrToken(ctauth.NewManager(store, time.Hour), "", http.HandlerFunc(h.HandleErrorStatistics))
	for _, tc := range []struct {
		dimension, key string
		status         int
	}{{"instance_user", "a:user:7", 200}, {"instance_user", "a:user:8", 403}, {"instance_user", "b:user:7", 403}, {"instance_channel", "a:channel:7", 403}, {"instance_user", "a:user:7:channel:2", 403}} {
		before := source.calls
		r := httptest.NewRequest("GET", "/api/dashboard/error-statistics?hours=1&dimension_type="+tc.dimension+"&dimension_key="+tc.key+"&site=other", nil)
		r.AddCookie(&http.Cookie{Name: "ct_session", Value: "stats-viewer"})
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s => %d %s", tc.key, w.Code, w.Body.String())
		}
		if tc.status != 200 && source.calls != before {
			t.Fatal("unauthorized query reached store")
		}
		if tc.status == 200 && (source.query.InstanceID != "a" || source.query.Key != "7" || source.query.Channels) {
			t.Fatalf("scope leaked: %+v", source.query)
		}
	}
}

func TestErrorStatisticsTimelineValidation(t *testing.T) {
	source := &errorStatisticsSourceStub{}
	h := NewHandler(nil).WithMetricSource(source)
	now := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	base := "/api/dashboard/error-statistics?hours=1&dimension_type=instance_user&dimension_key=a:user:7&instance_id=a&timeline=true&bucket=1m&start_time=" + now.Add(-time.Hour).Format(time.RFC3339) + "&end_time=" + now.Format(time.RFC3339)
	w := httptest.NewRecorder()
	h.HandleErrorStatistics(w, httptest.NewRequest("GET", base, nil))
	if w.Code != 200 || !source.query.Timeline || source.query.BucketSeconds != 60 || !source.query.Until.Equal(now) {
		t.Fatalf("query %s %+v", w.Body.String(), source.query)
	}
	fiveEnd := time.Now().UTC().Truncate(5 * time.Minute).Add(5 * time.Minute)
	five := "/api/dashboard/error-statistics?hours=1&dimension_type=instance_user&dimension_key=a:user:7&timeline=true&bucket=5m&start_time=" + fiveEnd.Add(-time.Hour).Format(time.RFC3339) + "&end_time=" + fiveEnd.Format(time.RFC3339)
	w = httptest.NewRecorder()
	h.HandleErrorStatistics(w, httptest.NewRequest("GET", five, nil))
	if w.Code != 200 || source.query.BucketSeconds != 300 {
		t.Fatal("current 5m interval rejected", w.Code, w.Body.String())
	}

	for _, bad := range []string{strings.Replace(base, "instance_id=a", "instance_id=b", 1), strings.Replace(base, "bucket=1m", "bucket=bad", 1), strings.Replace(base, "a:user:7", "a:user:7:channel:2", 1), strings.Replace(base, now.Format(time.RFC3339), now.Add(time.Second).Format(time.RFC3339), 1), strings.Replace(base, now.Format(time.RFC3339), now.Add(time.Nanosecond).Format(time.RFC3339Nano), 1)} {
		before := source.calls
		w = httptest.NewRecorder()
		h.HandleErrorStatistics(w, httptest.NewRequest("GET", bad, nil))
		if w.Code != 400 || source.calls != before {
			t.Fatalf("bad request reached source %d %s", w.Code, w.Body.String())
		}
	}
}
