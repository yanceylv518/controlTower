package mysqlstore

import (
	"context"

	"controltower/server/internal/billing"
)

func (s Store) UpsertBillingUpstreamChannels(ctx context.Context, items []billing.UpstreamChannelMapping) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range items {
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_channels(instance_id,channel_id,upstream_fp,base_url,key_tail,channel_name,updated_at) VALUES(?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE upstream_fp=VALUES(upstream_fp),base_url=VALUES(base_url),key_tail=VALUES(key_tail),channel_name=VALUES(channel_name),updated_at=VALUES(updated_at)`, v.InstanceID, v.ChannelID, v.UpstreamFP, v.BaseURL, v.KeyTail, v.ChannelName, v.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Store) ListBillingUpstreamChannels(ctx context.Context, site string) ([]billing.UpstreamChannelMapping, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instance_id,channel_id,upstream_fp,base_url,key_tail,channel_name,updated_at FROM billing_upstream_channels WHERE instance_id=? ORDER BY upstream_fp,channel_id`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.UpstreamChannelMapping{}
	for rows.Next() {
		var v billing.UpstreamChannelMapping
		if err = rows.Scan(&v.InstanceID, &v.ChannelID, &v.UpstreamFP, &v.BaseURL, &v.KeyTail, &v.ChannelName, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
