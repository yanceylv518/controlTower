package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"database/sql"
	"fmt"
	"time"
)

// RemoveBillingUpstream transfers current configuration atomically, preserving historical subjects.
func (s Store) RemoveBillingUpstream(ctx context.Context, site string, id, revision, target, targetRevision int64, actor string) (bool, error) {
	var archived bool
	err := retryBillingDeadlock(ctx, func() error {
		var err error
		archived, err = s.removeBillingUpstreamAttempt(ctx, site, id, revision, target, targetRevision, actor)
		return err
	})
	return archived, err
}
func (s Store) removeBillingUpstreamAttempt(ctx context.Context, site string, id, revision, target, targetRevision int64, actor string) (bool, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, `SELECT GET_LOCK('ct:billing-discount-write',10)`).Scan(&locked); err != nil || locked != 1 {
		return false, fmt.Errorf("discount lock unavailable")
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('ct:billing-discount-write')`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockUpstreamParents(ctx, tx, site); err != nil {
		return false, err
	}
	ups, err := readBillingUpstreams(ctx, tx, site)
	if err != nil {
		return false, err
	}
	var source, dest *billing.Upstream
	for i := range ups {
		if ups[i].ID == id {
			source = &ups[i]
		}
		if ups[i].ID == target {
			dest = &ups[i]
		}
	}
	if source == nil {
		return false, sql.ErrNoRows
	}
	if revision <= 0 || source.Revision != revision {
		return false, billing.ErrUpstreamRevisionConflict
	}
	if target != 0 && (dest == nil || target == id) {
		return false, billing.ErrUpstreamTransferConflict
	}
	if dest != nil && dest.Revision != targetRevision {
		return false, billing.ErrUpstreamRevisionConflict
	}
	if len(source.Channels) > 0 && dest == nil {
		return false, billing.ErrUpstreamInUse
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_jobs j JOIN billing_statement_jobs st ON st.job_id=j.id WHERE j.instance_id=? AND st.statement_type='upstream_statement' AND st.subject_id IN (?,?) AND j.status IN ('pending','running','publishing')`, site, id, target).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, fmt.Errorf("upstream_billing_busy")
	}
	now := time.Now().UTC()
	if dest != nil {
		var overlap int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_discount_rules a JOIN billing_discount_rules b ON a.instance_id=b.instance_id AND a.discount_type=b.discount_type AND a.channel_id=b.channel_id AND BINARY a.model_name=BINARY b.model_name AND a.effective_from<COALESCE(b.effective_to,DATE('9999-12-31')) AND b.effective_from<COALESCE(a.effective_to,DATE('9999-12-31')) WHERE a.instance_id=? AND a.discount_type='upstream_channel' AND a.subject_id=? AND b.subject_id=? AND EXISTS(SELECT 1 FROM billing_upstream_channel_bindings c WHERE c.instance_id=a.instance_id AND c.upstream_id=a.subject_id AND c.channel_id=a.channel_id)`, site, id, target).Scan(&overlap); err != nil {
			return false, err
		}
		if overlap > 0 {
			return false, billing.ErrDiscountOverlap
		}
		// Keep source discount records for history; copy only the channels being merged.
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_discount_rules(instance_id,discount_type,subject_id,channel_id,model_name,discount,effective_from,effective_to,remark,created_at,updated_at,updated_by) SELECT r.instance_id,r.discount_type,?,r.channel_id,r.model_name,r.discount,r.effective_from,r.effective_to,r.remark,?,?,? FROM billing_discount_rules r JOIN billing_upstream_channel_bindings b ON b.instance_id=r.instance_id AND b.upstream_id=r.subject_id AND b.channel_id=r.channel_id WHERE r.instance_id=? AND r.discount_type='upstream_channel' AND r.subject_id=?`, target, now, now, actor, site, id); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstream_channel_bindings SET upstream_id=?,association_source='manual',matched_prefix='',associated_at=?,associated_by=? WHERE instance_id=? AND upstream_id=?`, target, now, actor, site, id); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstream_prefixes SET upstream_id=?,needs_review=0 WHERE instance_id=? AND upstream_id=?`, target, site, id); err != nil {
			return false, err
		}
		for _, u := range source.URLs {
			if _, err = appendUpstreamURL(ctx, tx, site, target, u, now); err != nil {
				return false, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET revision=revision+1,updated_at=?,updated_by=? WHERE instance_id=? AND id=?`, now, actor, site, target); err != nil {
			return false, err
		}
	}
	var history int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM billing_statement_jobs st JOIN billing_jobs j ON j.id=st.job_id WHERE j.instance_id=? AND st.statement_type='upstream_statement' AND st.subject_id=?)+(SELECT COUNT(*) FROM billing_generation_tasks WHERE instance_id=? AND kind='upstream_statement' AND JSON_CONTAINS(subject_ids_json,CAST(? AS JSON)))+(SELECT COUNT(*) FROM billing_discount_rules WHERE instance_id=? AND discount_type='upstream_channel' AND subject_id=?)`, site, id, site, id, site, id).Scan(&history); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE billing_generation_ranges SET cancelled=1 WHERE instance_id=? AND kind='upstream_statement' AND subject_id=?`, site, id); err != nil {
		return false, err
	}
	if history > 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_prefixes WHERE instance_id=? AND upstream_id=?`, site, id); err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET archived=1,enabled=0,revision=revision+1,updated_at=?,updated_by=? WHERE instance_id=? AND id=?`, now, actor, site, id)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM billing_upstreams WHERE instance_id=? AND id=?`, site, id)
	}
	if err != nil {
		return false, err
	}
	return history > 0, tx.Commit()
}
