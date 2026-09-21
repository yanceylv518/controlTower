package mysqlstore

import (
	"context"
	af "controltower/internal/archivecontract"
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
	var active []byte
	var seen sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT config_json,status_json,seen_at,active_dataset_id,required_protocol_version FROM site_log_archive_control WHERE site_id=?`, site).Scan(&cb, &sb, &seen, &active, &v.RequiredProtocolVersion)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		v.ActiveDatasetID = hex.EncodeToString(active)
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

func (s Store) LatestLogArchiveMonth(ctx context.Context, site string) (string, error) {
	var date string
	err := s.db.QueryRowContext(ctx, `SELECT log_date FROM site_log_archive_days WHERE site_id=? AND NOT EXISTS (SELECT 1 FROM site_log_archive_control WHERE site_id=? AND active_dataset_id IS NOT NULL) AND log_date BETWEEN '0001-01-01' AND '9999-12-31' ORDER BY log_date DESC LIMIT 1`, site, site).Scan(&date)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", err
	}
	return d.Format("2006-01"), nil
}

func (s Store) ListLogArchiveDays(ctx context.Context, site, month string) ([]ac.Day, error) {
	start, err := time.Parse("2006-01", month)
	if err != nil {
		return nil, errors.New("invalid archive month")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT log_date,archived_rows,request_rows,error_rows,last_log_id,verified_at FROM site_log_archive_days WHERE site_id=? AND NOT EXISTS (SELECT 1 FROM site_log_archive_control WHERE site_id=? AND active_dataset_id IS NOT NULL) AND log_date>=? AND log_date<? ORDER BY log_date DESC`, site, site, start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02"))
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
	active, err := archiveFoundationBinding(ctx, tx, site)
	if err != nil {
		return err
	}
	// Versioned reconciliation is not available until the verification phase.
	if len(active) != 0 && c.ReconcileID != "" {
		return ac.ErrConflict
	}
	if c.Version != prev.Version {
		return ac.ErrConflict
	}
	if c.FullHistory && (len(active)==0 || c.ReconcileID!="") { return ac.ErrConflict }
	if prev.FullHistory && !c.FullHistory { return ac.ErrConflict }
	if prev.Running && c.HistoryImmutable!=prev.HistoryImmutable { return ac.ErrConflict }
	if c.HistoryImmutable!=prev.HistoryImmutable && observed.Workflow!=nil && (observed.Workflow.Phase=="verify" || observed.Workflow.Phase=="seal") { return ac.ErrConflict }
	if c.FullHistory && !prev.FullHistory {
		var pending int
		if err=tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND status IN ('running','retry_wait')`, active).Scan(&pending);err!=nil{return err}
		if pending!=0{return ac.ErrConflict}
	}
	changed := c.AgentID != prev.AgentID || c.InstanceID != prev.InstanceID
	if c.ReconcileID != "" && c.ReconcileID != prev.ReconcileID && (changed || prev.Running || observed.State != "paused" || observed.AppliedVersion != prev.Version || !observed.SupportsDailyCheck) {
		return ac.ErrConflict
	}
	if c.ReconcileID != "" && c.ReconcileID == prev.ReconcileID && c.ReconcileDate != prev.ReconcileDate {
		return ac.ErrConflict
	}
	if changed {
		live, err := archiveLeaseLive(ctx, tx, lease)
		if err != nil {
			return err
		}
		if live {
			return ac.ErrConflict
		}
	}
	if c.Running {
		var ready bool
		if !enabled {
			return ac.ErrConflict
		}
		if err := tx.QueryRowContext(ctx, `SELECT configured FROM log_archive_targets WHERE instance_id=? AND agent_id=? AND seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND`, c.InstanceID, c.AgentID).Scan(&ready); err != nil || !ready {
			return ac.ErrConflict
		}
		if len(active) != 0 {
			if err = archiveWriterTarget(ctx, tx, site, active, c); err != nil {
				return err
			}
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
	if len(active) != 0 {
		state := "paused"
		if c.Running {
			state = "active"
		}
		if _, err = tx.ExecContext(ctx, `UPDATE archive_datasets SET lifecycle_state=?,config_revision=config_revision+1,updated_at=UTC_TIMESTAMP(6) WHERE dataset_id=?`, state, active); err != nil {
			return err
		}
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
	c, observed, session, lease, err := archiveRow(ctx, tx, site)
	if err != nil {
		return out, err
	}
	out.Config = c
	out.SiteID = site
	now := time.Now().UTC()
	active, err := archiveFoundationBinding(ctx, tx, site)
	if err != nil {
		return out, err
	}
	if err = archiveFoundationPoll(ctx, tx, site, active, st); err != nil {
		return out, err
	}
	if st.Foundation != nil && st.Foundation.SupportsAtomicWriter() {
		if _, err = af.IDBytes(st.Session); err != nil {
			return out, err
		}
	}
	var epoch uint64
	if len(active) != 0 {
		if err = tx.QueryRowContext(ctx, `SELECT writer_epoch,UTC_TIMESTAMP(6) FROM site_log_archive_control WHERE site_id=?`, site).Scan(&epoch, &now); err != nil {
			return out, err
		}
		if st.Foundation != nil && st.Foundation.WriterEpoch != 0 {
			if st.Foundation.WriterEpoch > epoch {
				return out, ac.ErrConflict
			}
			if st.Session != session {
				return out, tx.Commit()
			}
		}
	}
	protocol := 1
	var foundation any
	if st.Foundation != nil {
		protocol = st.Foundation.ProtocolVersion
		b, _ := json.Marshal(st.Foundation)
		foundation = string(b)
	}
	// Advertise every member; only the configured executor may update site progress.
	_, err = tx.ExecContext(ctx, `INSERT INTO log_archive_targets(instance_id,agent_id,configured,seen_at,protocol_version,foundation_json) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE configured=VALUES(configured),seen_at=VALUES(seen_at),protocol_version=VALUES(protocol_version),foundation_json=VALUES(foundation_json)`, instance, st.AgentID, st.Configured, now, protocol, foundation)
	if err != nil {
		return out, err
	}
	if len(active) != 0 {
		atomic := st.Foundation != nil && archiveAtomicWriter(*st.Foundation)
		if !atomic {
			out.Config.Running = false
		}
		if st.Foundation == nil || c.InstanceID != instance || c.AgentID != st.AgentID {
			return out, tx.Commit()
		}
		if st.AppliedVersion > c.Version {
			return out, ac.ErrConflict
		}
		return archiveWriterPoll(ctx, tx, out, st, observed, enabled, session, lease, epoch, now, atomic)
	}
	if c.InstanceID != instance || c.AgentID != st.AgentID || (session != "" && session != st.Session && lease.Valid && lease.Time.After(now)) {
		return out, tx.Commit()
	}
	if st.SiteID == site && st.AppliedVersion > c.Version {
		return out, ac.ErrConflict
	}
	if st.SiteID != site {
		// Bootstrap the authoritative site identity without consuming receipts
		// or granting execution. Legacy agents echo SiteID on their next poll.
		return out, tx.Commit()
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
