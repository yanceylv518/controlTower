package archivejob

// Large records are staged in bounded chunks. Partial rows never enter a month
// table, and neither a raw cursor nor a summary cursor advances before commit.
import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"
	"time"
)

const largeChunkBytes = 4 * 1024 * 1024
const largeChunksPerTurn = 2

type largeCandidate struct {
	Table       string
	ID, Created int64
}

func (*largeCandidate) Error() string { return "archive_large_record_pending" }

type largeColumn struct {
	Name  string
	Bytes int64
	Null  bool
}
type largeRecord struct {
	Token, Mode, Table string
	ID, Created        int64
	Columns            []largeColumn
	Column             int
	Offset             int64
	HashState          []byte
	FieldHashState     []byte
	FieldDigests       []string
	ChunkBytes         int64
	Digest             string
	Summary            row
	Projection         *jsonProjection
	LiveVersion        string
}

func scanColumns(ctx context.Context, db queryer, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+q(table)+" LIMIT 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	sort.Strings(cols)
	return cols, err
}

// Preflight lengths rather than letting the MySQL driver materialize an entire
// oversized row. Ordinary rows still use the original query and bounded pages.
func readArchivePage(ctx context.Context, db queryer, table, query string, args ...any) ([]row, bool, error) {
	if len(args) == 0 || (table != "logs" && !monthName.MatchString(table)) || !strings.Contains(query, " * FROM ") {
		return nil, false, errors.New("archive_page_query_invalid")
	}
	cols, err := scanColumns(ctx, db, table)
	if err != nil {
		return nil, false, err
	}
	lengths := make([]string, len(cols))
	for i, col := range cols {
		lengths[i] = "COALESCE(OCTET_LENGTH(CAST(" + q(col) + " AS BINARY)),0)"
	}
	meta := strings.Replace(query, " * FROM ", " id,created_at,"+strings.Join(lengths, "+")+" FROM ", 1)
	records, err := db.QueryContext(ctx, meta, args...)
	if err != nil {
		return nil, false, err
	}
	count, total := 0, int64(0)
	limited := false
	var candidate *largeCandidate
	for records.Next() {
		var id, created, size int64
		if err = records.Scan(&id, &created, &size); err != nil {
			break
		}
		if size > maxPageBytes {
			limited = true
			if count == 0 {
				candidate = &largeCandidate{table, id, created}
			}
			break
		}
		if total+size > maxPageBytes {
			limited = true
			break
		}
		total += size
		count++
	}
	if err == nil {
		err = records.Err()
	}
	records.Close()
	if err != nil {
		return nil, false, err
	}
	if candidate != nil {
		return nil, false, candidate
	}
	if count == 0 {
		return []row{}, false, nil
	}
	bounded := append([]any(nil), args...)
	bounded[len(bounded)-1] = count
	out, byteLimited, err := readPage(ctx, db, query, bounded...)
	return out, limited || byteLimited, err
}
func largeMetadata(ctx context.Context, db queryer, table string, id int64) ([]largeColumn, error) {
	cols, err := scanColumns(ctx, db, table)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(cols))
	values := make([]sql.NullInt64, len(cols))
	dest := make([]any, len(cols))
	for i, col := range cols {
		parts[i] = "OCTET_LENGTH(CAST(" + q(col) + " AS BINARY))"
		dest[i] = &values[i]
	}
	if err = db.QueryRowContext(ctx, "SELECT "+strings.Join(parts, ",")+" FROM "+q(table)+" WHERE id=?", id).Scan(dest...); err != nil {
		return nil, err
	}
	out := make([]largeColumn, len(cols))
	for i, col := range cols {
		out[i] = largeColumn{col, values[i].Int64, !values[i].Valid}
	}
	return out, nil
}
func largeHash(state []byte) (hash.Hash, error) {
	h := sha256.New()
	if len(state) > 0 {
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(state); err != nil {
			return nil, errors.New("archive_large_hash_state_invalid")
		}
	}
	return h, nil
}
func hashSize(h hash.Hash, n int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	h.Write(b[:])
}
func hashFieldHeader(h hash.Hash, col largeColumn) {
	hashSize(h, int64(len(col.Name)))
	h.Write([]byte(col.Name))
	if col.Null {
		h.Write([]byte{0})
	} else {
		h.Write([]byte{1})
		hashSize(h, col.Bytes)
	}
}
func chainDigest(previous, digest string) string {
	sum := sha256.Sum256([]byte(previous + digest))
	return hex.EncodeToString(sum[:])
}
func (e *Engine) startLarge(ctx context.Context, c *sql.Conn, s *state, candidate *largeCandidate, mode string) error {
	var db queryer = c
	if candidate.Table == "logs" {
		db = e.source
	}
	cols, err := largeMetadata(ctx, db, candidate.Table, candidate.ID)
	if err != nil {
		return err
	}
	chunkSize := int64(largeChunkBytes)
	for _, conn := range []queryer{db, c} {
		var packet int64
		if err := conn.QueryRowContext(ctx, "SELECT @@max_allowed_packet").Scan(&packet); err != nil {
			return err
		}
		for chunkSize+4096 > packet && chunkSize > 64*1024 {
			chunkSize /= 2
		}
		if chunkSize+4096 > packet {
			return errors.New("archive_chunk_packet_limit")
		}
	}
	rec := &largeRecord{ChunkBytes: chunkSize, Summary: row{}, Token: id(), Mode: mode, Table: candidate.Table, ID: candidate.ID, Created: candidate.Created, Columns: cols, FieldDigests: make([]string, len(cols))}
	if mode == "live_summary" {
		if err = c.QueryRowContext(ctx, "SELECT version_id FROM log_archive_live_stats WHERE log_date=?", day(candidate.Created)).Scan(&rec.LiveVersion); err != nil {
			return err
		}
		s.LargeLive = rec
		return nil
	} else if mode == "collect" {
		s.LargeCollection = rec
	} else {
		s.LargeHistory = rec
	}
	p := &s.Collection
	if mode != "collect" {
		p = &s.HistoryProgress
	}
	p.Step = mode
	p.Date = day(rec.Created)
	p.Table = table(day(rec.Created))
	p.Error = ""
	p.UpdatedAt = time.Now().UTC()
	return nil // outer Step atomically persists the new transfer without moving data cursors
}
func (e *Engine) largeStep(ctx context.Context, c *sql.Conn, s *state, r *largeRecord) error {
	err := e.processLarge(ctx, c, s, r)
	if r.Mode == "summarize" && largeSummaryDataError(err) {
		return failDay(ctx, c, s, code(err))
	}
	return err
}

func largeSummaryDataError(err error) bool {
	return err != nil && (strings.HasPrefix(err.Error(), "invalid_billing_") || err.Error() == "archive_large_pricing_evidence_limit" || err.Error() == "archive_large_dimension_limit")
}
func (e *Engine) processLarge(ctx context.Context, c *sql.Conn, s *state, r *largeRecord) error {
	if len(r.Columns) == 0 || len(r.FieldDigests) != len(r.Columns) || r.Column < 0 || r.Column > len(r.Columns) || r.Offset < 0 || (r.Column < len(r.Columns) && r.Offset > r.Columns[r.Column].Bytes) {
		return errors.New("archive_large_checkpoint_invalid")
	}
	if r.ChunkBytes < 64*1024 || r.ChunkBytes > largeChunkBytes {
		return errors.New("archive_large_chunk_size_invalid")
	}
	if r.Mode == "live_summary" {
		var version string
		if err := c.QueryRowContext(ctx, "SELECT version_id FROM log_archive_live_stats WHERE log_date=?", day(r.Created)).Scan(&version); err != nil {
			return err
		}
		if version != r.LiveVersion {
			s.LargeLive = nil
			return saveStandalone(ctx, c, *s)
		}
	}
	if r.Mode != "collect" && r.Mode != "live_summary" && (s.History.Step != r.Mode || s.History.Date != day(r.Created)) {
		return errors.New("archive_large_checkpoint_mismatch")
	}
	if r.Mode == "verify_archive" || r.Mode == "summarize" {
		var revision uint64
		if err := c.QueryRowContext(ctx, "SELECT revision FROM log_archive_days WHERE log_date=?", s.History.Date).Scan(&revision); err != nil {
			return err
		}
		if revision != s.History.Revision {
			tx, err := c.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err = tx.ExecContext(ctx, "DELETE FROM log_archive_large_chunks WHERE transfer_id=?", r.Token); err != nil {
				return err
			}
			s.LargeHistory = nil
			s.History = history{}
			if err = save(ctx, tx, *s); err != nil {
				return err
			}
			return tx.Commit()
		}
	}
	var db queryer = c
	if r.Table == "logs" {
		db = e.source
	}
	if r.Digest != "" {
		return e.finishLarge(ctx, c, s, r)
	}
	cols, err := largeMetadata(ctx, db, r.Table, r.ID)
	if err != nil {
		return err
	}
	if fmt.Sprint(cols) != fmt.Sprint(r.Columns) {
		return errors.New("archive_large_record_changed")
	}
	fieldHash, err := largeHash(r.FieldHashState)
	if err != nil {
		return err
	}
	h, err := largeHash(r.HashState)
	if err != nil {
		return err
	}
	// Read first, then start target transaction: target reads and writes share a connection.
	type chunk struct {
		col   int
		index int64
		data  []byte
	}
	chunks := []chunk{}
	started := time.Now()
	for len(chunks) < largeChunksPerTurn && r.Column < len(r.Columns) {
		col := r.Columns[r.Column]
		if r.isSummary() && !summaryColumns[col.Name] {
			r.Column++
			continue
		}
		if r.isSummary() && col.Null {
			r.Summary[col.Name] = nil
		}
		if r.isSummary() && !col.Null && col.Bytes == 0 {
			v := ""
			r.Summary[col.Name] = &v
		}
		if r.Offset == 0 {
			hashFieldHeader(h, col)
		}
		if col.Null || col.Bytes == 0 {
			r.Column++
			continue
		}
		length := r.ChunkBytes
		if remaining := col.Bytes - r.Offset; remaining < length {
			length = remaining
		}
		var data []byte
		err = db.QueryRowContext(ctx, "SELECT /*+ MAX_EXECUTION_TIME(3000) */ SUBSTRING(CAST("+q(col.Name)+" AS BINARY),?,?) FROM "+q(r.Table)+" WHERE id=?", r.Offset+1, length, r.ID).Scan(&data)
		if err != nil {
			return err
		}
		if int64(len(data)) != length {
			return errors.New("archive_large_chunk_changed")
		}
		if r.isSummary() {
			if col.Name == "other" {
				if r.Projection == nil {
					r.Projection = &jsonProjection{}
				}
				if err = r.Projection.Feed(data); err != nil {
					return err
				}
			} else {
				value := r.Summary.text(col.Name)
				if len(value)+len(data) > maxLargeSummaryBytes {
					return errors.New("archive_large_dimension_limit")
				}
				value += string(data)
				r.Summary[col.Name] = &value
			}
		}
		h.Write(data)
		fieldHash.Write(data)
		chunks = append(chunks, chunk{r.Column, r.Offset / r.ChunkBytes, data})
		r.Offset += length
		if r.Offset == col.Bytes {
			r.FieldDigests[r.Column] = hex.EncodeToString(fieldHash.Sum(nil))
			fieldHash = sha256.New()
			r.Column++
			r.Offset = 0
		}
		if time.Since(started) >= time.Second {
			break
		}
	}
	r.FieldHashState, err = fieldHash.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	r.HashState, err = h.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return err
	}
	if r.Column == len(r.Columns) {
		r.Digest = hex.EncodeToString(h.Sum(nil))
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Archive verification only needs the rolling digest, not a second copy of payload.
	if r.Mode != "verify_archive" && !r.isSummary() {
		for _, chunk := range chunks {
			if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_large_chunks VALUES(?,?,?,?)", r.Token, chunk.col, chunk.index, chunk.data); err != nil {
				return err
			}
		}
	}
	p := &s.Collection
	if r.Mode != "collect" {
		p = &s.HistoryProgress
	}
	if r.Mode != "live_summary" {
		p.Error = ""
		p.UpdatedAt = time.Now().UTC()
	}
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}

// Compare complete field lengths and SHA-256 digests in the archive database, returning only a boolean to
// the Agent. This also validates already-existing large legacy month records.
func matchesLarge(ctx context.Context, tx *sql.Tx, table string, r *largeRecord) (bool, error) {
	for i, col := range r.Columns {
		var length sql.NullInt64
		if err := tx.QueryRowContext(ctx, "SELECT OCTET_LENGTH(CAST("+q(col.Name)+" AS BINARY)) FROM "+q(table)+" WHERE id=?", r.ID).Scan(&length); err != nil {
			return false, err
		}
		if length.Valid == col.Null || (length.Valid && length.Int64 != col.Bytes) {
			return false, nil
		}
		if col.Null || col.Bytes == 0 {
			continue
		}
		var digest sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT SHA2(CAST("+q(col.Name)+" AS BINARY),256) FROM "+q(table)+" WHERE id=?", r.ID).Scan(&digest)
		if err != nil {
			return false, err
		}
		if !digest.Valid || digest.String != r.FieldDigests[i] {
			return false, nil
		}

	}
	return true, nil
}
func publishLarge(ctx context.Context, tx *sql.Tx, table string, r *largeRecord) (bool, bool, error) {
	var found int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM "+q(table)+" WHERE id=? FOR UPDATE", r.ID).Scan(&found)
	inserted := errors.Is(err, sql.ErrNoRows)
	if err != nil && !inserted {
		return false, false, err
	}
	if !inserted {
		equal, err := matchesLarge(ctx, tx, table, r)
		if err != nil {
			return false, false, err
		}
		if equal {
			return false, false, nil
		}
	}
	var packet int64
	if err = tx.QueryRowContext(ctx, "SELECT @@max_allowed_packet").Scan(&packet); err != nil {
		return false, false, err
	}
	max := int64(0)
	for _, col := range r.Columns {
		if col.Bytes > max {
			max = col.Bytes
		}
	}
	if max+4096 > packet {
		return false, false, fmt.Errorf("archive_target_packet_limit:id=%d,required=%d,configured=%d", r.ID, max+4096, packet)
	}
	// group_concat_max_len is set on the owned connection before this transaction.
	names, values, updates := []string{}, []string{}, []string{}
	args := []any{}
	for i, col := range r.Columns {
		name := q(col.Name)
		names = append(names, name)
		if col.Null {
			values = append(values, "NULL")
		} else if col.Bytes == 0 {
			values = append(values, "_binary''")
		} else {
			values = append(values, "(SELECT GROUP_CONCAT(payload ORDER BY chunk_index SEPARATOR '') FROM log_archive_large_chunks WHERE transfer_id=? AND column_index=?)")
			args = append(args, r.Token, i)
		}
		if col.Name != "id" {
			updates = append(updates, name+"=VALUES("+name+")")
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO "+q(table)+" ("+strings.Join(names, ",")+") SELECT "+strings.Join(values, ",")+" ON DUPLICATE KEY UPDATE "+strings.Join(updates, ","), args...)
	if err != nil {
		return false, false, err
	}
	equal, err := matchesLarge(ctx, tx, table, r)
	if err != nil {
		return false, false, err
	}
	if !equal {
		return false, false, errors.New("archive_large_publish_mismatch")
	}
	return inserted, !inserted, nil
}
func (e *Engine) finishLarge(ctx context.Context, c *sql.Conn, s *state, r *largeRecord) error {
	var aggregateKey string
	var a aggregate
	var err error
	if r.isSummary() {
		if r.Projection != nil {
			value, parseErr := r.Projection.Finish()
			if parseErr != nil {
				return parseErr
			}
			r.Summary["other"] = &value
		}
		aggregateKey, a, err = parseAggregate(r.Summary)
		if err != nil {
			return err
		}
	}
	if r.Mode == "collect" || r.Mode == "verify_source" {
		if err = e.ensureMonth(ctx, c, table(day(r.Created))); err != nil {
			return err
		}
		max := int64(0)
		for _, col := range r.Columns {
			if col.Bytes > max {
				max = col.Bytes
			}
		}
		if _, err = c.ExecContext(ctx, "SET SESSION group_concat_max_len=?", max+1); err != nil {
			return err
		}
	}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.Mode == "collect" || r.Mode == "verify_source" {
		inserted, changed, err := publishLarge(ctx, tx, table(day(r.Created)), r)
		if err != nil {
			return err
		}
		if err = touchRawDay(ctx, tx, s, day(r.Created), inserted || changed); err != nil {
			return err
		}
		// A large raw replacement must not materialize its old value just to
		// compute a live delta. Atomically invalidate and rebuild this date.
		if (inserted || changed) && s.includesSummary(day(r.Created)) {
			if err = invalidateLive(ctx, tx, day(r.Created)); err != nil {
				return err
			}
		}
		before := s.Collection.AfterID
		task := "collection"
		if r.Mode != "collect" {
			before = s.History.AfterID
			task = "history"
		}
		ins, ch, un := 0, 0, 0
		if inserted {
			ins = 1
		} else if changed {
			ch = 1
		} else {
			un = 1
		}
		if err = receipt(ctx, tx, task, before, r.ID, 1, ins, ch, un); err != nil {
			return err
		}
	}
	switch r.Mode {
	case "collect":
		s.Collection.AfterID = r.ID
		s.Collection.Rows++
		s.Collection.Date = day(r.Created)
		s.Collection.Table = table(day(r.Created))
		s.Collection.Step = "collect"
		s.Collection.Error = ""
		s.Collection.UpdatedAt = time.Now().UTC()
		s.LargeCollection = nil
	case "verify_source":
		s.History.SourceHash = chainDigest(s.History.SourceHash, r.Digest)
		s.History.SourceRows++
		s.History.AfterID = r.ID
		s.History.AfterCreated = r.Created
		s.HistoryProgress.AfterID = r.ID
		s.HistoryProgress.Rows = s.History.SourceRows
		s.LargeHistory = nil
	case "verify_archive":
		s.History.TargetHash = chainDigest(s.History.TargetHash, r.Digest)
		s.History.TargetRows++
		s.History.AfterID = r.ID
		s.History.AfterCreated = r.Created
		s.HistoryProgress.AfterID = r.ID
		s.HistoryProgress.Rows = s.History.TargetRows
		s.LargeHistory = nil
	case "summarize":
		if err = writeAggregateBatch(ctx, tx, s.History.Version, s.History.Date, map[string]aggregate{aggregateKey: a}); err != nil {
			return err
		}
		s.History.AfterID = r.ID
		s.History.AfterCreated = r.Created
		s.HistoryProgress.AfterID = r.ID
		s.LargeHistory = nil
	case "live_summary":
		if err = writeAggregateBatch(ctx, tx, r.LiveVersion, day(r.Created), map[string]aggregate{aggregateKey: a}); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE log_archive_live_stats SET after_created=?,after_id=?,error_code='',retry_at=NULL,updated_at=UTC_TIMESTAMP(6) WHERE log_date=? AND version_id=?", r.Created, r.ID, day(r.Created), r.LiveVersion); err != nil {
			return err
		}
		s.LargeLive = nil
	default:
		return errors.New("archive_large_mode_invalid")
	}
	if r.Mode != "collect" && r.Mode != "live_summary" {
		s.HistoryProgress.Error = ""
		s.HistoryProgress.UpdatedAt = time.Now().UTC()
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM log_archive_large_chunks WHERE transfer_id=?", r.Token); err != nil {
		return err
	}
	if err = save(ctx, tx, *s); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *largeRecord) isSummary() bool { return r.Mode == "summarize" || r.Mode == "live_summary" }

func saveStandalone(ctx context.Context, c *sql.Conn, s state) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = save(ctx, tx, s); err != nil {
		return err
	}
	return tx.Commit()
}
