package mysqlstore

import (
	"context"
	"database/sql"
	"log"
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
	return s.refreshUpstreamChannels(ctx, site, false)
}
func (s Store) refreshUpstreamChannels(ctx context.Context, site string, force bool) error {
	if s.upstreamSource == nil {
		return nil
	}
	var state *upstreamRefreshState
	if s.upstreamRefresh != nil {
		value, _ := s.upstreamRefresh.LoadOrStore(site, &upstreamRefreshState{})
		state = value.(*upstreamRefreshState)
		state.mu.Lock()
		defer state.mu.Unlock()
		if !force && time.Since(state.at) < 30*time.Second {
			return nil
		}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	channels, err := s.upstreamSource.CurrentChannels(queryCtx, site)
	if err != nil {
		return err
	}
	if err = s.SyncBillingUpstreamChannels(queryCtx, site, channels); err != nil {
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
	return retryBillingDeadlock(ctx, func() error { return s.syncBillingUpstreamChannelsAttempt(ctx, site, channels, restore, actor) })
}
func (s Store) syncBillingUpstreamChannelsAttempt(ctx context.Context, site string, channels []billing.ConfiguredChannel, restore []int64, actor string) error {
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
	// Seed URLs from every existing binding before considering new channels.
	// Newly learned URLs from new channels are persisted, but don't bias this batch by ID order.
	for _, c := range ordered {
		owner := owners[c.ChannelID]
		endpoint, e := billing.NormalizeUpstreamURL(c.BaseURL)
		if owner == 0 || e != nil || knownURLs[owner][endpoint] {
			continue
		}
		if _, e = appendUpstreamURL(ctx, tx, site, owner, endpoint, now); e != nil {
			return e
		}
		knownURLs[owner][endpoint] = true
		changed[owner] = true
	}
	urlOwners := map[string]map[int64]bool{}
	for id, urls := range knownURLs {
		for u := range urls {
			if urlOwners[u] == nil {
				urlOwners[u] = map[int64]bool{}
			}
			urlOwners[u][id] = true
		}
	}
	for _, c := range ordered {
		if c.ChannelID <= 0 || blocked[c.ChannelID] {
			continue
		}
		owner := owners[c.ChannelID]
		matched := ""
		if owner == 0 {
			endpoint, _ := billing.NormalizeUpstreamURL(c.BaseURL)
			var ambiguous bool
			owner, matched, ambiguous = billing.MatchUpstream(c.ChannelName, urlOwners[endpoint], prefixes, reviewPrefixes)
			if ambiguous {
				continue
			}
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

// RunBillingUpstreamSync discovers channels even when no page or billing job is opened.
func (s Store) RunBillingUpstreamSync(ctx context.Context) {
	s.runBillingUpstreamSync(ctx, 30*time.Second)
}
func (s Store) runBillingUpstreamSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		sites, err := s.ListBillingSites(ctx)
		if err != nil && ctx.Err() == nil {
			log.Print("upstream sync: site directory unavailable")
		}
		var wg sync.WaitGroup
		limit := make(chan struct{}, 4)
		for _, site := range sites {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case limit <- struct{}{}:
			}
			wg.Add(1)
			go func(site string) {
				defer wg.Done()
				defer func() { <-limit }()
				if err := s.refreshUpstreamChannels(ctx, site, true); err != nil && ctx.Err() == nil {
					log.Printf("upstream sync failed for site %s; retained saved configuration", site)
				}
			}(site)
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
