package mysqlstore

import (
	"context"
	ac "controltower/internal/archivecontrol"
	aj "controltower/internal/archivejob"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type archiveJobsStore struct{ Store }

func (s Store) NewArchiveStore() ac.Store { return archiveJobsStore{s} }
func (s archiveJobsStore) LatestLogArchiveMonth(context.Context, string) (string, error) {
	return "", nil
}
func (s archiveJobsStore) ListLogArchiveDays(context.Context, string, string) ([]ac.Day, error) {
	return []ac.Day{}, nil
}
func (s archiveJobsStore) ListLogArchives(ctx context.Context, site string) ([]ac.Item, error) {
	v := ac.Item{SiteID: site, Name: site, Config: ac.Default(), Targets: []ac.Target{}, Days: []ac.Day{}}
	v.Config.Tasks = &aj.Settings{}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MAX(enabled),0) FROM instances WHERE deleted=0 AND "+archiveSiteExpr+"=?", site).Scan(&count, &v.Enabled); err != nil {
		return nil, err
	}
	if count == 0 {
		return []ac.Item{}, nil
	}
	var config, status []byte
	var seen sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT config_json,status_json,seen_at FROM log_archive_control WHERE site_id=?", site).Scan(&config, &status, &seen)
	if err == nil {
		if json.Unmarshal(config, &v.Config) != nil || json.Unmarshal(status, &v.Status) != nil {
			return nil, errors.New("invalid_new_archive_control")
		}
		if seen.Valid {
			v.SeenAt = &seen.Time
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT e.instance_id,i.name,e.agent_id,e.configured,e.seen_at FROM log_archive_executors e JOIN instances i ON i.id=e.instance_id WHERE i.deleted=0 AND i.enabled=1 AND "+"COALESCE(NULLIF(i.site_id,''),i.id)=? AND e.seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND", site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t ac.Target
		if err = rows.Scan(&t.InstanceID, &t.Name, &t.AgentID, &t.Configured, &t.SeenAt); err != nil {
			return nil, err
		}
		v.Targets = append(v.Targets, t)
	}
	return []ac.Item{v}, rows.Err()
}
func (s archiveJobsStore) UpdateLogArchive(ctx context.Context, site string, c ac.Config, actor string) error {
	if c.Tasks == nil || !c.Validate() {
		return ac.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var actual string
	var enabled bool
	if err = tx.QueryRowContext(ctx, "SELECT "+archiveSiteExpr+",enabled FROM instances WHERE id=? AND deleted=0 FOR UPDATE", c.InstanceID).Scan(&actual, &enabled); err != nil || actual != site || !enabled {
		return ac.ErrConflict
	}
	var raw []byte
	var session string
	var live bool
	if err = tx.QueryRowContext(ctx, "SELECT config_json,session_id,COALESCE(lease_until>UTC_TIMESTAMP(6),0) FROM log_archive_control WHERE site_id=? FOR UPDATE", site).Scan(&raw, &session, &live); err != nil {
		return err
	}
	var old ac.Config
	if json.Unmarshal(raw, &old) != nil {
		return ac.ErrConflict
	}
	if old.Version != c.Version {
		return ac.ErrConflict
	}
	if live && (old.InstanceID != c.InstanceID || old.AgentID != c.AgentID) {
		return ac.ErrConflict
	}
	if c.Running {
		var ready bool
		if err = tx.QueryRowContext(ctx, "SELECT configured FROM log_archive_executors WHERE instance_id=? AND agent_id=? AND seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND", c.InstanceID, c.AgentID).Scan(&ready); err != nil || !ready {
			return ac.ErrConflict
		}
	}
	c.Version++
	b, _ := json.Marshal(c)
	if _, err = tx.ExecContext(ctx, "UPDATE log_archive_control SET config_json=? WHERE site_id=?", string(b), site); err != nil {
		return err
	}
	// Keep normal server audit records; these are not archive execution state.
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_audits(id,instance_id,operation_type,target_type,target_id,actor_id,before_summary,after_summary,status,created_at) VALUES(REPLACE(UUID(),'-',''),?,'archive.manage','log_archive',?,?,?,?,'success',UTC_TIMESTAMP(6))`, c.InstanceID, site, actor, string(raw), string(b))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s archiveJobsStore) PollLogArchive(ctx context.Context, instance string, st ac.Status) (ac.Response, error) {
	out := ac.Response{LeaseSeconds: 30}
	if st.Engine == nil || !st.Validate() {
		return out, ac.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var enabled bool
	if err = tx.QueryRowContext(ctx, "SELECT "+archiveSiteExpr+",enabled FROM instances WHERE id=? AND deleted=0 FOR UPDATE", instance).Scan(&out.SiteID, &enabled); err != nil {
		return out, err
	}
	initial := ac.Default()
	initial.AgentID = st.AgentID
	initial.InstanceID = instance
	initial.Tasks = &aj.Settings{}
	b, _ := json.Marshal(initial)
	if _, err = tx.ExecContext(ctx, "INSERT IGNORE INTO log_archive_control(site_id,config_json,status_json) VALUES(?,?,'{}')", out.SiteID, string(b)); err != nil {
		return out, err
	}
	var raw []byte
	var session string
	var live bool
	if err = tx.QueryRowContext(ctx, "SELECT config_json,session_id,COALESCE(lease_until>UTC_TIMESTAMP(6),0) FROM log_archive_control WHERE site_id=? FOR UPDATE", out.SiteID).Scan(&raw, &session, &live); err != nil {
		return out, err
	}
	if json.Unmarshal(raw, &out.Config) != nil || out.Config.Tasks == nil {
		return out, ac.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_executors VALUES(?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE configured=VALUES(configured),seen_at=VALUES(seen_at)", instance, st.AgentID, st.Configured); err != nil {
		return out, err
	}
	if enabled && out.Config.InstanceID == instance && out.Config.AgentID == st.AgentID && (!live || session == st.Session) {
		out.StatusAccepted = true
		st.SiteID = out.SiteID
		b, _ = json.Marshal(st)
		out.Granted = out.Config.Running && st.Configured
		if _, err = tx.ExecContext(ctx, "UPDATE log_archive_control SET status_json=?,seen_at=UTC_TIMESTAMP(6),session_id=?,lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 30 SECOND) WHERE site_id=?", string(b), st.Session, out.SiteID); err != nil {
			return out, err
		}
		for _, d := range st.Engine.Days {
			b, _ = json.Marshal(d)
			if _, err = tx.ExecContext(ctx, "INSERT INTO log_archive_day_reports VALUES(?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE detail_json=VALUES(detail_json),updated_at=VALUES(updated_at)", out.SiteID, d.Date, string(b)); err != nil {
				return out, err
			}
		}
	}
	return out, tx.Commit()
}
func (s archiveJobsStore) ListJobDays(ctx context.Context, site, month string) ([]aj.Day, error) {
	first, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT detail_json FROM log_archive_day_reports WHERE site_id=? AND log_date>=? AND log_date<? ORDER BY log_date", site, first.Format("2006-01-02"), first.AddDate(0, 1, 0).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aj.Day{}
	for rows.Next() {
		var raw []byte
		var d aj.Day
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
