package directcontrol

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
)

type flakyListingController struct {
	fakeController
	failures int
	calls    int
}

func (c *flakyListingController) List(context.Context) ([]channelcontrol.Channel, error) {
	c.calls++
	if c.calls <= c.failures {
		return nil, channelcontrol.ErrListChanged
	}
	return []channelcontrol.Channel{{ID: 7, Name: "fresh", Models: "m", Weight: 15, Priority: 2, Status: 1}}, nil
}

// A list that moved under pagination is re-read instead of failing the
// operator's refresh; a list that keeps moving still fails, and a moving list
// is never stored.
func TestRefreshChannelsRetriesChangedListThenGivesUp(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for direct refresh retry test")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = mysqlstore.ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	site := fmt.Sprintf("direct-retry-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"channel_current", "channel_base_values", "tuning_continuous_states"} {
			_, _ = db.Exec("DELETE FROM "+table+" WHERE instance_id=?", site)
		}
		_, _ = db.Exec(`DELETE FROM instances WHERE id=?`, site)
	}()
	const key = "01234567890123456789012345678901"
	token, err := secrets.Encrypt(key, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	inner := mysqlstore.New(db)
	if err = inner.UpdateControlConfigForSite(site, "http://local-test", token, 7, now); err != nil {
		t.Fatal(err)
	}
	flaky := &flakyListingController{failures: channelListAttempts - 1}
	s := Wrap(inner, key).WithFactory(func(string, string, int64) Controller { return flaky }, nil)
	if err = s.RefreshChannels(context.Background(), site, "test"); err != nil || flaky.calls != channelListAttempts {
		t.Fatalf("changed list not retried: calls=%d err=%v", flaky.calls, err)
	}
	var stored int
	if err = db.QueryRow(`SELECT COUNT(*) FROM channel_current WHERE instance_id=? AND channel_id=7`, site).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("fresh list not stored after retry: %d %v", stored, err)
	}
	stuck := &flakyListingController{failures: channelListAttempts}
	s = Wrap(inner, key).WithFactory(func(string, string, int64) Controller { return stuck }, nil)
	if err = s.RefreshChannels(context.Background(), site, "test"); !errors.Is(err, channelcontrol.ErrListChanged) || stuck.calls != channelListAttempts {
		t.Fatalf("persistently moving list must fail after %d attempts: calls=%d err=%v", channelListAttempts, stuck.calls, err)
	}
}
