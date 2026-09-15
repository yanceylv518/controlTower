package mysqlstore

import (
	"context"
	"controltower/server/internal/voicealert"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestVoiceSiteConfigIntegration(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local test DB")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	a := fmt.Sprintf("voice-site-%d", time.Now().UnixNano())
	b := a + "b"
	now := time.Now().UTC()
	defer func() {
		for _, site := range []string{a, b} {
			db.Exec("DELETE FROM voice_alert_site_config WHERE site_id=?", site)
			db.Exec("DELETE FROM voice_alert_calls WHERE site_id=?", site)
			db.Exec("DELETE FROM operation_audits WHERE target_id=? AND operation_type='voice_alert.configure'", site)
			db.Exec("DELETE FROM instances WHERE id=?", site)
		}
	}()
	for _, site := range []string{a, b} {
		if _, err := db.Exec(`INSERT INTO instances(id,name,site_id,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
			t.Fatal(err)
		}
	}
	store := voicealert.Store{DB: db}
	c := voicealert.DefaultConfig()
	c.Enabled = true
	c.TtsCode = "TTS_test"
	c.Recipients = []voicealert.Recipient{{Phone: "13600000999"}}
	if err := store.SaveConfig(ctx, a, c, "test"); err != nil {
		t.Fatal(err)
	}
	c.Percent = 31
	c.UsePercent = false
	c.Enabled = false
	if err := store.SaveConfig(ctx, b, c, "test"); err != nil {
		t.Fatal(err)
	}
	ca, err := store.Config(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := store.Config(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if !ca.Enabled || ca.Percent != 20 || !ca.UsePercent || cb.Enabled || cb.Percent != 31 || cb.UsePercent {
		t.Fatal(ca, cb)
	}
	ca = ca.WithDirectionRules()
	ca.Rise.Enabled = false
	ca.Rise.Percent = 70
	ca.Fall.Percent = 25
	ca.Fall.Delta = 15000000
	ca.Fall.UsePercent = false
	if err := store.SaveConfig(ctx, a, ca, "test"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Config(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rise.Enabled || loaded.Rise.Percent != 70 || loaded.Fall.Percent != 25 || loaded.Fall.Delta != 15000000 || loaded.Fall.UsePercent {
		t.Fatal("direction rules not persisted", loaded)
	}
	other, err := store.Config(ctx, b)
	if err != nil || other.Rule("上涨").Percent != 31 {
		t.Fatal("other site overwritten", other, err)
	}
	c.Recipients[0].Targets = []string{a + "/1"}
	if err := store.SaveConfig(ctx, b, c, "test"); err == nil {
		t.Fatal("cross-site save accepted")
	}
	configs, err := store.Configs(ctx)
	if err != nil || !configs[a].Enabled || configs[b].Enabled {
		t.Fatal(configs, err)
	}
	for i, site := range []string{a, b} {
		if _, err := db.Exec(`INSERT INTO voice_alert_calls(id,site_id,user_id,phone,window_end,min_tpm,max_tpm,direction,status,created_at,suppress_until) VALUES(?,?,1,'13600000999',?,0,20000000,'up','unknown',?,?)`, fmt.Sprintf("vs%d%d", time.Now().UnixNano()%1000000000, i), site, now, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	calls, err := store.Calls(ctx, a)
	if err != nil || len(calls) != 1 || calls[0].Site != a {
		t.Fatal(calls, err)
	}
	// Provider quota remains shared across sites for the same phone.
	id, err := store.Claim(ctx, voicealert.Target{Site: b, UserID: 99, Phone: "13600000999"}, now, 0, 20000000, "up", now)
	if err != nil || id != "" {
		t.Fatal("cross-site quota lost", id, err)
	}
}
