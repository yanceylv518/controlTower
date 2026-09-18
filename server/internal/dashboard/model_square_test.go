package dashboard

import (
	"context"
	"controltower/server/internal/storage"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelSquareCacheLifecycle(t *testing.T) {
	c := NewModelSquareCache()
	now := time.Now()
	c.now = func() time.Time { return now }
	calls := 0
	fail := false
	fetch := func(context.Context) (SquareResponse, error) {
		calls++
		if fail {
			return SquareResponse{}, errors.New("offline")
		}
		return SquareResponse{Items: []SquareModel{}, GroupRatios: map[string]float64{"vip": 0.5}}, nil
	}
	get := func(key string, force bool) SquareResponse {
		t.Helper()
		v, e := c.get(context.Background(), key, force, fetch)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := get("a", false)
	get("a", false)
	get("a", true)
	if calls != 1 || first.Source != "newapi" {
		t.Fatalf("calls=%d value=%+v", calls, first)
	}
	get("b", false)
	if calls != 2 {
		t.Fatal("sites must be isolated")
	}
	now = now.Add(11 * time.Second)
	get("a", true)
	if calls != 3 {
		t.Fatal("manual refresh missing")
	}
	now = now.Add(6 * time.Minute)
	fail = true
	stale := get("a", false)
	get("a", true)
	if !stale.Stale || stale.Warning == "" || calls != 4 {
		t.Fatal("stale fallback/backoff failed")
	}
	now = now.Add(time.Minute)
	fail = false
	fresh := get("a", false)
	if fresh.Stale || fresh.Warning != "" || fresh.Items == nil || calls != 5 {
		t.Fatal("empty successful response must replace stale data")
	}
}

type squareConfig struct{ url string }

func (s *squareConfig) ControlConfigForSite(string) (storage.SiteControlConfig, error) {
	return storage.SiteControlConfig{APIURL: s.url}, nil
}
func (*squareConfig) UpdateControlConfigForSite(string, string, string, int64, time.Time) error {
	return nil
}

func TestModelSquareSourceAndRetiredWrites(t *testing.T) {
	calls := 0
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || (r.URL.Path != "/api/pricing" && r.URL.Path != "/changed/api/pricing") {
			t.Errorf("unexpected source request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":[{"model_name":"test","model_ratio":0,"completion_ratio":2,"enable_groups":["vip"],"supported_endpoint_types":["openai"]}],"group_ratio":{"vip":0.5},"vendors":[]}`))
	}))
	defer source.Close()
	cfg := &squareConfig{url: source.URL}
	h := ModelSquareHandler{Config: cfg, Cache: NewModelSquareCache()}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/api/dashboard/model-square?instance_id=a", nil))
		want := 200
		if method == "PUT" || method == "DELETE" {
			want = 405
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		if want == 200 && (!strings.Contains(w.Body.String(), `"model_ratio":0`) || !strings.Contains(w.Body.String(), `"vip":0.5`)) {
			t.Fatal(w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	cfg.url = source.URL + "/changed"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=a", nil))
	if calls != 2 {
		t.Fatal("configuration change must invalidate cache")
	}
}

func TestModelSquareRejectsInvalidSource(t *testing.T) {
	for _, body := range []string{`{"success":false}`, `{"success":true,"data":[],"group_ratio":null}`, `{"success":true,"data":[{}],"group_ratio":{}}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
			defer source.Close()
			h := ModelSquareHandler{Config: &squareConfig{url: source.URL}, Cache: NewModelSquareCache()}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/?instance_id=a", nil))
			if w.Code != 502 || strings.Contains(w.Body.String(), body) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
