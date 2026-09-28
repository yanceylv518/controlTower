package archivejob

import (
	"fmt"
	"strings"
	"testing"
)

func TestLargeLiveFailureDoesNotBlockOtherDatesMySQL(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
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
			if _, err = c.ExecContext(ctx, "ALTER TABLE logs_202601 MODIFY other LONGTEXT"); err != nil {
				t.Fatal(err)
			}
			prefix := "{\"ignored\":\""
			if malformed {
				prefix = "invalid"
			}
			if _, err = c.ExecContext(ctx, `INSERT INTO logs_202601(id,created_at,type,quota,other) VALUES(1,?,2,3,'{}'),(2,?,2,7,CONCAT(?,REPEAT('x',?), '"}'))`, start+1, start+86401, prefix, 9*1024*1024); err != nil {
				t.Fatal(err)
			}
			if _, err = c.ExecContext(ctx, "INSERT INTO log_archive_days(log_date,revision,state,updated_at) VALUES('2026-01-01',1,'pending',UTC_TIMESTAMP(6)),('2026-01-02',1,'pending',UTC_TIMESTAMP(6))"); err != nil {
				t.Fatal(err)
			}
			if err = e.liveStep(ctx, c, 1000); err != nil {
				t.Fatal(err)
			}
			s, err := load(ctx, c)
			if err != nil || s.LargeLive == nil {
				t.Fatal("missing transfer", err)
			}
			token := s.LargeLive.Token
			if malformed {
				for i := 0; i < 20; i++ {
					err = e.liveStep(ctx, c, 1000)
					if err != nil {
						break
					}
				}
				if err == nil || !strings.Contains(err.Error(), "invalid_billing_other_json") {
					t.Fatal("missing parse failure", err)
				}
			} else {
				if _, err = c.ExecContext(ctx, "UPDATE log_archive_live_stats SET retry_at=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 60 SECOND),error_code='test_transient' WHERE log_date='2026-01-02'"); err != nil {
					t.Fatal(err)
				}
			}
			if err = e.liveStep(ctx, c, 1000); err != nil {
				t.Fatal(err)
			}
			var ready bool
			if err = c.QueryRowContext(ctx, "SELECT ready FROM log_archive_live_stats WHERE log_date='2026-01-01'").Scan(&ready); err != nil || !ready {
				t.Fatal("other date starved", ready, err)
			}
			s, err = load(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if malformed && s.LargeLive != nil {
				t.Fatal("bad transfer still owns slot")
			}
			if !malformed && (s.LargeLive == nil || s.LargeLive.Token != token) {
				t.Fatal("transient checkpoint lost")
			}
		})
	}
}

func TestLargeLiveProgressWithAppendsAndReplacementMySQL(t *testing.T) {
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
	if _, err = c.ExecContext(ctx, "ALTER TABLE logs_202601 MODIFY other LONGTEXT"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(ctx, `INSERT INTO logs_202601(id,created_at,type,quota,other) VALUES(1,?,2,7,CONCAT('{"ignored":"',REPEAT('x',?), '","cache_tokens":17}'))`, start+1, 9*1024*1024); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ExecContext(ctx, "INSERT INTO log_archive_days(log_date,revision,state,updated_at) VALUES('2026-01-01',1,'pending',UTC_TIMESTAMP(6))"); err != nil {
		t.Fatal(err)
	}
	write := func(n int, quota string) {
		t.Helper()
		s, err := load(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := c.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		r := testRow(map[string]string{"id": fmt.Sprint(n), "created_at": fmt.Sprint(start + int64(n)), "type": "2", "quota": quota, "other": "{}"})
		if _, _, err = writeRaw(ctx, tx, r, &s); err != nil {
			t.Fatal(err)
		}
		if err = flushLive(ctx, tx, &s); err != nil {
			t.Fatal(err)
		}
		if err = save(ctx, tx, s); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 40; i++ {
		if err = e.liveStep(ctx, c, 1000); err != nil {
			t.Fatal(err)
		}
		write(i+2, "1") // A changing day revision must not restart the large row.
	}
	var ready bool
	if err = c.QueryRowContext(ctx, "SELECT ready FROM log_archive_live_stats WHERE log_date='2026-01-01'").Scan(&ready); err != nil || !ready {
		t.Fatal("large live starved", ready, err)
	}
	check := func(want string) {
		t.Helper()
		var got string
		if err = c.QueryRowContext(ctx, "SELECT CAST(SUM(CAST(amounts->>'$.quota' AS DECIMAL(65,0))) AS CHAR) FROM log_archive_daily_stats s JOIN log_archive_live_stats l ON s.version_id=l.version_id WHERE l.log_date='2026-01-01'").Scan(&got); err != nil || got != want {
			t.Fatal("wrong live quota", got, want, err)
		}
	}
	check("47")
	write(1, "3") // Replacing an old huge row must not load it into Agent memory.
	for i := 0; i < 5; i++ {
		if err = e.liveStep(ctx, c, 1000); err != nil {
			t.Fatal(err)
		}
	}
	check("43")
}
