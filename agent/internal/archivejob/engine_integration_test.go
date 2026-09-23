package archivejob

import (
	"context"
	aj "controltower/internal/archivejob"
	"database/sql"
	"encoding/json"
	"github.com/go-sql-driver/mysql"
	"os"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ARCHIVE_TEST_DSN for isolated new archive database tests")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	source := "ct_jobs_src_" + id()
	target := "ct_jobs_dst_" + id()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	for _, name := range []string{source, target} {
		if _, err = admin.ExecContext(ctx, "CREATE DATABASE "+q(name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		cancel()
		for _, name := range []string{source, target} {
			_, _ = admin.Exec("DROP DATABASE " + q(name))
		}
		admin.Close()
	})
	cfg.DBName = source
	src := cfg.FormatDSN()
	cfg.DBName = target
	e, err := Open(src, cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	_, err = e.source.ExecContext(ctx, "CREATE TABLE logs(id BIGINT PRIMARY KEY,created_at BIGINT NOT NULL,type INT,user_id BIGINT,channel BIGINT,model_name VARCHAR(64),prompt_tokens BIGINT,completion_tokens BIGINT,quota BIGINT,other TEXT,INDEX(created_at)) ENGINE=InnoDB")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.target.ExecContext(ctx, "CREATE TABLE logs_202601 LIKE "+q(source)+".logs")
	if err != nil {
		t.Fatal(err)
	}
	return e, ctx
}
func TestNewEngineInitializationRepairSealAndResumeMySQL(t *testing.T) {
	e, ctx := fixture(t)
	start, _ := dateBounds("2026-01-01")
	for _, r := range []struct {
		id, at   int64
		discount string
	}{{1, start + 1, "0.56"}, {100, start + 100, "0.8"}, {101, start + 86401, "0.56"}} {
		_, err := e.source.ExecContext(ctx, "INSERT INTO logs VALUES(?,?,2,7,199,'m',10,5,53,?)", r.id, r.at, `{"model_ratio":10,"user_model_discount":`+r.discount+`,"quota_before_discount":95,"quota_after_discount":53}`)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Only ID 100 was previously archived. The new collector must not start at 0.
	rows, err := readRows(ctx, e.source, "SELECT * FROM logs WHERE id=100")
	if err != nil {
		t.Fatal(err)
	}
	cols := "id,created_at,type,user_id,channel,model_name,prompt_tokens,completion_tokens,quota,other"
	r := rows[0]
	_, err = e.target.ExecContext(ctx, "INSERT INTO logs_202601 ("+cols+") VALUES(100,?,2,7,199,'m',10,5,53,?)", start+100, r.text("other"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.target.ExecContext(ctx, "CREATE TABLE archive_pipeline(sentinel INT)")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.target.ExecContext(ctx, "INSERT INTO archive_pipeline VALUES(77)")
	st, err := e.Step(ctx, aj.Settings{Collection: true}, 10, 60, true)
	if err != nil {
		t.Fatal(err)
	}
	if st.Collection.AfterID != 101 || st.Collection.Rows != 1 {
		t.Fatalf("collector did not initialize from max: %+v", st.Collection)
	}
	for i := 0; i < 12; i++ {
		st, err = e.Step(ctx, aj.Settings{History: true}, 10, 60, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = e.target.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_archive_daily_stats s JOIN log_archive_days d ON d.version_id=s.version_id WHERE d.log_date='2026-01-01' AND d.state='sealed'").Scan(&count); err != nil || count != 2 {
		t.Fatal("two discount groups not sealed", count, err)
	}
	var cursorJSON []byte
	if err = e.target.QueryRowContext(ctx, "SELECT state_json FROM log_archive_meta").Scan(&cursorJSON); err != nil {
		t.Fatal(err)
	}
	var saved state
	_ = json.Unmarshal(cursorJSON, &saved)
	if saved.Collection.AfterID != 101 || saved.Collection.Rows != 1 {
		t.Fatal("historical repair moved collector", saved.Collection)
	}
	e.ready = false
	st, err = e.Step(ctx, aj.Settings{Collection: true}, 10, 60, true)
	if err != nil || st.Collection.AfterID != 101 || st.Collection.Rows != 1 {
		t.Fatal("restart reset cursor", st, err)
	}
	if err = e.target.QueryRowContext(ctx, "SELECT sentinel FROM archive_pipeline").Scan(&count); err != nil || count != 77 {
		t.Fatal("legacy auxiliary table touched", err)
	}
}
