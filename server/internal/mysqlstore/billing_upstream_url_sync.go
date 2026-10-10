package mysqlstore

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"sync"
	"time"

	"controltower/server/internal/billing"
)

type billingUpstreamChannelSource interface {
	CurrentChannels(context.Context, string) ([]billing.ConfiguredChannel, error)
}

func (s Store) WithBillingUpstreamSource(source billingUpstreamChannelSource) Store {
	s.upstreamSource = source
	s.upstreamRefresh = &sync.Map{}
	return s
}

type upstreamRefreshState struct {
	mu sync.Mutex
	at time.Time
}

func (s Store) refreshBillingUpstreamChannels(ctx context.Context, site string) error {
	if s.upstreamSource == nil {
		return nil
	}
	var state *upstreamRefreshState
	if s.upstreamRefresh != nil {
		value, _ := s.upstreamRefresh.LoadOrStore(site, &upstreamRefreshState{})
		state = value.(*upstreamRefreshState)
		state.mu.Lock()
		defer state.mu.Unlock()
		if time.Since(state.at) < 30*time.Second {
			return nil
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	channels, err := s.upstreamSource.CurrentChannels(queryCtx, site)
	if err != nil {
		return err
	}
	if err = s.SyncBillingUpstreamChannels(ctx, site, channels); err != nil {
		return err
	}
	if state != nil {
		state.at = time.Now()
	}
	return nil
}
func (s Store) SyncBillingUpstreamChannels(ctx context.Context, site string, channels []billing.ConfiguredChannel) error {
	return s.SyncBillingUpstreamChannelsWithRestore(ctx, site, channels, nil, "auto-channel")
}

// Discovery preserves manual/legacy ownership. Prefix transfers change rules for
// future channels; moving an existing channel requires an explicit owner check.
func (s Store) SyncBillingUpstreamChannelsWithRestore(ctx context.Context, site string, channels []billing.ConfiguredChannel, restore []int64, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockUpstreamParents(ctx, tx, site); err != nil {
		return err
	}
	ups, err := readBillingUpstreams(ctx, tx, site)
	if err != nil {
		return err
	}
	prefixes := map[string]int64{}
	reviewPrefixes := map[string]bool{}
	owners := map[int64]int64{}
	names := map[int64]string{}
	knownURLs := map[int64]map[string]bool{}
	for _, up := range ups {
		knownURLs[up.ID] = map[string]bool{}
		for _, u := range up.URLs {
			knownURLs[up.ID][u] = true
		}
		for _, p := range up.ReviewPrefixes {
			reviewPrefixes[p] = true
		}
		for _, p := range up.ChannelPrefixes {
			prefixes[p] = up.ID
		}
		for _, c := range up.Channels {
			owners[c.ChannelID] = up.ID
			names[c.ChannelID] = c.ChannelName
		}
	}
	directory := map[int64]bool{}
	for _, c := range channels {
		directory[c.ChannelID] = true
	}
	for _, id := range restore {
		if id <= 0 || !directory[id] {
			return billing.ErrUpstreamChannelConflict
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM billing_upstream_channel_exclusions WHERE instance_id=? AND channel_id=?`, site, id); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT channel_id FROM billing_upstream_channel_exclusions WHERE instance_id=?`, site)
	if err != nil {
		return err
	}
	blocked := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		blocked[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	ordered := append([]billing.ConfiguredChannel(nil), channels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ChannelID < ordered[j].ChannelID })
	changed := map[int64]bool{}
	created := map[int64]bool{}
	now := time.Now().UTC()
	for _, c := range ordered {
		if c.ChannelID <= 0 || blocked[c.ChannelID] {
			continue
		}
		owner := owners[c.ChannelID]
		matched := ""
		if owner == 0 {
			owner, matched = billing.ChannelPrefix(c.ChannelName, prefixes)
			if reviewPrefixes[matched] {
				continue
			} // Block pending rules instead of falling back to a shorter prefix or inventing a duplicate upstream.
			if owner == 0 {
				p, _, _ := strings.Cut(strings.TrimSpace(c.ChannelName), "_")
				p = strings.TrimSpace(p)
				if p == "" || len([]rune(p)) > 128 {
					continue
				}
				// Display-name uniqueness uses a different collation from literal prefixes.
				// A collision is not permission to add a rule to an existing upstream.
				var existingID int64
				err = tx.QueryRowContext(ctx, `SELECT id FROM billing_upstreams WHERE instance_id=? AND name=?`, site, p).Scan(&existingID)
				if err == nil {
					continue
				}
				if err != sql.ErrNoRows {
					return err
				}
				result, e := tx.ExecContext(ctx, `INSERT INTO billing_upstreams(instance_id,name,enabled,remark,created_at,updated_at,updated_by,revision) VALUES(?,?,0,'',?,?,?,1)`, site, p, now, now, actor)
				if e != nil {
					return e
				}
				owner, err = result.LastInsertId()
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_prefixes(instance_id,prefix,upstream_id,needs_review) VALUES(?,?,?,0)`, site, p, owner); err != nil {
					return err
				}
				prefixes[p] = owner
				matched = p
				created[owner] = true
				knownURLs[owner] = map[string]bool{}
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at,models_json,association_source,matched_prefix,associated_at,associated_by) VALUES(?,?,?,?,?,'null','auto',?,?,?)`, site, owner, c.ChannelID, c.ChannelName, now, matched, now, actor); err != nil {
				return err
			}
			changed[owner] = true
			owners[c.ChannelID] = owner
			names[c.ChannelID] = c.ChannelName
		} else if names[c.ChannelID] != c.ChannelName {
			if _, err = tx.ExecContext(ctx, `UPDATE billing_upstream_channel_bindings SET channel_name=? WHERE instance_id=? AND channel_id=?`, c.ChannelName, site, c.ChannelID); err != nil {
				return err
			}
			changed[owner] = true
		}
		if endpoint, e := billing.NormalizeUpstreamURL(c.BaseURL); e == nil && !knownURLs[owner][endpoint] {
			added, e := appendUpstreamURL(ctx, tx, site, owner, endpoint, now)
			if e != nil {
				return e
			}
			if added {
				changed[owner] = true
			}
			knownURLs[owner][endpoint] = true
		}
	}
	for id := range changed {
		if created[id] {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE billing_upstreams SET revision=revision+1,updated_at=?,updated_by=? WHERE instance_id=? AND id=?`, now, actor, site, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO billing_upstream_sync_state(instance_id,synced_at) VALUES(?,?) ON DUPLICATE KEY UPDATE synced_at=VALUES(synced_at)`, site, now); err != nil {
		return err
	}
	return tx.Commit()
}

// Retained for callers that already loaded the source directory explicitly.
func (s Store) ListBillingUpstreamsWithChannels(ctx context.Context, site string, channels []billing.ConfiguredChannel) ([]billing.Upstream, error) {
	if err := s.SyncBillingUpstreamChannels(ctx, site, channels); err != nil {
		return nil, err
	}
	return s.listBillingUpstreams(ctx, site)
}
