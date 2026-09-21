package logarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controltower/internal/archivecontract"
)

//go:embed migrations/*.sql
var foundationMigrations embed.FS

var (
	ErrFoundationNotPrepared  = errors.New("archive foundation is not prepared")
	ErrFoundationIdentity     = errors.New("archive foundation identity mismatch")
	ErrFoundationSchema       = errors.New("archive foundation schema mismatch; explicit migration required")
	ErrFoundationVersion      = errors.New("archive foundation version is unsupported")
	ErrFoundationLegacyWriter = errors.New("legacy archive writes are disabled for a prepared archive database")
)

// FoundationInfo is safe to include in a registration manifest. Fingerprints
// are hashes; connection strings and physical database identifiers stay local.
type FoundationInfo struct {
	Identity          archivecontract.Identity `json:"identity"`
	FormatVersion     int                      `json:"format_version"`
	SchemaFingerprint string                   `json:"schema_fingerprint"`
	SourceFingerprint string                   `json:"source_fingerprint"`
	CatalogRevision   uint64                   `json:"catalog_revision,string"`
	WriterEpoch       uint64                   `json:"writer_epoch,string"`
}

type foundationFingerprint struct{ source, schema [32]byte }

type foundationQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// HasFoundation fails closed even if preparation stopped before binding the
// metadata row. It does not imply any date is sealed or ready for billing.
func (w *Worker) HasFoundation(ctx context.Context) (bool, error) {
	return foundationTableExists(ctx, w.target, "archive_dataset_meta")
}

func foundationTableExists(ctx context.Context, db foundationQuery, table string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&count); err != nil {
		return false, errors.New("archive foundation table check failed")
	}
	return count != 0, nil
}

func (w *Worker) foundationFingerprints(ctx context.Context) (foundationFingerprint, error) {
	var fp foundationFingerprint
	var uuid, db string
	if err := w.source.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&uuid, &db); err != nil || uuid == "" || db == "" {
		return fp, errors.New("archive source identity check failed")
	}
	identity, _ := json.Marshal([]string{uuid, db})
	fp.source = sha256.Sum256(identity)
	definition, err := schema(ctx, w.source)
	if err != nil {
		return fp, err
	}
	fp.schema = sha256.Sum256([]byte(definition))
	return fp, nil
}

// PrepareFoundation is an explicit, offline preparation operation. Stop legacy
// archive writers before running it. It does not run from the normal archive
// loop, copy logs, adopt legacy counts, grant a writer lease, or publish dates.
func (w *Worker) PrepareFoundation(ctx context.Context, identity archivecontract.Identity) (FoundationInfo, error) {
	var empty FoundationInfo
	if err := identity.Validate(); err != nil {
		return empty, err
	}
	if err := w.prepareFoundationTemplate(ctx); err != nil {
		return empty, err
	}
	if err := w.Check(ctx); err != nil {
		return empty, err
	}
	fp, err := w.foundationFingerprints(ctx)
	if err != nil {
		return empty, err
	}
	conn, release, err := lockFoundationPreparation(ctx, w.target)
	if err != nil {
		return empty, err
	}
	defer release()
	exists, err := foundationTableExists(ctx, conn, "archive_dataset_meta")
	if err != nil {
		return empty, err
	}
	bound := false
	if exists {
		_, err = readFoundation(ctx, conn, identity, fp)
		if err != nil && !errors.Is(err, ErrFoundationNotPrepared) {
			return empty, err
		}
		bound = err == nil
		if bound {
			var active int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM archive_dataset_meta WHERE writer_lease_until > UTC_TIMESTAMP(6)").Scan(&active); err != nil {
				return empty, ErrFoundationSchema
			}
			if active != 0 {
				return empty, errors.New("archive preparation requires a paused writer")
			}
		}
	}
	if !bound {
		if err := requireEmptyFoundation(ctx, conn); err != nil {
			return empty, err
		}
	}
	if err := applyFoundationMigrations(ctx, conn); err != nil {
		return empty, err
	}
	if err := inspectFoundationStructure(ctx, conn); err != nil {
		return empty, err
	}
	if !bound {
		dataset, _ := archivecontract.IDBytes(identity.DatasetID)
		generation, _ := archivecontract.IDBytes(identity.SourceGenerationID)
		_, err = conn.ExecContext(ctx, `INSERT INTO archive_dataset_meta
  (singleton_id,dataset_id,source_generation_id,site_id,format_version,source_identity_hash,schema_fingerprint,writer_epoch,catalog_revision,unscoped_blocking_issues,updated_at)
VALUES (1,?,?,?,?,?,?,0,0,0,UTC_TIMESTAMP(6))`, dataset, generation, identity.SiteID, archivecontract.FormatVersion, fp.source[:], fp.schema[:])
		if err != nil {
			return empty, errors.New("archive dataset binding failed")
		}
	}
	return readFoundation(ctx, conn, identity, fp)
}

func lockFoundationPreparation(ctx context.Context, target *sql.DB) (*sql.Conn, func(), error) {
	conn, err := target.Conn(ctx)
	if err != nil {
		return nil, nil, errors.New("archive preparation connection failed")
	}
	var uuid, database string
	if err := conn.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&uuid, &database); err != nil || uuid == "" || database == "" {
		conn.Close()
		return nil, nil, errors.New("archive target identity check failed")
	}
	lockHash := sha256.Sum256([]byte(strings.ToLower(uuid) + "\x00" + strings.ToLower(database)))
	lockName := fmt.Sprintf("ct.archive.prepare.%x", lockHash[:20])
	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 10)", lockName).Scan(&locked); err != nil || !locked.Valid || locked.Int64 != 1 {
		conn.Close()
		return nil, nil, errors.New("archive preparation is already running or lock unavailable")
	}
	return conn, func() { releaseFoundationLock(conn, lockName); conn.Close() }, nil
}

func releaseFoundationLock(conn *sql.Conn, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var released sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
		// A session with an uncertain advisory lock must not re-enter the pool.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
}

// InspectFoundation performs only reads, including checks against the source.
func (w *Worker) InspectFoundation(ctx context.Context, identity archivecontract.Identity) (FoundationInfo, error) {
	w.invalidateFoundationCache()
	return w.inspectFoundationCached(ctx, identity)
}

func (w *Worker) inspectFoundationUncached(ctx context.Context, identity archivecontract.Identity) (FoundationInfo, error) {
	var empty FoundationInfo
	if err := identity.Validate(); err != nil {
		return empty, err
	}
	if err := w.Check(ctx); err != nil {
		return empty, err
	}
	fp, err := w.foundationFingerprints(ctx)
	if err != nil {
		return empty, err
	}
	exists, err := w.HasFoundation(ctx)
	if err != nil {
		return empty, err
	}
	if !exists {
		return empty, ErrFoundationNotPrepared
	}
	info, err := readFoundation(ctx, w.target, identity, fp)
	if err != nil {
		return empty, err
	}
	migrations, err := loadFoundationMigrations()
	if err != nil {
		return empty, err
	}
	applied, err := foundationMigrationHistory(ctx, w.target, migrations)
	if err != nil {
		return empty, err
	}
	if len(applied) != len(migrations) {
		return empty, ErrFoundationNotPrepared
	}
	if err := inspectFoundationStructure(ctx, w.target); err != nil {
		return empty, err
	}
	return info, nil
}

func readFoundation(ctx context.Context, db foundationQuery, expected archivecontract.Identity, fp foundationFingerprint) (FoundationInfo, error) {
	var info FoundationInfo
	rows, err := db.QueryContext(ctx, `SELECT singleton_id,site_id,dataset_id,source_generation_id,format_version,source_identity_hash,schema_fingerprint,writer_epoch,catalog_revision FROM archive_dataset_meta ORDER BY singleton_id LIMIT 2`)
	if err != nil {
		return info, ErrFoundationSchema
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var singleton int
		var dataset, generation, sourceHash, schemaHash []byte
		if err := rows.Scan(&singleton, &info.Identity.SiteID, &dataset, &generation, &info.FormatVersion, &sourceHash, &schemaHash, &info.WriterEpoch, &info.CatalogRevision); err != nil {
			return FoundationInfo{}, ErrFoundationSchema
		}
		info.Identity.DatasetID, info.Identity.SourceGenerationID = hex.EncodeToString(dataset), hex.EncodeToString(generation)
		if count != 1 || singleton != 1 || !info.Identity.Equal(expected) || !bytes.Equal(sourceHash, fp.source[:]) {
			return FoundationInfo{}, ErrFoundationIdentity
		}
		if info.FormatVersion != archivecontract.FormatVersion {
			return FoundationInfo{}, ErrFoundationVersion
		}
		if !bytes.Equal(schemaHash, fp.schema[:]) {
			return FoundationInfo{}, ErrFoundationSchema
		}
		info.SchemaFingerprint, info.SourceFingerprint = hex.EncodeToString(schemaHash), hex.EncodeToString(sourceHash)
	}
	if rows.Err() != nil {
		return FoundationInfo{}, errors.New("archive dataset metadata read failed")
	}
	if count == 0 {
		return FoundationInfo{}, ErrFoundationNotPrepared
	}
	return info, nil
}

func requireEmptyFoundation(ctx context.Context, db foundationQuery) error {
	for _, table := range []string{"archive_checkpoints", "archive_batch_receipts", "archive_days", "archive_day_versions"} {
		exists, err := foundationTableExists(ctx, db, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM "+quote(table)+" LIMIT 1) AS existing_foundation").Scan(&count); err != nil {
			return ErrFoundationSchema
		}
		if count != 0 {
			return errors.New("archive foundation data has no dataset binding; adoption is forbidden")
		}
	}
	return nil
}

type foundationMigration struct {
	version  int
	sql      string
	checksum [32]byte
}

func loadFoundationMigrations() ([]foundationMigration, error) {
	entries, err := foundationMigrations.ReadDir("migrations")
	if err != nil {
		return nil, errors.New("archive migrations unavailable")
	}
	var migrations []foundationMigration
	for i, entry := range entries {
		if entry.IsDir() {
			return nil, errors.New("invalid archive migration entry")
		}
		raw, err := foundationMigrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, errors.New("archive migration unavailable")
		}
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%03d_", &version); err != nil || version != i+1 {
			return nil, errors.New("archive migrations are not consecutive")
		}
		// Checksums are stable across checkout newline conventions.
		statement := strings.TrimSpace(strings.ReplaceAll(string(raw), "\r\n", "\n"))
		migrations = append(migrations, foundationMigration{version: version, sql: statement, checksum: sha256.Sum256([]byte(statement))})
	}
	return migrations, nil
}

const foundationMigrationLedger = `CREATE TABLE IF NOT EXISTS archive_schema_migrations (
  version INT UNSIGNED NOT NULL PRIMARY KEY,
  checksum BINARY(32) NOT NULL,
  applied_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`

func foundationMigrationHistory(ctx context.Context, db foundationQuery, migrations []foundationMigration) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version,checksum FROM archive_schema_migrations ORDER BY version")
	if err != nil {
		return nil, ErrFoundationNotPrepared
	}
	defer rows.Close()
	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		var checksum []byte
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, ErrFoundationSchema
		}
		if version != len(applied)+1 || version > len(migrations) {
			return nil, ErrFoundationVersion
		}
		if !bytes.Equal(checksum, migrations[version-1].checksum[:]) {
			return nil, ErrFoundationSchema
		}
		applied[version] = true
	}
	if rows.Err() != nil {
		return nil, errors.New("archive migration history read failed")
	}
	return applied, nil
}

func applyFoundationMigrations(ctx context.Context, conn *sql.Conn) error {
	migrations, err := loadFoundationMigrations()
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, foundationMigrationLedger); err != nil {
		return errors.New("archive migration ledger initialization failed")
	}
	applied, err := foundationMigrationHistory(ctx, conn, migrations)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		// MySQL DDL commits independently. Each step is idempotent, and its
		// receipt is appended only after DDL succeeds so interrupted runs resume.
		if err := applyFoundationStatement(ctx, conn, migration.sql); err != nil {
			return fmt.Errorf("archive migration %03d failed", migration.version)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO archive_schema_migrations(version,checksum,applied_at) VALUES(?,?,UTC_TIMESTAMP(6))", migration.version, migration.checksum[:]); err != nil {
			return fmt.Errorf("archive migration %03d receipt failed; retry preparation", migration.version)
		}
	}
	return nil
}

// ADD COLUMN is not idempotent on all supported MySQL versions. An interrupted
// migration may have committed its DDL but lost the receipt. Only the exact
// expected column is accepted in that case; conflicting definitions fail closed.
func applyFoundationStatement(ctx context.Context, conn *sql.Conn, statement string) error {
	if table, column, ok := foundationAddedColumn(statement); ok {
		var got foundationColumn
		err := conn.QueryRowContext(ctx, `SELECT COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COALESCE(CHARACTER_SET_NAME,''),COALESCE(COLLATION_NAME,''),EXTRA,COALESCE(COLUMN_DEFAULT,'') FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, column.name).Scan(&got.name, &got.kind, &got.nullable, &got.charset, &got.collation, &got.extra, &got.defaultValue)
		if err == nil {
			got.kind = foundationIntegerWidth.ReplaceAllString(got.kind, "$1")
			if got != column {
				return ErrFoundationSchema
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return ErrFoundationSchema
		}
	}
	_, err := conn.ExecContext(ctx, statement)
	return err
}
