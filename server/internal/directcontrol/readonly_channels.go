package directcontrol

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/secrets"
	"github.com/go-sql-driver/mysql"
)

const readonlyChannelLimit = 5000

func (s Store) refreshReadonlyChannels(ctx context.Context, site, encrypted string) error {
	dsn, err := secrets.Decrypt(s.secretKey, encrypted)
	if err != nil {
		return fmt.Errorf("cannot decrypt readonly channel configuration")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("invalid readonly channel configuration")
	}
	cfg.MultiStatements = false
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return fmt.Errorf("cannot open readonly channel connection")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	at := time.Now().UTC()
	channels, err := readReadonlyChannels(queryCtx, db)
	if err != nil {
		return fmt.Errorf("readonly channel query failed: %w", err)
	}
	return s.StoreReadonlyChannels(site, channels, at, encrypted)
}

func readReadonlyChannels(ctx context.Context, db *sql.DB) ([]channelcontrol.Channel, error) {
	// Explicit public metadata only; never fetch keys or other channel secrets.
	rows, err := db.QueryContext(ctx, "SELECT id,COALESCE(name,''),COALESCE(status,1),COALESCE(weight,0),COALESCE(models,''),COALESCE(`group`,''),COALESCE(priority,0) FROM channels ORDER BY id LIMIT ?", readonlyChannelLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]channelcontrol.Channel, 0)
	for rows.Next() {
		var v channelcontrol.Channel
		if err = rows.Scan(&v.ID, &v.Name, &v.Status, &v.Weight, &v.Models, &v.Group, &v.Priority); err != nil {
			return nil, err
		}
		if v.ID <= 0 || v.Priority < 0 {
			return nil, fmt.Errorf("invalid channel metadata")
		}
		items = append(items, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) > readonlyChannelLimit {
		return nil, fmt.Errorf("readonly channel list exceeds %d; keeping previous snapshot", readonlyChannelLimit)
	}
	return items, nil
}

// A single bounded worker refreshes each configured site once per minute.
// Configuration is resolved each pass and checked again when persisting.
func (s Store) RunReadonlyChannelSync(ctx context.Context) {
	for {
		s.syncReadonlyChannelSites(ctx)
		timer := time.NewTimer(time.Minute)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s Store) syncReadonlyChannelSites(ctx context.Context) {
	sites, err := s.ListEnabledSites()
	if err != nil {
		log.Printf("readonly channel sync cannot list sites")
		return
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return
		}
		encrypted, err := s.ReadonlyDSNForSite(site)
		if err == nil && encrypted != "" {
			err = s.refreshReadonlyChannels(ctx, site, encrypted)
		}
		if err != nil {
			log.Printf("readonly channel sync site=%s failed; preserving previous snapshot: %v", site, err)
		}
	}
}
