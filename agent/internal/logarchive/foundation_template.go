package logarchive

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var foundationInnoDBOption = regexp.MustCompile(`(?i)\bENGINE\s*=\s*InnoDB\b`)

// Only explicit preparation may initialize an entirely empty target. Existing
// databases must already have a compatible logs template and are never adopted
// based solely on a database name or on legacy aggregate counts.
func (w *Worker) prepareFoundationTemplate(ctx context.Context) error {
	var sourceUUID, sourceDatabase, targetUUID, targetDatabase string
	if err := w.source.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&sourceUUID, &sourceDatabase); err != nil || sourceUUID == "" || sourceDatabase == "" {
		return errors.New("archive source identity check failed")
	}
	if err := w.target.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&targetUUID, &targetDatabase); err != nil || targetUUID == "" || targetDatabase == "" {
		return errors.New("archive target identity check failed")
	}
	if samePhysicalArchiveDatabase(sourceUUID, sourceDatabase, targetUUID, targetDatabase) {
		return errors.New("archive target resolves to source database")
	}
	exists, err := foundationTableExists(ctx, w.target, "logs")
	if err != nil || exists {
		return err
	}
	conn, release, err := lockFoundationPreparation(ctx, w.target)
	if err != nil {
		return err
	}
	defer release()
	// Verify the identity of the reserved target session that will perform DDL;
	// do not rely on a pooled connection observed before waiting for the lock.
	if err := conn.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&targetUUID, &targetDatabase); err != nil || targetUUID == "" || targetDatabase == "" {
		return errors.New("archive target identity check failed")
	}
	if samePhysicalArchiveDatabase(sourceUUID, sourceDatabase, targetUUID, targetDatabase) {
		return errors.New("archive target resolves to source database")
	}
	// Another preparation may have initialized the template while we waited.
	exists, err = foundationTableExists(ctx, conn, "logs")
	if err != nil || exists {
		return err
	}
	var tables int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()").Scan(&tables); err != nil {
		return errors.New("archive empty-target check failed")
	}
	if tables != 0 {
		return errors.New("archive target without logs must be an empty database")
	}
	if _, err := schema(ctx, w.source); err != nil {
		return err
	}
	var engine string
	if err := w.source.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs'").Scan(&engine); err != nil || !strings.EqualFold(engine, "InnoDB") {
		return errors.New("archive source logs must use InnoDB")
	}
	rows, err := w.source.QueryContext(ctx, "SELECT INDEX_NAME,COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND NON_UNIQUE=0")
	if err != nil {
		return errors.New("archive source index check failed")
	}
	count := 0
	for rows.Next() {
		var name, column string
		if err := rows.Scan(&name, &column); err != nil || name != "PRIMARY" || column != "id" {
			rows.Close()
			return errors.New("archive template requires only PRIMARY KEY(id) as a unique constraint")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 {
		return errors.New("archive template requires PRIMARY KEY(id)")
	}
	var table, statement string
	if err := w.source.QueryRowContext(ctx, "SHOW CREATE TABLE logs").Scan(&table, &statement); err != nil || table != "logs" {
		return errors.New("archive source template unavailable")
	}
	if err := validateFoundationTemplate(statement); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, statement); err != nil {
		return errors.New("archive empty-target template initialization failed")
	}
	return nil
}

func validateFoundationTemplate(statement string) error {
	trimmed := strings.TrimSpace(statement)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(trimmed, "CREATE TABLE `logs` (") || !foundationInnoDBOption.MatchString(trimmed) || strings.Contains(trimmed, ";") {
		return errors.New("archive source template has an unsupported definition")
	}
	// Reject options that can address files/other databases or depend on
	// external table layout. False positives in comments fail closed.
	for _, forbidden := range []string{"DATA DIRECTORY", "INDEX DIRECTORY", "TABLESPACE", "CONNECTION", "PARTITION", "FOREIGN KEY", "REFERENCES", "ENGINE=FEDERATED", "ENGINE=MYISAM"} {
		if strings.Contains(upper, forbidden) {
			return errors.New("archive source template has unsupported table options")
		}
	}
	return nil
}
