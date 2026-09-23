package mysqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

// Reservation survives process restarts and partial DDL. Session is separate
// from the old writer session: an old lease must drain before preparation.
type archivePreparation struct {
	Identity af.Identity `json:"identity"`
	Session  string      `json:"session"`
	Token    string      `json:"token"`
}

func archiveAutoPreparePoll(ctx context.Context, tx *sql.Tx, out *ac.Response, instance string, st ac.Status, active []byte, enabled bool, session string, lease sql.NullTime) (bool, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT prepare_json FROM site_log_archive_control WHERE site_id=?`, out.SiteID).Scan(&raw); err != nil {
		return true, err
	}
	if len(raw) == 0 && (!st.AutoPrepare || st.Foundation != nil) {
		return false, nil
	}
	var pending archivePreparation
	if len(raw) > 0 && (json.Unmarshal(raw, &pending) != nil || pending.Identity.Validate() != nil) {
		return true, ac.ErrConflict
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return true, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO log_archive_targets(instance_id,agent_id,configured,seen_at,auto_prepare) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE configured=VALUES(configured),seen_at=VALUES(seen_at),auto_prepare=VALUES(auto_prepare),foundation_json=NULL`, instance, st.AgentID, st.Configured, now, st.AutoPrepare)
	if err != nil {
		return true, err
	}
	c := out.Config
	selected := c.InstanceID == instance && c.AgentID == st.AgentID
	if !selected || !st.AutoPrepare || st.Foundation != nil {
		out.Config.Running = false
		return true, nil
	}
	if st.SiteID != "" && st.SiteID != out.SiteID {
		return rejectArchivePreparation(ctx, tx, out, st, now, "archive_prepare_identity_mismatch")
	}
	st.SiteID = out.SiteID
	// Only the explicitly enabled executor may initialize storage. Paused
	// discovery still advertises a target so the existing start button works.
	if enabled && c.Running && st.Configured && st.PrepareDiscovered {
		var existing *af.Dataset
		if len(active) > 0 {
			d, e := readArchiveDataset(ctx, tx, out.SiteID, hex.EncodeToString(active), false)
			if e != nil {
				return true, e
			}
			existing = &d
		}
		if existing != nil && !c.FullHistory {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM archive_tasks WHERE dataset_id=? AND status IN ('running','retry_wait')`, active).Scan(&count); err != nil {
				return true, err
			}
			if count > 0 {
				return rejectArchivePreparation(ctx, tx, out, st, now, "archive_prepare_tasks_pending")
			}
		}
		// Recover a lost completion response without re-binding/incrementing
		// configuration or dropping a subsequently issued writer lease.
		if len(raw) == 0 && existing != nil && st.Prepared != nil {
			var previous []byte
			if err = tx.QueryRowContext(ctx, `SELECT status_json FROM site_log_archive_control WHERE site_id=?`, out.SiteID).Scan(&previous); err != nil {
				return true, err
			}
			var saved ac.Status
			if json.Unmarshal(previous, &saved) == nil && saved.Session == st.Session && saved.Prepared != nil && *saved.Prepared == *st.Prepared && saved.PreparedToken == st.PreparedToken && existing.Identity.Equal(st.Prepared.Identity) {
				out.PreparedAccepted = true
				return true, nil
			}
		}
		if len(raw) == 0 {
			if existing != nil {
				pending.Identity = existing.Identity
			} else if st.PrepareIdentity != nil {
				pending.Identity = *st.PrepareIdentity
			} else {
				ids := make([]byte, 32)
				if _, err = rand.Read(ids); err != nil {
					return true, err
				}
				pending.Identity = af.Identity{SiteID: out.SiteID, DatasetID: hex.EncodeToString(ids[:16]), SourceGenerationID: hex.EncodeToString(ids[16:])}
			}
		}
		if pending.Identity.Validate() != nil || pending.Identity.SiteID != out.SiteID || (st.PrepareIdentity != nil && !pending.Identity.Equal(*st.PrepareIdentity)) {
			return rejectArchivePreparation(ctx, tx, out, st, now, "archive_prepare_identity_mismatch")
		}
		live := lease.Valid && lease.Time.After(now)
		owns := pending.Session == st.Session && session == st.Session
		if !live || owns {
			pending.Session = st.Session
			if !owns || !live || pending.Token == "" {
				rawToken := make([]byte, 16)
				if _, err = rand.Read(rawToken); err != nil {
					return true, err
				}
				pending.Token = hex.EncodeToString(rawToken)
			}
			if st.Prepared != nil {
				if st.Prepared.Validate() != nil || !st.Prepared.Identity.Equal(pending.Identity) {
					return rejectArchivePreparation(ctx, tx, out, st, now, "archive_prepare_identity_mismatch")
				}
				// A completion may only use its own still-live preparation lease.
				if owns && live && st.PreparedToken == pending.Token {
					if err = bindAutomaticArchive(ctx, tx, out, *st.Prepared, existing); err != nil {
						if errors.Is(err, ac.ErrConflict) || errors.Is(err, af.ErrConflict) {
							return rejectArchivePreparation(ctx, tx, out, st, now, "archive_prepare_registration_conflict")
						}
						return true, err
					}
					out.PreparedAccepted = true
					st.State = "waiting"
					st.Error = ""
					st.PreparePhase = "registering"
					return true, savePreparationStatus(ctx, tx, out.SiteID, st, now)
				}
			}
			out.Prepare = &pending.Identity
			out.PrepareToken = pending.Token
			out.LeaseSeconds = 120
			if _, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET session_id=?,lease_until=? WHERE site_id=?`, st.Session, now.Add(120*time.Second), out.SiteID); err != nil {
				return true, err
			}
		} else if st.State != "error" {
			st.State = "waiting"
			st.Error = ""
			st.PreparePhase = "waiting_lease"
		}
		b, _ := json.Marshal(pending)
		if _, err = tx.ExecContext(ctx, `UPDATE site_log_archive_control SET prepare_json=? WHERE site_id=?`, string(b), out.SiteID); err != nil {
			return true, err
		}
	} else if !c.Running {
		st.State = "paused"
		st.Error = ""
		st.PreparePhase = ""
	}
	return true, savePreparationStatus(ctx, tx, out.SiteID, st, now)
}

// Keep business rejections visible in the authenticated status stream instead
// of repeatedly returning HTTP 409 and making a healthy Agent look offline.
func rejectArchivePreparation(ctx context.Context, tx *sql.Tx, out *ac.Response, st ac.Status, now time.Time, code string) (bool, error) {
	out.PrepareError = code
	st.SiteID = out.SiteID
	st.State = "error"
	st.PreparePhase = "failed"
	st.Error = code
	return true, savePreparationStatus(ctx, tx, out.SiteID, st, now)
}

func savePreparationStatus(ctx context.Context, tx *sql.Tx, site string, st ac.Status, now time.Time) error {
	b, _ := json.Marshal(st)
	_, err := tx.ExecContext(ctx, `UPDATE site_log_archive_control SET status_json=?,seen_at=? WHERE site_id=?`, string(b), now, site)
	return err
}

// Authenticated Agent evidence binds collection only. The reserved storage
// reference is not a readonly connection and cannot authorize billing/reading.
func bindAutomaticArchive(ctx context.Context, tx *sql.Tx, out *ac.Response, r af.Registration, existing *af.Dataset) error {
	if existing != nil {
		if !existing.Identity.Equal(r.Identity) || existing.SchemaFingerprint != r.SchemaFingerprint || existing.SourceFingerprint != r.SourceFingerprint || existing.ArchiveFormatVersion != r.ArchiveFormatVersion {
			return ac.ErrConflict
		}
	} else {
		identity, _ := json.Marshal(archiveSourceIdentity{SchemaFingerprint: r.SchemaFingerprint, SourceFingerprint: r.SourceFingerprint})
		_, err := tx.ExecContext(ctx, `INSERT INTO archive_datasets(dataset_id,site_id,source_generation_id,storage_ref,source_identity_json,archive_format_version,lifecycle_state,created_at,updated_at) VALUES(?,?,?,?,?,?,'active',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, archiveIDBytes(r.DatasetID), r.SiteID, archiveIDBytes(r.SourceGenerationID), "agent-"+r.DatasetID, string(identity), r.ArchiveFormatVersion)
		if err != nil {
			return archiveConflictError(err)
		}
	}
	if !out.Config.FullHistory || existing == nil || out.Config.ReconcileID != "" || out.Config.ReconcileDate != "" {
		out.Config.Version++
	}
	out.Config.FullHistory = true
	// The old per-day reconciler has drained with its lease. Its configuration
	// must not leave the newly bound full workflow invalid or permanently idle.
	out.Config.ReconcileID, out.Config.ReconcileDate = "", ""
	after, _ := json.Marshal(out.Config)
	_, err := tx.ExecContext(ctx, `UPDATE site_log_archive_control SET active_dataset_id=?,required_protocol_version=2,config_json=?,prepare_json=NULL,session_id='',lease_until=NULL WHERE site_id=?`, archiveIDBytes(r.DatasetID), string(after), r.SiteID)
	if err != nil {
		return err
	}
	return nil
}
