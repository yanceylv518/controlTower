package dashboard

import (
	"context"
	"controltower/server/internal/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type presetMemoryStore struct {
	sites map[string]storage.ChannelGroupPresets
}

func (s *presetMemoryStore) LoadChannelGroupPresets(_ context.Context, site string) (storage.ChannelGroupPresets, error) {
	if v, ok := s.sites[site]; ok {
		return v, nil
	}
	return storage.ChannelGroupPresets{Items: []storage.ChannelGroupPreset{}}, nil
}
func (s *presetMemoryStore) SaveChannelGroupPresets(_ context.Context, site string, v storage.ChannelGroupPresets, _ string, _ time.Time) error {
	if s.sites[site].Revision != v.Revision {
		return storage.ErrGroupPresetConflict
	}
	v.Revision++
	s.sites[site] = v
	return nil
}
func TestGroupPresetsIsolationCRUDAndConcurrentEditors(t *testing.T) {
	s := &presetMemoryStore{sites: map[string]storage.ChannelGroupPresets{}}
	h := ChannelGroupPresetsHandler{Store: s}
	call := func(method, site, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/?site_id="+site, strings.NewReader(body)))
		return w
	}
	body := `{"revision":0,"items":[{"id":"a","name":"主力客户","groups":[" vip ","new-group","vip"]}]}`
	w := call("PUT", "one", body)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result storage.ChannelGroupPresets
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Revision != 1 || len(result.Items[0].Groups) != 2 {
		t.Fatal(result, err)
	}
	if w = call("PUT", "one", body); w.Code != http.StatusConflict {
		t.Fatal("stale editor overwrote collection", w.Code)
	}
	if w = call("GET", "two", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("site leak", w.Body.String())
	}
	if w = call("PUT", "one", `{"revision":1,"items":[{"id":"a","name":"改名","groups":["custom"]}]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("GET", "one", ""); !strings.Contains(w.Body.String(), "改名") {
		t.Fatal(w.Body.String())
	}
	if w = call("PUT", "one", `{"revision":2,"items":[]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("GET", "one", ""); !strings.Contains(w.Body.String(), `"revision":3`) || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal(w.Body.String())
	}
}
func TestGroupPresetsRejectInvalidInput(t *testing.T) {
	h := ChannelGroupPresetsHandler{Store: &presetMemoryStore{sites: map[string]storage.ChannelGroupPresets{}}}
	for _, body := range []string{
		`{"revision":-1,"items":[]}`, `{"revision":0}`, `{"items":[{"id":"a","name":"","groups":["x"]}]}`,
		`{"items":[{"id":"a","name":"valid","groups":[]}]}`, `{"items":[{"id":"a","name":"valid","groups":["a,b"]}]}`,
		`{"items":[{"id":"a","name":"valid","groups":["bad\nname"]}]}`,
		`{"items":[{"id":"a","name":"valid","groups":["` + strings.Repeat("x", 129) + `"]}]}`,
		`{"items":[{"id":"a","name":"X","groups":["x"]},{"id":"b","name":"x","groups":["y"]}]}`,
		`{"items":[{"id":"a","name":"X","groups":["x"]},{"id":"a","name":"Y","groups":["y"]}]}`,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PUT", "/?site_id=one", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", body, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
