package mysqlstore

import (
	"context"
	ac "controltower/internal/archivecontrol"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const archiveSiteExpr = "COALESCE(NULLIF(site_id,''),id)"

func (s Store) ListLogArchives(ctx context.Context, site string) ([]ac.Item, error) {
	v := ac.Item{SiteID: site, Name: site, Config: ac.Default(), Targets: []ac.Target{}}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(enabled),0) FROM instances WHERE deleted=0 AND `+archiveSiteExpr+`=?`, site).Scan(&count, &v.Enabled); err != nil {
		return nil, err
	}
	if count == 0 {
		return []ac.Item{}, nil
	}
	var cb, sb string
	var seen sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT config_json,status_json,seen_at FROM site_log_archive_control WHERE site_id=?`, site).Scan(&cb, &sb, &seen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if json.Unmarshal([]byte(cb), &v.Config) != nil || json.Unmarshal([]byte(sb), &v.Status) != nil {
			return nil, errors.New("invalid archive state")
		}
		if seen.Valid {
			v.SeenAt = &seen.Time
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.instance_id,i.name,t.agent_id,t.configured,t.seen_at FROM log_archive_targets t JOIN instances i ON i.id=t.instance_id WHERE i.deleted=0 AND i.enabled=1 AND COALESCE(NULLIF(i.site_id,''),i.id)=? AND t.seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND ORDER BY i.name,t.agent_id`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t ac.Target
		if err := rows.Scan(&t.InstanceID, &t.Name, &t.AgentID, &t.Configured, &t.SeenAt); err != nil {
			return nil, err
		}
		v.Targets = append(v.Targets, t)
	}
	return []ac.Item{v}, rows.Err()
}

func (s Store) ListLogArchiveDays(ctx context.Context, site, month string) ([]ac.Day, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, errors.New("invalid archive month")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT log_date,archived_rows,request_rows,error_rows,last_log_id,verified_at FROM site_log_archive_days WHERE site_id=? AND log_date>=? AND log_date<? ORDER BY log_date DESC`, site, start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ac.Day{}
	for rows.Next() {
		var d ac.Day
		if err := rows.Scan(&d.Date, &d.ArchivedRows, &d.RequestRows, &d.ErrorRows, &d.LastID, &d.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func ensureArchive(ctx context.Context, tx *sql.Tx, site string) error {
	b, _ := json.Marshal(ac.Default())
	_, err := tx.ExecContext(ctx, `INSERT INTO site_log_archive_control(site_id,config_json,status_json) VALUES(?,?,'{}') ON DUPLICATE KEY UPDATE site_id=VALUES(site_id)`, site, string(b))
	return err
}
func archiveRow(ctx context.Context, tx *sql.Tx, site string) (ac.Config, ac.Status, string, sql.NullTime, error) {
	var c ac.Config
	var st ac.Status
	var cb, sb, session string
	var lease sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT config_json,status_json,session_id,lease_until FROM site_log_archive_control WHERE site_id=? FOR UPDATE`, site).Scan(&cb, &sb, &session, &lease)
	if err != nil {
		return c, st, session, lease, err
	}
	if json.Unmarshal([]byte(cb), &c) != nil || json.Unmarshal([]byte(sb), &st) != nil {
		return c, st, session, lease, errors.New("invalid archive state")
	}
	return c, st, session, lease, nil
}
func (s Store) UpdateLogArchive(ctx context.Context, site string, c ac.Config, actor string) error {
	if !c.Validate() || c.InstanceID == "" || len(c.InstanceID) > 64 {
		return errors.New("invalid_archive_config")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the chosen member first, consistently with Agent polling.
	var actual string
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT `+archiveSiteExpr+`,enabled FROM instances WHERE id=? AND deleted=0 FOR UPDATE`, c.InstanceID).Scan(&actual, &enabled); err != nil || actual != site {
		return ac.ErrConflict
	}
	if err = ensureArchive(ctx, tx, site); err != nil {
		return err
	}
	prev, observed, _, lease, err := archiveRow(ctx, tx, site)
	if err != nil {
		return err
	}
	if c.Version != prev.Version {
		return ac.ErrConflict
	}
	changed := c.AgentID != prev.AgentID || c.InstanceID != prev.InstanceID
	if c.ReconcileID != "" && c.ReconcileID != prev.ReconcileID && (changed || prev.Running || observed.State != "paused" || observed.AppliedVersion != prev.Version || !observed.SupportsDailyCheck) {
		return ac.ErrConflict
	}
	if c.ReconcileID != "" && c.ReconcileID == prev.ReconcileID && c.ReconcileDate != prev.ReconcileDate {
		return ac.ErrConflict
	}
	if changed && lease.Valid && lease.Time.After(time.Now().UTC()) {
		return ac.ErrConflict
	}
	if c.Running {
		var ready bool
		if !enabled {
			return ac.ErrConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT configured FROM log_archive_targets WHERE instance_id=? AND agent_id=? AND seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND`, c.InstanceID, c.AgentID).Scan(&ready); err != nil || !ready {
			return ac.ErrConflict
		}
	}
	c.Version++
	b, _ := json.Marshal(c)
	before, _ := json.Marshal(prev)
	// Changing executor must not display the old executor's progress as its own.
	if changed {
		_, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET config_json=?,status_json='{}',seen_at=NULL,session_id='',lease_until=NULL WHERE site_id=?`, string(b), site)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET config_json=? WHERE site_id=?`, string(b), site)
	}
	if err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(?,?,'archive.manage','site_log_archive',?,?,?,?,'success',?)`, hex.EncodeToString(raw), c.InstanceID, site, actor, string(before), string(b), time.Now().UTC())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) PollLogArchive(ctx context.Context, instance string, st ac.Status) (ac.Response, error) {
	var out ac.Response
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var site string
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT `+archiveSiteExpr+`,enabled FROM instances WHERE id=? AND deleted=0 FOR UPDATE`, instance).Scan(&site, &enabled); err != nil {
		return out, err
	}
	if err = ensureArchive(ctx, tx, site); err != nil {
		return out, err
	}
	c, _, session, lease, err := archiveRow(ctx, tx, site)
	if err != nil {
		return out, err
	}
	out.Config = c
	out.SiteID = site
	now := time.Now().UTC()
	// Advertise every member; only the configured executor may update site progress.
	_, err = tx.ExecContext(ctx, `INSERT INTO log_archive_targets(instance_id,agent_id,configured,seen_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE configured=VALUES(configured),seen_at=VALUES(seen_at)`, instance, st.AgentID, st.Configured, now)
	if err != nil {
		return out, err
	}
	if c.InstanceID != instance || c.AgentID != st.AgentID || (session != "" && session != st.Session && lease.Valid && lease.Time.After(now)) {
		return out, tx.Commit()
	}
	if st.SiteID == site && st.AppliedVersion > c.Version {
		return out, ac.ErrConflict
	}
	if st.SiteID != site {
		st.AppliedVersion = 0
		st.State = "waiting"
	}
	out.Granted = enabled && c.Running && st.Configured
	var until any = nil
	if lease.Valid {
		until = lease.Time
	}
	if out.Granted {
		out.LeaseSeconds = 120
		until = now.Add(120 * time.Second)
	}
	for _, d := range st.Days {
		if !d.Validate() {
			return out, errors.New("invalid archive day")
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO site_log_archive_days(site_id,log_date,archived_rows,request_rows,error_rows,last_log_id,verified_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE archived_rows=IF(verified_at<=VALUES(verified_at),VALUES(archived_rows),archived_rows),request_rows=IF(verified_at<=VALUES(verified_at),VALUES(request_rows),request_rows),error_rows=IF(verified_at<=VALUES(verified_at),VALUES(error_rows),error_rows),last_log_id=GREATEST(last_log_id,VALUES(last_log_id)),verified_at=GREATEST(verified_at,VALUES(verified_at))`, site, d.Date, d.ArchivedRows, d.RequestRows, d.ErrorRows, d.LastID, d.VerifiedAt)
		if err != nil {
			return out, err
		}
	}
	st.Days = nil
	out.StatusAccepted = true
	b, _ := json.Marshal(st)
	_, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET status_json=?,seen_at=?,session_id=?,lease_until=? WHERE site_id=?`, string(b), now, st.Session, until, site)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
