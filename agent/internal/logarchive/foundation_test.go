package logarchive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"controltower/internal/archivecontract"
)

type foundationReadConnector struct{ meta [][]driver.Value }

func (c foundationReadConnector) Connect(context.Context) (driver.Conn, error) {
	return foundationReadConn{conn: conn{db: &testDB{}}, meta: c.meta}, nil
}
func (foundationReadConnector) Driver() driver.Driver { return testDriver{} }

type foundationReadConn struct {
	conn
	meta [][]driver.Value
}

func (c foundationReadConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(query, "SELECT singleton_id,") {
		return nil, errors.New("unexpected foundation query")
	}
	return result([]string{"singleton_id", "site_id", "dataset_id", "source_generation_id", "format_version", "source_identity_hash", "schema_fingerprint", "writer_epoch", "catalog_revision"}, c.meta...), nil
}

func TestFoundationBindingValidation(t *testing.T) {
	identity := foundationTestIdentity()
	dataset, _ := archivecontract.IDBytes(identity.DatasetID)
	generation, _ := archivecontract.IDBytes(identity.SourceGenerationID)
	fp := foundationFingerprint{source: sha256.Sum256([]byte("source")), schema: sha256.Sum256([]byte("schema"))}
	base := []driver.Value{int64(1), identity.SiteID, dataset, generation, int64(archivecontract.FormatVersion), fp.source[:], fp.schema[:], int64(0), int64(0)}
	for _, test := range []struct {
		name   string
		mutate func([]driver.Value)
		extra  bool
		want   error
	}{
		{name: "valid"},
		{name: "wrong_site", mutate: func(row []driver.Value) { row[1] = "other" }, want: ErrFoundationIdentity},
		{name: "wrong_dataset", mutate: func(row []driver.Value) { row[2] = make([]byte, 16) }, want: ErrFoundationIdentity},
		{name: "wrong_generation", mutate: func(row []driver.Value) { row[3] = make([]byte, 16) }, want: ErrFoundationIdentity},
		{name: "wrong_source", mutate: func(row []driver.Value) { row[5] = make([]byte, 32) }, want: ErrFoundationIdentity},
		{name: "wrong_schema", mutate: func(row []driver.Value) { row[6] = make([]byte, 32) }, want: ErrFoundationSchema},
		{name: "future_format", mutate: func(row []driver.Value) { row[4] = int64(999) }, want: ErrFoundationVersion},
		{name: "invalid_singleton", mutate: func(row []driver.Value) { row[0] = int64(2) }, want: ErrFoundationIdentity},
		{name: "extra_row", extra: true, want: ErrFoundationIdentity},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := append([]driver.Value(nil), base...)
			if test.mutate != nil {
				test.mutate(row)
			}
			rows := [][]driver.Value{row}
			if test.extra {
				rows = append(rows, base)
			}
			db := sql.OpenDB(foundationReadConnector{meta: rows})
			defer db.Close()
			info, err := readFoundation(context.Background(), db, identity, fp)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if test.want == nil && (!info.Identity.Equal(identity) || len(info.SourceFingerprint) != 64 || len(info.SchemaFingerprint) != 64) {
				t.Fatalf("invalid manifest %+v", info)
			}
		})
	}
	db := sql.OpenDB(foundationReadConnector{})
	defer db.Close()
	if _, err := readFoundation(context.Background(), db, identity, fp); !errors.Is(err, ErrFoundationNotPrepared) {
		t.Fatalf("empty binding accepted: %v", err)
	}
}

func TestFoundationLegacyFence(t *testing.T) {
	sourceState, targetState := &testDB{source: true}, &testDB{foundation: true}
	w := &Worker{source: sql.OpenDB(connector{sourceState}), target: sql.OpenDB(connector{targetState})}
	defer w.Close()
	if _, err := w.Pass(context.Background()); !errors.Is(err, ErrFoundationLegacyWriter) {
		t.Fatalf("Pass: %v", err)
	}
	if err := w.ReconcileStep(context.Background(), &Reconciler{}); !errors.Is(err, ErrFoundationLegacyWriter) {
		t.Fatalf("ReconcileStep: %v", err)
	}
	if len(sourceState.queries) != 0 || len(targetState.queries) != 0 {
		t.Fatal("legacy operation issued writes after finding foundation")
	}
}

func TestFoundationMigrationManifest(t *testing.T) {
	migrations, err := loadFoundationMigrations()
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"archive_dataset_meta", "archive_checkpoints", "archive_batch_receipts", "archive_day_versions", "archive_days", "archive_log_state", "", "", "archive_scan_tasks", "archive_ingest_issues", "archive_reconcile_runs", "archive_reconcile_issues", "archive_seal_builds", "archive_billing_evidence_template", "billing_facts_template", "archive_fact_issues", "archive_subject_index", "", "archive_raw_repairs"}
	counts := []int{13, 10, 10, 22, 13, 2, 0, 0, 19, 8, 25, 8, 13, 6, 35, 5, 7, 0, 12}
	names = append(names, "archive_workflow", "archive_workflow_days", "archive_scan_evidence")
	counts = append(counts, 3, 6, 7)
	if len(migrations) != len(names) {
		t.Fatal("unexpected migration count")
	}
	for index, migration := range migrations {
		if index == 6 || index == 7 || index == 17 {
			table, column, ok := foundationAddedColumn(migration.sql)
			wantTable := "archive_log_state"
			if index == 17 {
				wantTable = "archive_batch_receipts"
			}
			if !ok || table != wantTable || column.nullable != "YES" {
				t.Fatal("invalid resumable ledger extension")
			}
			continue
		}
		name, columns := foundationExpectedColumns(migration.sql)
		if name != names[index] || len(columns) != counts[index] || migration.version != index+1 {
			t.Fatalf("migration %d: table=%s columns=%d", index+1, name, len(columns))
		}
		if migration.checksum != sha256.Sum256([]byte(migration.sql)) {
			t.Fatal("invalid migration checksum")
		}
		if strings.Count(migration.sql, "CREATE TABLE") != 1 || strings.Contains(migration.sql, ";") {
			t.Fatal("migration must contain exactly one independently resumable DDL statement")
		}
	}
}

func TestFoundationTemplateValidation(t *testing.T) {
	valid := "CREATE TABLE `logs` (\n  `id` bigint NOT NULL,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"
	if err := validateFoundationTemplate(valid); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		strings.Replace(valid, "`logs`", "`other`", 1),
		strings.Replace(valid, "InnoDB", "MyISAM", 1),
		valid + "; DROP TABLE logs",
		valid + " DATA DIRECTORY='/outside'",
		valid + " INDEX DIRECTORY='/outside'",
		valid + " TABLESPACE external_space",
		valid + " CONNECTION='remote'",
		valid + " PARTITION BY HASH(id) PARTITIONS 2",
		strings.Replace(valid, "PRIMARY KEY (`id`)", "FOREIGN KEY (`id`) REFERENCES another_table(id)", 1),
	} {
		if err := validateFoundationTemplate(statement); err == nil {
			t.Fatal("unsupported source template accepted")
		}
	}
}

func TestFoundationCaseOnlyDatabaseAliases(t *testing.T) {
	for _, pair := range [][4]string{
		{"server-uuid", "newapi", "server-uuid", "NEWAPI"},
		{"SERVER-UUID", "NewApi", "server-uuid", "newapi"},
	} {
		if !samePhysicalArchiveDatabase(pair[0], pair[1], pair[2], pair[3]) {
			t.Fatal("case-only physical alias was accepted")
		}
	}
	if samePhysicalArchiveDatabase("server-a", "newapi", "server-b", "newapi") || samePhysicalArchiveDatabase("server-a", "newapi", "server-a", "archive") {
		t.Fatal("distinct source/target identities rejected")
	}
	if worker, err := Open("user:password@tcp(localhost:3306)/newapi", "user:password@tcp(localhost:3306)/NEWAPI", "test", t.TempDir(), 10); err == nil {
		worker.Close()
		t.Fatal("case-only DSN alias accepted")
	}
}
