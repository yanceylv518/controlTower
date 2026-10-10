package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"controltower/server/internal/billing"
)

type urlUpstreamStore struct {
	auditUpstreamStore
	saved []billing.Upstream
}

func (s *urlUpstreamStore) PutBillingUpstream(_ context.Context, v billing.Upstream) (billing.Upstream, error) {
	s.saved = append(s.saved, v)
	if v.ID == 0 {
		v.ID = 10
	}
	return v, nil
}

type urlChannelSource struct {
	channels []billing.ConfiguredChannel
	err      error
	site     string
}

func (s *urlChannelSource) CurrentChannels(_ context.Context, site string) ([]billing.ConfiguredChannel, error) {
	s.site = site
	return s.channels, s.err
}
func TestUpstreamURLCreateAndAppend(t *testing.T) {
	source := &urlChannelSource{channels: []billing.ConfiguredChannel{{ChannelID: 1, ChannelName: "old", BaseURL: "https://old.example"}, {ChannelID: 2, ChannelName: "new", BaseURL: "https://NEW.example:443/", Models: "a,b"}, {ChannelID: 3, BaseURL: "https://other.example"}}}
	for _, update := range []bool{false, true} {
		store := &urlUpstreamStore{}
		method := http.MethodPost
		body := `{"instance_id":"a","name":"up","enabled":true,"url":" https://new.example/ ","channels":[{"channel_id":3}],"urls":["https://other.example"]}`
		if update {
			method = http.MethodPut
			store.items = []billing.Upstream{{ID: 7, InstanceID: "a", Name: "up", URLs: []string{"https://old.example"}, Channels: []billing.UpstreamChannel{{ChannelID: 1, ChannelName: "old", Models: []string{"selected"}}, {ChannelID: 99, ChannelName: "retired"}}}}
			body = strings.Replace(body, `"instance_id"`, `"id":7,"instance_id"`, 1)
		}
		w := httptest.NewRecorder()
		BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(method, "/", strings.NewReader(body)))
		if w.Code != 200 || len(store.saved) != 1 {
			t.Fatalf("status=%d %s", w.Code, w.Body.String())
		}
		v := store.saved[0]
		if v.URL != "https://new.example" || source.site != "a" {
			t.Fatalf("saved=%+v", v)
		}
		want := 0
		if update {
			want = 2
			if v.URLs[0] != "https://old.example" || v.Channels[0].Models[0] != "selected" {
				t.Fatal("old association lost")
			}
		}
		if len(v.Channels) != want {
			t.Fatalf("channels=%+v", v.Channels)
		}

	}
}
func TestUpstreamURLValidationAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		items     []billing.Upstream
		sourceErr error
		status    int
	}{
		{name: "optional URL", status: 200},
		{name: "invalid", url: "example.com", status: 400},
		{name: "same-site URL", url: "https://a.example/", items: []billing.Upstream{{ID: 2, InstanceID: "a", URLs: []string{"https://a.example"}}}, status: 200},
		{name: "other-site URL", url: "https://a.example", items: []billing.Upstream{{ID: 2, InstanceID: "b", URLs: []string{"https://a.example"}}}, status: 200},
		{name: "occupied channel", url: "https://a.example", items: []billing.Upstream{{ID: 2, InstanceID: "a", Channels: []billing.UpstreamChannel{{ChannelID: 5}}}}, status: 200},
		{name: "source error", url: "https://a.example", sourceErr: errors.New("offline"), status: 200},
		{name: "no match", url: "https://future.example", status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &urlUpstreamStore{auditUpstreamStore: auditUpstreamStore{items: tc.items}}
			source := &urlChannelSource{channels: []billing.ConfiguredChannel{{ChannelID: 5, BaseURL: "https://a.example"}}, err: tc.sourceErr}
			body, _ := json.Marshal(map[string]any{"instance_id": "a", "name": "new", "url": tc.url})
			w := httptest.NewRecorder()
			BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
			if w.Code != tc.status {
				t.Fatalf("status=%d %s", w.Code, w.Body.String())
			}
			if tc.status != 200 && len(store.saved) != 0 {
				t.Fatal("invalid request mutated upstream")
			}
		})
	}
}

func TestUpstreamManualChannels(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`"add_channel_ids":[2],"remove_channel_ids":[1]`, 200},
		{`"add_channel_ids":[3]`, 409},
		{`"add_channel_ids":[999]`, 400},
		{`"add_channel_ids":[2],"remove_channel_ids":[2]`, 400},
	} {
		store := &urlUpstreamStore{auditUpstreamStore: auditUpstreamStore{items: []billing.Upstream{{ID: 1, InstanceID: "a", Name: "one", Channels: []billing.UpstreamChannel{{ChannelID: 1}, {ChannelID: 99, Models: []string{"kept"}}}}, {ID: 2, InstanceID: "a", Channels: []billing.UpstreamChannel{{ChannelID: 3}}}}}}
		source := &urlChannelSource{channels: []billing.ConfiguredChannel{{ChannelID: 1}, {ChannelID: 2, ChannelName: "manual"}, {ChannelID: 3}}}
		w := httptest.NewRecorder()
		BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"id":1,"instance_id":"a","name":"one",`+tc.body+`}`)))
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
		if tc.status == 200 {
			v := store.saved[0]
			if len(v.Channels) != 2 || v.Channels[0].ChannelID != 99 || len(v.Channels[0].Models) != 1 || v.Channels[1].ChannelID != 2 {
				t.Fatalf("channels=%+v", v.Channels)
			}
		} else if len(store.saved) > 0 {
			t.Fatal("invalid selection saved")
		}
	}
}

func TestUpstreamPrefixesValidation(t *testing.T) {
	for _, tc := range []struct {
		prefixes string
		status   int
	}{{`[" vendor_ ","alias","vendor"]`, 200}, {`[""]`, 400}, {`["___"]`, 400}} {
		store := &urlUpstreamStore{}
		source := &urlChannelSource{}
		w := httptest.NewRecorder()
		BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"instance_id":"a","name":"Display Name","channel_prefixes":`+tc.prefixes+`}`)))
		if w.Code != tc.status {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		if tc.status == 200 {
			v := store.saved[0]
			if len(v.ChannelPrefixes) != 2 || v.ChannelPrefixes[0] != "vendor" || v.ChannelPrefixes[1] != "alias" {
				t.Fatal(v.ChannelPrefixes)
			}
		}
	}
}

type managedUpstreamStore struct {
	urlUpstreamStore
	syncCalls int
	restore   []int64
	syncError error
}

func (s *managedUpstreamStore) ListBillingUpstreams(context.Context, string) ([]billing.Upstream, error) {
	return nil, errors.New("live read must not be called by config handler")
}
func (s *managedUpstreamStore) ListBillingUpstreamsConfig(ctx context.Context, site string) ([]billing.Upstream, error) {
	return s.auditUpstreamStore.ListBillingUpstreams(ctx, site)
}
func (s *managedUpstreamStore) SyncBillingUpstreamChannels(context.Context, string, []billing.ConfiguredChannel) error {
	s.syncCalls++
	if len(s.saved) == 0 {
		return errors.New("sync attempted before save")
	}
	return s.syncError
}
func (s *managedUpstreamStore) SyncBillingUpstreamChannelsWithRestore(_ context.Context, _ string, _ []billing.ConfiguredChannel, ids []int64, _ string) error {
	s.syncCalls++
	s.restore = ids
	return s.syncError
}
func TestUpstreamConfigReadIsPureAndOfflineSafe(t *testing.T) {
	for _, offline := range []bool{false, true} {
		store := &managedUpstreamStore{urlUpstreamStore: urlUpstreamStore{auditUpstreamStore: auditUpstreamStore{items: []billing.Upstream{{ID: 1, InstanceID: "a", Name: "vendor", Revision: 2, Channels: []billing.UpstreamChannel{{ChannelID: 99, ChannelName: "old"}}}}}}}
		source := &urlChannelSource{channels: []billing.ConfiguredChannel{{ChannelID: 1, ChannelName: "new"}}}
		if offline {
			source.err = errors.New("offline")
		}
		w := httptest.NewRecorder()
		BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?instance_id=a", nil))
		var result struct {
			Items           []billing.Upstream          `json:"items"`
			Channels        []billing.ConfiguredChannel `json:"channels"`
			SourceAvailable bool                        `json:"source_available"`
			SyncError       string                      `json:"sync_error"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		if w.Code != 200 || len(result.Items) != 1 || store.syncCalls != 0 || len(store.saved) != 0 || result.SourceAvailable == offline {
			t.Fatalf("read writes or unavailable: %d %+v", w.Code, result)
		}
		found := false
		for _, c := range result.Channels {
			if c.ChannelID == 99 {
				found = c.SourceMissing
			}
		}
		if !found {
			t.Fatal("missing source channel masquerades as disabled")
		}
	}
}
func TestUpstreamSaveOfflineRevisionAndPostSaveSync(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		offline    bool
		syncError  error
		status     int
		saves      int
	}{
		{name: "offline metadata", body: `"revision":2`, offline: true, status: 200, saves: 1},
		{name: "stale editor", body: `"revision":1`, status: 409},
		{name: "revision absent", body: `"remark":"old browser"`, status: 409},
		{name: "offline unknown channel", body: `"revision":2,"add_channel_ids":[99]`, offline: true, status: 409},
		{name: "offline known removal", body: `"revision":2,"remove_channel_ids":[1]`, offline: true, status: 200, saves: 1},
		{name: "post save discovery failure", body: `"revision":2`, syncError: errors.New("sync offline"), status: 200, saves: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &managedUpstreamStore{urlUpstreamStore: urlUpstreamStore{auditUpstreamStore: auditUpstreamStore{items: []billing.Upstream{{ID: 1, InstanceID: "a", Name: "vendor", Revision: 2, Channels: []billing.UpstreamChannel{{ChannelID: 1, ChannelName: "known"}}}}}}, syncError: tc.syncError}
			source := &urlChannelSource{}
			if tc.offline {
				source.err = errors.New("offline")
			}
			w := httptest.NewRecorder()
			BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"id":1,"instance_id":"a","name":"changed",`+tc.body+`}`)))
			if w.Code != tc.status || len(store.saved) != tc.saves {
				t.Fatalf("%d %s saved=%d", w.Code, w.Body.String(), len(store.saved))
			}
			if tc.offline && store.syncCalls != 0 {
				t.Fatal("offline attempted discovery")
			}
			if tc.syncError != nil && (!strings.Contains(w.Body.String(), "upstream_sync_failed") || store.syncCalls != 1) {
				t.Fatal("save success hidden by failed discovery")
			}
		})
	}
}
func TestUpstreamExplicitSyncAndRestore(t *testing.T) {
	store := &managedUpstreamStore{}
	source := &urlChannelSource{channels: []billing.ConfiguredChannel{{ChannelID: 7, ChannelName: "new_channel"}}}
	w := httptest.NewRecorder()
	BillingUpstreamConfigHandler{Store: store, Source: source}.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/?action=sync&instance_id=a", strings.NewReader(`{"restore_auto_channel_ids":[7]}`)))
	if w.Code != 200 || store.syncCalls != 1 || len(store.restore) != 1 || store.restore[0] != 7 || len(store.saved) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
func TestUpstreamManualTransferAuditIncludesPreviousOwner(t *testing.T) {
	store := &urlUpstreamStore{auditUpstreamStore: auditUpstreamStore{items: []billing.Upstream{{ID: 1, InstanceID: "a", Name: "target", Revision: 3}, {ID: 2, InstanceID: "a", Name: "source", Revision: 4, Channels: []billing.UpstreamChannel{{ChannelID: 9, ChannelName: "other_gpt"}}}}}}
	w := httptest.NewRecorder()
	BillingUpstreamConfigHandler{Store: store, Source: &urlChannelSource{}}.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"id":1,"instance_id":"a","name":"target","revision":3,"channel_transfers":[{"channel_id":9,"from_upstream_id":2}],"channel_prefixes":["other"],"prefix_transfers":[{"prefix":"other","from_upstream_id":2}]}`)))
	if w.Code != 200 || len(store.audits) != 1 || !strings.Contains(store.audits[0].AfterSummary, `"from_upstream_id":2`) {
		t.Fatalf("%d %s audits=%+v", w.Code, w.Body.String(), store.audits)
	}
}

type removableUpstreamStore struct {
	urlUpstreamStore
	args []int64
	site string
}

func (s *removableUpstreamStore) RemoveBillingUpstream(ctx context.Context, site string, id, revision, target, targetRevision int64, actor string) (bool, error) {
	s.site = site
	s.args = []int64{id, revision, target, targetRevision}
	return true, nil
}
func TestUpstreamMergeDeleteContract(t *testing.T) {
	store := &removableUpstreamStore{}
	w := httptest.NewRecorder()
	BillingUpstreamConfigHandler{Store: store}.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/?instance_id=a&id=7&revision=3&target_id=9&target_revision=4", nil))
	if w.Code != 200 || store.site != "a" || len(store.args) != 4 || store.args[0] != 7 || store.args[1] != 3 || store.args[2] != 9 || store.args[3] != 4 || !strings.Contains(w.Body.String(), `"archived":true`) {
		t.Fatalf("delete contract %d %s %+v", w.Code, w.Body.String(), store.args)
	}
}
