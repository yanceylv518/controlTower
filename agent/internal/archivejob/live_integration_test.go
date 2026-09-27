package archivejob

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestLiveStatsCorrectionReplayAndRestartMySQL(t *testing.T) {
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
	s := state{}
	makeRow := func(id, offset int, quota, model string) row {
		return testRow(map[string]string{"id": fmt.Sprint(id), "created_at": fmt.Sprint(start + int64(offset)), "type": "2", "user_id": "7", "channel": "8", "model_name": model, "prompt_tokens": "10", "completion_tokens": "0", "quota": quota, "other": "{}"})
	}
	write := func(commit bool, records ...row) {
		t.Helper()
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, r := range records {
			if _, _, err = writeRaw(ctx, tx, r, &s); err != nil {
				t.Fatal(err)
			}
		}
		if err = flushLive(ctx, tx, &s); err != nil {
			t.Fatal(err)
		}
		if commit {
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, second := makeRow(1, 10, "9007199254740993", "a"), makeRow(2, 20, "20", "b")
	write(true, first, second)
	if err = e.buildLive(ctx, c, "2026-01-01", 1); err != nil {
		t.Fatal(err)
	}
	// Correct a covered row, insert behind the cursor, and modify an uncovered row.
	first = makeRow(1, 10, "9007199254740999", "c")
	second = makeRow(2, 20, "25", "b")
	late := makeRow(3, 5, "30", "a")
	write(true, first, second, late, makeRow(5, 100, "50", "e"))
	write(true, first, second, late, makeRow(5, 100, "50", "e"))
	// Rolled back raw changes must also roll back their contribution.
	write(false, makeRow(1, 10, "1", "c"))
	// A new Engine object uses only committed database checkpoints.
	restarted := &Engine{}
	for i := 0; i < 3; i++ {
		if err = restarted.buildLive(ctx, c, "2026-01-01", 1); err != nil {
			t.Fatal(err)
		}
	}
	write(true, makeRow(4, 2, "40", "d"))
	expected := map[string]aggregate{}
	raw, err := readRows(ctx, c, "SELECT * FROM logs_202601 ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range raw {
		if err = delta(expected, r, 1); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := c.QueryContext(ctx, "SELECT group_hash,amounts FROM log_archive_daily_stats s JOIN log_archive_live_stats l ON s.version_id=l.version_id WHERE l.log_date='2026-01-01'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var b []byte
		if err = rows.Scan(&key, &b); err != nil {
			t.Fatal(err)
		}
		var got map[string]string
		if err = json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		want, ok := expected[key]
		if !ok {
			for _, v := range got {
				if v != "0" {
					t.Fatalf("removed group still charged: %v", got)
				}
			}
			continue
		}
		if !reflect.DeepEqual(got, want.Amounts) {
			t.Fatalf("group mismatch: got %v want %v", got, want.Amounts)
		}
		delete(expected, key)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var ready bool
	if err = c.QueryRowContext(ctx, "SELECT ready FROM log_archive_live_stats WHERE log_date='2026-01-01'").Scan(&ready); err != nil || !ready {
		t.Fatal("initial snapshot must finish while new rows arrive", ready, err)
	}
	if len(expected) != 0 {
		t.Fatal("missing groups", expected)
	}
}
