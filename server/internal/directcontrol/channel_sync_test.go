package directcontrol

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/mysqlstore"
	"controltower/server/internal/secrets"
	"controltower/server/internal/tuning"
)

type listingController struct{ fakeController }

func (c *listingController) List(context.Context) ([]channelcontrol.Channel, error) {
	return []channelcontrol.Channel{{ID: 7, Name: "fresh", Models: "m", Weight: 15, Priority: 2, Status: 1}}, c.err
}

func TestDirectRefreshAndWriteImmediatelyUpdateDashboard(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN for direct refresh integration test")
	}
	db, err := mysqlstore.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = mysqlstore.ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	site := fmt.Sprintf("direct-sync-%d", time.Now().UnixNano())
	now := time.Now().UTC()
	if _, err = db.Exec(`INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at,updated_at) VALUES(?,?,?,'test','local','',1,?,?)`, site, site, site, now, now); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"channel_current", "channel_commands", "channel_base_values", "tuning_continuous_states", "tuning_recommendations", "operation_audits"} {
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
	c := &listingController{}
	s := Wrap(inner, key).WithFactory(func(string, string, int64) Controller { return c }, nil)
	if err = s.RefreshChannels(context.Background(), site, "test"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListChannelBaseValues(site, "")
	if err != nil || len(rows) != 1 || rows[0].CurrentPriority != 2 {
		t.Fatalf("live refresh failed: %#v %v", rows, err)
	}
	priority := int64(9)
	rec := tuning.Recommendation{ID: site + "-write", InstanceID: site, ChannelID: 7, ChannelName: "fresh", Rule: "base_priority_sync", CurrentWeight: 15, ProposedWeight: 15, CurrentPriority: &rows[0].CurrentPriority, ProposedPriority: &priority, CreatedAt: now, ModeAtCreation: "manual"}
	commandID, err := s.CreateContinuousWeightChange(rec, "test", now)
	if err != nil || commandID == "" {
		t.Fatal(err)
	}
	var recordedRule string
	if err = db.QueryRow(`SELECT rule FROM tuning_recommendations WHERE command_id=?`, commandID).Scan(&recordedRule); err != nil || recordedRule != "base_priority_sync" {
		t.Fatalf("missing priority sync audit trail: %s %v", recordedRule, err)
	}
	rows, err = s.ListChannelBaseValues(site, "")
	if err != nil || len(rows) != 1 || rows[0].CurrentPriority != 9 || rows[0].CurrentWeight != 15 {
		t.Fatalf("write not immediately visible: %#v %v", rows, err)
	}
	c.err = fmt.Errorf("new-api unavailable")
	priority = 10
	if _, err = s.CreateContinuousWeightChange(rec, "test", now); err == nil {
		t.Fatal("failed write accepted")
	}
	rows, err = s.ListChannelBaseValues(site, "")
	if err != nil || rows[0].CurrentPriority != 9 {
		t.Fatalf("failed target displayed: %#v %v", rows, err)
	}
}
