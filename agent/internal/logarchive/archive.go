// Package logarchive copies raw NewAPI logs to a dedicated MySQL database.
// It never issues a write against the source database.
package logarchive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"controltower/agent/internal/fileatomic"
	ac "controltower/internal/archivecontrol"
	"github.com/go-sql-driver/mysql"
)

type Worker struct {
	source, target *sql.DB
	path           string
	batchSize      int
	delay          time.Duration
}

func (w *Worker) WithDelay(delay time.Duration) *Worker { w.delay = delay; return w }

type checkpoint struct {
	AfterID          int64     `json:"after_id"`
	CompletedAt      time.Time `json:"completed_at"`
	LastVerifiedAt   time.Time `json:"last_verified_at,omitempty"`
	LastVerifiedRows int       `json:"last_verified_rows,omitempty"`
	Days             []ac.Day  `json:"days,omitempty"`
}

// Progress is a read-only snapshot; callers serialize it with Pass.
func (w *Worker) Progress() (int64, time.Time, int, error) {
	c, err := w.load()
	return c.AfterID, c.LastVerifiedAt, c.LastVerifiedRows, err
}
func (w *Worker) SetBatchSize(n int)      { w.batchSize = n }
func (w *Worker) Days() ([]ac.Day, error) { c, err := w.load(); return c.Days, err }

// Open keeps separate connection pools and binds progress to both databases.
func Open(sourceDSN, targetDSN, instance, dataDir string, batchSize int) (*Worker, error) {
	src, err := mysql.ParseDSN(sourceDSN)
	if err != nil {
		return nil, errors.New("invalid source archive DSN")
	}
	dst, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		return nil, errors.New("invalid target archive DSN")
	}
	if src.DBName == "" || dst.DBName == "" || (src.Net == dst.Net && src.Addr == dst.Addr && src.DBName == dst.DBName) {
		return nil, errors.New("archive requires a dedicated target database distinct from source")
	}
	if batchSize < 1 || batchSize > 5000 {
		return nil, errors.New("invalid archive batch size")
	}
	// Raw bytes preserve NULLs, decimal precision and timestamp text.
	src.ParseTime, dst.ParseTime = false, false
	src.MultiStatements, dst.MultiStatements = false, false
	for _, c := range []*mysql.Config{src, dst} {
		if c.Params == nil {
			c.Params = make(map[string]string)
		}
		c.Params["time_zone"] = "'+00:00'"
		c.Timeout = 10 * time.Second
		c.ReadTimeout = 30 * time.Second
		c.WriteTimeout = 30 * time.Second
	}
	source, err := sql.Open("mysql", src.FormatDSN())
	if err != nil {
		return nil, errors.New("cannot open archive source")
	}
	target, err := sql.Open("mysql", dst.FormatDSN())
	if err != nil {
		source.Close()
		return nil, errors.New("cannot open archive target")
	}
	for _, db := range []*sql.DB{source, target} {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(10 * time.Minute)
	}
	identity, _ := json.Marshal([]string{instance, src.Net, src.Addr, src.DBName, dst.Net, dst.Addr, dst.DBName})
	hash := sha256.Sum256(identity)
	return &Worker{source: source, target: target, path: filepath.Join(dataDir, fmt.Sprintf("log-archive-monthly-v1-%x.json", hash[:12])), batchSize: batchSize}, nil
}

func (w *Worker) Close() { w.source.Close(); w.target.Close() }

// Check is read-only, including on the target. Provision logs before enabling.
func (w *Worker) Check(ctx context.Context) error {
	// Detect aliases of the same MySQL database as well as identical DSNs.
	var sourceUUID, targetUUID, sourceDB, targetDB string
	if err := w.source.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&sourceUUID, &sourceDB); err != nil {
		return errors.New("archive source identity check failed")
	}
	if err := w.target.QueryRowContext(ctx, "SELECT @@server_uuid, DATABASE()").Scan(&targetUUID, &targetDB); err != nil {
		return errors.New("archive target identity check failed")
	}
	if sourceUUID == targetUUID && sourceDB == targetDB {
		return errors.New("archive target resolves to source database")
	}
	var engine string
	if err := w.target.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs'").Scan(&engine); err != nil || !strings.EqualFold(engine, "InnoDB") {
		return errors.New("archive target requires a provisioned InnoDB logs table")
	}
	// Additional unique constraints could merge unrelated source records during replay.
	rows, err := w.target.QueryContext(ctx, "SELECT INDEX_NAME, COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' AND NON_UNIQUE=0")
	if err != nil {
		return errors.New("archive target index check failed")
	}
	count := 0
	for rows.Next() {
		var index, column string
		if err := rows.Scan(&index, &column); err != nil {
			rows.Close()
			return errors.New("archive target index check failed")
		}
		if index != "PRIMARY" || column != "id" {
			rows.Close()
			return errors.New("archive target must have only PRIMARY KEY(id) as a unique constraint")
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || count != 1 {
		return errors.New("archive target requires PRIMARY KEY(id)")
	}
	srcSchema, err := schema(ctx, w.source)
	if err != nil {
		return err
	}
	dstSchema, err := schema(ctx, w.target)
	if err != nil {
		return err
	}
	if srcSchema != dstSchema {
		return errors.New("archive logs column definitions differ; align target schema before retrying")
	}
	return nil
}

func schema(ctx context.Context, db *sql.DB, table ...string) (string, error) {
	name := "logs"
	if len(table) > 0 {
		name = table[0]
	}
	rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COALESCE(CHARACTER_SET_NAME,''), COALESCE(COLLATION_NAME,''), EXTRA FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? ORDER BY ORDINAL_POSITION", name)
	if err != nil {
		return "", errors.New("archive schema query failed")
	}
	defer rows.Close()
	var columns [][]string
	for rows.Next() {
		c := make([]string, 6)
		if rows.Scan(&c[0], &c[1], &c[2], &c[3], &c[4], &c[5]) != nil {
			return "", errors.New("archive schema read failed")
		}
		if strings.Contains(strings.ToUpper(c[5]), "GENERATED") {
			return "", errors.New("archive does not support generated logs columns")
		}
		columns = append(columns, c)
	}
	if rows.Err() != nil || len(columns) == 0 {
		return "", errors.New("archive logs schema unavailable")
	}
	b, _ := json.Marshal(columns)
	return string(b), nil
}

func (w *Worker) load() (checkpoint, error) {
	var cp checkpoint
	b, err := fileatomic.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return cp, nil
	}
	if err != nil {
		return cp, errors.New("cannot read archive checkpoint")
	}
	if json.Unmarshal(b, &cp) != nil || cp.AfterID < 0 {
		return checkpoint{}, errors.New("invalid archive checkpoint")
	}
	return cp, nil
}

// Pass copies one bounded prefix older than the delay window. It never skips
// a visible recent row to advance to an older timestamp with a larger ID.
func (w *Worker) Pass(ctx context.Context) (int, error) {
	cp, err := w.load()
	if err != nil {
		return 0, err
	}
	if err := w.Check(ctx); err != nil {
		return 0, err
	}
	var sourceNow int64
	if err := w.source.QueryRowContext(ctx, "SELECT UNIX_TIMESTAMP()").Scan(&sourceNow); err != nil {
		return 0, errors.New("archive source clock unavailable")
	}
	delay := w.delay
	if delay <= 0 {
		delay = 5 * time.Minute
	}
	cutoff := sourceNow - int64(delay/time.Second)
	rows, err := w.source.QueryContext(ctx, "SELECT * FROM logs WHERE id > ? ORDER BY id ASC LIMIT ?", cp.AfterID, w.batchSize)
	if err != nil {
		return 0, errors.New("archive source query failed")
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return 0, errors.New("archive source columns unavailable")
	}
	idIndex := -1
	createdIndex := -1
	for i, c := range columns {
		if c == "created_at" {
			createdIndex = i
		}
		if c == "id" {
			idIndex = i
		}
	}
	if idIndex < 0 || createdIndex < 0 {
		rows.Close()
		return 0, errors.New("archive source requires id and created_at columns")
	}
	var batch [][]any
	for rows.Next() {
		raw := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range raw {
			dest[i] = &raw[i]
		}
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return 0, errors.New("archive source scan failed")
		}
		if raw[createdIndex] != nil {
			ts, err := strconv.ParseInt(string(raw[createdIndex]), 10, 64)
			if err != nil {
				rows.Close()
				return 0, errors.New("invalid archive timestamp")
			}
			if ts > cutoff {
				break
			}
		}
		id, err := strconv.ParseInt(string(raw[idIndex]), 10, 64)
		if err != nil || id <= cp.AfterID {
			rows.Close()
			return 0, errors.New("archive source id is not increasing")
		}
		values := make([]any, len(columns))
		for i, v := range raw {
			if v != nil {
				values[i] = string(v)
			}
		}
		batch = append(batch, values)
		cp.AfterID = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, errors.New("archive source read failed")
	}
	if len(batch) > 0 {
		var updated []ac.Day
		if err := writeMonthlyBatch(ctx, w.target, columns, batch, &updated); err != nil {
			return 0, err
		}
		for _, d := range updated {
			found := false
			for i := range cp.Days {
				if cp.Days[i].Date == d.Date {
					cp.Days[i] = d
					found = true
					break
				}
			}
			if !found {
				cp.Days = append(cp.Days, d)
			}
		}
	}
	if len(batch) == 0 {
		return 0, nil
	}
	cp.LastVerifiedAt = time.Now().UTC()
	cp.LastVerifiedRows = len(batch)
	if err := os.MkdirAll(filepath.Dir(w.path), 0700); err != nil {
		return 0, errors.New("cannot create archive state directory")
	}
	b, _ := json.Marshal(cp)
	if err := fileatomic.WriteFile(w.path, b, 0600); err != nil {
		return 0, errors.New("cannot save archive checkpoint; batch will replay")
	}
	return len(batch), nil
}

func quote(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func insertSQL(columns []string) string {
	names, placeholders, updates := []string{}, []string{}, []string{}
	for _, c := range columns {
		q := quote(c)
		names = append(names, q)
		placeholders = append(placeholders, "?")
		if c != "id" {
			updates = append(updates, q+"=VALUES("+q+")")
		}
	}
	if len(updates) == 0 {
		updates = append(updates, "`id`=VALUES(`id`)")
	}
	return "INSERT INTO logs (" + strings.Join(names, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ") ON DUPLICATE KEY UPDATE " + strings.Join(updates, ",")
}

func writeBatch(ctx context.Context, db *sql.DB, columns []string, batch [][]any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("archive target transaction failed")
	}
	defer tx.Rollback()
	// Refuse silent truncation when the target schema is incompatible.
	if _, err := tx.ExecContext(ctx, "SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"); err != nil {
		return errors.New("archive target strict mode failed")
	}
	stmt, err := tx.PrepareContext(ctx, insertSQL(columns))
	if err != nil {
		return errors.New("archive target schema incompatible or INSERT/UPDATE permission missing")
	}
	defer stmt.Close()
	for _, row := range batch {
		if _, err := stmt.ExecContext(ctx, row...); err != nil {
			return errors.New("archive target write failed; checkpoint unchanged")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("archive target commit failed; checkpoint unchanged")
	}
	return nil
}
