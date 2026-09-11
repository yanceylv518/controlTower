package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/ingest"
	"controltower/server/internal/settings"
	"controltower/server/internal/storage"
)

func TestNotificationsRouteBySiteAndRule(t *testing.T) {
	for _, balanceOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "all rules", true: "retired switches ignored"}[balanceOnly], func(t *testing.T) {
			store := ingest.NewMemoryStore()
			for _, instance := range []storage.Instance{{ID: "a1", SiteID: "a"}, {ID: "a2", SiteID: "a"}, {ID: "b1", SiteID: "b"}, {ID: "legacy-site"}} {
				if err := store.CreateInstance(instance); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			var got []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					ID string `json:"alert_id"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				mu.Lock()
				got = append(got, r.URL.Path+":"+payload.ID)
				mu.Unlock()
				w.WriteHeader(200)
			}))
			defer server.Close()
			for _, channel := range []storage.NotificationChannel{
				{ID: "a-business", SiteID: "a", RuleKeys: []string{"user_low_balance"}, Enabled: true},
				{ID: "a-ops", SiteID: "a", RuleKeys: []string{"high_cpu"}, Enabled: true},
				{ID: "b-all", SiteID: "b", Enabled: true},
				{ID: "legacy-instance", SiteID: "legacy-site", Enabled: true},
				{ID: "unassigned", Enabled: true},
				{ID: "disabled", SiteID: "a", Enabled: false},
			} {
				channel.ChannelType = "webhook"
				channel.WebhookURL = server.URL + "/" + channel.ID
				if err := store.UpsertNotificationChannel(channel); err != nil {
					t.Fatal(err)
				}
			}
			alerts := []storage.Alert{
				{ID: "a-balance", InstanceID: "a1", RuleKey: "user_low_balance", Status: "firing"},
				{ID: "a-cpu", InstanceID: "a2", RuleKey: "high_cpu", Status: "firing"},
				{ID: "a-memory", InstanceID: "a1", RuleKey: "high_memory", Status: "firing"},
				{ID: "b-balance", InstanceID: "b1", RuleKey: "user_low_balance", Status: "firing"},
				{ID: "b-cpu", InstanceID: "b1", RuleKey: "high_cpu", Status: "firing"},
				{ID: "unknown", InstanceID: "missing", RuleKey: "user_low_balance", Status: "firing"},
				{ID: "legacy", InstanceID: "legacy-site", RuleKey: "user_low_balance", Status: "firing"},
				{ID: "silenced", InstanceID: "a1", RuleKey: "user_low_balance", Status: "silenced"},
			}
			h := NewHandler(store).WithNotificationStore(store).WithInstanceStore(store)
			if balanceOnly {
				h = h.WithSettingsProvider(settings.NewProvider(&dashboardSettingsStore{values: map[string]string{settings.NotificationsEnabled: "false", settings.NotifyBalanceOnly: "true"}}, 0))
			}
			for i := 0; i < 2; i++ {
				if err := h.dispatchAlertNotifications(alerts); err != nil {
					t.Fatal(err)
				}
			}
			want := []string{"/a-business:a-balance", "/b-all:b-balance", "/legacy-instance:legacy"}
			want = append(want, "/a-ops:a-cpu", "/b-all:b-cpu")
			sort.Strings(want)
			sort.Strings(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("deliveries = %v, want %v", got, want)
			}
		})
	}
}

func TestNotificationChannelSiteEditingAndLegacyAssignment(t *testing.T) {
	store := ingest.NewMemoryStore()
	for _, site := range []string{"a", "b"} {
		if err := store.CreateInstance(storage.Instance{ID: site + "1", SiteID: site}); err != nil {
			t.Fatal(err)
		}
	}
	created := time.Now().UTC().Add(-time.Hour)
	for _, c := range []storage.NotificationChannel{
		{ID: "a-channel", SiteID: "a", RuleKeys: []string{"high_cpu"}},
		{ID: "b-channel", SiteID: "b"},
		{ID: "old"},
	} {
		c.Name = c.ID
		c.ChannelType = "dingtalk"
		c.WebhookURL = "https://example.com/private-hook"
		c.SecretValue = "keep-secret"
		c.CreatedAt = created
		if err := store.UpsertNotificationChannel(c); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(store).WithNotificationStore(store).WithInstanceStore(store)
	get := func(query string) []NotificationChannelItem {
		t.Helper()
		rr := httptest.NewRecorder()
		h.HandleNotificationChannels(rr, httptest.NewRequest("GET", "/?"+query, nil))
		if rr.Code != 200 {
			t.Fatalf("list status %d: %s", rr.Code, rr.Body.String())
		}
		var result NotificationChannelListResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(rr.Body.Bytes(), []byte("keep-secret")) || bytes.Contains(rr.Body.Bytes(), []byte("https://example.com/private-hook")) {
			t.Fatal("credentials exposed")
		}
		return result.Items
	}
	if items := get("site_id=a"); len(items) != 1 || items[0].ID != "a-channel" {
		t.Fatalf("wrong site list: %+v", items)
	}
	if items := get("unassigned=true"); len(items) != 1 || items[0].ID != "old" {
		t.Fatalf("wrong unassigned list: %+v", items)
	}
	post := func(id, site string, keys []string, want int) {
		t.Helper()
		body, _ := json.Marshal(NotificationChannelRequest{ID: id, SiteID: site, RuleKeys: keys, Name: "edited", ChannelType: "dingtalk", Enabled: true})
		rr := httptest.NewRecorder()
		h.HandleNotificationChannels(rr, httptest.NewRequest("POST", "/", bytes.NewReader(body)))
		if rr.Code != want {
			t.Fatalf("save %s/%s status %d want %d: %s", id, site, rr.Code, want, rr.Body.String())
		}
	}
	post("a-channel", "b", []string{"user_low_balance"}, 409)
	post("a-channel", "missing", nil, 400)
	post("a-channel", "a", []string{"typo"}, 400)
	post("a-channel", "a", []string{"user_low_balance", "user_low_balance"}, 200)
	post("old", "a", []string{"high_cpu"}, 200)
	post("old", "b", nil, 409)
	if items := get("unassigned=true"); len(items) != 0 {
		t.Fatal("assigned channel still unassigned")
	}
	channels, _ := store.QueryNotificationChannels(false)
	for _, c := range channels {
		if c.WebhookURL != "https://example.com/private-hook" || c.SecretValue != "keep-secret" || !c.CreatedAt.Equal(created) {
			t.Fatalf("editing lost existing settings: %s", c.ID)
		}
		if c.ID == "a-channel" && !reflect.DeepEqual(c.RuleKeys, []string{"user_low_balance"}) {
			t.Fatalf("rules not saved: %v", c.RuleKeys)
		}
	}
	post("a-channel", "a", []string{}, 200)
	if items := get("site_id=a"); len(items) != 2 {
		t.Fatalf("assignment missing: %v", items)
	}
}

func TestNotificationDeliveriesAndResendStayInSite(t *testing.T) {
	store := ingest.NewMemoryStore()
	now := time.Now().UTC()
	for _, site := range []string{"a", "b"} {
		if err := store.CreateInstance(storage.Instance{ID: site + "1", SiteID: site}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertCurrentAlerts([]storage.Alert{{ID: site, InstanceID: site + "1", Status: "firing"}}, now); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertNotificationDelivery(storage.NotificationDelivery{ID: site, AlertID: site, ChannelID: "old", Status: "failed", NextAttemptAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	h := NewHandler(store).WithNotificationStore(store)
	rr := httptest.NewRecorder()
	h.HandleNotificationDeliveries(rr, httptest.NewRequest("GET", "/?site_id=a", nil))
	var result NotificationDeliveryListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rr.Code != 200 || len(result.Items) != 1 || result.Items[0].ID != "a" {
		t.Fatalf("cross-site delivery: %s", rr.Body.String())
	}
	for _, tc := range []struct {
		site string
		code int
	}{{"b", 404}, {"", 400}, {"a", 200}} {
		req := httptest.NewRequest("POST", "/?site_id="+tc.site, nil)
		req.SetPathValue("id", "a")
		rr := httptest.NewRecorder()
		h.HandleNotificationResend(rr, req)
		if rr.Code != tc.code {
			t.Fatalf("resend for %q got %d: %s", tc.site, rr.Code, rr.Body.String())
		}
	}
}
