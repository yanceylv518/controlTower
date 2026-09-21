package mysqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"time"

	af "controltower/internal/archivecontract"
	ac "controltower/internal/archivecontrol"
)

func archiveAtomicWriter(status af.FoundationStatus) bool {
	return status.SupportsAtomicWriter()
}

func archiveLeaseLive(ctx context.Context, tx *sql.Tx, lease sql.NullTime) (bool, error) {
	if !lease.Valid {
		return false, nil
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(6)`).Scan(&now); err != nil {
		return false, err
	}
	return lease.Time.After(now), nil
}

// Read capability evidence from a recent identity-checked poll rather than from
// an administrator's request. A foundation-only Agent cannot start the writer.
func archiveWriterTarget(ctx context.Context, tx *sql.Tx, site string, active []byte, config ac.Config) error {
	var raw []byte
	var automatic bool
	if err := tx.QueryRowContext(ctx, `SELECT foundation_json,auto_prepare FROM log_archive_targets WHERE instance_id=? AND agent_id=? AND configured=1 AND seen_at>UTC_TIMESTAMP()-INTERVAL 90 SECOND`, config.InstanceID, config.AgentID).Scan(&raw, &automatic); err != nil {
		return ac.ErrConflict
	}
	// Starting a freshly restarted automatic Agent authorizes preparation;
	// actual writes still require a verified Foundation and writer grant.
	if automatic && len(raw) == 0 {
		return nil
	}
	var foundation af.FoundationStatus
	if config.FullHistory && (json.Unmarshal(raw, &foundation) != nil || !foundation.SupportsWorkflow()) {
		return ac.ErrConflict
	}
	if json.Unmarshal(raw, &foundation) != nil || foundation.Validate() != nil || !archiveAtomicWriter(foundation) {
		return ac.ErrConflict
	}
	if err := archiveFoundationPoll(ctx, tx, site, active, ac.Status{SiteID: site, Foundation: &foundation}); err != nil {
		return ac.ErrConflict
	}
	return nil
}

func archiveWriterPoll(ctx context.Context, tx *sql.Tx, out ac.Response, st, observed ac.Status, enabled bool, session string, lease sql.NullTime, epoch uint64, now time.Time, atomic bool) (ac.Response, error) {
	live := lease.Valid && lease.Time.After(now)
	if session != "" && session != st.Session && live {
		return out, tx.Commit()
	}
	// Downgraded Agents may advertise their capability, but cannot replace the
	// receipt/status of an earlier writer or clear its unexpired lease.
	if !atomic && (live || (observed.Foundation != nil && observed.Foundation.WriterEpoch != 0)) {
		return out, tx.Commit()
	}
	accept := st.AppliedVersion >= observed.AppliedVersion
	if st.Foundation.WriterEpoch != 0 && st.Foundation.WriterEpoch < epoch {
		accept = false
	}
	if observed.Foundation != nil && observed.Foundation.WriterEpoch != 0 {
		if st.Foundation.WriterEpoch == 0 || st.Foundation.CatalogRevision < observed.Foundation.CatalogRevision {
			accept = false
		}
	}
	if !atomic {
		switch st.State {
		case "paused", "error", "waiting", "unconfigured":
		default:
			st.State = "paused"
		}
	}
	grant := atomic && enabled && out.Config.Running && out.Config.ReconcileID == "" && st.Configured && st.AppliedVersion == out.Config.Version
	if out.Config.FullHistory && !st.Foundation.SupportsWorkflow() {
		grant = false
	}
	if grant {
		bootstrapRetry := session == st.Session && live && st.Foundation.WriterEpoch < epoch
		if !bootstrapRetry {
			if session != st.Session || !live {
				if epoch == math.MaxUint64 {
					return out, ac.ErrConflict
				}
				epoch++
			}
			session = st.Session
			lease = sql.NullTime{Time: now.Add(120 * time.Second), Valid: true}
			if _, err := tx.ExecContext(ctx, `UPDATE site_log_archive_control SET writer_epoch=?,session_id=?,lease_until=? WHERE site_id=?`, epoch, session, lease.Time, out.SiteID); err != nil {
				return out, err
			}
		}
		// A zero-epoch retry can recover the original grant, but cannot extend
		// it until the Agent has actually claimed the target's writer fence.
		seconds := int(lease.Time.Sub(now) / time.Second)
		if seconds >= 60 {
			out.Granted, out.LeaseSeconds = true, seconds
			out.WriterGrant = &af.WriterGrant{Identity: st.Foundation.Identity, ProtocolVersion: af.ProtocolVersion, WriterEpoch: epoch, Session: st.Session, ConfigVersion: out.Config.Version, LeaseSeconds: seconds}
		}
	}
	if err := archiveBackfillPoll(ctx, tx, &out, st, now); err != nil {
		return out, err
	}
	if !out.BackfillAccepted {
		st.Backfill = observed.Backfill
	}
	if !out.ReconcileAccepted {
		st.Reconcile = observed.Reconcile
	}
	if !out.SealAccepted {
		st.Seal = observed.Seal
	}
	if accept {
		// A new session's initial target state must not move the displayed
		// committed cursor backwards. The archive remains the authority.
		if st.LastID < observed.LastID {
			st.LastID = observed.LastID
		}
		if observed.LastSuccess != nil && (st.LastSuccess == nil || st.LastSuccess.Before(*observed.LastSuccess)) {
			st.LastSuccess = observed.LastSuccess
		}
		st.Days = nil
		b, _ := json.Marshal(st)
		if _, err := tx.ExecContext(ctx, `UPDATE site_log_archive_control SET status_json=?,seen_at=? WHERE site_id=?`, string(b), now, out.SiteID); err != nil {
			return out, err
		}
		out.StatusAccepted = true
	}
	return out, tx.Commit()
}
