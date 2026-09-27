package archivereader

import (
	"context"
	"controltower/server/internal/secrets"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/go-sql-driver/mysql"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestJobReaderMySQL(t *testing.T) {
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("CT_ARCHIVE_TEST_DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	raw := make([]byte, 8)
	if _, err = rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(raw)
	name, user := "ct_job_read_"+suffix, "ct_jr_"+suffix
	exec := func(db *sql.DB, q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(admin, "CREATE DATABASE `"+name+"`")
	defer admin.Exec("DROP DATABASE `" + name + "`")
	exec(admin, "CREATE USER '"+user+"'@'%' IDENTIFIED BY '"+suffix+"'")
	defer admin.Exec("DROP USER '" + user + "'@'%'")
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema, err := os.ReadFile("../../../agent/internal/archivejob/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range strings.Split(string(schema), ";") {
		if strings.TrimSpace(q) != "" {
			exec(db, q)
		}
	}
	exec(db, `CREATE TABLE logs_202607(id BIGINT PRIMARY KEY,created_at BIGINT,type INT,user_id BIGINT,channel BIGINT,model_name VARCHAR(100),prompt_tokens BIGINT,completion_tokens BIGINT NULL,quota BIGINT,content TEXT,KEY(created_at,id))`)
	hash, version := strings.Repeat("a", 64), strings.Repeat("b", 32)
	exec(db, `INSERT INTO log_archive_meta VALUES(1,1,?,'{}',NOW(6))`, hash)
	exec(db, `INSERT INTO log_archive_days(log_date,state,version_id,updated_at) VALUES('2026-07-04','sealed',?,NOW(6)),('2026-07-05','pending','',NOW(6))`, version)
	exec(db, `INSERT INTO log_archive_day_versions VALUES(?,'2026-07-04',1,3,?,1,NOW(6))`, version, hash)
	for _, v := range []string{version, strings.Repeat("c", 32)} {
		for _, g := range []string{strings.Repeat("1", 64), strings.Repeat("2", 64)} {
			exec(db, `INSERT INTO log_archive_daily_stats VALUES(?,?,'2026-07-04','{"user_id":"7","model_name":"m","channel":8}','{"quota":"9007199254740993"}')`, v, g)
		}
	}
	start := time.Date(2026, 7, 3, 16, 0, 0, 0, time.UTC).Unix()
	exec(db, `INSERT INTO logs_202607 VALUES(1,?,2,7,8,'m',1,0,5,'a'),(2,?,2,7,8,'m',1,0,6,'b'),(3,?,2,7,8,'m',1,NULL,0,'c'),(4,?,5,7,8,'m',1,0,0,?), (5,?,2,7,8,'m',1,0,0,'outside')`, start, start, start, start, strings.Repeat("x", 5000), start+86400)
	for _, table := range []string{"log_archive_meta", "log_archive_days", "log_archive_day_versions", "log_archive_daily_stats", "logs_202607"} {
		exec(admin, "GRANT SELECT ON `"+name+"`.`"+table+"` TO '"+user+"'@'%'")
	}
	cfg.User = user
	cfg.Passwd = suffix
	t.Setenv("CT_JOB_READ_TEST_DSN", cfg.FormatDSN())
	file := filepath.Join(t.TempDir(), "connections.json")
	if err = os.WriteFile(file, []byte(`{"job:site":{"dsn_env":"CT_JOB_READ_TEST_DSN","source_hash":"`+hash+`"},"job:wrong":{"dsn_env":"CT_JOB_READ_TEST_DSN","source_hash":"`+strings.Repeat("d", 64)+`"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	reader := Reader{ConnectionsFile: file}
	ctx := context.Background()
	t.Run("saved connection probe and read use pinned identity", func(t *testing.T) {
		host, portText, e := net.SplitHostPort(cfg.Addr)
		if e != nil {
			t.Fatal(e)
		}
		port, e := strconv.Atoi(portText)
		if e != nil {
			t.Fatal(e)
		}
		cipher, e := secrets.Encrypt("local-test-key", suffix)
		if e != nil {
			t.Fatal(e)
		}
		connection := Connection{Host: host, Port: port, Database: name, Username: user, EncryptedPassword: cipher, SourceHash: hash}
		saved := Reader{SecretKey: "local-test-key", Connections: connectionFake{c: connection}}
		if got, e := saved.ProbeConnection(ctx, connection); e != nil || got != hash {
			t.Fatalf("probe %s %v", got, e)
		}
		if page, e := saved.ReadJob(ctx, JobQuery{Site: "site", Kind: "anomalies", Date: "2026-07-04", Limit: 100}); e != nil || len(page.Items) != 1 {
			t.Fatalf("saved read %+v %v", page, e)
		}
		connection.SourceHash = strings.Repeat("f", 64)
		if _, e := saved.ProbeConnection(ctx, connection); !errors.Is(e, ErrIdentity) {
			t.Fatal("wrong identity accepted", e)
		}
	})
	t.Run("anomaly totals include all pages and separate null from zero", func(t *testing.T) {
		summary, e := reader.ReadJob(ctx, JobQuery{Site: "site", Kind: "anomalies", Date: "2026-07-04", Limit: 1, UserID: "7", Model: "m", ChannelID: "8"})
		if e != nil || summary.HasMore || len(summary.Items) != 1 {
			t.Fatalf("summary %+v %v", summary, e)
		}
		for k, want := range map[string]string{"hour": "0", "log_rows": "4", "consumption": "3", "empty_output": "2", "missing_output": "1", "error": "1", "charged_empty_output": "2"} {
			if summary.Items[0][k] != want {
				t.Fatalf("%s=%v want %s", k, summary.Items[0][k], want)
			}
		}
		empty, e := reader.ReadJob(ctx, JobQuery{Site: "site", Kind: "anomalies", Date: "2026-07-04", Limit: 100, UserID: "99"})
		if e != nil || len(empty.Items) != 0 {
			t.Fatalf("filtered %+v %v", empty, e)
		}
		exec(db, `INSERT INTO logs_202607 VALUES(6,?,2,7,8,'m',1,0,0,'late')`, start+86399)
		defer exec(db, `DELETE FROM logs_202607 WHERE id=6`)
		summary, e = reader.ReadJob(ctx, JobQuery{Site: "site", Kind: "anomalies", Date: "2026-07-04", Limit: 1})
		if e != nil || summary.HasMore || len(summary.Items) != 2 || summary.Items[1]["hour"] != "23" {
			t.Fatalf("hour boundary %+v %v", summary, e)
		}
	})
	q := JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 1, Category: "empty_output"}
	page, err := reader.ReadJob(ctx, q)
	if err != nil || !page.HasMore || page.Items[0]["id"] != "1" {
		t.Fatalf("first page %+v %v", page, err)
	}
	q.AfterTime = start
	q.AfterID = 1
	page, err = reader.ReadJob(ctx, q)
	if err != nil || page.HasMore || page.Items[0]["id"] != "2" {
		t.Fatalf("second page %+v %v", page, err)
	}
	q.AfterID = 0
	q.AfterTime = 0
	q.Category = "missing_output"
	page, err = reader.ReadJob(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0]["id"] != "3" {
		t.Fatalf("missing %+v %v", page, err)
	}
	q.Category = "error"
	page, err = reader.ReadJob(ctx, q)
	if err != nil || page.Items[0]["content_truncated"] != "1" || len(page.Items[0]["content_preview"].(string)) != 4096 {
		t.Fatalf("error %+v %v", page, err)
	}
	q.Category = ""
	q.Kind = "stats"
	page, err = reader.ReadJob(ctx, q)
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.Version != version {
		t.Fatalf("stats %+v %v", page, err)
	}
	q.Version = page.Version
	q.AfterHash = page.Items[0]["group_hash"].(string)
	page, err = reader.ReadJob(ctx, q)
	if err != nil || page.HasMore || len(page.Items) != 1 {
		t.Fatalf("stats continuation %+v %v", page, err)
	}
	q.Version = strings.Repeat("c", 32)
	if _, err = reader.ReadJob(ctx, q); !errors.Is(err, ErrVersion) {
		t.Fatal("stale version", err)
	}
	q.Version = ""
	q.AfterHash = ""
	q.Date = "2026-07-05"
	if _, err = reader.ReadJob(ctx, q); !errors.Is(err, ErrVersion) {
		t.Fatal("unsealed", err)
	}
	q.Kind = "days"
	q.Date = "2026-07"
	page, err = reader.ReadJob(ctx, q)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("days %+v %v", page, err)
	}
	q.Site = "wrong"
	if _, err = reader.ReadJob(ctx, q); !errors.Is(err, ErrIdentity) {
		t.Fatal("wrong site", err)
	}

	t.Run("channel_id source schema", func(t *testing.T) {
		exec(db, "ALTER TABLE logs_202607 RENAME COLUMN channel TO channel_id")
		defer exec(db, "ALTER TABLE logs_202607 RENAME COLUMN channel_id TO channel")
		detail := JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 100, ChannelID: "8", Category: "empty_output"}
		result, e := reader.ReadJob(ctx, detail)
		if e != nil || len(result.Items) != 2 || result.Items[0]["channel"] != "8" {
			t.Fatalf("channel_id detail: %+v %v", result, e)
		}
		detail.ChannelID = "999"
		result, e = reader.ReadJob(ctx, detail)
		if e != nil || len(result.Items) != 0 {
			t.Fatalf("filter: %+v %v", result, e)
		}
		detail.Kind = "anomalies"
		detail.Category = ""
		detail.ChannelID = "8"
		result, e = reader.ReadJob(ctx, detail)
		if e != nil || len(result.Items) == 0 {
			t.Fatalf("channel_id summary: %+v %v", result, e)
		}
	})
	t.Run("missing channel is unknown", func(t *testing.T) {
		exec(db, "ALTER TABLE logs_202607 RENAME COLUMN channel TO original_channel")
		defer exec(db, "ALTER TABLE logs_202607 RENAME COLUMN original_channel TO channel")
		detail := JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 100}
		result, e := reader.ReadJob(ctx, detail)
		if e != nil || len(result.Items) == 0 || result.Items[0]["channel"] != nil {
			t.Fatalf("missing: %+v %v", result, e)
		}
		detail.ChannelID = "8"
		_, e = reader.ReadJob(ctx, detail)
		if !errors.Is(e, ErrChannelColumn) {
			t.Fatal(e)
		}
	})
	t.Run("both channel names use same fallback as statistics", func(t *testing.T) {
		exec(db, "ALTER TABLE logs_202607 ADD COLUMN channel_id BIGINT NULL")
		defer exec(db, "ALTER TABLE logs_202607 DROP COLUMN channel_id")
		exec(db, "UPDATE logs_202607 SET channel_id=9 WHERE id=1")
		for _, kind := range []string{"logs", "anomalies"} {
			result, e := reader.ReadJob(ctx, JobQuery{Site: "site", Kind: kind, Date: "2026-07-04", Limit: 100, ChannelID: "9"})
			if e != nil || len(result.Items) != 1 {
				t.Fatalf("%s primary: %+v %v", kind, result, e)
			}
			result, e = reader.ReadJob(ctx, JobQuery{Site: "site", Kind: kind, Date: "2026-07-04", Limit: 100, ChannelID: "8"})
			if e != nil || len(result.Items) == 0 || (kind == "logs" && len(result.Items) != 3) {
				t.Fatalf("%s fallback: %+v %v", kind, result, e)
			}
		}
	})
	t.Run("source without content still supports detail", func(t *testing.T) {
		exec(db, "ALTER TABLE logs_202607 RENAME COLUMN content TO source_content")
		defer exec(db, "ALTER TABLE logs_202607 RENAME COLUMN source_content TO content")
		result, e := reader.ReadJob(ctx, JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 100})
		if e != nil || len(result.Items) != 4 || result.Items[0]["content_preview"] != nil {
			t.Fatalf("optional content: %+v %v", result, e)
		}
	})
	t.Run("required schema mismatch has a specific safe error", func(t *testing.T) {
		exec(db, "ALTER TABLE logs_202607 RENAME COLUMN quota TO source_quota")
		defer exec(db, "ALTER TABLE logs_202607 RENAME COLUMN source_quota TO quota")
		_, e := reader.ReadJob(ctx, JobQuery{Site: "site", Kind: "logs", Date: "2026-07-04", Limit: 100})
		if ReadErrorCode(e) != "archive_read_schema_mismatch" {
			t.Fatal(e)
		}
	})
	q.Site = "site"
	exec(admin, "GRANT INSERT ON `"+name+"`.`logs_202607` TO '"+user+"'@'%'")
	if _, err = reader.ReadJob(ctx, q); !errors.Is(err, ErrPermissions) {
		t.Fatal("writer accepted", err)
	}
}
