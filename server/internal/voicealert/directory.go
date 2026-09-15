package voicealert

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type CustomerSource interface {
	CustomerNames(context.Context, string) (map[int64]string, error)
}
type SiteSource interface {
	SiteIDs(context.Context) ([]string, error)
}
type CustomerList struct {
	Customers        []Target `json:"customers"`
	UnavailableSites []string `json:"unavailable_sites"`
}

// Directory shares one bounded identity-only refresh between the settings page
// and the worker. Failed sites are omitted, never treated as zero traffic.
type Directory struct {
	Sites  SiteSource
	Source CustomerSource
	mu     sync.Mutex
	next   time.Time
	list   CustomerList
}

func (d *Directory) List(ctx context.Context) (CustomerList, error) {
	if d == nil || d.Sites == nil || d.Source == nil {
		return CustomerList{}, errors.New("customer directory unavailable")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if time.Now().Before(d.next) {
		return d.copy(), nil
	}
	sites, err := d.Sites.SiteIDs(ctx)
	if err != nil {
		return CustomerList{}, err
	}
	list := CustomerList{Customers: []Target{}, UnavailableSites: []string{}}
	for _, site := range sites {
		if err := ctx.Err(); err != nil {
			return CustomerList{}, err
		}
		names, err := d.Source.CustomerNames(ctx, site)
		if err != nil {
			list.UnavailableSites = append(list.UnavailableSites, site)
			continue
		}
		for id, name := range names {
			if id > 0 && id <= 9007199254740991 && strings.TrimSpace(name) != "" {
				list.Customers = append(list.Customers, Target{Site: site, UserID: id, Label: strings.TrimSpace(name)})
			}
		}
	}
	sort.Slice(list.Customers, func(i, j int) bool {
		a, b := list.Customers[i], list.Customers[j]
		if a.Site != b.Site {
			return a.Site < b.Site
		}
		return a.UserID < b.UserID
	})
	d.list = list
	d.next = time.Now().Add(time.Minute)
	return d.copy(), nil
}

func (d *Directory) copy() CustomerList {
	return CustomerList{Customers: append([]Target{}, d.list.Customers...), UnavailableSites: append([]string{}, d.list.UnavailableSites...)}
}

func (s Store) SiteIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT CASE WHEN site_id='' THEN id ELSE site_id END AS site FROM instances WHERE enabled=1 ORDER BY site`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sites := []string{}
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (s Store) Customers(ctx context.Context) (CustomerList, error) {
	return s.Directory.List(ctx)
}

func (s Store) Targets(ctx context.Context) ([]Target, error) {
	list, err := s.Customers(ctx)
	return list.Customers, err
}
