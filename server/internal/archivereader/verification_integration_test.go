package archivereader

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testReadonlyVerification(t *testing.T, ctx context.Context, admin, target *sql.DB, r Reader, reg af.Registration, database, user string) {
	t.Helper()
	for _, table := range []string{"archive_reconcile_runs", "archive_reconcile_issues"} {
		if _, err := admin.ExecContext(ctx, "GRANT SELECT ON `"+database+"`.`"+table+"` TO '"+user+"'@'%'"); err != nil {
			t.Fatal("grant verification metadata", err)
		}
	}
	runID := strings.Repeat("c", 32)
	run, _ := af.IDBytes(runID)
	matchedID := strings.Repeat("9", 32)
	matched, _ := af.IDBytes(matchedID)
	versionID := strings.Repeat("d", 32)
	version, _ := af.IDBytes(versionID)
	building, _ := af.IDBytes(strings.Repeat("e", 32))
	date := "2026-09-02"
	_, end, _ := af.DateBounds(date)
	assurance, _ := json.Marshal(af.VerificationAssurance{StableBeforeUnix: end, ValidUntilUnix: end + 86400, Evidence: "synthetic declaration"})
	_, err := target.ExecContext(ctx, `INSERT INTO archive_reconcile_runs(run_id,task_id,attempt,log_date,method,state,phase,policy_json,assurance_json,schema_fingerprint,start_revision,final_revision,writer_epoch,scan_json,summary_json,issue_count,started_at,updated_at,completed_at) VALUES(?,?,1,?,'stable_window_paged','matched','completed','{}',?,?,2,2,1,'{}','{}',0,UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)),(?,?,1,?,'stable_window_paged','mismatched','completed','{}',?,?,2,2,1,'{}','{}',201,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, matched, matched, date, string(assurance), make([]byte, 32), run, run, date, string(assurance), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := target.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for n := int64(1); n <= 201; n++ {
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_reconcile_issues(run_id,source_id,issue_kind,source_row_hash,updated_at) VALUES(?,?,'missing_target',?,UTC_TIMESTAMP(6))`, run, 9007199254740992+n, make([]byte, 32))
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	manifestValue := map[string]any{"identity": reg.Identity, "date": date, "version_id": versionID, "version_no": "1", "mutation_revision": "2", "publish_revision": "3", "run_id": matchedID}
	manifest, _ := json.Marshal(manifestValue)
	hash, _ := af.ManifestHash(manifest)
	digest, _ := hex.DecodeString(hash)
	_, err = target.ExecContext(ctx, `INSERT INTO archive_day_versions(day_version_id,log_date,version_no,build_task_id,state,verified_mutation_revision,reconcile_run_id,parser_version,fact_schema_version,evidence_codec_version,storage_month,manifest_json,manifest_hash,publish_revision,created_at,published_at) VALUES(?,?,1,?,'published',2,?,1,1,1,'202609',?,?,3,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6)),(?,?,2,?,'building',2,?,1,1,1,'202609',NULL,NULL,NULL,UTC_TIMESTAMP(6),NULL)`, version, date, run, matched, string(manifest), digest, building, date, run, matched)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.ExecContext(ctx, `INSERT INTO archive_days(log_date,state,current_version_id,mutation_revision,catalog_revision,updated_at) VALUES(?,'sealed',?,2,3,UTC_TIMESTAMP(6))`, date, version)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadDayVerification(ctx, reg, date, "", 0)
	if err != nil {
		t.Fatal("read day verification", err)
	}
	if len(got.Runs) != 2 || got.SelectedRunID != runID || len(got.Issues) != 200 || got.NextIssueID == nil || *got.NextIssueID != 9007199254741192 || len(got.Versions) != 1 || !got.Versions[0].IsCurrent || got.ArchiveBilling {
		t.Fatalf("unbounded or promoted evidence: %+v", got)
	}
	next, err := r.ReadDayVerification(ctx, reg, date, runID, *got.NextIssueID)
	if err != nil || len(next.Issues) != 1 || next.NextIssueID != nil || next.Issues[0].SourceID != 9007199254741193 {
		t.Fatal("large-ID issue cursor lost precision", err)
	}
	if _, err = r.ReadDayVerification(ctx, reg, date, "", *got.NextIssueID); !errors.Is(err, af.ErrConflict) {
		t.Fatal("cursor without fixed run accepted", err)
	}
	if _, err = r.ReadDayVerification(ctx, reg, "2026-09-03", runID, 0); !errors.Is(err, af.ErrNotFound) {
		t.Fatal("cross-date run leaked", err)
	}
	wrong := reg
	wrong.SourceGenerationID = strings.Repeat("f", 32)
	if _, err = r.ReadDayVerification(ctx, wrong, date, "", 0); !errors.Is(err, af.ErrIdentity) {
		t.Fatal("wrong generation accepted", err)
	}
	manifestValue["run_id"] = runID
	wrongManifest, _ := json.Marshal(manifestValue)
	wrongHash, _ := af.ManifestHash(wrongManifest)
	wrongDigest, _ := hex.DecodeString(wrongHash)
	if _, err = target.ExecContext(ctx, `UPDATE archive_day_versions SET manifest_json=?,manifest_hash=? WHERE day_version_id=?`, string(wrongManifest), wrongDigest, version); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadDayVerification(ctx, reg, date, "", 0); !errors.Is(err, af.ErrConflict) {
		t.Fatal("self-consistent hash rebound manifest to a different run", err)
	}
	if _, err = target.ExecContext(ctx, `UPDATE archive_day_versions SET manifest_json=?,manifest_hash=? WHERE day_version_id=?`, string(manifest), digest, version); err != nil {
		t.Fatal(err)
	}
	if _, err = target.ExecContext(ctx, `UPDATE archive_day_versions SET manifest_json=JSON_SET(manifest_json,'$.date','2026-09-03') WHERE day_version_id=?`, version); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadDayVerification(ctx, reg, date, "", 0); !errors.Is(err, af.ErrConflict) {
		t.Fatal("corrupt manifest accepted", err)
	}
}
