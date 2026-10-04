package directcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
)

// Limit the real runtime to this fixture site. Only the metric sample is
// synthetic; decisions, state, receipts and control gates use real MySQL.
type latencyRuntimeStore struct {
	Store
	site string
}

func (s latencyRuntimeStore) ListEnabledSites() ([]string, error) { return []string{s.site}, nil }
func (s latencyRuntimeStore) QueryMetrics(string, time.Time, time.Time) ([]tuning.ChannelMetric, error) {
	var out []tuning.ChannelMetric
	for i := int64(1); i <= 3; i++ {
		out = append(out, tuning.ChannelMetric{ChannelID: i, RequestCount: 30, TTFTP50: 1, TTFTP90: 1, TTFTP95: 1, SpeedSamples: 30, SpeedTTFTP50: 1, SpeedTTFTP90: 1, SpeedTTFTP95: 1})
	}
	return out, nil
}

func TestTuningRuntimeLatencyIsolationIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for isolated integration DB")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = mysqlstore.ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	site := fmt.Sprintf("latency-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	defer func() {
		for _, table := range []string{"operation_audits", "channel_commands", "tuning_recommendations", "tuning_continuous_states", "channel_base_values", "channel_current", "tuning_policies"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	inner := mysqlstore.New(db)
	if err = inner.CreateInstance(storage.Instance{ID: site, SiteID: site, Name: site, Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	channels := []channelcontrol.Channel{{ID: 1, Name: "slow", Models: "m", Weight: 50, Status: 1, Group: "default"}, {ID: 2, Name: "peer", Models: "m", Weight: 50, Status: 1, Group: "default"}, {ID: 3, Name: "urgent", Models: "urgent", Weight: 100, Status: 1, Group: "default"}}
	if err = inner.StoreInstanceChannels(site, channels, now); err != nil {
		t.Fatal(err)
	}
	var bases []tuning.ChannelBaseValue
	for _, c := range channels {
		bases = append(bases, tuning.ChannelBaseValue{ChannelID: c.ID, ModelName: c.Models, BaseWeight: 100})
	}
	if err = inner.SaveChannelBaseValues(site, "test", bases, now); err != nil {
		t.Fatal(err)
	}
	p := tuning.DefaultPolicy()
	p.DispatchModes = map[string]string{"m": "auto", "urgent": "auto"}
	if err = inner.PutPolicy(tuning.PolicyRecord{InstanceID: site, Policy: p, Mode: "observe", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	peer, urgent := make(chan struct{}, 1), make(chan struct{}, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/channel/"))
			weight := 50
			if id == 3 {
				weight = 100
			}
			fmt.Fprintf(w, `{"success":true,"data":{"id":%d,"weight":%d,"status":1,"priority":0,"group":"default"}}`, id, weight)
			return
		}
		var body struct {
			ID     int `json:"id"`
			Weight int `json:"weight"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch body.ID {
		case 1:
			close(blocked)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		case 2:
			peer <- struct{}{}
		case 3:
			if body.Weight == 0 {
				urgent <- struct{}{}
			}
		}
		fmt.Fprint(w, `{"success":true}`)
	}))
	defer api.Close()
	encrypted, err := secrets.Encrypt("test-key", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = inner.UpdateControlConfigForSite(site, api.URL, encrypted, 7, now); err != nil {
		t.Fatal(err)
	}
	e := tuning.NewEngine(latencyRuntimeStore{Wrap(inner, "test-key"), site})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = e.Run(ctx) }()
	defer func() {
		cancel()
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("engine failed to drain")
		}
	}()
	wait := func(ch <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal(message)
		}
	}
	wait(blocked, "slow write did not start")
	wait(peer, "healthy peer blocked by slow HTTP write")
	e.SubmitFastCircuitBatch(tuning.FastCircuitBatch{InstanceID: site, ReportedAt: time.Now().UTC(), Metrics: []tuning.FastCircuitMetric{{ChannelID: 3, RequestCount: 50, ErrorCount: 50}}})
	wait(urgent, "fast circuit blocked by normal HTTP write")
	deadline := time.Now().Add(5 * time.Second)
	for {
		states, err := inner.ListContinuousStates(site)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range states {
			if s.ChannelID == 3 && s.Phase == "circuit" && s.LastWrittenWeight != nil && *s.LastWrittenWeight == 0 {
				var recorded int
				if err := db.QueryRow(`SELECT COUNT(*) FROM tuning_recommendations r JOIN channel_commands c ON c.id=r.command_id WHERE r.instance_id=? AND r.channel_id=3 AND r.rule='circuit_opened' AND c.status='succeeded'`, site).Scan(&recorded); err != nil || recorded != 1 {
					t.Fatalf("missing confirmed circuit record: count=%d err=%v", recorded, err)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("fast circuit receipt was not persisted while normal write was blocked")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
