package logarchive

import (
	"testing"
)

func TestAutomaticFoundationDiscoveryAndLegacyPreservationMySQL(t *testing.T) {
	w, ctx := foundationTestWorker(t)
	if i, err := w.FoundationIdentity(ctx); err != nil || i != nil {
		t.Fatalf("empty discovery: %v %v", i, err)
	}
	if _, err := w.target.ExecContext(ctx, `CREATE TABLE logs_202609 LIKE logs`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.target.ExecContext(ctx, `INSERT INTO logs_202609(id,created_at,type,quota,other) VALUES(42,1788278400,2,99,'original')`); err != nil {
		t.Fatal(err)
	}
	i := foundationTestIdentity()
	for n := 0; n < 2; n++ {
		info, err := w.PrepareFoundation(ctx, i)
		if err != nil || !info.Identity.Equal(i) {
			t.Fatalf("preparation %d: %+v %v", n, info, err)
		}
		discovered, err := w.FoundationIdentity(ctx)
		if err != nil || discovered == nil || !discovered.Equal(i) {
			t.Fatalf("restart discovery: %v %v", discovered, err)
		}
	}
	var quota int
	var other string
	if err := w.target.QueryRowContext(ctx, `SELECT quota,other FROM logs_202609 WHERE id=42`).Scan(&quota, &other); err != nil || quota != 99 || other != "original" {
		t.Fatal("preparation changed legacy rows")
	}
	var count, latest int
	if err := w.target.QueryRowContext(ctx, `SELECT COUNT(*),MAX(version) FROM archive_schema_migrations`).Scan(&count, &latest); err != nil || count != 22 || latest != 22 {
		t.Fatalf("migration ledger %d/%d: %v", count, latest, err)
	}
	i.SiteID = "wrong-site"
	if _, err := w.PrepareFoundation(ctx, i); err == nil {
		t.Fatal("rebound existing target")
	}
}
