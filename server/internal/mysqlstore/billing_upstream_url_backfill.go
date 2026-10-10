package mysqlstore

import (
	"context"
	"controltower/server/internal/billing"
	"sort"
	"time"
)

type UpstreamURLBackfill struct {
	UpstreamID      int64    `json:"upstream_id"`
	Name            string   `json:"name"`
	Channels        int      `json:"channels"`
	MissingChannels int      `json:"missing_channels"`
	InvalidURLs     int      `json:"invalid_urls"`
	URLs            []string `json:"urls"`
	Added           []string `json:"added"`
	Conflicts       []string `json:"conflicts"`
}

// URL records describe endpoints used by each upstream, including shared gateways.
func (s Store) BackfillBillingUpstreamURLs(ctx context.Context, site string, channels []billing.ConfiguredChannel, apply bool) ([]UpstreamURLBackfill, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockUpstreamParents(ctx, tx, site); err != nil {
		return nil, err
	}
	ups, err := readBillingUpstreams(ctx, tx, site)
	if err != nil {
		return nil, err
	}
	directory := map[int64]string{}
	for _, c := range channels {
		directory[c.ChannelID] = c.BaseURL
	}
	reports := []UpstreamURLBackfill{}
	now := time.Now().UTC()
	for _, up := range ups {
		r := UpstreamURLBackfill{UpstreamID: up.ID, Name: up.Name, Channels: len(up.Channels), URLs: []string{}, Added: []string{}, Conflicts: []string{}}
		known := map[string]bool{}
		for _, u := range up.URLs {
			known[u] = true
		}
		candidates := map[string]bool{}
		for _, c := range up.Channels {
			raw, ok := directory[c.ChannelID]
			if !ok {
				r.MissingChannels++
				continue
			}
			url, e := billing.NormalizeUpstreamURL(raw)
			if e != nil {
				r.InvalidURLs++
				continue
			}
			candidates[url] = true
		}
		for u := range candidates {
			r.URLs = append(r.URLs, u)
		}
		sort.Strings(r.URLs)
		for _, u := range r.URLs {
			if known[u] {
				continue
			}
			r.Added = append(r.Added, u)
			if apply {
				if _, err = appendUpstreamURL(ctx, tx, site, up.ID, u, now); err != nil {
					return nil, err
				}
			}
		}
		if apply && len(r.Added) > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET revision=revision+1,updated_at=?,updated_by='url-backfill' WHERE instance_id=? AND id=?`, now, site, up.ID); err != nil {
				return nil, err
			}
		}
		reports = append(reports, r)
	}
	if !apply {
		return reports, nil
	}
	return reports, tx.Commit()
}
