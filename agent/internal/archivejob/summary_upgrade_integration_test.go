package archivejob

import (
	"encoding/json"
	"testing"
)

func TestSummaryUpgradeAndSealedRebuildMySQL(t *testing.T) {
	e, ctx := fixture(t)
	c, err := e.target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = e.prepare(ctx, c); err != nil {
		t.Fatal(err)
	}
	start, _ := dateBounds("2026-01-01")
	if _, err = c.ExecContext(ctx, `INSERT INTO logs_202601(id,created_at,type,user_id,channel,model_name,prompt_tokens,completion_tokens,quota,other) VALUES(1,?,2,9,8,'m',100,50,12345,'{"cache_tokens":90,"image_input":30,"model_ratio":10,"image_ratio":1}')`, start+1); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readArchivePage(ctx, c, "logs_202601", "SELECT * FROM logs_202601 ORDER BY created_at,id LIMIT ?", 100)
	if err != nil {
		t.Fatal(err)
	}
	hash := chain("", rows[0])
	old := id()
	live := id()
	if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_days(log_date,state,version_id,updated_at) VALUES('2026-01-01','sealed',?,UTC_TIMESTAMP(6))`, old); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_day_versions VALUES(?,'2026-01-01',1,1,?,2,UTC_TIMESTAMP(6))`, old, hash); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_daily_stats VALUES(?,'old','2026-01-01','{}','{"quota":"12345"}')`, old); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(ctx, `INSERT INTO log_archive_live_stats(log_date,version_id,ready,updated_at) VALUES('2026-01-01',?,1,UTC_TIMESTAMP(6))`, live); err != nil {
		t.Fatal(err)
	}
	s := state{SummaryVersion: 2, FirstDate: "2026-01-01", Frontier: "2026-01-02"}
	if err = ensureSummaryGeneration(ctx, c, &s, "2026-01-01", e.currentTime()); err != nil {
		t.Fatal(err)
	}
	var newLive, published string
	var ready bool
	if err = c.QueryRowContext(ctx, `SELECT version_id,ready FROM log_archive_live_stats WHERE log_date='2026-01-01'`).Scan(&newLive, &ready); err != nil {
		t.Fatal(err)
	}
	if newLive == live || ready {
		t.Fatal("old live summary not isolated")
	}
	if err = ensureSummaryGeneration(ctx, c, &s, "2026-01-01", e.currentTime()); err != nil {
		t.Fatal(err)
	}
	var stable string
	c.QueryRowContext(ctx, `SELECT version_id FROM log_archive_live_stats WHERE log_date='2026-01-01'`).Scan(&stable)
	if stable != newLive {
		t.Fatal("upgrade not idempotent")
	}
	// The upstream logs table is empty: rebuild must use verified archive bytes.
	for i := 0; i < 4; i++ {
		if err = e.historyStep(ctx, c, &s, 100, true); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if err = c.QueryRowContext(ctx, `SELECT version_id FROM log_archive_days WHERE log_date='2026-01-01'`).Scan(&published); err != nil {
				t.Fatal(err)
			}
			if published != old {
				t.Fatal("published before complete")
			}
		}
	}
	var version int
	var raw []byte
	if err = c.QueryRowContext(ctx, `SELECT v.parser_version,s.amounts FROM log_archive_days d JOIN log_archive_day_versions v ON v.version_id=d.version_id JOIN log_archive_daily_stats s ON s.version_id=v.version_id WHERE d.log_date='2026-01-01'`).Scan(&version, &raw); err != nil {
		t.Fatal(err)
	}
	var amounts map[string]string
	if err = json.Unmarshal(raw, &amounts); err != nil {
		t.Fatal(err)
	}
	if version != summaryParserVersion || amounts["quota"] != "12345" || amounts["input_tokens"] != "0" || amounts["image_input_tokens"] != "30" {
		t.Fatalf("version=%d amounts=%v", version, amounts)
	}
	var n int
	if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_archive_daily_stats WHERE version_id=?", old).Scan(&n); err != nil || n != 1 {
		t.Fatal("old audit snapshot lost", err)
	}
	if err = e.buildLive(ctx, c, "2026-01-01", 100); err != nil {
		t.Fatal(err)
	}
	if err = c.QueryRowContext(ctx, "SELECT amounts FROM log_archive_daily_stats WHERE version_id=?", newLive).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &amounts); err != nil || amounts["image_input_tokens"] != "30" {
		t.Fatal("live usage missing", err)
	}
}
