package directcontrol

import (
	"context"
	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"
)

// Both DSNs must point at disposable, isolated databases. The source database
// contains a minimal NewAPI channels table; it is never a real NewAPI instance.
func TestReadonlyChannelSourceIntegration(t *testing.T) {
	dsn, sourceDSN := os.Getenv("CT_MYSQL_TEST_DSN"), os.Getenv("CT_CHANNEL_SOURCE_TEST_DSN")
	if dsn == "" || sourceDSN == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN and CT_CHANNEL_SOURCE_TEST_DSN")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = mysqlstore.ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("mysql", sourceDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(source, "CREATE TABLE IF NOT EXISTS channels(id BIGINT PRIMARY KEY,name TEXT,status INT,weight BIGINT,models TEXT,`group` TEXT,priority BIGINT)")
	const channelID = 9876000
	defer source.Exec("DELETE FROM channels WHERE id BETWEEN ? AND ?", channelID, channelID+readonlyChannelLimit+1)
	exec(source, "INSERT INTO channels(id,name,status,weight,models,`group`,priority) VALUES(?,'readonly channel',1,20,'m','default',0)", channelID)
	site := fmt.Sprintf("readonly-%d", time.Now().UnixNano())
	now := time.Now().UTC().Add(-time.Minute)
	exec(db, `INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now)
	defer func() {
		for _, table := range []string{"channel_current", "channel_base_values", "tuning_continuous_states"} {
			db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		db.Exec("DELETE FROM instances WHERE id=?", site)
	}()
	inner := mysqlstore.New(db)
	if err := inner.StoreInstanceChannels(site, []channelcontrol.Channel{{ID: channelID, Name: "agent", Models: "m", Status: 1, Weight: 100, Priority: 12}}, now); err != nil {
		t.Fatal(err)
	}
	exec(db, `INSERT INTO tuning_continuous_states(instance_id,channel_id,model_name,phase,updated_at) VALUES(?,?,'m','circuit',?)`, site, channelID, now)
	const key = "channel-source-integration-key"
	encrypted, err := secrets.Encrypt(key, sourceDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.UpdateReadonlyDSNForSite(site, encrypted, now); err != nil {
		t.Fatal(err)
	}
	store := Wrap(inner, key).WithFactory(func(string, string, int64) Controller {
		t.Fatal("readonly refresh invoked HTTP controller")
		return nil
	}, nil)
	assert := func() {
		t.Helper()
		rows, err := store.ListChannelBaseValues(site, "")
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		v := rows[0]
		if v.BasePriority != 12 || v.BaseWeight != 100 || v.CurrentPriority != 0 || v.CurrentWeight != 20 {
			t.Fatalf("anchor overwritten or online values stale: %#v", v)
		}
		var phase string
		if err := db.QueryRow("SELECT phase FROM tuning_continuous_states WHERE instance_id=? AND channel_id=?", site, channelID).Scan(&phase); err != nil || phase != "circuit" {
			t.Fatalf("lost circuit: %s %v", phase, err)
		}
	}
	if err := store.RefreshChannels(context.Background(), site, "test"); err != nil {
		t.Fatal(err)
	}
	assert()
	// Invalid configured connections must never fall back to HTTP or Agent data.
	broken, err := secrets.Encrypt(key, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := inner.UpdateReadonlyDSNForSite(site, broken, now); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshChannels(context.Background(), site, "test"); err == nil {
		t.Fatal("missing source table succeeded")
	}
	assert()
	if ok, err := inner.AcceptAgentChannelSnapshots(site); err != nil || ok {
		t.Fatal("failed readonly source accepted Agent fallback")
	}
	if err := inner.UpdateReadonlyDSNForSite(site, encrypted, now); err != nil {
		t.Fatal(err)
	}
	// The background runner uses the same configured source, without any HTTP config.
	store.syncReadonlyChannelSites(context.Background())
	assert()
	// Do not publish truncated channel lists as complete inventories.
	tx, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= readonlyChannelLimit; i++ {
		if _, err := tx.Exec("INSERT INTO channels(id,name,status,weight,models,priority) VALUES(?,'c',1,1,'m',1)", channelID+i); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshChannels(context.Background(), site, "test"); err == nil {
		t.Fatal("truncated source inventory accepted")
	}
	assert()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readReadonlyChannels(ctx, source); err == nil {
		t.Fatal("canceled query succeeded")
	}
}
