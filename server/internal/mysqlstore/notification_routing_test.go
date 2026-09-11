package mysqlstore

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"controltower/server/internal/storage"
)

func TestMySQLNotificationRoutingRoundTrip(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run notification routing persistence test")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("routing-%d", time.Now().UnixNano())
	defer func() { _, _ = db.Exec("DELETE FROM notification_channels WHERE id=?", id) }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	// An old-style insert leaves site_id empty and rule_keys SQL NULL.
	_, err = db.Exec(`INSERT INTO notification_channels (id,channel_type,name,webhook_url,secret_value,enabled,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, id, "webhook", "routing test", "https://example.com/hook", "secret", true, now, now)
	if err != nil {
		t.Fatal(err)
	}
	store := New(db)
	read := func() storage.NotificationChannel {
		t.Helper()
		items, err := store.QueryNotificationChannels(false)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == id {
				return item
			}
		}
		t.Fatal("saved channel missing")
		return storage.NotificationChannel{}
	}
	channel := read()
	if channel.SiteID != "" || len(channel.RuleKeys) != 0 {
		t.Fatal("legacy defaults incorrect")
	}
	channel.SiteID = "site-a"
	channel.RuleKeys = []string{"user_low_balance", "high_cpu"}
	if err := store.UpsertNotificationChannel(channel); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.SiteID != "site-a" || !reflect.DeepEqual(got.RuleKeys, channel.RuleKeys) || got.SecretValue != channel.SecretValue {
		t.Fatal("routing settings failed to round-trip")
	}
	channel.RuleKeys = []string{}
	if err := store.UpsertNotificationChannel(channel); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got.RuleKeys) != 0 || got.SiteID != "site-a" {
		t.Fatal("all-types update lost site scope")
	}
}
