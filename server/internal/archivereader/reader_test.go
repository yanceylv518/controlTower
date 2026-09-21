package archivereader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ac "controltower/internal/archivecontract"
)

func TestPermittedGrantRequiresTableScopedSelect(t *testing.T) {
	for _, table := range []string{"archive_dataset_meta", "archive_days", "archive_day_versions", "archive_schema_migrations", "archive_subject_index", "billing_facts_202609", "archive_reconcile_runs", "archive_reconcile_issues", "archive_fact_issues", "archive_raw_repairs"} {
		grant := "GRANT SELECT ON `archive_test`.`" + table + "` TO `reader`@`localhost`"
		if !permittedGrant(grant, "archive_test") {
			t.Errorf("catalog/facts SELECT rejected: %s", grant)
		}
	}
	if !permittedGrant("GRANT USAGE ON *.* TO `reader`@`localhost`", "archive_test") {
		t.Fatal("unprivileged account declaration rejected")
	}
	for _, grant := range []string{
		"GRANT ALL PRIVILEGES ON *.* TO `reader`@`localhost`",
		"GRANT ALL PRIVILEGES ON `archive_test`.* TO `reader`@`localhost`",
		"GRANT SELECT ON *.* TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.* TO `reader`@`localhost`",
		"GRANT SELECT, INSERT ON `archive_test`.`archive_days` TO `reader`@`localhost`",
		"GRANT UPDATE ON `archive_test`.`archive_days` TO `reader`@`localhost`",
		"GRANT DELETE ON `archive_test`.`billing_facts_202609` TO `reader`@`localhost`",
		"GRANT CREATE ON `archive_test`.* TO `reader`@`localhost`",
		"GRANT SELECT ON `another_archive`.`archive_days` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`logs` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`logs_202609` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`logs_undated` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`archive_log_state` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`archive_billing_evidence_202609` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`archive_checkpoints` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`archive_batch_receipts` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`billing_facts_202609_extra` TO `reader`@`localhost`",
		"GRANT SELECT ON `archive_test`.`archive_days` TO `reader`@`localhost` WITH GRANT OPTION",
		"GRANT USAGE ON *.* TO `reader`@`localhost` WITH GRANT OPTION",
		"GRANT `archive_reader_role`@`%` TO `reader`@`localhost`",
		"GRANT PROXY ON ``@`` TO `reader`@`localhost`",
	} {
		if permittedGrant(grant, "archive_test") {
			t.Errorf("unsafe grant accepted: %s", grant)
		}
	}
}

func TestArchiveReaderRejectsUntrustedConnectionConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, contents string
	}{
		{"unknown-secret-field", `{"archive-a":{"dsn_env":"ARCHIVE_TEST_DSN","dsn":"synthetic-secret"}}`},
		{"raw-dsn-in-place-of-env", `{"archive-a":{"dsn_env":"reader:synthetic-secret@tcp(host)/db"}}`},
		{"missing-storage-ref", `{"archive-b":{"dsn_env":"ARCHIVE_TEST_DSN"}}`},
		{"extra-json", `{"archive-a":{"dsn_env":"ARCHIVE_TEST_DSN"}} {}`},
		{"malformed", `{"archive-a":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "archive-connections.json")
			if err := os.WriteFile(file, []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			r := Reader{ConnectionsFile: file}
			db, err := r.open(context.Background(), "archive-a")
			if db != nil || !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("invalid deployment reference not rejected/redacted: db=%v error=%v", db, err)
			}
		})
	}
}

func TestArchiveReaderRejectsIdentityBeforeOpeningConnection(t *testing.T) {
	r := Reader{ConnectionsFile: filepath.Join(t.TempDir(), "does-not-exist.json")}
	if _, err := r.ReadCatalog(context.Background(), ac.Registration{}); !errors.Is(err, ac.ErrIdentity) {
		t.Fatalf("invalid identity reached connection opening: %v", err)
	}
}
