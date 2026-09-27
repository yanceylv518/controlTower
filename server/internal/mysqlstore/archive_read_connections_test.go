package mysqlstore

import (
	"context"
	ar "controltower/server/internal/archivereader"
	"controltower/server/internal/secrets"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestArchiveConnectionPersistence(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated MySQL")
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
	site := fmt.Sprintf("connection-test-%d", time.Now().UnixNano())
	defer db.Exec("DELETE FROM archive_read_connections WHERE site_id=?", site)
	s := New(db)
	cipher, _ := secrets.Encrypt("test-key", "synthetic-password")
	c := ar.Connection{Host: "archive.internal", Port: 3306, Database: "archive", Username: "reader", TLS: true, SourceHash: strings.Repeat("a", 64), EncryptedPassword: cipher}
	if err = s.SaveArchiveConnection(ctx, site, c, "tester"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadArchiveConnection(ctx, site)
	if err != nil || got.Version != 1 || got.EncryptedPassword != cipher || !got.PasswordSet {
		t.Fatalf("read %+v %v", got, err)
	}
	if err = s.SaveArchiveConnection(ctx, site, c, "tester"); !errors.Is(err, ar.ErrConnectionConflict) {
		t.Fatal("duplicate accepted", err)
	}
	got.Host = "new-host"
	if err = s.SaveArchiveConnection(ctx, site, got, "tester"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveArchiveConnection(ctx, site, got, "tester"); !errors.Is(err, ar.ErrConnectionConflict) {
		t.Fatal("stale update accepted", err)
	}
	var raw string
	if err = db.QueryRow("SELECT config_json FROM archive_read_connections WHERE site_id=?", site).Scan(&raw); err != nil || strings.Contains(raw, "synthetic-password") || strings.Contains(raw, "v1:") {
		t.Fatal("password entered public JSON")
	}
}
