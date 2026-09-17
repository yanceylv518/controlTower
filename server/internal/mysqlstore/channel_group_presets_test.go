package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestChannelGroupPresetsPersistence(t *testing.T) {
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
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	site := fmt.Sprintf("presets-%d", time.Now().UnixNano())
	defer db.Exec("DELETE FROM channel_group_presets WHERE site_id=?", site)
	s := New(db)
	v := storage.ChannelGroupPresets{Items: []storage.ChannelGroupPreset{{ID: "a", Name: "主力", Groups: []string{"vip", "custom"}}}}
	if err = s.SaveChannelGroupPresets(ctx, site, v, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveChannelGroupPresets(ctx, site, v, "other", time.Now()); !errors.Is(err, storage.ErrGroupPresetConflict) {
		t.Fatal("initial overwrite", err)
	}
	v, err = s.LoadChannelGroupPresets(ctx, site)
	if err != nil || v.Revision != 1 || v.Items[0].Name != "主力" {
		t.Fatal(v, err)
	}
	other, err := s.LoadChannelGroupPresets(ctx, site+"other")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("site leak", other, err)
	}
	v.Items = []storage.ChannelGroupPreset{}
	if err = s.SaveChannelGroupPresets(ctx, site, v, "test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveChannelGroupPresets(ctx, site, v, "other", time.Now()); !errors.Is(err, storage.ErrGroupPresetConflict) {
		t.Fatal("stale overwrite", err)
	}
	v, err = s.LoadChannelGroupPresets(ctx, site)
	if err != nil || v.Revision != 2 || len(v.Items) != 0 {
		t.Fatal(v, err)
	}
}
