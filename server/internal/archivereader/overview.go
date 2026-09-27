package archivereader

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Only materialized groups are read here. Opening a dashboard never scans raw
// monthly logs, and a sealed and live version can never both enter the total.
func readOverview(ctx context.Context, tx *sql.Tx, q JobQuery) (JobPage, error) {
	page := JobPage{Items: []map[string]any{}}
	start, _ := time.Parse("2006-01", q.Date)
	end := start.AddDate(0, 1, 0)
	if q.From != "" {
		start, _ = time.Parse("2006-01-02", q.From)
		end, _ = time.Parse("2006-01-02", q.Through)
		end = end.AddDate(0, 0, 1)
	}
	rows, err := tx.QueryContext(ctx, `SELECT DATE_FORMAT(d.log_date,'%Y-%m-%d'),d.state,COALESCE(CASE WHEN v.version_id IS NOT NULL AND (v.parser_version>=2 OR COALESCE(l.ready,0)=0) THEN v.version_id ELSE l.version_id END,''),IF(v.version_id IS NOT NULL,1,COALESCE(l.ready,0)),IF(v.version_id IS NOT NULL,'',COALESCE(l.error_code,'')),DATE_FORMAT(COALESCE(v.sealed_at,l.updated_at),'%Y-%m-%dT%H:%i:%sZ') FROM log_archive_days d LEFT JOIN log_archive_day_versions v ON v.version_id=d.version_id AND v.log_date=d.log_date AND v.revision=d.revision AND d.state='sealed' LEFT JOIN log_archive_live_stats l ON l.log_date=d.log_date WHERE d.log_date>=? AND d.log_date<? ORDER BY d.log_date`, start.Format("2006-01-02"), end.Format("2006-01-02"))
	if err != nil {
		var dbErr *mysql.MySQLError
		if errors.As(err, &dbErr) && dbErr.Number == 1146 {
			return page, connectionError{code: "archive_statistics_schema_required"}
		}
		return page, readFailure(err)
	}
	days := []map[string]any{}
	versions := map[string]string{}
	for rows.Next() {
		var date, state, version, code string
		var ready bool
		var updated sql.NullString
		if err = rows.Scan(&date, &state, &version, &ready, &code, &updated); err != nil {
			rows.Close()
			return page, readFailure(err)
		}
		days = append(days, map[string]any{"date": date, "state": state, "ready": ready, "version": version, "error": code, "updated_at": updated.String})
		versions[date] = version
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, readFailure(err)
	}
	// Use the exact version manifest above in the same repeatable-read snapshot.
	result := map[string]map[string]any{}
	scanned := 0
	options := map[string]map[string]bool{"user_id": {}, "model_name": {}, "channel_id": {}}
	clauses := []string{}
	args := []any{}
	for _, day := range days {
		date := day["date"].(string)
		version := versions[date]
		if version != "" {
			clauses = append(clauses, "(log_date=? AND version_id=?)")
			args = append(args, date, version)
		}
	}
	if len(clauses) > 0 {
		query := `SELECT /*+ MAX_EXECUTION_TIME(3000) */ DATE_FORMAT(log_date,'%Y-%m-%d'),group_hash,LEFT(dimensions,1048577),LEFT(amounts,1048577) FROM log_archive_daily_stats WHERE (` + strings.Join(clauses, " OR ") + ")"
		if q.UserID != "" {
			query += ` AND dimensions->>'$.user_id'=?`
			args = append(args, q.UserID)
		}
		if q.Model != "" {
			query += ` AND dimensions->>'$.model_name'=?`
			args = append(args, q.Model)
		}
		if q.ChannelID != "" {
			query += ` AND COALESCE(NULLIF(dimensions->>'$.channel_id','null'),dimensions->>'$.channel')=?`
			args = append(args, q.ChannelID)
		}
		query += " LIMIT 100001"
		scannedBytes := 0
		rs, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return page, readFailure(e)
		}
		for rs.Next() {
			scanned++
			if scanned > 100000 {
				rs.Close()
				return page, connectionError{code: "archive_statistics_limit"}
			}
			var date, hash string
			var dims, amounts []byte
			if e = rs.Scan(&date, &hash, &dims, &amounts); e != nil {
				rs.Close()
				return page, readFailure(e)
			}
			scannedBytes += len(dims) + len(amounts)
			if scannedBytes > 64<<20 {
				rs.Close()
				return page, connectionError{code: "archive_statistics_limit"}
			}
			var d map[string]any
			var a map[string]string
			if len(dims) > 1<<20 || len(amounts) > 1<<20 || json.Unmarshal(dims, &d) != nil || json.Unmarshal(amounts, &a) != nil {
				rs.Close()
				return page, connectionError{code: "archive_statistics_limit"}
			}
			if a["log_rows"] == "0" {
				continue
			}
			for key, values := range options {
				value := d[key]
				if value == nil && key == "channel_id" {
					value = d["channel"]
				}
				if value != nil && len(values) < 200 {
					values[fmt.Sprint(value)] = true
				}
			}
			name := d[q.Dimension]
			if name == nil && q.Dimension == "channel_id" {
				name = d["channel"]
			}
			kind := fmt.Sprint(d["type"])
			keyRaw, _ := json.Marshal([]any{date, name, kind})
			key := string(keyRaw)
			group, ok := result[key]
			if !ok {
				if len(result) >= 10000 {
					rs.Close()
					return page, connectionError{code: "archive_statistics_limit"}
				}
				group = map[string]any{"date": date, "dimensions": map[string]any{q.Dimension: name, "type": kind}, "amounts": map[string]string{}}
				result[key] = group
			}
			target := group["amounts"].(map[string]string)
			// Explicit missing counters survive grouping; old summaries without an
			// anomaly parser remain distinguishable from a measured zero.
			for _, metric := range []string{"requests", "quota", "prompt_tokens", "completion_tokens", "cache_tokens"} {
				if _, ok := a[metric]; !ok && a[metric+"_missing"] == "" {
					a[metric+"_missing"] = a["log_rows"]
				}
			}
			for k, v := range a {
				n, ok := new(big.Int).SetString(v, 10)
				if !ok || n.Sign() < 0 {
					rs.Close()
					return page, ErrUnavailable
				}
				old := new(big.Int)
				if target[k] != "" {
					old.SetString(target[k], 10)
				}
				target[k] = old.Add(old, n).String()
			}
		}
		e = rs.Err()
		rs.Close()
		if e != nil {
			return page, readFailure(e)
		}
	}
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stats := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		stats = append(stats, result[key])
	}
	item := map[string]any{"days": days, "rows": stats, "options": options, "observed_at": time.Now().UTC().Format(time.RFC3339)}
	encoded, err := json.Marshal(item)
	if err != nil || len(encoded) > 2<<20 {
		return page, connectionError{code: "archive_statistics_limit"}
	}
	if err = tx.Commit(); err != nil {
		return page, readFailure(err)
	}
	page.Items = append(page.Items, item)
	return page, nil
}
