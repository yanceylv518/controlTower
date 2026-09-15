package voicealert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Store struct {
	DB        *sql.DB
	Directory *Directory
}

func (s Store) Config(ctx context.Context, site string) (Config, error) {
	c := DefaultConfig()
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT config_json FROM voice_alert_site_config WHERE site_id=?", site).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return s.legacyConfig(ctx, site)
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	return c, c.Validate()
}
func (s Store) SaveConfig(ctx context.Context, site string, c Config, actor string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.ValidateSite(site); err != nil {
		return err
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE enabled=1 AND CASE WHEN site_id='' THEN id ELSE site_id END=?`, site).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("站点不存在或未启用")
	}
	// Legacy manually registered targets are no longer an allowlist.
	c.Targets = nil
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var guard int
	if err = tx.QueryRowContext(ctx, "SELECT id FROM voice_dispatch_guard WHERE id=1 FOR UPDATE").Scan(&guard); err != nil {
		return err
	}
	var before string
	err = tx.QueryRowContext(ctx, "SELECT config_json FROM voice_alert_site_config WHERE site_id=?", site).Scan(&before)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `INSERT INTO voice_alert_site_config(site_id,config_json,updated_by,updated_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE config_json=VALUES(config_json),updated_by=VALUES(updated_by),updated_at=VALUES(updated_at)`, site, string(raw), actor, now); err != nil {
		return err
	}
	// Keep recipient numbers out of the general operation audit.
	redact := func(raw string) string {
		var v Config
		if json.Unmarshal([]byte(raw), &v) != nil {
			return "{}"
		}
		for i := range v.Recipients {
			v.Recipients[i].Phone = "[redacted]"
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	id, err := randomID(16)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,'','voice_alert.configure','voice_alert',?,?,?,?,'succeeded',?)`, id, site, actor, redact(before), redact(string(raw)), now); err != nil {
		return err
	}
	return tx.Commit()
}

const sourcesSQL = `SELECT i.id FROM instances i WHERE i.enabled=1 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=?
AND (EXISTS(SELECT 1 FROM agents a WHERE a.instance_id=i.id AND (a.source_latest_log_id>0 OR a.last_log_id>0)) OR EXISTS(SELECT 1 FROM log_offsets o WHERE o.instance_id=i.id AND o.last_log_id>0))`

var ErrCoverage = errors.New("waiting for six minutes of continuous customer rate coverage")

// Snapshot returns 11 overlapping 60s totals spaced 30s apart, ending at the
// slowest collector watermark. Each source must cover all six minutes. Missing
// data (including old Agents) never becomes an artificial zero or decline.
func (s Store) Snapshot(ctx context.Context, t Target, now time.Time) ([]int64, time.Time, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sources.id,(SELECT MAX(bucket_time) FROM user_rate_seconds r WHERE r.instance_id=sources.id AND r.user_id=0 AND r.bucket_time<=?) FROM (`+sourcesSQL+`) sources`, now, t.Site)
	if err != nil {
		return nil, time.Time{}, err
	}
	type source struct {
		id  string
		end time.Time
	}
	sources := []source{}
	end := now
	for rows.Next() {
		var id string
		var stamp sql.NullTime
		if err = rows.Scan(&id, &stamp); err != nil {
			break
		}
		if !stamp.Valid || now.Sub(stamp.Time) > 90*time.Second {
			err = ErrCoverage
			break
		}
		sources = append(sources, source{id, stamp.Time})
		if stamp.Time.Before(end) {
			end = stamp.Time
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, time.Time{}, err
	}
	if len(sources) == 0 {
		return nil, time.Time{}, ErrCoverage
	}
	start := end.Add(-6 * time.Minute)
	buckets := map[int64]int64{}
	for _, src := range sources {
		marks, e := s.DB.QueryContext(ctx, `SELECT bucket_time,request_count FROM user_rate_seconds WHERE instance_id=? AND user_id=0 AND bucket_time>=? AND bucket_time<=? ORDER BY bucket_time`, src.id, start.Add(-60*time.Second), src.end)
		if e != nil {
			return nil, end, e
		}
		var stamps []time.Time
		for marks.Next() {
			var stamp time.Time
			var invalid int64
			if e = marks.Scan(&stamp, &invalid); e != nil {
				break
			}
			if invalid != 0 {
				e = ErrCoverage
				break
			}
			stamps = append(stamps, stamp)
		}
		if e == nil {
			e = marks.Err()
		}
		marks.Close()
		if e != nil {
			return nil, end, e
		}
		if !continuous(stamps, start, end) {
			return nil, end, ErrCoverage
		}
		data, e := s.DB.QueryContext(ctx, `SELECT bucket_time,tokens FROM user_rate_seconds WHERE instance_id=? AND user_id=? AND bucket_time>=? AND bucket_time<?`, src.id, t.UserID, start, end)
		if e != nil {
			return nil, end, e
		}
		for data.Next() {
			var stamp time.Time
			var tokens int64
			if e = data.Scan(&stamp, &tokens); e != nil {
				break
			}
			if tokens < 0 {
				e = ErrCoverage
				break
			}
			buckets[stamp.Unix()] += tokens
		}
		if e == nil {
			e = data.Err()
		}
		data.Close()
		if e != nil {
			return nil, end, e
		}
	}
	return rolling(buckets, end), end, nil
}
func continuous(stamps []time.Time, start, end time.Time) bool {
	if len(stamps) == 0 || stamps[0].After(start) || stamps[len(stamps)-1].Before(end) {
		return false
	}
	for i := 1; i < len(stamps); i++ {
		if stamps[i].Sub(stamps[i-1]) > 60*time.Second {
			return false
		}
	}
	return true
}
func rolling(buckets map[int64]int64, end time.Time) []int64 {
	values := make([]int64, 11)
	for i := range values {
		stop := end.Add(time.Duration(i-10) * 30 * time.Second).Unix()
		for sec := stop - 60; sec < stop; sec++ {
			values[i] += buckets[sec]
		}
	}
	return values
}

// Claim serializes customer cooldown and recipient quotas across all workers.
// The unknown record is committed BEFORE the external side effect. Crashes and
// ambiguous network failures therefore cannot immediately repeat the call.
func (s Store) Claim(ctx context.Context, t Target, end time.Time, low, high int64, direction string, now time.Time) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var guard int
	if err = tx.QueryRowContext(ctx, "SELECT id FROM voice_dispatch_guard WHERE id=1 FOR UPDATE").Scan(&guard); err != nil {
		return "", err
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM voice_alert_calls WHERE site_id=? AND user_id=? AND phone=? AND suppress_until>?`, t.Site, t.UserID, t.Phone, now).Scan(&n); err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	var minute, hour, day int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(created_at>?),0),COALESCE(SUM(created_at>?),0),COUNT(*) FROM voice_alert_calls WHERE phone=? AND created_at>?`, now.Add(-time.Minute), now.Add(-time.Hour), t.Phone, now.Add(-24*time.Hour)).Scan(&minute, &hour, &day); err != nil {
		return "", err
	}
	if minute >= 1 || hour >= 5 || day >= 20 {
		return "", nil
	}
	id, err := randomID(7)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO voice_alert_calls(id,site_id,user_id,phone,window_end,min_tpm,max_tpm,direction,status,created_at,suppress_until) VALUES(?,?,?,?,?,?,?,?,'unknown',?,?)`, id, t.Site, t.UserID, t.Phone, end, low, high, direction, now, now.Add(10*time.Minute+15*time.Second))
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}
func (s Store) Finish(ctx context.Context, id string, result Result, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE voice_alert_calls SET status=?,result_code=?,call_id=?,request_id=?,suppress_until=GREATEST(suppress_until,?) WHERE id=?`, result.Status, result.Code, result.CallID, result.RequestID, now.Add(10*time.Minute), id)
	return err
}

type CallRecord struct {
	ID            string    `json:"id"`
	Site          string    `json:"site"`
	UserID        int64     `json:"user_id"`
	Phone         string    `json:"phone"`
	WindowEnd     time.Time `json:"window_end"`
	MinTPM        int64     `json:"min_tpm"`
	MaxTPM        int64     `json:"max_tpm"`
	Direction     string    `json:"direction"`
	Status        string    `json:"status"`
	CallID        string    `json:"call_id"`
	RequestID     string    `json:"request_id"`
	Code          string    `json:"code"`
	CreatedAt     time.Time `json:"created_at"`
	SuppressUntil time.Time `json:"suppress_until"`
}

func (s Store) Calls(ctx context.Context, site string) ([]CallRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,site_id,user_id,phone,window_end,min_tpm,max_tpm,direction,status,call_id,request_id,result_code,created_at,suppress_until FROM voice_alert_calls WHERE site_id=? ORDER BY created_at DESC LIMIT 100`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CallRecord{}
	for rows.Next() {
		var c CallRecord
		if err = rows.Scan(&c.ID, &c.Site, &c.UserID, &c.Phone, &c.WindowEnd, &c.MinTPM, &c.MaxTPM, &c.Direction, &c.Status, &c.CallID, &c.RequestID, &c.Code, &c.CreatedAt, &c.SuppressUntil); err != nil {
			return nil, err
		}
		if len(c.Phone) > 7 {
			c.Phone = c.Phone[:3] + "****" + c.Phone[len(c.Phone)-4:]
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
