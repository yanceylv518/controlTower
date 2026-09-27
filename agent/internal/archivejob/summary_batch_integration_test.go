package archivejob

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Keep the previous per-group implementation as a test oracle for all JSON keys,
// including missing counters and metrics unknown to the current parser.
func legacySummaryWrite(ctx context.Context, tx *sql.Tx, version, date string, groups map[string]aggregate) error {
	for key, a := range groups {
		var raw []byte
		err := tx.QueryRowContext(ctx, "SELECT amounts FROM log_archive_daily_stats WHERE version_id=? AND group_hash=? FOR UPDATE", version, key).Scan(&raw)
		if err == nil {
			var old map[string]string
			if err = json.Unmarshal(raw, &old); err != nil {
				return err
			}
			for k, v := range old {
				if err = add(a.Amounts, k, v); err != nil {
					return err
				}
			}
		} else if err != sql.ErrNoRows {
			return err
		}
		dimensions, _ := json.Marshal(a.Dimensions)
		amounts, _ := json.Marshal(a.Amounts)
		if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_daily_stats VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE amounts=VALUES(amounts)", version, key, date, string(dimensions), string(amounts)); err != nil {
			return err
		}
	}
	return nil
}

func TestSummaryBatchMatchesLegacyMySQL(t *testing.T) {
	e, ctx := fixture(t)
	c, err := e.target.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = e.prepare(ctx, c); err != nil {
		t.Fatal(err)
	}
	build := func(wide bool) map[string]aggregate {
		groups := map[string]aggregate{}
		for i := 0; i < 384; i++ {
			r := testRow(map[string]string{"id": fmt.Sprint(i + 1), "type": "2", "user_id": fmt.Sprint(i % 3), "token_id": fmt.Sprint(i % 7), "channel_id": fmt.Sprint(i % 5), "model_name": fmt.Sprint(i), "group": "g", "username": "u", "token_name": "t", "quota": "9007199254740993", "prompt_tokens": "9007199254740993", "completion_tokens": "0", "other": fmt.Sprintf(`{"user_model_discount":%s,"model_ratio":2,"cache_tokens":0,"billing_expr":%q}`, []string{"0.5", "1"}[i%2], strings.Repeat("x", 64))})
			if i%3 == 0 {
				delete(r, "completion_tokens")
			}
			if i%5 == 0 {
				v := "5"
				r["type"] = &v
			}
			if wide {
				v := `{"billing_expr":"` + strings.Repeat("a", 12000) + `"}`
				r["other"] = &v
			}
			key, a, err := parseAggregate(r)
			if err != nil {
				t.Fatal(err)
			}
			a.Amounts["future_metric"] = "10000000000000000000000000000000000000000"
			groups[key] = a
		}
		return groups
	}
	for _, wide := range []bool{false, true} {
		versions := []string{id(), id()}
		for method, version := range versions {
			began := time.Now()
			for page := 0; page < 2; page++ {
				tx, err := c.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				groups := build(wide)
				if page == 1 {
					for _, a := range groups {
						delete(a.Amounts, "future_metric")
					}
				}
				if method == 0 {
					err = legacySummaryWrite(ctx, tx, version, "2026-01-01", groups)
				} else {
					err = writeAggregateBatch(ctx, tx, version, "2026-01-01", groups)
				}
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("wide=%t method=%d, 384 groups x 2 pages: %s", wide, method, time.Since(began))
		}
		read := func(version string) map[string]any {
			rows, err := c.QueryContext(ctx, "SELECT group_hash,dimensions,amounts FROM log_archive_daily_stats WHERE version_id=?", version)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			result := map[string]any{}
			for rows.Next() {
				var key, d, a string
				if err = rows.Scan(&key, &d, &a); err != nil {
					t.Fatal(err)
				}
				var dimensions any
				var amounts map[string]string
				if err = json.Unmarshal([]byte(d), &dimensions); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal([]byte(a), &amounts); err != nil {
					t.Fatal(err)
				}
				if amounts["quota"] != "18014398509481986" || amounts["future_metric"] != "10000000000000000000000000000000000000000" {
					t.Fatal(amounts)
				}
				result[key] = []any{dimensions, amounts}
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			return result
		}
		old, new := read(versions[0]), read(versions[1])
		if len(new) != 384 || !reflect.DeepEqual(old, new) {
			t.Fatal("dimensions or amounts differ from legacy")
		}
	}
}

func TestSummaryBatchCheckpointFailureRollsBackMySQL(t *testing.T) {
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
	for i := 1; i <= 260; i++ {
		if _, err = e.target.ExecContext(ctx, "INSERT INTO logs_202601 VALUES(?,?,2,7,8,?,1,0,10,'{}')", i, start+int64(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := load(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	s.History = history{Date: "2026-01-01", Step: "summarize", Version: id()}
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = save(ctx, tx, s); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = e.target.ExecContext(ctx, "CREATE TRIGGER fail_checkpoint BEFORE UPDATE ON log_archive_meta FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='checkpoint failure'"); err != nil {
		t.Fatal(err)
	}
	if err = e.summarize(ctx, c, &s, 1000); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	var count int
	if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_archive_daily_stats").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial writes committed: %d %v", count, err)
	}
	s, err = load(ctx, c)
	if err != nil || s.History.AfterID != 0 {
		t.Fatalf("checkpoint advanced: %+v %v", s, err)
	}
	if _, err = e.target.ExecContext(ctx, "DROP TRIGGER fail_checkpoint"); err != nil {
		t.Fatal(err)
	}
	if err = e.summarize(ctx, c, &s, 1000); err != nil {
		t.Fatal(err)
	}
	s, err = load(ctx, c)
	if err != nil || s.History.AfterID != 260 || s.History.Step != "seal" {
		t.Fatalf("resume: %+v %v", s, err)
	}
	var quota string
	if err = c.QueryRowContext(ctx, `SELECT SUM(CAST(amounts->>'$.quota' AS DECIMAL(65,0))) FROM log_archive_daily_stats`).Scan(&quota); err != nil || quota != "2600" {
		t.Fatalf("duplicate or missing: %s %v", quota, err)
	}
}
