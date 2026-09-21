package logarchive

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"controltower/internal/archivecontract"
	"github.com/go-sql-driver/mysql"
)

func foundationTestWorker(t *testing.T) (*Worker, context.Context) {
	t.Helper()
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ARCHIVE_TEST_DSN for isolated archive foundation integration tests")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid archive test DSN")
	}
	config.DBName = ""
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal("open archive test administrator failed")
	}
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	base := "ct_archive_test_" + hex.EncodeToString(suffix[:])
	sourceName, targetName := base+"_s", base+"_t"
	for _, name := range []string{sourceName, targetName} {
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quote(name)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
			t.Fatal("create isolated archive test database failed")
		}
		t.Cleanup(func() {
			if !strings.HasPrefix(name, "ct_archive_test_") {
				t.Error("unsafe test cleanup name")
				return
			}
			if _, err := admin.Exec("DROP DATABASE " + quote(name)); err != nil {
				t.Error("drop isolated archive test database failed")
			}
		})
		if _, err := admin.ExecContext(ctx, "CREATE TABLE "+quote(name)+".logs (id BIGINT NOT NULL PRIMARY KEY,created_at BIGINT NULL,type INT NULL,quota BIGINT NULL,other TEXT NULL) ENGINE=InnoDB"); err != nil {
			t.Fatal("create archive fixture logs failed")
		}
	}
	config.DBName = sourceName
	sourceDSN := config.FormatDSN()
	config.DBName = targetName
	worker, err := Open(sourceDSN, config.FormatDSN(), "foundation-test", t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	return worker, ctx
}

func foundationTestIdentity() archivecontract.Identity {
	return archivecontract.Identity{SiteID: "archive-test", DatasetID: strings.Repeat("1", 32), SourceGenerationID: strings.Repeat("2", 32)}
}

func TestFoundationMySQL(t *testing.T) {
	t.Run("initialize_empty_target_without_copying_rows", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		if _, err := worker.source.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota) VALUES(1,1788278400,2,50)"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "DROP TABLE logs"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.PrepareFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
		var rows int
		if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs").Scan(&rows) != nil || rows != 0 {
			t.Fatal("empty target initialization copied source rows")
		}
		if _, err := worker.InspectFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reject_nonempty_target_without_template", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		if _, err := worker.target.ExecContext(ctx, "DROP TABLE logs"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "CREATE TABLE unrelated(id INT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.PrepareFoundation(ctx, foundationTestIdentity()); err == nil || !strings.Contains(err.Error(), "empty database") {
			t.Fatalf("nonempty target was adopted: %v", err)
		}
		var count int
		if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&count) != nil || count != 1 {
			t.Fatal("rejected target was modified")
		}
	})
	for _, test := range []struct{ name, alter string }{
		{"reject_generated_source_template", "ALTER TABLE logs ADD generated_id BIGINT GENERATED ALWAYS AS (id+1) STORED"},
		{"reject_source_extra_unique_index", "ALTER TABLE logs ADD UNIQUE KEY unique_quota(quota)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, ctx := foundationTestWorker(t)
			if _, err := worker.target.ExecContext(ctx, "DROP TABLE logs"); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.source.ExecContext(ctx, test.alter); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.PrepareFoundation(ctx, foundationTestIdentity()); err == nil {
				t.Fatal("unsupported source schema was accepted")
			}
			var count int
			if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&count) != nil || count != 0 {
				t.Fatal("rejected template modified target")
			}
		})
	}
	t.Run("reject_source_target_alias_before_ddl", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		originalTarget := worker.target
		worker.target = worker.source
		_, err := worker.PrepareFoundation(ctx, foundationTestIdentity())
		worker.target = originalTarget
		if err == nil || !strings.Contains(err.Error(), "resolves to source") {
			t.Fatalf("source alias accepted: %v", err)
		}
		var count int
		if worker.source.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME LIKE 'archive%'").Scan(&count) != nil || count != 0 {
			t.Fatal("preparation wrote to source database")
		}
	})
	t.Run("reject_case_only_physical_alias", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		var mode int
		if err := worker.source.QueryRowContext(ctx, "SELECT @@lower_case_table_names").Scan(&mode); err != nil {
			t.Fatal("read MySQL identifier mode failed")
		}
		if mode == 0 {
			t.Skip("server uses case-sensitive identifiers; case-fold policy covered by unit tests")
		}
		var database string
		if err := worker.source.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
			t.Fatal("read test source database failed")
		}
		config, err := mysql.ParseDSN(os.Getenv("CT_ARCHIVE_TEST_DSN"))
		if err != nil {
			t.Fatal("invalid archive test DSN")
		}
		config.DBName = strings.ToUpper(database)
		alias, err := sql.Open("mysql", config.FormatDSN())
		if err != nil {
			t.Fatal("open test source alias failed")
		}
		defer alias.Close()
		aliasWorker := &Worker{source: worker.source, target: alias, batchSize: 10}
		if err := aliasWorker.Check(ctx); err == nil || !strings.Contains(err.Error(), "resolves to source") {
			t.Fatalf("case-only alias passed Check: %v", err)
		}
		if _, err := aliasWorker.PrepareFoundation(ctx, foundationTestIdentity()); err == nil || !strings.Contains(err.Error(), "resolves to source") {
			t.Fatalf("case-only alias passed preparation: %v", err)
		}
		var count int
		if worker.source.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME LIKE 'archive%'").Scan(&count) != nil || count != 0 {
			t.Fatal("alias preparation wrote source database")
		}
	})
	t.Run("serialize_preparation_with_advisory_lock", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		worker.target.SetMaxOpenConns(2)
		lockConn, err := worker.target.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer lockConn.Close()
		var uuid, database string
		if err := lockConn.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE()").Scan(&uuid, &database); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(strings.ToLower(uuid) + "\x00" + strings.ToLower(database)))
		lockName := fmt.Sprintf("ct.archive.prepare.%x", hash[:20])
		var locked int
		if err := lockConn.QueryRowContext(ctx, "SELECT GET_LOCK(?,0)", lockName).Scan(&locked); err != nil || locked != 1 {
			t.Fatal("fixture could not acquire preparation lock")
		}
		defer releaseFoundationLock(lockConn, lockName)
		short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()
		if _, err := worker.PrepareFoundation(short, foundationTestIdentity()); err == nil {
			t.Fatal("concurrent preparation ignored advisory lock")
		}
		var count int
		if lockConn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='archive_dataset_meta'").Scan(&count) != nil || count != 0 {
			t.Fatal("blocked preparation performed DDL")
		}
	})
	t.Run("initialize_repeat_inspect_and_legacy_fence", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		identity := foundationTestIdentity()
		if _, err := worker.target.ExecContext(ctx, "INSERT INTO logs(id,created_at,type,quota) VALUES(1,1788278400,2,50)"); err != nil {
			t.Fatal(err)
		}
		first, err := worker.PrepareFoundation(ctx, identity)
		if err != nil {
			t.Fatal(err)
		}
		if !first.Identity.Equal(identity) || first.FormatVersion != archivecontract.FormatVersion || len(first.SourceFingerprint) != 64 || len(first.SchemaFingerprint) != 64 || first.CatalogRevision != 0 || first.WriterEpoch != 0 {
			t.Fatalf("unexpected foundation: %+v", first)
		}
		second, err := worker.PrepareFoundation(ctx, identity)
		if err != nil || second != first {
			t.Fatalf("repeat preparation changed binding: %+v, %v", second, err)
		}
		inspected, err := worker.InspectFoundation(ctx, identity)
		if err != nil || inspected != first {
			t.Fatalf("inspect changed binding: %+v, %v", inspected, err)
		}
		for _, table := range []string{"archive_checkpoints", "archive_batch_receipts", "archive_days", "archive_day_versions"} {
			var count int
			if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(table)).Scan(&count) != nil || count != 0 {
				t.Fatalf("legacy rows adopted into %s", table)
			}
		}
		if _, err := worker.Pass(ctx); !errors.Is(err, ErrFoundationLegacyWriter) {
			t.Fatalf("legacy writer not fenced: %v", err)
		}
		r, err := worker.NewReconciler(strings.Repeat("3", 32), "2026-09-01")
		if err != nil {
			t.Fatal(err)
		}
		if err := worker.ReconcileStep(ctx, r); !errors.Is(err, ErrFoundationLegacyWriter) {
			t.Fatalf("legacy reconciliation not fenced: %v", err)
		}
		var sessions int
		if err := worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_dataset_meta WHERE writer_session IS NOT NULL OR writer_lease_until IS NOT NULL").Scan(&sessions); err != nil || sessions != 0 {
			t.Fatal("preparation granted a writer session")
		}
	})
	t.Run("resume_ddl_without_receipt", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		migrations, err := loadFoundationMigrations()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, foundationMigrationLedger); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := worker.target.ExecContext(ctx, migrations[i].sql); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				if _, err := worker.target.ExecContext(ctx, "INSERT INTO archive_schema_migrations VALUES(?,?,UTC_TIMESTAMP(6))", migrations[i].version, migrations[i].checksum[:]); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := worker.Pass(ctx); !errors.Is(err, ErrFoundationLegacyWriter) {
			t.Fatalf("partial preparation did not fence legacy: %v", err)
		}
		if _, err := worker.PrepareFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.InspectFoundation(ctx, foundationTestIdentity()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reject_identity_before_migrations", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		identity := foundationTestIdentity()
		if _, err := worker.PrepareFoundation(ctx, identity); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "DELETE FROM archive_schema_migrations WHERE version=5"); err != nil {
			t.Fatal(err)
		}
		for _, wrong := range []archivecontract.Identity{
			{SiteID: "other", DatasetID: identity.DatasetID, SourceGenerationID: identity.SourceGenerationID},
			{SiteID: identity.SiteID, DatasetID: strings.Repeat("3", 32), SourceGenerationID: identity.SourceGenerationID},
			{SiteID: identity.SiteID, DatasetID: identity.DatasetID, SourceGenerationID: strings.Repeat("3", 32)},
		} {
			if _, err := worker.PrepareFoundation(ctx, wrong); !errors.Is(err, ErrFoundationIdentity) {
				t.Fatalf("wrong identity accepted: %v", err)
			}
		}
		var count int
		migrations, _ := loadFoundationMigrations()
		if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_schema_migrations").Scan(&count) != nil || count != len(migrations)-1 {
			t.Fatal("wrong identity changed migrations")
		}
	})
	t.Run("reject_mutated_metadata_and_schema", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		identity := foundationTestIdentity()
		if _, err := worker.PrepareFoundation(ctx, identity); err != nil {
			t.Fatal(err)
		}
		checks := []struct {
			sql     string
			restore string
			want    error
		}{
			{"UPDATE archive_dataset_meta SET format_version=999", "UPDATE archive_dataset_meta SET format_version=2", ErrFoundationVersion},
			{"UPDATE archive_dataset_meta SET source_identity_hash=REPEAT(CHAR(0),32)", "", ErrFoundationIdentity},
		}
		for _, check := range checks {
			if _, err := worker.target.ExecContext(ctx, check.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.PrepareFoundation(ctx, identity); !errors.Is(err, check.want) {
				t.Fatalf("metadata change accepted: %v", err)
			}
			if check.restore != "" {
				if _, err := worker.target.ExecContext(ctx, check.restore); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
	t.Run("reject_extra_metadata_row", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		identity := foundationTestIdentity()
		if _, err := worker.PrepareFoundation(ctx, identity); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, `INSERT INTO archive_dataset_meta SELECT 2,UNHEX(REPEAT('3',32)),source_generation_id,site_id,format_version,source_identity_hash,schema_fingerprint,writer_epoch,writer_session,writer_lease_until,catalog_revision,unscoped_blocking_issues,updated_at FROM archive_dataset_meta WHERE singleton_id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.PrepareFoundation(ctx, identity); !errors.Is(err, ErrFoundationIdentity) {
			t.Fatalf("multiple metadata rows accepted: %v", err)
		}
	})
	t.Run("reject_active_writer_before_migrations", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		identity := foundationTestIdentity()
		if _, err := worker.PrepareFoundation(ctx, identity); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "UPDATE archive_dataset_meta SET writer_lease_until=UTC_TIMESTAMP(6)+INTERVAL 1 MINUTE"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "DELETE FROM archive_schema_migrations WHERE version=5"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.PrepareFoundation(ctx, identity); err == nil || !strings.Contains(err.Error(), "paused writer") {
			t.Fatalf("active writer accepted: %v", err)
		}
		var count int
		migrations, _ := loadFoundationMigrations()
		if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_schema_migrations").Scan(&count) != nil || count != len(migrations)-1 {
			t.Fatal("active writer changed migrations")
		}
	})
	for _, test := range []struct {
		name, sql string
		want      error
	}{
		{"changed_migration_checksum", "UPDATE archive_schema_migrations SET checksum=REPEAT(CHAR(0),32) WHERE version=1", ErrFoundationSchema},
		{"future_migration", "INSERT INTO archive_schema_migrations VALUES(9999,REPEAT(CHAR(0),32),UTC_TIMESTAMP(6))", ErrFoundationVersion},
		{"changed_foundation_column", "ALTER TABLE archive_checkpoints MODIFY stream_type VARCHAR(23) CHARACTER SET ascii COLLATE ascii_bin NOT NULL", ErrFoundationSchema},
		{"changed_foundation_unique_index", "ALTER TABLE archive_checkpoints ADD UNIQUE KEY unexpected_unique(after_id)", ErrFoundationSchema},
		{"changed_foreign_key_action", "ALTER TABLE archive_days ADD CONSTRAINT fk_archive_current_day_version FOREIGN KEY(log_date,current_version_id) REFERENCES archive_day_versions(log_date,day_version_id) ON DELETE CASCADE ON UPDATE RESTRICT", ErrFoundationSchema},
		{"changed_source_schema", "ALTER TABLE logs ADD new_field INT NULL", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, ctx := foundationTestWorker(t)
			identity := foundationTestIdentity()
			if _, err := worker.PrepareFoundation(ctx, identity); err != nil {
				t.Fatal(err)
			}
			if test.name == "changed_foreign_key_action" {
				if _, err := worker.target.ExecContext(ctx, "ALTER TABLE archive_days DROP FOREIGN KEY fk_archive_current_day_version"); err != nil {
					t.Fatal(err)
				}
			}
			if test.name == "changed_source_schema" {
				if _, err := worker.source.ExecContext(ctx, test.sql); err != nil {
					t.Fatal(err)
				}
				if _, err := worker.target.ExecContext(ctx, test.sql); err != nil {
					t.Fatal(err)
				}
				test.want = ErrFoundationSchema
			} else if _, err := worker.target.ExecContext(ctx, test.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := worker.PrepareFoundation(ctx, identity); !errors.Is(err, test.want) {
				t.Fatalf("change accepted: %v", err)
			}
			if _, err := worker.InspectFoundation(ctx, identity); !errors.Is(err, test.want) {
				t.Fatalf("inspection accepted change: %v", err)
			}
		})
	}
	t.Run("reject_unbound_nonempty_foundation", func(t *testing.T) {
		worker, ctx := foundationTestWorker(t)
		migrations, _ := loadFoundationMigrations()
		if _, err := worker.target.ExecContext(ctx, migrations[1].sql); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.target.ExecContext(ctx, "INSERT INTO archive_checkpoints(stream_key,stream_type,after_id,cursor_version,updated_at) VALUES('incremental','incremental',42,1,UTC_TIMESTAMP(6))"); err != nil {
			t.Fatal(err)
		}
		if _, err := worker.PrepareFoundation(ctx, foundationTestIdentity()); err == nil {
			t.Fatal("unbound checkpoint adopted")
		}
		var count int
		if worker.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='archive_dataset_meta'").Scan(&count) != nil || count != 0 {
			t.Fatal("failed adoption created dataset metadata")
		}
	})
}
