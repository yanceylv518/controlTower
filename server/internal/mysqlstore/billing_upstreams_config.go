package mysqlstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controltower/server/internal/billing"
)

type upstreamReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Reading saved configuration never discovers channels or mutates ownership.
func (s Store) ListBillingUpstreams(ctx context.Context, site string) ([]billing.Upstream, error) {
	if err := s.refreshBillingUpstreamChannels(ctx, site); err != nil {
		return nil, err
	}
	return s.listBillingUpstreams(ctx, site)
}
func (s Store) ListBillingUpstreamsConfig(ctx context.Context, site string) ([]billing.Upstream, error) {
	return s.listBillingUpstreams(ctx, site)
}
func (s Store) listBillingUpstreams(ctx context.Context, site string) ([]billing.Upstream, error) {
	return readBillingUpstreams(ctx, s.db, site)
}
func readBillingUpstreams(ctx context.Context, q upstreamReader, site string) ([]billing.Upstream, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,instance_id,name,enabled,remark,created_at,updated_at,updated_by,revision FROM billing_upstreams WHERE instance_id=? AND archived=0 ORDER BY name,id`, site)
	if err != nil {
		return nil, err
	}
	items := []billing.Upstream{}
	byID := map[int64]int{}
	for rows.Next() {
		var v billing.Upstream
		if err = rows.Scan(&v.ID, &v.InstanceID, &v.Name, &v.Enabled, &v.Remark, &v.CreatedAt, &v.UpdatedAt, &v.UpdatedBy, &v.Revision); err != nil {
			rows.Close()
			return nil, err
		}
		v.URLs = []string{}
		v.ChannelPrefixes = []string{}
		v.SuggestedPrefixes = []string{}
		v.Channels = []billing.UpstreamChannel{}
		byID[v.ID] = len(items)
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT upstream_id,prefix,needs_review FROM billing_upstream_prefixes WHERE instance_id=? ORDER BY prefix`, site)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var p string
		var review bool
		if err = rows.Scan(&id, &p, &review); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := byID[id]; ok {
			items[i].ChannelPrefixes = append(items[i].ChannelPrefixes, p)
			if review {
				items[i].ReviewPrefixes = append(items[i].ReviewPrefixes, p)
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT upstream_id,channel_id,channel_name,COALESCE(models_json,JSON_ARRAY()),association_source,matched_prefix,associated_at,associated_by FROM billing_upstream_channel_bindings WHERE instance_id=? ORDER BY upstream_id,channel_id`, site)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var c billing.UpstreamChannel
		var models string
		var at sql.NullTime
		if err = rows.Scan(&id, &c.ChannelID, &c.ChannelName, &models, &c.AssociationSource, &c.MatchedPrefix, &at, &c.AssociatedBy); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal([]byte(models), &c.Models); err != nil {
			rows.Close()
			return nil, err
		}
		if at.Valid {
			c.AssociatedAt = &at.Time
		}
		if i, ok := byID[id]; ok {
			items[i].Channels = append(items[i].Channels, c)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT upstream_id,url FROM billing_upstream_urls WHERE instance_id=? ORDER BY created_at,url`, site)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var u string
		if err = rows.Scan(&id, &u); err != nil {
			rows.Close()
			return nil, err
		}
		if i, ok := byID[id]; ok {
			items[i].URLs = append(items[i].URLs, u)
		}
	}
	err = rows.Err()
	rows.Close()
	for i := range items {
		items[i].SuggestedPrefixes = billing.SuggestUpstreamPrefixes(items[i])
	}
	return items, err
}

// All ownership writers lock site parents in ID order before rules/bindings.
func lockUpstreamParents(ctx context.Context, tx *sql.Tx, site string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM billing_upstreams WHERE instance_id=? ORDER BY id FOR UPDATE`, site)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func appendUpstreamURL(ctx context.Context, tx *sql.Tx, site string, id int64, url string, now time.Time) (bool, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
	result, err := tx.ExecContext(ctx, `INSERT IGNORE INTO billing_upstream_urls(instance_id,url_hash,url,upstream_id,created_at) VALUES(?,?,?,?,?)`, site, hash, url, id, now)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (s Store) PutBillingUpstream(ctx context.Context, item billing.Upstream) (billing.Upstream, error) {
	var saved billing.Upstream
	err := retryBillingDeadlock(ctx, func() error { var err error; saved, err = s.putBillingUpstreamAttempt(ctx, item); return err })
	return saved, err
}
func (s Store) putBillingUpstreamAttempt(ctx context.Context, item billing.Upstream) (billing.Upstream, error) {
	endpoint := strings.TrimSpace(item.URL)
	if endpoint != "" {
		var err error
		endpoint, err = billing.NormalizeUpstreamURL(endpoint)
		if err != nil {
			return item, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	if err = lockUpstreamParents(ctx, tx, item.InstanceID); err != nil {
		return item, err
	}
	existing, err := readBillingUpstreams(ctx, tx, item.InstanceID)
	if err != nil {
		return item, err
	}
	before := map[int64]billing.Upstream{}
	owners := map[int64]int64{}
	bindings := map[int64]billing.UpstreamChannel{}
	prefixOwners := map[string]int64{}
	for _, up := range existing {
		before[up.ID] = up
		for _, c := range up.Channels {
			owners[c.ChannelID] = up.ID
			bindings[c.ChannelID] = c
		}
		for _, p := range up.ChannelPrefixes {
			prefixOwners[p] = up.ID
		}
	}
	now := time.Now().UTC()
	isNew := item.ID == 0
	if !isNew {
		old, ok := before[item.ID]
		if !ok {
			return item, sql.ErrNoRows
		}
		if item.Revision <= 0 || old.Revision != item.Revision {
			return item, billing.ErrUpstreamRevisionConflict
		}
	}
	if isNew {
		result, e := tx.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,enabled,remark,created_at,updated_at,updated_by,revision) VALUES(?,?,?,?,?,?,?,1)`, item.InstanceID, item.Name, item.Enabled, item.Remark, now, now, item.UpdatedBy)
		if e != nil {
			return item, e
		}
		item.ID, err = result.LastInsertId()
		if err != nil {
			return item, err
		}
		if item.ChannelPrefixes == nil {
			item.ChannelPrefixes = []string{item.Name}
		}
	}
	changedOthers := map[int64]bool{}
	transfers := map[string]int64{}
	for _, v := range item.PrefixTransfers {
		if v.FromUpstreamID <= 0 || v.FromUpstreamID == item.ID || transfers[v.Prefix] != 0 {
			return item, billing.ErrUpstreamTransferConflict
		}
		transfers[v.Prefix] = v.FromUpstreamID
	}
	if item.ChannelPrefixes != nil {
		requested := map[string]bool{}
		for _, p := range item.ChannelPrefixes {
			if p == "" || len([]rune(p)) > 128 {
				return item, fmt.Errorf("invalid_channel_prefix")
			}
			requested[p] = true
			owner := prefixOwners[p]
			if from := transfers[p]; from != 0 {
				if owner != from {
					return item, billing.ErrUpstreamTransferConflict
				}
				changedOthers[from] = true
			} else if owner != 0 && owner != item.ID {
				return item, billing.ErrUpstreamPrefixConflict
			}
		}
		for p := range transfers {
			if !requested[p] {
				return item, billing.ErrUpstreamTransferConflict
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_prefixes WHERE instance_id=? AND upstream_id=?`, item.InstanceID, item.ID); err != nil {
			return item, err
		}
		for p := range requested {
			if from := transfers[p]; from != 0 {
				if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_prefixes WHERE instance_id=? AND prefix=? AND upstream_id=?`, item.InstanceID, p, from); err != nil {
					return item, err
				}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_prefixes(instance_id,prefix,upstream_id,needs_review) VALUES(?,?,?,0)`, item.InstanceID, p, item.ID); err != nil {
				return item, err
			}
		}
	} else if len(transfers) > 0 {
		return item, billing.ErrUpstreamTransferConflict
	}
	if endpoint != "" {
		if _, err = appendUpstreamURL(ctx, tx, item.InstanceID, item.ID, endpoint, now); err != nil {
			return item, err
		}
	}
	remove := map[int64]bool{}
	add := map[int64]bool{}
	channelTransfers := map[int64]int64{}
	for _, id := range item.RemoveChannelIDs {
		if id <= 0 {
			return item, billing.ErrUpstreamChannelConflict
		}
		remove[id] = true
	}
	for _, id := range item.AddChannelIDs {
		if id <= 0 || remove[id] {
			return item, billing.ErrUpstreamChannelConflict
		}
		add[id] = true
	}
	supplied := map[int64]billing.UpstreamChannel{}
	for _, c := range item.Channels {
		supplied[c.ChannelID] = c
		if isNew {
			add[c.ChannelID] = true
		}
	}
	for _, v := range item.ChannelTransfers {
		if v.ChannelID <= 0 || v.FromUpstreamID <= 0 || v.FromUpstreamID == item.ID || remove[v.ChannelID] || channelTransfers[v.ChannelID] != 0 {
			return item, billing.ErrUpstreamTransferConflict
		}
		if owners[v.ChannelID] != v.FromUpstreamID {
			return item, billing.ErrUpstreamTransferConflict
		}
		channelTransfers[v.ChannelID] = v.FromUpstreamID
		add[v.ChannelID] = true
	}
	for id := range remove {
		if owners[id] != item.ID {
			return item, billing.ErrUpstreamChannelConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_channel_exclusions(instance_id,channel_id,created_at,channel_name,previous_upstream_id,updated_by) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE created_at=VALUES(created_at),channel_name=VALUES(channel_name),previous_upstream_id=VALUES(previous_upstream_id),updated_by=VALUES(updated_by)`, item.InstanceID, id, now, bindings[id].ChannelName, item.ID, item.UpdatedBy); err != nil {
			return item, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=? AND channel_id=?`, item.InstanceID, item.ID, id); err != nil {
			return item, err
		}
	}
	for id := range add {
		from := channelTransfers[id]
		owner := owners[id]
		if owner != 0 && owner != item.ID && from == 0 {
			return item, billing.ErrUpstreamChannelConflict
		}
		c, ok := supplied[id]
		if owner != 0 {
			c = bindings[id]
			ok = true
		}
		if !ok {
			return item, billing.ErrUpstreamChannelConflict
		}
		models, e := json.Marshal(c.Models)
		if e != nil {
			return item, e
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_channel_exclusions WHERE instance_id=? AND channel_id=?`, item.InstanceID, id); err != nil {
			return item, err
		}
		if from != 0 {
			// Transfer only ownership; old discounts and frozen bills remain with their original subject.
			if _, err = tx.ExecContext(ctx, `UPDATE billing_upstream_channel_bindings SET upstream_id=?,association_source='manual',matched_prefix='',associated_at=?,associated_by=? WHERE instance_id=? AND channel_id=? AND upstream_id=?`, item.ID, now, item.UpdatedBy, item.InstanceID, id, from); err != nil {
				return item, err
			}
			changedOthers[from] = true
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at,models_json,association_source,matched_prefix,associated_at,associated_by) VALUES(?,?,?,?,?,?,'manual','',?,?) ON DUPLICATE KEY UPDATE association_source='manual',matched_prefix='',associated_at=VALUES(associated_at),associated_by=VALUES(associated_by)`, item.InstanceID, item.ID, id, c.ChannelName, now, string(models), now, item.UpdatedBy)
			if err != nil {
				return item, err
			}
		}
	}
	if !isNew {
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET name=?,enabled=?,remark=?,updated_at=?,updated_by=?,revision=revision+1 WHERE instance_id=? AND id=?`, item.Name, item.Enabled, item.Remark, now, item.UpdatedBy, item.InstanceID, item.ID); err != nil {
			return item, err
		}
	}
	for id := range changedOthers {
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET revision=revision+1,updated_at=?,updated_by=? WHERE instance_id=? AND id=?`, now, item.UpdatedBy, item.InstanceID, id); err != nil {
			return item, err
		}
	}
	saved, err := readBillingUpstreams(ctx, tx, item.InstanceID)
	if err != nil {
		return item, err
	}
	for _, v := range saved {
		if v.ID == item.ID {
			item = v
			break
		}
	}
	return item, tx.Commit()
}

func (s Store) BillingUpstreamChannelExclusions(ctx context.Context, site string) ([]billing.ConfiguredChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id,channel_name FROM billing_upstream_channel_exclusions WHERE instance_id=? ORDER BY channel_id`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.ConfiguredChannel{}
	for rows.Next() {
		var c billing.ConfiguredChannel
		if err = rows.Scan(&c.ChannelID, &c.ChannelName); err != nil {
			return nil, err
		}
		c.AutoExcluded = true
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s Store) BillingUpstreamSyncedAt(ctx context.Context, site string) (*time.Time, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx, `SELECT synced_at FROM billing_upstream_sync_state WHERE instance_id=?`, site).Scan(&at)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &at, err
}

func (s Store) DeleteBillingUpstream(ctx context.Context, site string, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockUpstreamParents(ctx, tx, site); err != nil {
		return err
	}
	var uses int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=?)+(SELECT COUNT(*) FROM billing_statement_jobs j JOIN billing_jobs b ON b.id=j.job_id WHERE b.instance_id=? AND j.statement_type='upstream_statement' AND j.subject_id=?)+(SELECT COUNT(*) FROM billing_discount_rules WHERE instance_id=? AND discount_type='upstream_channel' AND subject_id=?)+(SELECT COUNT(*) FROM billing_generation_tasks WHERE instance_id=? AND kind='upstream_statement' AND JSON_CONTAINS(subject_ids_json,CAST(? AS JSON)))`, site, id, site, id, site, id, site, id).Scan(&uses); err != nil {
		return err
	}
	if uses > 0 {
		return billing.ErrUpstreamInUse
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM billing_upstreams WHERE instance_id=? AND id=?`, site, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}
