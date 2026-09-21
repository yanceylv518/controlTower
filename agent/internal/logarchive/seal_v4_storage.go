package logarchive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	af "controltower/internal/archivecontract"
	facts "controltower/internal/archivefacts"
)

func canonicalArchiveJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, ErrWriterCheckpoint
	}
	return json.Marshal(v)
}
func (w *Worker) ensureSealMonths(ctx context.Context, g af.WriterGrant, dates []string) error {
	meta, err := readWriterMeta(ctx, w.target, g.Identity, false)
	if err != nil {
		return err
	}
	if err = requireWriter(meta, g); err != nil {
		return err
	}
	migrations, err := loadFoundationMigrations()
	if err != nil {
		return err
	}
	months := map[string]bool{}
	for _, date := range dates {
		months[strings.ReplaceAll(date[:7], "-", "")] = true
	}
	for i, base := range []string{"archive_billing_evidence_", "billing_facts_"} {
		template := base + "template"
		var count int
		if err = w.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT 1 FROM "+quote(template)+" LIMIT 1) x").Scan(&count); err != nil || count != 0 {
			return ErrFoundationSchema
		}
		want, err := schema(ctx, w.target, template)
		if err != nil {
			return err
		}
		for month := range months {
			table := base + month
			if _, err = w.target.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+quote(table)+" LIKE "+quote(template)); err != nil {
				return err
			}
			got, err := schema(ctx, w.target, table)
			if err != nil || got != want {
				return ErrFoundationSchema
			}
			var engine string
			if err = w.target.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", table).Scan(&engine); err != nil || !strings.EqualFold(engine, "InnoDB") {
				return ErrFoundationSchema
			}
			if err = inspectFoundationIndexes(ctx, w.target, table, migrations[13+i].sql); err != nil {
				return err
			}
		}
	}
	return nil
}

type sealRawPage struct {
	Columns []string
	Types   []facts.Column
	Rows    [][]any
	Done    bool
}

func readSealRawPage(ctx context.Context, tx *sql.Tx, d *sealDay, budget af.ScanBudget, audit bool) (sealRawPage, error) {
	p := sealRawPage{Done: true}
	table := "logs_" + strings.ReplaceAll(d.Date[:7], "-", "")
	exists, err := foundationTableExists(ctx, tx, table)
	if err != nil {
		return p, err
	}
	if !exists {
		return p, nil
	}
	columns, err := tx.QueryContext(ctx, `SELECT COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COALESCE(CHARACTER_SET_NAME,''),COALESCE(COLLATION_NAME,''),EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return p, err
	}
	var definition [][]string
	for columns.Next() {
		c := make([]string, 6)
		if err = columns.Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5]); err != nil {
			columns.Close()
			return p, err
		}
		if strings.Contains(strings.ToUpper(c[5]), "GENERATED") {
			columns.Close()
			return p, sealCode("schema_changed")
		}
		definition = append(definition, c)
		p.Types = append(p.Types, facts.Column{Name: c[0], Type: c[1]})
	}
	err = columns.Err()
	columns.Close()
	if err != nil {
		return p, err
	}
	rawDefinition, _ := json.Marshal(definition)
	fingerprint := sha256.Sum256(rawDefinition)
	if len(definition) == 0 || hex.EncodeToString(fingerprint[:]) != d.Run.SchemaFingerprint {
		return p, sealCode("schema_changed")
	}
	cursor := d.Raw
	if audit {
		cursor = d.Audit
	}
	from, to, _ := af.DateBounds(d.Date)
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quote(table)+` WHERE created_at>=? AND created_at<? AND (created_at>? OR(created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`, from, to, cursor.AfterCreated, cursor.AfterCreated, cursor.AfterID, budget.MaxRows+1)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Columns, err = rows.Columns()
	if err != nil || len(p.Columns) != len(p.Types) {
		return p, ErrFoundationSchema
	}
	for i, c := range p.Columns {
		if c != p.Types[i].Name {
			return p, ErrFoundationSchema
		}
	}
	var total uint64
	started := time.Now()
	for rows.Next() {
		if len(p.Rows) >= budget.MaxRows || time.Since(started) > time.Duration(budget.MaxDurationMillis)*time.Millisecond/2 {
			p.Done = false
			break
		}
		raw := make([]sql.RawBytes, len(p.Columns))
		dest := make([]any, len(raw))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return p, err
		}
		var size uint64
		row := make([]any, len(raw))
		for i, v := range raw {
			size += uint64(len(v))
			if v != nil {
				row[i] = string(v)
			}
		}
		if size > budget.MaxRowBytes {
			return p, sealCode("row_too_large")
		}
		if total+size > budget.MaxBytes {
			p.Done = false
			break
		}
		total += size
		p.Rows = append(p.Rows, row)
	}
	if rows.Err() != nil {
		return p, rows.Err()
	}
	if !p.Done && len(p.Rows) == 0 {
		return p, sealCode("budget_exhausted")
	}
	return p, nil
}

func sealDayPage(ctx context.Context, tx *sql.Tx, d *sealDay, budget af.ScanBudget, audit bool) (bool, error) {
	if _, err := tx.ExecContext(ctx, "SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"); err != nil {
		return false, err
	}
	p, err := readSealRawPage(ctx, tx, d, budget, audit)
	if err != nil {
		return false, err
	}
	var schemaHash [32]byte
	decoded, err := hex.DecodeString(d.Run.SchemaFingerprint)
	if err != nil || len(decoded) != 32 {
		return false, ErrFoundationSchema
	}
	copy(schemaHash[:], decoded)
	for _, row := range p.Rows {
		scan := &d.Raw
		if audit {
			scan = &d.Audit
		}
		rawHash, err := scan.add(p.Columns, row)
		if err != nil {
			return false, err
		}
		values := map[string][]byte{}
		for i, c := range p.Columns {
			if row[i] == nil {
				values[c] = nil
			} else {
				values[c] = []byte(row[i].(string))
			}
		}
		result, err := facts.Build(p.Types, values, schemaHash, rawHash)
		if err != nil || result.Blocking || result.LogDate != d.Date {
			return false, sealCode("fact_invalid")
		}
		if result.Fact == nil {
			continue
		}
		f := *result.Fact
		if err = storeSealFact(ctx, tx, *d, f, result.Evidence, audit); err != nil {
			return false, err
		}
		if !audit {
			if err = addSealFact(d, f); err != nil {
				return false, err
			}
		}
	}
	return p.Done, nil
}

var sealFactColumns = []string{"day_version_id", "source_log_id", "log_date", "created_unix", "log_type", "user_id", "token_id", "channel_id", "username_snapshot", "token_name_snapshot", "model_name", "group_name", "request_id", "upstream_request_id", "quota", "source_prompt_tokens", "source_completion_tokens", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cache_write_5m_tokens", "cache_write_1h_tokens", "image_input_tokens", "image_output_tokens", "audio_input_tokens", "audio_output_tokens", "pricing_snapshot_json", "usage_context_json", "evidence_hash", "raw_row_hash", "fact_hash", "fact_json", "parse_state", "issue_codes_json"}

func sealNullable[T int64 | string](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
func sealFactValues(d sealDay, f facts.Fact) []any {
	raw, _ := json.Marshal(f)
	issues, _ := json.Marshal(f.IssueCodes)
	return []any{archiveID(d.VersionID), f.SourceLogID, f.LogDate, f.CreatedUnix, f.LogType, sealNullable(f.UserID), sealNullable(f.TokenID), sealNullable(f.ChannelID), sealNullable(f.UsernameSnapshot), sealNullable(f.TokenNameSnapshot), sealNullable(f.ModelName), sealNullable(f.GroupName), sealNullable(f.RequestID), sealNullable(f.UpstreamRequestID), sealNullable(f.Quota), sealNullable(f.SourcePromptTokens), sealNullable(f.SourceCompletionTokens), sealNullable(f.InputTokens), sealNullable(f.OutputTokens), sealNullable(f.CacheReadTokens), sealNullable(f.CacheWriteTokens), sealNullable(f.CacheWrite5mTokens), sealNullable(f.CacheWrite1hTokens), sealNullable(f.ImageInputTokens), sealNullable(f.ImageOutputTokens), sealNullable(f.AudioInputTokens), sealNullable(f.AudioOutputTokens), string(f.PricingSnapshotJSON), string(f.UsageContextJSON), f.EvidenceHash[:], f.RawRowHash[:], f.FactHash[:], string(raw), f.ParseState, string(issues)}
}

func storeSealFact(ctx context.Context, tx *sql.Tx, d sealDay, f facts.Fact, e facts.Evidence, audit bool) error {
	month := strings.ReplaceAll(d.Date[:7], "-", "")
	eTable := "archive_billing_evidence_" + month
	fTable := "billing_facts_" + month
	if !audit {
		_, err := tx.ExecContext(ctx, "INSERT INTO "+quote(eTable)+`(evidence_hash,codec_version,source_schema_hash,payload,payload_bytes,created_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE evidence_hash=evidence_hash`, e.Hash[:], e.CodecVersion, e.SourceSchemaHash[:], e.Payload, len(e.Payload))
		if err != nil {
			return err
		}
	}
	var payload, sourceHash []byte
	var codec int
	var size uint64
	if err := tx.QueryRowContext(ctx, "SELECT codec_version,source_schema_hash,payload,payload_bytes FROM "+quote(eTable)+" WHERE evidence_hash=?", e.Hash[:]).Scan(&codec, &sourceHash, &payload, &size); err != nil {
		return sealCode("fact_invalid")
	}
	if codec != e.CodecVersion || !bytes.Equal(sourceHash, e.SourceSchemaHash[:]) || !bytes.Equal(payload, e.Payload) || size != uint64(len(e.Payload)) {
		return sealCode("fact_invalid")
	}
	names := make([]string, len(sealFactColumns))
	for i, c := range sealFactColumns {
		names[i] = quote(c)
	}
	values := sealFactValues(d, f)
	if !audit {
		_, err := tx.ExecContext(ctx, "INSERT INTO "+quote(fTable)+" ("+strings.Join(names, ",")+") VALUES("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+") ON DUPLICATE KEY UPDATE source_log_id=source_log_id", values...)
		if err != nil {
			return err
		}
	}
	got := make([][]byte, len(values))
	dest := make([]any, len(got))
	for i := range got {
		dest[i] = &got[i]
	}
	if err := tx.QueryRowContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM "+quote(fTable)+" WHERE day_version_id=? AND source_log_id=?", archiveID(d.VersionID), f.SourceLogID).Scan(dest...); err != nil {
		return sealCode("fact_invalid")
	}
	for i, want := range values {
		if want == nil {
			if got[i] != nil {
				return sealCode("fact_invalid")
			}
			continue
		}
		if got[i] == nil {
			return sealCode("fact_invalid")
		}
		var raw []byte
		if v, ok := want.([]byte); ok {
			raw = v
		} else {
			raw = []byte(fmt.Sprint(want))
		}
		if strings.HasSuffix(sealFactColumns[i], "_json") {
			left, err := canonicalArchiveJSON(raw)
			if err != nil {
				return err
			}
			right, err := canonicalArchiveJSON(got[i])
			if err != nil || !bytes.Equal(left, right) {
				return sealCode("fact_invalid")
			}
		} else if !bytes.Equal(raw, got[i]) {
			return sealCode("fact_invalid")
		}
	}
	if !audit {
		for _, code := range f.IssueCodes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO archive_fact_issues(day_version_id,source_log_id,issue_code,evidence_hash,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE source_log_id=source_log_id`, archiveID(d.VersionID), f.SourceLogID, code, e.Hash[:]); err != nil {
				return err
			}
		}
		if err := storeSealSubjects(ctx, tx, d, f); err != nil {
			return err
		}
	}
	// The fact is reconstructed from immutable evidence on every audit page,
	// so a matching row hash alone cannot hide corrupted normalized columns.
	if facts.FactDigest(f) != f.FactHash {
		return sealCode("fact_invalid")
	}
	return nil
}

// The projection is keyed by immutable version. Readers must join published
// versions; staging a page cannot change the historical identity of an older
// published version or expose an unfinished build.
func storeSealSubjects(ctx context.Context, tx *sql.Tx, d sealDay, f facts.Fact) error {
	if f.UserID == nil || *f.UserID <= 0 {
		return nil
	}
	type subject struct {
		kind       string
		id, parent int64
		name       *string
	}
	subjects := []subject{{"user", *f.UserID, 0, f.UsernameSnapshot}}
	if f.TokenID != nil && *f.TokenID > 0 {
		subjects = append(subjects, subject{"token", *f.TokenID, *f.UserID, f.TokenNameSnapshot})
	}
	for _, s := range subjects {
		_, err := tx.ExecContext(ctx, `INSERT INTO archive_subject_index(subject_type,subject_id,parent_user_id,name_snapshot,first_log_date,last_log_date,day_version_id) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE name_snapshot=COALESCE(VALUES(name_snapshot),name_snapshot),first_log_date=LEAST(first_log_date,VALUES(first_log_date)),last_log_date=GREATEST(last_log_date,VALUES(last_log_date))`, s.kind, s.id, s.parent, sealNullable(s.name), d.Date, d.Date, archiveID(d.VersionID))
		if err != nil {
			return err
		}
	}
	return nil
}

// CanonicalManifestHash is used by readers after MySQL normalizes JSON spaces.
// All integer values requiring cross-language precision are encoded as strings.
func CanonicalManifestHash(raw []byte) ([32]byte, error) {
	b, err := canonicalArchiveJSON(raw)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}
