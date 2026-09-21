package mysqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"

	"github.com/go-sql-driver/mysql"
)

// RegisterArchiveDataset binds an independently prepared archive schema to a
// paused site. Registration deliberately does not enable a writer or billing.
func (s Store) RegisterArchiveDataset(ctx context.Context, r af.Registration, actor string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if actor == "" || len(actor) > 128 {
		return errors.New("invalid archive registration actor")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Use the same member -> site-control lock order as the legacy poller.
	rows, err := tx.QueryContext(ctx, `SELECT id,`+archiveSiteExpr+` FROM instances WHERE deleted=0 AND `+archiveSiteExpr+`=? ORDER BY id FOR UPDATE`, r.SiteID)
	if err != nil {
		return err
	}
	var members []string
	for rows.Next() {
		var id, actualSite string
		if err = rows.Scan(&id, &actualSite); err != nil {
			_ = rows.Close()
			return err
		}
		// Legacy CT identifiers use a case-insensitive collation. Dataset
		// identities are exact and must not bind an alternate spelling.
		if actualSite != r.SiteID {
			_ = rows.Close()
			return af.ErrIdentity
		}
		members = append(members, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	if len(members) == 0 {
		return af.ErrNotFound
	}
	if err = ensureArchive(ctx, tx, r.SiteID); err != nil {
		return err
	}
	c, _, _, lease, err := archiveRow(ctx, tx, r.SiteID)
	if err != nil {
		return err
	}
	live, err := archiveLeaseLive(ctx, tx, lease)
	if err != nil {
		return err
	}
	if c.Running || live {
		return af.ErrConflict
	}
	var preparation []byte
	if err = tx.QueryRowContext(ctx, `SELECT prepare_json FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&preparation); err != nil {
		return err
	}
	if len(preparation) != 0 {
		return af.ErrConflict
	}
	var active []byte
	if err = tx.QueryRowContext(ctx, `SELECT active_dataset_id FROM site_log_archive_control WHERE site_id=?`, r.SiteID).Scan(&active); err != nil {
		return err
	}
	datasetID := archiveIDBytes(r.DatasetID)
	existing, err := readArchiveDataset(ctx, tx, r.SiteID, r.DatasetID, true)
	if err == nil {
		if !sameArchiveRegistration(existing.Registration, r) || existing.LifecycleState != "paused" {
			return af.ErrConflict
		}
		if hex.EncodeToString(active) == hex.EncodeToString(datasetID) {
			return tx.Commit()
		}
	} else if !errors.Is(err, af.ErrNotFound) {
		return err
	} else {
		identity, _ := json.Marshal(archiveSourceIdentity{SchemaFingerprint: r.SchemaFingerprint, SourceFingerprint: r.SourceFingerprint})
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_datasets(dataset_id,site_id,source_generation_id,storage_ref,source_identity_json,archive_format_version,lifecycle_state,created_at,updated_at) VALUES(?,?,?,?,?,?,'paused',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, datasetID, r.SiteID, archiveIDBytes(r.SourceGenerationID), r.StorageRef, string(identity), r.ArchiveFormatVersion)
		if err != nil {
			return archiveConflictError(err)
		}
	}
	before, _ := json.Marshal(c)
	c.Version++
	c.Running = false
	c.ReconcileID, c.ReconcileDate = "", ""
	after, _ := json.Marshal(c)
	if _, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET active_dataset_id=?,required_protocol_version=2,config_json=?,status_json='{}',seen_at=NULL,session_id='',lease_until=NULL WHERE site_id=?`, datasetID, string(after), r.SiteID); err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	summary, _ := json.Marshal(struct {
		Registration af.Registration `json:"registration"`
		State        string          `json:"state"`
	}{r, "paused"})
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,'archive.manage','archive_dataset',?,?,?,?,'success',UTC_TIMESTAMP(6))`, hex.EncodeToString(raw), members[0], r.DatasetID, actor, string(before), string(summary))
	if err != nil {
		return err
	}
	return tx.Commit()
}

type archiveSourceIdentity struct {
	SchemaFingerprint string `json:"schema_fingerprint"`
	SourceFingerprint string `json:"source_fingerprint"`
}

type archiveDatasetQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s Store) GetArchiveDataset(ctx context.Context, siteID, datasetID string) (af.Dataset, error) {
	if len(archiveIDBytes(datasetID)) != 16 || siteID == "" || len(siteID) > 64 {
		return af.Dataset{}, af.ErrIdentity
	}
	return readArchiveDataset(ctx, s.db, siteID, datasetID, false)
}

func readArchiveDataset(ctx context.Context, db archiveDatasetQuerier, site, id string, lock bool) (af.Dataset, error) {
	var d af.Dataset
	var datasetID, generationID, hash []byte
	var sourceJSON []byte
	query := `SELECT dataset_id,site_id,source_generation_id,storage_ref,source_identity_json,archive_format_version,lifecycle_state,config_revision,observed_catalog_revision,observed_catalog_hash,unscoped_blocking_issues,created_at,updated_at FROM archive_datasets WHERE dataset_id=? AND site_id=?`
	if lock {
		query += " FOR UPDATE"
	}
	err := db.QueryRowContext(ctx, query, archiveIDBytes(id), site).Scan(&datasetID, &d.SiteID, &generationID, &d.StorageRef, &sourceJSON, &d.ArchiveFormatVersion, &d.LifecycleState, &d.ConfigRevision, &d.ObservedCatalogRevision, &hash, &d.UnscopedBlockingIssues, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, af.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.DatasetID, d.SourceGenerationID, d.CatalogHash = hex.EncodeToString(datasetID), hex.EncodeToString(generationID), hex.EncodeToString(hash)
	var source archiveSourceIdentity
	if err = json.Unmarshal(sourceJSON, &source); err != nil {
		return d, errors.New("invalid archive source identity")
	}
	d.SchemaFingerprint, d.SourceFingerprint = source.SchemaFingerprint, source.SourceFingerprint
	return d, nil
}

// ApplyArchiveCatalog replaces a complete directory snapshot atomically. It is
// called with the result of an identity-checked read-only archive connection,
// never with Agent-reported legacy day counters.
func (s Store) ApplyArchiveCatalog(ctx context.Context, snapshot af.CatalogSnapshot) (bool, error) {
	hash, err := snapshot.Hash()
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	d, err := readArchiveDataset(ctx, tx, snapshot.SiteID, snapshot.DatasetID, true)
	if err != nil {
		return false, err
	}
	if !d.Identity.Equal(snapshot.Identity) {
		return false, af.ErrIdentity
	}
	if snapshot.CatalogRevision < d.ObservedCatalogRevision {
		return false, tx.Commit()
	}
	if snapshot.CatalogRevision == d.ObservedCatalogRevision && d.CatalogHash != "" {
		if hash != d.CatalogHash {
			return false, af.ErrConflict
		}
		return false, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM archive_day_catalog WHERE dataset_id=?`, archiveIDBytes(snapshot.DatasetID)); err != nil {
		return false, err
	}
	for _, day := range snapshot.Days {
		_, err = tx.ExecContext(ctx, `INSERT INTO archive_day_catalog(dataset_id,log_date,state,current_version_id,manifest_hash,catalog_revision,mutation_revision,all_rows,consume_rows,consume_quota,verified_at,block_reason,synced_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, archiveIDBytes(snapshot.DatasetID), day.Date, day.State, archiveNullableID(day.CurrentVersionID), archiveNullableHash(day.ManifestHash), snapshot.CatalogRevision, day.MutationRevision, day.AllRows, day.ConsumeRows, day.ConsumeQuota, day.VerifiedAt, archiveNullableText(day.BlockReason))
		if err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE archive_datasets SET observed_catalog_revision=?,observed_catalog_hash=?,unscoped_blocking_issues=?,updated_at=UTC_TIMESTAMP(6) WHERE dataset_id=?`, snapshot.CatalogRevision, archiveNullableHash(hash), snapshot.UnscopedBlockingIssues, archiveIDBytes(snapshot.DatasetID))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func sameArchiveRegistration(a, b af.Registration) bool {
	return a.Identity.Equal(b.Identity) && a.StorageRef == b.StorageRef && a.SchemaFingerprint == b.SchemaFingerprint && a.SourceFingerprint == b.SourceFingerprint && a.ArchiveFormatVersion == b.ArchiveFormatVersion
}

func archiveIDBytes(id string) []byte {
	b, _ := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return b
}

func archiveNullableID(id string) any {
	if id == "" {
		return nil
	}
	return archiveIDBytes(id)
}

func archiveNullableHash(hash string) any {
	if hash == "" {
		return nil
	}
	b, _ := hex.DecodeString(hash)
	return b
}

func archiveNullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func archiveConflictError(err error) error {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return af.ErrConflict
	}
	return err
}

func archiveFoundationBinding(ctx context.Context, tx *sql.Tx, site string) ([]byte, error) {
	var id []byte
	err := tx.QueryRowContext(ctx, `SELECT active_dataset_id FROM site_log_archive_control WHERE site_id=?`, site).Scan(&id)
	return id, err
}

func archiveFoundationPoll(ctx context.Context, tx *sql.Tx, site string, active []byte, st ac.Status) error {
	if st.Foundation == nil {
		return nil
	}
	if err := st.Foundation.Validate(); err != nil {
		return err
	}
	if len(active) == 0 || st.SiteID != site || st.Foundation.SiteID != site || hex.EncodeToString(active) != hex.EncodeToString(archiveIDBytes(st.Foundation.DatasetID)) {
		return af.ErrIdentity
	}
	d, err := readArchiveDataset(ctx, tx, site, st.Foundation.DatasetID, false)
	if err != nil {
		return err
	}
	if !d.Identity.Equal(st.Foundation.Identity) || d.ArchiveFormatVersion != st.Foundation.FormatVersion {
		return af.ErrIdentity
	}
	if len(st.Days) != 0 || st.Reconciliation != nil {
		return af.ErrUnsupported
	}
	return nil
}
