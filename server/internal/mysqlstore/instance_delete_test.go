package mysqlstore

import (
	"context"
	"controltower/server/internal/storage"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestDeleteInstancePreservesHistoryAndRevokesAccess(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires test MySQL")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = ApplyDir(context.Background(), db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	now := time.Now().UTC()
	id := fmt.Sprintf("delete-test-%d", now.UnixNano())
	defer db.Exec("DELETE FROM instances WHERE id=?", id)
	defer db.Exec("DELETE FROM instance_tokens WHERE instance_id=?", id)
	if err = s.CreateInstance(storage.Instance{ID: id, Name: "retained", Enabled: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	token := fmt.Sprintf("%064d", now.UnixNano())
	if err = s.CreateInstanceToken(storage.InstanceToken{InstanceID: id, TokenHash: token, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteInstance(id, now); err == nil {
		t.Fatal("active instance deleted")
	}
	if err = s.UpdateInstance(id, id, "retained", false, now); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateReadonlyDSNForSite(id, "encrypted-test", now); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteInstance(id, now); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range items {
		if v.ID == id {
			t.Fatal("deleted listed")
		}
	}
	v, ok, err := s.InstanceByID(id)
	if err != nil || !ok || !v.Deleted || v.Enabled {
		t.Fatal(v, ok, err)
	}
	// Even direct update attempts cannot reactivate the reserved identity.
	_ = s.UpdateInstance(id, id, "retained", true, now)
	if _, ok, err = s.InstanceIDByTokenHash(token, now); err != nil || ok {
		t.Fatal("deleted token accepted", err)
	}
	if config, err := s.ReadonlyDSNForSite(id); err != nil || config != "encrypted-test" {
		t.Fatal("shared config lost", err)
	}
	if err = s.CreateInstance(storage.Instance{ID: id, Enabled: true, CreatedAt: now, UpdatedAt: now}); err == nil {
		t.Fatal("identity reused")
	}
}
