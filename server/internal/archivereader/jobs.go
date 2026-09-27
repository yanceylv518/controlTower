package archivereader

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

var ErrQuery = errors.New("archive_invalid_query")
var ErrIdentity = errors.New("archive_identity_or_schema_mismatch")
var ErrVersion = errors.New("archive_sealed_version_unavailable")
var ErrPageSize = errors.New("archive_read_row_too_large")
var ErrIndex = errors.New("archive_read_time_index_required")
var hash64 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var hash32 = regexp.MustCompile(`^[a-f0-9]{32}$`)
var jobMonth = regexp.MustCompile(`^logs_[0-9]{6}$`)
var numericID = regexp.MustCompile(`^[0-9]{1,20}$`)

// JobQuery never accepts a database, table name, DSN or storage reference.
// Stats continuations must carry the returned immutable version.
type JobQuery struct {
	Site      string `json:"site_id"`
	Kind      string `json:"kind"`
	Date      string `json:"date"`
	Limit     int    `json:"limit"`
	Version   string `json:"version"`
	AfterHash string `json:"after_hash"`
	AfterTime int64  `json:"after_time"`
	AfterID   int64  `json:"after_id"`
	UserID    string `json:"user_id"`
	ChannelID string `json:"channel_id"`
	Model     string `json:"model"`
	Category  string `json:"category"`
}

type JobPage struct {
	Items   []map[string]any `json:"items"`
	Version string           `json:"version,omitempty"`
	HasMore bool             `json:"has_more"`
}

func (q JobQuery) Validate() error {
	if q.Site == "" || len(q.Site) > 64 || q.Limit < 1 || q.Limit > 200 || len(q.Model) > 256 {
		return ErrQuery
	}
	layout := "2006-01-02"
	if q.Kind == "days" {
		layout = "2006-01"
	} else if q.Kind != "stats" && q.Kind != "logs" && q.Kind != "anomalies" {
		return ErrQuery
	}
	d, err := time.ParseInLocation(layout, q.Date, time.FixedZone("Beijing", 28800))
	if err != nil || d.Format(layout) != q.Date {
		return ErrQuery
	}
	if (q.UserID != "" && !numericID.MatchString(q.UserID)) || (q.ChannelID != "" && !numericID.MatchString(q.ChannelID)) {
		return ErrQuery
	}
	if q.Version != "" && !hash32.MatchString(q.Version) {
		return ErrQuery
	}
	if q.AfterHash != "" && (!hash64.MatchString(q.AfterHash) || q.Version == "") {
		return ErrQuery
	}
	if q.AfterID < 0 || q.AfterTime < 0 || (q.AfterTime == 0 && q.AfterID != 0) {
		return ErrQuery
	}
	if q.AfterTime != 0 && (q.AfterTime < d.Unix() || q.AfterTime >= d.AddDate(0, 0, 1).Unix()) {
		return ErrQuery
	}
	if q.Category != "" && q.Category != "empty_output" && q.Category != "error" && q.Category != "missing_output" {
		return ErrQuery
	}
	if q.Kind != "logs" && (q.Category != "" || q.AfterTime != 0 || q.AfterID != 0) {
		return ErrQuery
	}
	if q.Kind != "stats" && (q.Version != "" || q.AfterHash != "") {
		return ErrQuery
	}
	if q.Kind == "days" && (q.UserID != "" || q.Model != "" || q.ChannelID != "") {
		return ErrQuery
	}
	return nil
}

func permittedJobGrant(grant, database string) bool {
	if strings.HasPrefix(grant, "GRANT USAGE ON *.* TO ") && !strings.Contains(grant, "WITH GRANT OPTION") {
		return true
	}
	m := selectGrant.FindStringSubmatch(grant)
	if len(m) != 3 || m[1] != database || strings.Contains(grant, "WITH GRANT OPTION") {
		return false
	}
	switch m[2] {
	case "log_archive_meta", "log_archive_days", "log_archive_day_versions", "log_archive_daily_stats":
		return true
	}
	return jobMonth.MatchString(m[2])
}

// ReadJob uses a deployment-owned job:<site_id> binding, never the retired
// dataset registration. Every read verifies the pinned source identity.
func (r Reader) ReadJob(ctx context.Context, q JobQuery) (JobPage, error) {
	page := JobPage{Items: []map[string]any{}}
	if err := q.Validate(); err != nil {
		return page, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, expected, err := r.openJob(ctx, q.Site)
	if err != nil {
		return page, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, ErrUnavailable
	}
	defer tx.Rollback()
	var source string
	var schema int
	if err = tx.QueryRowContext(ctx, "SELECT source_hash,schema_version FROM log_archive_meta WHERE singleton_id=1").Scan(&source, &schema); err != nil {
		return page, ErrUnavailable
	}
	if source != expected || schema != 1 {
		return page, ErrIdentity
	}
	if q.Kind == "logs" || q.Kind == "anomalies" {
		var count int
		month := "logs_" + strings.ReplaceAll(q.Date[:7], "-", "")
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND seq_in_index=1 AND column_name='created_at'`, month).Scan(&count) != nil {
			return page, ErrUnavailable
		}
		if count == 0 {
			return page, ErrIndex
		}
	}
	if q.Kind == "stats" {
		err = tx.QueryRowContext(ctx, `SELECT d.version_id FROM log_archive_days d JOIN log_archive_day_versions v ON v.version_id=d.version_id AND v.log_date=d.log_date AND v.revision=d.revision WHERE d.log_date=? AND d.state='sealed'`, q.Date).Scan(&page.Version)
		if errors.Is(err, sql.ErrNoRows) {
			return page, ErrVersion
		}
		if err != nil {
			return page, ErrUnavailable
		}
		if q.Version != "" && q.Version != page.Version {
			return page, ErrVersion
		}
	}
	query, args := jobSQL(q, page.Version)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return page, ErrUnavailable
	}
	columns, err := rows.Columns()
	if err != nil {
		rows.Close()
		return page, ErrUnavailable
	}
	pageBytes := 0
	for rows.Next() {
		if len(page.Items) == q.Limit && q.Kind != "days" && q.Kind != "anomalies" {
			page.HasMore = true
			break
		}
		values := make([]sql.RawBytes, len(columns))
		dest := make([]any, len(columns))
		for i := range dest {
			dest[i] = &values[i]
		}
		if rows.Scan(dest...) != nil {
			rows.Close()
			return page, ErrUnavailable
		}
		item := map[string]any{}
		rowBytes := 0
		for _, value := range values {
			rowBytes += len(value)
		}
		if rowBytes > 1<<20 {
			rows.Close()
			return JobPage{}, ErrPageSize
		}
		if pageBytes+rowBytes > 2<<20 {
			page.HasMore = true
			break
		}
		pageBytes += rowBytes
		for i, k := range columns {
			if values[i] == nil {
				item[k] = nil
			} else {
				item[k] = string(values[i])
			}
		}
		page.Items = append(page.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, ErrUnavailable
	}
	if tx.Commit() != nil {
		return page, ErrUnavailable
	}
	return page, nil
}

func jobSQL(q JobQuery, version string) (string, []any) {
	if q.Kind == "anomalies" {
		return anomalySQL(q)
	}
	if q.Kind == "days" {
		d, _ := time.Parse("2006-01", q.Date)
		return `SELECT /*+ MAX_EXECUTION_TIME(3000) */ DATE_FORMAT(log_date,'%Y-%m-%d') AS date,state,version_id,revision,raw_rows,step,error_code FROM log_archive_days WHERE log_date>=? AND log_date<? ORDER BY log_date LIMIT 31`, []any{d.Format("2006-01-02"), d.AddDate(0, 1, 0).Format("2006-01-02")}
	}
	var query string
	var args []any
	if q.Kind == "stats" {
		query = `SELECT /*+ MAX_EXECUTION_TIME(3000) */ group_hash,LEFT(dimensions,1048577) AS dimensions,LEFT(amounts,1048577) AS amounts FROM log_archive_daily_stats WHERE log_date=? AND version_id=? AND group_hash>?`
		args = []any{q.Date, version, q.AfterHash}
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
		query += " ORDER BY group_hash LIMIT ?"
	} else {
		d, _ := time.ParseInLocation("2006-01-02", q.Date, time.FixedZone("Beijing", 28800))
		// Explicit projection: no API keys, IPs or arbitrary source columns.
		// Content is a bounded error preview; truncation is visible to callers.
		query = `SELECT /*+ MAX_EXECUTION_TIME(3000) */ id,created_at,type,user_id,channel,model_name,prompt_tokens,completion_tokens,quota,LEFT(content,4096) AS content_preview,(CHAR_LENGTH(content)>4096) AS content_truncated FROM ` + "`logs_" + d.Format("200601") + "`" + ` WHERE created_at>=? AND created_at<? AND (created_at>? OR (created_at=? AND id>?))`
		args = []any{d.Unix(), d.AddDate(0, 0, 1).Unix(), q.AfterTime, q.AfterTime, q.AfterID}
		switch q.Category {
		case "empty_output":
			query += " AND type=2 AND completion_tokens=0"
		case "missing_output":
			query += " AND type=2 AND completion_tokens IS NULL"
		case "error":
			query += " AND type=5"
		}
		if q.UserID != "" {
			query += " AND user_id=?"
			args = append(args, q.UserID)
		}
		if q.Model != "" {
			query += " AND model_name=?"
			args = append(args, q.Model)
		}
		if q.ChannelID != "" {
			query += " AND channel=?"
			args = append(args, q.ChannelID)
		}
		query += " ORDER BY created_at,id LIMIT ?"
	}
	return query, append(args, q.Limit+1)
}

func (r Reader) openJobFile(ctx context.Context, site string) (*sql.DB, string, error) {
	f, err := os.Open(r.ConnectionsFile)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	defer f.Close()
	var entries map[string]struct {
		DSNEnv     string `json:"dsn_env"`
		SourceHash string `json:"source_hash"`
	}
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if dec.Decode(&entries) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, "", ErrUnavailable
	}
	ref := "job:" + site
	expected := entries[ref].SourceHash
	if !hash64.MatchString(expected) {
		return nil, "", ErrIdentity
	}
	db, err := r.openChecked(ctx, ref, permittedJobGrant)
	if err != nil {
		return nil, "", err
	}
	return db, expected, nil
}
