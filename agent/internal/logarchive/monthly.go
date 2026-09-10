package logarchive

import (
	"context"
	ac "controltower/internal/archivecontrol"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The layout has a fixed timezone so restarting on a different host cannot
// move records across tables. Dates are based on source created_at, not ingest time.
var archiveLocation = time.FixedZone("UTC+08:00", 8*3600)
var measureNames = []string{"log_rows", "request_rows", "consume_rows", "error_rows", "prompt_tokens", "completion_tokens", "quota"}

type contribution struct {
	Day    string   `json:"day"`
	Values []string `json:"values"`
}

func (c contribution) month() string {
	if c.Day == "undated" {
		return "undated"
	}
	return strings.ReplaceAll(c.Day[:7], "-", "")
}

func rowContribution(columns []string, row []any) (int64, contribution, error) {
	m := make(map[string]any, len(columns))
	for i, col := range columns {
		m[col] = row[i]
	}
	read := func(key string) (int64, error) {
		v, exists := m[key]
		if !exists {
			return 0, errors.New("archive requires id, created_at, type, prompt_tokens, completion_tokens and quota columns")
		}
		if v == nil {
			return 0, nil
		}
		s, ok := v.(string)
		if !ok {
			return 0, errors.New("invalid archive numeric field")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, errors.New("invalid archive numeric field")
		}
		return n, nil
	}
	id, err := read("id")
	if err != nil || id <= 0 {
		return 0, contribution{}, errors.New("invalid archive id")
	}
	ts, err := read("created_at")
	if err != nil {
		return 0, contribution{}, err
	}
	c := contribution{Day: "undated", Values: []string{"1", "0", "0", "0", "0", "0", "0"}}
	if ts > 0 {
		d := time.Unix(ts, 0).In(archiveLocation)
		if d.Year() < 1970 || d.Year() > 9999 {
			return 0, c, errors.New("archive timestamp out of range")
		}
		c.Day = d.Format("2006-01-02")
	}
	typ, err := read("type")
	if err != nil {
		return 0, c, err
	}
	if typ == 2 || typ == 5 {
		c.Values[1] = "1"
	}
	if typ == 2 {
		c.Values[2] = "1"
	}
	if typ == 5 {
		c.Values[3] = "1"
	}
	for i, key := range []string{"prompt_tokens", "completion_tokens", "quota"} {
		n, err := read(key)
		if err != nil {
			return 0, c, err
		}
		// Non-request records (e.g. recharges) must not inflate usage statistics.
		if typ == 2 || typ == 5 {
			c.Values[i+4] = strconv.FormatInt(n, 10)
		}
	}
	return id, c, nil
}

func validContribution(c contribution) bool {
	if c.Day != "undated" {
		if _, err := time.Parse("2006-01-02", c.Day); err != nil {
			return false
		}
	}
	if len(c.Values) != len(measureNames) {
		return false
	}
	for _, s := range c.Values {
		if _, ok := new(big.Int).SetString(s, 10); !ok {
			return false
		}
	}
	return true
}

// Deltas are grouped in memory; normal batches write at most one statistics row
// per affected day/month. No aggregate scan of source or archived details is needed.
type deltas map[string][]*big.Int

func (d deltas) add(key string, c contribution, sign int64) {
	if d[key] == nil {
		for range measureNames {
			d[key] = append(d[key], new(big.Int))
		}
	}
	for i, s := range c.Values {
		v, _ := new(big.Int).SetString(s, 10)
		v.Mul(v, big.NewInt(sign))
		d[key][i].Add(d[key][i], v)
	}
}

func statsDDL(table string) string {
	fields := []string{"period_key VARCHAR(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY"}
	for _, name := range measureNames {
		fields = append(fields, quote(name)+" DECIMAL(38,0) NOT NULL DEFAULT 0")
	}
	return "CREATE TABLE IF NOT EXISTS " + quote(table) + " (" + strings.Join(fields, ",") + ") ENGINE=InnoDB"
}

func ensureMonthlyTables(ctx context.Context, db *sql.DB, months []string) error {
	ddl := []string{
		"CREATE TABLE IF NOT EXISTS archive_log_state (id BIGINT NOT NULL PRIMARY KEY, contribution JSON NOT NULL) ENGINE=InnoDB",
		statsDDL("log_daily_stats"), statsDDL("log_monthly_stats"),
	}
	for _, month := range months {
		ddl = append(ddl, "CREATE TABLE IF NOT EXISTS "+quote("logs_"+month)+" LIKE logs")
	}
	for _, q := range ddl {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return errors.New("archive monthly table provisioning failed; check target CREATE privilege and template")
		}
	}
	// Existing tables may predate this worker: CREATE IF NOT EXISTS alone
	// cannot establish transactional safety or schema compatibility.
	template, err := schema(ctx, db)
	if err != nil {
		return err
	}
	names := []string{"archive_log_state", "log_daily_stats", "log_monthly_stats"}
	for _, month := range months {
		name := "logs_" + month
		names = append(names, name)
		actual, err := schema(ctx, db, name)
		if err != nil || actual != template {
			return errors.New("archive monthly table schema differs from template")
		}
		rows, err := db.QueryContext(ctx, "SELECT INDEX_NAME, COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND NON_UNIQUE=0", name)
		if err != nil {
			return errors.New("archive monthly index check failed")
		}
		n := 0
		for rows.Next() {
			var index, col string
			if rows.Scan(&index, &col) != nil || index != "PRIMARY" || col != "id" {
				rows.Close()
				return errors.New("archive monthly table requires only PRIMARY KEY(id) as a unique constraint")
			}
			n++
		}
		err = rows.Err()
		rows.Close()
		if err != nil || n != 1 {
			return errors.New("archive monthly index check failed")
		}
	}
	for _, name := range names {
		var engine string
		if db.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", name).Scan(&engine) != nil || !strings.EqualFold(engine, "InnoDB") {
			return errors.New("archive monthly and statistics tables must use InnoDB")
		}
	}
	return nil
}

func writeMonthlyBatch(ctx context.Context, db *sql.DB, columns []string, batch [][]any, progress ...*[]ac.Day) error {
	current := make(map[int64]contribution, len(batch))
	monthSet := map[string]bool{}
	ids := make([]any, 0, len(batch))
	for _, row := range batch {
		id, c, err := rowContribution(columns, row)
		if err != nil {
			return err
		}
		if _, exists := current[id]; exists {
			return errors.New("duplicate id in archive batch")
		}
		current[id] = c
		ids = append(ids, id)
		monthSet[c.month()] = true
	}
	months := []string{}
	for m := range monthSet {
		months = append(months, m)
	}
	sort.Strings(months)
	// MySQL DDL implicitly commits: all provisioning must happen before BeginTx.
	if err := ensureMonthlyTables(ctx, db, months); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("archive target transaction failed")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET SESSION sql_mode='STRICT_ALL_TABLES,NO_ENGINE_SUBSTITUTION'"); err != nil {
		return errors.New("archive target strict mode failed")
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := tx.QueryContext(ctx, "SELECT id, contribution FROM archive_log_state WHERE id IN ("+marks+") ORDER BY id FOR UPDATE", ids...)
	if err != nil {
		return errors.New("archive contribution lookup failed")
	}
	old := map[int64]contribution{}
	for rows.Next() {
		var id int64
		var raw []byte
		var c contribution
		if rows.Scan(&id, &raw) != nil || json.Unmarshal(raw, &c) != nil || !validContribution(c) {
			rows.Close()
			return errors.New("invalid archive contribution state")
		}
		old[id] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return errors.New("archive contribution read failed")
	}
	days, monthly := deltas{}, deltas{}
	grouped := map[string][][]any{}
	ledger := [][]any{}
	for i, row := range batch {
		id := ids[i].(int64)
		next := current[id]
		if prev, ok := old[id]; ok {
			days.add(prev.Day, prev, -1)
			monthly.add(prev.month(), prev, -1)
			if prev.month() != next.month() {
				// created_at corrections must not leave a duplicate in the old month.
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+quote("logs_"+prev.month())+" WHERE id=?", id); err != nil {
					return errors.New("archive previous month relocation failed")
				}
			}
		}
		days.add(next.Day, next, 1)
		monthly.add(next.month(), next, 1)
		grouped[next.month()] = append(grouped[next.month()], row)
		raw, _ := json.Marshal(next)
		ledger = append(ledger, []any{id, string(raw)})
	}
	for _, month := range months {
		if err := insertRows(ctx, tx, "logs_"+month, columns, grouped[month]); err != nil {
			return err
		}
	}
	if err := insertRows(ctx, tx, "archive_log_state", []string{"id", "contribution"}, ledger); err != nil {
		return err
	}
	if err := writeDeltas(ctx, tx, "log_daily_stats", days); err != nil {
		return err
	}
	if err := writeDeltas(ctx, tx, "log_monthly_stats", monthly); err != nil {
		return err
	}
	for _, month := range months {
		if err := verifyRows(ctx, tx, "logs_"+month, columns, grouped[month]); err != nil {
			return err
		}
	}
	var snapshots []ac.Day
	if len(progress) > 0 {
		keys := []string{}
		for d := range days {
			keys = append(keys, d)
		}
		sort.Strings(keys)
		args := []any{}
		for _, d := range keys {
			args = append(args, d)
		}
		rows, err := tx.QueryContext(ctx, "SELECT period_key,log_rows,request_rows,error_rows FROM log_daily_stats WHERE period_key IN ("+strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")+")", args...)
		if err != nil {
			return errors.New("archive daily progress lookup failed")
		}
		last := map[string]int64{}
		for id, c := range current {
			if id > last[c.Day] {
				last[c.Day] = id
			}
		}
		for rows.Next() {
			var d ac.Day
			if rows.Scan(&d.Date, &d.ArchivedRows, &d.RequestRows, &d.ErrorRows) != nil {
				rows.Close()
				return errors.New("archive daily progress scan failed")
			}
			d.LastID = last[d.Date]
			d.VerifiedAt = time.Now().UTC()
			if !d.Validate() {
				rows.Close()
				return errors.New("invalid archive daily progress")
			}
			snapshots = append(snapshots, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(snapshots) != len(keys) {
			return errors.New("archive daily progress incomplete")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("archive target commit failed; checkpoint unchanged")
	}
	if len(progress) > 0 {
		*progress[0] = snapshots
	}
	return nil
}

func insertRows(ctx context.Context, tx *sql.Tx, table string, columns []string, rows [][]any) error {
	// Keep each network write below roughly 1 MiB / 30,000 parameters; a
	// single large original row is sent alone and still subject to MySQL limits.
	for len(rows) > 0 {
		n, bytes := 0, 0
		for n < len(rows) {
			size := 0
			for _, v := range rows[n] {
				if s, ok := v.(string); ok {
					size += len(s)
				}
			}
			if n > 0 && (bytes+size > 1024*1024 || (n+1)*len(columns) > 30000) {
				break
			}
			bytes += size
			n++
		}
		q := insertSQL(columns)
		q = strings.Replace(q, "INSERT INTO logs", "INSERT INTO "+quote(table), 1)
		one := "(" + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + ")"
		q = strings.Replace(q, "VALUES "+one, "VALUES "+strings.TrimSuffix(strings.Repeat(one+",", n), ","), 1)
		args := []any{}
		for _, row := range rows[:n] {
			args = append(args, row...)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return errors.New("archive target write failed; checkpoint unchanged")
		}
		rows = rows[n:]
	}
	return nil
}

func writeDeltas(ctx context.Context, tx *sql.Tx, table string, values deltas) error {
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args := []any{key}
		nonzero := false
		names, updates := []string{"period_key"}, []string{}
		for i, name := range measureNames {
			v := values[key][i]
			nonzero = nonzero || v.Sign() != 0
			args = append(args, v.String())
			names = append(names, quote(name))
			updates = append(updates, quote(name)+"="+quote(name)+"+VALUES("+quote(name)+")")
		}
		if !nonzero {
			continue
		}
		q := "INSERT INTO " + quote(table) + " (" + strings.Join(names, ",") + ") VALUES (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ") ON DUPLICATE KEY UPDATE " + strings.Join(updates, ",")
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return errors.New("archive statistics update failed; checkpoint unchanged")
		}
	}
	return nil
}
