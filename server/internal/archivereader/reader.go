// Package archivereader opens dedicated, table-scoped SELECT-only connections.
// It deliberately exposes neither a SQL handle nor raw log/evidence reads.
package archivereader

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	ac "controltower/internal/archivecontract"
	"github.com/go-sql-driver/mysql"
)

var ErrUnavailable = errors.New("archive_readonly_unavailable")
var ErrPermissions = errors.New("archive_readonly_permissions_required")
var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)
var factTable = regexp.MustCompile(`^billing_facts_[0-9]{6}$`)
var selectGrant = regexp.MustCompile("^GRANT SELECT ON `([^`]+)`\\.`([^`]+)` TO ")

type Reader struct{ ConnectionsFile string }

// The file contains storage references -> environment variable names, not DSNs.
// Both are deployment-owned; an API caller can only select a registered ref.
func (r Reader) open(ctx context.Context, ref string) (*sql.DB, error) {
	f, err := os.Open(r.ConnectionsFile)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	var entries map[string]struct {
		DSNEnv string `json:"dsn_env"`
	}
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if d.Decode(&entries) != nil || d.Decode(new(any)) != io.EOF || !envName.MatchString(entries[ref].DSNEnv) {
		return nil, ErrUnavailable
	}
	cfg, err := mysql.ParseDSN(os.Getenv(entries[ref].DSNEnv))
	if err != nil || cfg.DBName == "" || cfg.User == "" {
		return nil, ErrUnavailable
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.MultiStatements = false
	cfg.AllowAllFiles = false
	cfg.AllowCleartextPasswords = false
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	// Do not execute arbitrary session variables from a DSN.
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, ErrUnavailable
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, ErrUnavailable
	}
	if err = checkGrants(ctx, db, cfg.DBName); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func permittedGrant(grant, database string) bool {
	if strings.HasPrefix(grant, "GRANT USAGE ON *.* TO ") && !strings.Contains(grant, "WITH GRANT OPTION") {
		return true
	}
	m := selectGrant.FindStringSubmatch(grant)
	if len(m) != 3 || m[1] != database || strings.Contains(grant, "WITH GRANT OPTION") {
		return false
	}
	switch m[2] {
	case "archive_dataset_meta", "archive_days", "archive_day_versions", "archive_schema_migrations", "archive_subject_index", "archive_reconcile_runs", "archive_reconcile_issues", "archive_fact_issues", "archive_raw_repairs":
		return true
	}
	return factTable.MatchString(m[2])
}
func checkGrants(ctx context.Context, db *sql.DB, database string) error {
	var role string
	if db.QueryRowContext(ctx, "SELECT CURRENT_ROLE()").Scan(&role) != nil || role != "NONE" {
		return ErrPermissions
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR CURRENT_USER()")
	if err != nil {
		return ErrPermissions
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var grant string
		if rows.Scan(&grant) != nil || !permittedGrant(grant, database) {
			return ErrPermissions
		}
		n++
	}
	if rows.Err() != nil || n == 0 {
		return ErrPermissions
	}
	return nil
}

func (r Reader) ReadCatalog(ctx context.Context, registration ac.Registration) (ac.CatalogSnapshot, error) {
	var snapshot ac.CatalogSnapshot
	if err := registration.Validate(); err != nil {
		return snapshot, err
	}
	db, err := r.open(ctx, registration.StorageRef)
	if err != nil {
		return snapshot, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return snapshot, ErrUnavailable
	}
	defer tx.Rollback()
	var count int
	if tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_dataset_meta").Scan(&count) != nil || count != 1 {
		return snapshot, ac.ErrIdentity
	}
	var dataset, generation, schema, source []byte
	var site string
	var format int
	err = tx.QueryRowContext(ctx, `SELECT dataset_id,source_generation_id,site_id,format_version,schema_fingerprint,source_identity_hash,catalog_revision,unscoped_blocking_issues FROM archive_dataset_meta WHERE singleton_id=1`).Scan(&dataset, &generation, &site, &format, &schema, &source, &snapshot.CatalogRevision, &snapshot.UnscopedBlockingIssues)
	if err != nil {
		return snapshot, ErrUnavailable
	}
	snapshot.Identity = ac.Identity{SiteID: site, DatasetID: hex.EncodeToString(dataset), SourceGenerationID: hex.EncodeToString(generation)}
	if !snapshot.Identity.Equal(registration.Identity) || hex.EncodeToString(schema) != registration.SchemaFingerprint || hex.EncodeToString(source) != registration.SourceFingerprint {
		return snapshot, ac.ErrIdentity
	}
	if format != ac.FormatVersion {
		return snapshot, ac.ErrUnsupported
	}
	rows, err := tx.QueryContext(ctx, `SELECT DATE_FORMAT(d.log_date,'%Y-%m-%d'),d.state,d.current_version_id,v.manifest_hash,d.mutation_revision,v.all_rows,v.consume_rows,v.consume_quota,v.published_at,d.blocking_issue_count,v.state,d.catalog_revision,v.verified_mutation_revision,v.publish_revision FROM archive_days d LEFT JOIN archive_day_versions v ON v.day_version_id=d.current_version_id AND v.log_date=d.log_date ORDER BY d.log_date LIMIT 20001`)
	if err != nil {
		return snapshot, ErrUnavailable
	}
	for rows.Next() {
		var day ac.CatalogDay
		var version, manifest []byte
		var all, consume, quota, versionState sql.NullString
		var verified sql.NullTime
		var blocking uint64
		var dayRevision uint64
		var verifiedRevision, publishRevision sql.Null[uint64]
		if rows.Scan(&day.Date, &day.State, &version, &manifest, &day.MutationRevision, &all, &consume, &quota, &verified, &blocking, &versionState, &dayRevision, &verifiedRevision, &publishRevision) != nil {
			rows.Close()
			return snapshot, ErrUnavailable
		}
		day.CurrentVersionID = hex.EncodeToString(version)
		day.ManifestHash = hex.EncodeToString(manifest)
		if all.Valid {
			day.AllRows = &all.String
		}
		if consume.Valid {
			day.ConsumeRows = &consume.String
		}
		if quota.Valid {
			day.ConsumeQuota = &quota.String
		}
		if verified.Valid {
			day.VerifiedAt = &verified.Time
		}
		if blocking > 0 {
			day.BlockReason = "blocking_issues"
		}
		if dayRevision > snapshot.CatalogRevision || (day.State == "sealed" && (versionState.String != "published" || !verifiedRevision.Valid || verifiedRevision.V != day.MutationRevision || !publishRevision.Valid || publishRevision.V > snapshot.CatalogRevision)) {
			rows.Close()
			return snapshot, ac.ErrConflict
		}
		snapshot.Days = append(snapshot.Days, day)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return snapshot, ErrUnavailable
	}
	if err = snapshot.Validate(); err != nil {
		return snapshot, err
	}
	if err = tx.Commit(); err != nil {
		return snapshot, ErrUnavailable
	}
	return snapshot, nil
}
