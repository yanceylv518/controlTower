package archivereader

import (
	"context"
	ac "controltower/internal/archivecontract"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/go-sql-driver/mysql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveReadonlyIntegration(t *testing.T) {
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ARCHIVE_TEST_DSN for isolated MySQL archive tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open test database")
	}
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(raw)
	database, user, password := "ct_archive_reader_"+suffix, "ct_ar_"+suffix, suffix+suffix
	if _, err = admin.ExecContext(ctx, "CREATE DATABASE `"+database+"`"); err != nil {
		t.Fatal("create isolated database failed")
	}
	defer admin.Exec("DROP DATABASE `" + database + "`")
	if _, err = admin.ExecContext(ctx, "CREATE USER '"+user+"'@'%' IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatal("create isolated reader account failed")
	}
	defer admin.Exec("DROP USER '" + user + "'@'%'")
	cfg.DBName = database
	target, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	paths, err := filepath.Glob("../../../agent/internal/logarchive/migrations/*.sql")
	if err != nil || len(paths) < 5 {
		t.Fatal("target migration files unavailable")
	}
	for _, path := range paths {
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		for _, q := range strings.Split(string(b), ";") {
			if strings.TrimSpace(q) == "" {
				continue
			}
			if _, e = target.ExecContext(ctx, q); e != nil {
				t.Fatalf("target migration %s failed: %v", filepath.Base(path), e)
			}
		}
	}
	i := ac.Identity{SiteID: "site", DatasetID: strings.Repeat("a", 32), SourceGenerationID: strings.Repeat("b", 32)}
	did, _ := ac.IDBytes(i.DatasetID)
	gid, _ := ac.IDBytes(i.SourceGenerationID)
	hash := make([]byte, 32)
	_, err = target.ExecContext(ctx, `INSERT INTO archive_dataset_meta(singleton_id,dataset_id,source_generation_id,site_id,format_version,source_identity_hash,schema_fingerprint,writer_epoch,catalog_revision,unscoped_blocking_issues,updated_at) VALUES(1,?,?,?,2,?,?,0,0,0,UTC_TIMESTAMP(6))`, did, gid, i.SiteID, hash, hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"archive_dataset_meta", "archive_days", "archive_day_versions"} {
		if _, err = admin.ExecContext(ctx, "GRANT SELECT ON `"+database+"`.`"+table+"` TO '"+user+"'@'%'"); err != nil {
			t.Fatal("grant isolated reader failed")
		}
	}
	cfg.User = user
	cfg.Passwd = password
	t.Setenv("CT_TEST_ARCHIVE_READONLY_DSN", cfg.FormatDSN())
	path := filepath.Join(t.TempDir(), "connections.json")
	if err = os.WriteFile(path, []byte(`{"local-test":{"dsn_env":"CT_TEST_ARCHIVE_READONLY_DSN"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := Reader{ConnectionsFile: path}
	reg := ac.Registration{Identity: i, StorageRef: "local-test", ArchiveFormatVersion: 2, SchemaFingerprint: strings.Repeat("0", 64), SourceFingerprint: strings.Repeat("0", 64)}
	snapshot, err := r.ReadCatalog(ctx, reg)
	if err != nil {
		t.Fatal("read prepared empty directory:", err)
	}
	if len(snapshot.Days) != 0 || snapshot.CatalogRevision != 0 {
		t.Fatal("empty directory promoted")
	}
	_, err = target.ExecContext(ctx, `INSERT INTO archive_days(log_date,state,updated_at) VALUES('2026-09-01','unknown',UTC_TIMESTAMP(6))`)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = r.ReadCatalog(ctx, reg)
	if err != nil || len(snapshot.Days) != 1 || snapshot.Days[0].AllRows != nil {
		t.Fatalf("unknown day changed: %+v %v", snapshot, err)
	}
	for _, state := range []string{"pending_verify", "empty_candidate", "blocked"} {
		if _, err = target.ExecContext(ctx, `UPDATE archive_days SET state=? WHERE log_date='2026-09-01'`, state); err != nil {
			t.Fatal(err)
		}
		snapshot, err = r.ReadCatalog(ctx, reg)
		if err != nil || len(snapshot.Days) != 1 || snapshot.Days[0].State != state || snapshot.Days[0].AllRows != nil || snapshot.Days[0].VerifiedAt != nil || snapshot.Days[0].CurrentVersionID != "" {
			t.Fatalf("P3 scan state must remain unverified: %+v %v", snapshot, err)
		}
	}
	reg.SiteID = "other"
	if _, err = r.ReadCatalog(ctx, reg); !errors.Is(err, ac.ErrIdentity) {
		t.Fatal("wrong site accepted", err)
	}
	reg.SiteID = i.SiteID
	if _, err = target.ExecContext(ctx, "UPDATE archive_days SET catalog_revision=1"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadCatalog(ctx, reg); !errors.Is(err, ac.ErrConflict) {
		t.Fatal("partial catalog accepted", err)
	}
	if _, err = target.ExecContext(ctx, "UPDATE archive_dataset_meta SET catalog_revision=1"); err != nil {
		t.Fatal(err)
	}
	_, err = target.ExecContext(ctx, `INSERT INTO archive_day_versions(day_version_id,log_date,version_no,build_task_id,state,verified_mutation_revision,reconcile_run_id,parser_version,fact_schema_version,evidence_codec_version,storage_month,all_rows,consume_rows,consume_quota,manifest_hash,publish_revision,created_at,published_at) VALUES(?,'2026-09-01',1,?,'published',1,?,1,1,1,'202609',0,0,0,?,1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, did, did, did, hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ExecContext(ctx, "UPDATE archive_days SET current_version_id=?,state='sealed',mutation_revision=2", did); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadCatalog(ctx, reg); !errors.Is(err, ac.ErrConflict) {
		t.Fatal("stale sealed version accepted", err)
	}
	if _, err = target.ExecContext(ctx, "UPDATE archive_day_versions SET verified_mutation_revision=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadCatalog(ctx, reg); err != nil {
		t.Fatal("consistent directory rejected", err)
	}
	if _, err = target.ExecContext(ctx, "UPDATE archive_day_versions SET publish_revision=2"); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadCatalog(ctx, reg); !errors.Is(err, ac.ErrConflict) {
		t.Fatal("future published version accepted", err)
	}
	testReadonlyVerification(t,ctx,admin,target,r,reg,database,user)
	if _, err = admin.ExecContext(ctx, "GRANT INSERT ON `"+database+"`.`archive_days` TO '"+user+"'@'%'"); err != nil {
		t.Fatal("grant test write privilege failed")
	}
	if _, err = r.ReadCatalog(ctx, reg); !errors.Is(err, ErrPermissions) {
		t.Fatal("write-capable archive account accepted", err)
	}
}
