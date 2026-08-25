package mysqlstore

import (
	"context"
	"database/sql"
	"time"

	"controltower/server/internal/billing"
)

func (s Store) ListBillingUpstreams(ctx context.Context, site string) ([]billing.Upstream, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,instance_id,name,enabled,remark,created_at,updated_at,updated_by FROM billing_upstreams WHERE instance_id=? ORDER BY name,id`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.Upstream{}
	byID := map[int64]int{}
	for rows.Next() {
		var item billing.Upstream
		if err = rows.Scan(&item.ID, &item.InstanceID, &item.Name, &item.Enabled, &item.Remark, &item.CreatedAt, &item.UpdatedAt, &item.UpdatedBy); err != nil {
			return nil, err
		}
		items = append(items, item)
		byID[item.ID] = len(items) - 1
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	bindings, err := s.db.QueryContext(ctx, `SELECT b.upstream_id,b.channel_id,b.channel_name FROM billing_upstream_channel_bindings b JOIN billing_upstreams u ON u.id=b.upstream_id AND u.instance_id=b.instance_id WHERE b.instance_id=? ORDER BY b.upstream_id,b.channel_id`, site)
	if err != nil {
		return nil, err
	}
	defer bindings.Close()
	for bindings.Next() {
		var upstreamID int64
		var channel billing.UpstreamChannel
		if err = bindings.Scan(&upstreamID, &channel.ChannelID, &channel.ChannelName); err != nil {
			return nil, err
		}
		if index, ok := byID[upstreamID]; ok {
			items[index].Channels = append(items[index].Channels, channel)
		}
	}
	return items, bindings.Err()
}

func (s Store) PutBillingUpstream(ctx context.Context, item billing.Upstream) (billing.Upstream, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if item.ID == 0 {
		result, e := tx.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,enabled,remark,created_at,updated_at,updated_by) VALUES(?,?,?,?,?,?,?)`, item.InstanceID, item.Name, item.Enabled, item.Remark, now, now, item.UpdatedBy)
		if e != nil {
			return item, e
		}
		item.ID, err = result.LastInsertId()
		if err != nil {
			return item, err
		}
		item.CreatedAt = now
	} else {
		result, e := tx.ExecContext(ctx, `UPDATE billing_upstreams SET name=?,enabled=?,remark=?,updated_at=?,updated_by=? WHERE id=? AND instance_id=?`, item.Name, item.Enabled, item.Remark, now, item.UpdatedBy, item.ID, item.InstanceID)
		if e != nil {
			return item, e
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return item, sql.ErrNoRows
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM billing_upstream_channel_bindings WHERE instance_id=? AND upstream_id=?`, item.InstanceID, item.ID); e != nil {
			return item, e
		}
	}
	for _, channel := range item.Channels {
		if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at) VALUES(?,?,?,?,?)`, item.InstanceID, item.ID, channel.ChannelID, channel.ChannelName, now); err != nil {
			return item, err
		}
	}
	item.UpdatedAt = now
	return item, tx.Commit()
}

func (s Store) DeleteBillingUpstream(ctx context.Context, site string, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM billing_upstreams WHERE id=? AND instance_id=?`, id, site)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
