package channelcollector

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Snapshot struct {
	ChannelID   int64
	ChannelName string
	Status      string
	Weight      int64
	ModelsText  string
	GroupName   string
	Priority    int64
	CapturedAt  time.Time
}

type MySQLCollector struct {
	db            *sql.DB
	interval      time.Duration
	mu            sync.Mutex
	lastCheckedAt time.Time
	lastHash      string
}

func NewMySQLCollector(db *sql.DB) *MySQLCollector {
	return NewMySQLCollectorWithInterval(db, 10*time.Minute)
}

func NewMySQLCollectorWithInterval(db *sql.DB, interval time.Duration) *MySQLCollector {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	return &MySQLCollector{db: db, interval: interval}
}

func (c *MySQLCollector) Collect(ctx context.Context, limit int) ([]Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	if !c.lastCheckedAt.IsZero() && now.Sub(c.lastCheckedAt) < c.interval {
		return nil, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	rows, err := c.db.QueryContext(ctx, collectChannelsSQL(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	capturedAt := now
	items := make([]Snapshot, 0)
	for rows.Next() {
		var item Snapshot
		if err := rows.Scan(&item.ChannelID, &item.ChannelName, &item.Status, &item.Weight, &item.ModelsText, &item.GroupName, &item.Priority); err != nil {
			return nil, err
		}
		item.Status = normalizeStatus(item.Status)
		item.ModelsText = strings.TrimSpace(item.ModelsText)
		item.CapturedAt = capturedAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	c.lastCheckedAt = now
	hash := snapshotHash(items)
	if hash == c.lastHash {
		return nil, nil
	}
	c.lastHash = hash
	return items, nil
}

func snapshotHash(items []Snapshot) string {
	hasher := sha256.New()
	for _, item := range items {
		_, _ = hasher.Write([]byte(strconv.FormatInt(item.ChannelID, 10)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(item.ChannelName))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(item.Status))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(strconv.FormatInt(item.Weight, 10)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(item.ModelsText))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(item.GroupName))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(strconv.FormatInt(item.Priority, 10)))
		_, _ = hasher.Write([]byte{0xff})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// ChannelState carries the fields the alert notifier needs per channel.
type ChannelState struct {
	Name     string
	Disabled bool
}

// FetchStates returns per-channel name and enabled/disabled state for alert
// labeling and suppression. new-api status 1 means enabled; anything else
// (2 manual disabled, 3 auto disabled) counts as disabled.
func FetchStates(ctx context.Context, db *sql.DB) (map[int64]ChannelState, error) {
	rows, err := db.QueryContext(ctx, "SELECT id, COALESCE(name, ''), COALESCE(status, 1) FROM channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[int64]ChannelState)
	for rows.Next() {
		var id int64
		var name string
		var status int
		if err := rows.Scan(&id, &name, &status); err != nil {
			return nil, err
		}
		states[id] = ChannelState{Name: strings.TrimSpace(name), Disabled: status != 1}
	}
	return states, rows.Err()
}

// FetchNames returns the channel id to name mapping for alert messages.
func FetchNames(ctx context.Context, db *sql.DB) (map[int64]string, error) {
	states, err := FetchStates(ctx, db)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(states))
	for id, state := range states {
		names[id] = state.Name
	}
	return names, nil
}

func collectChannelsSQL() string {
	return "SELECT id, COALESCE(name, ''), CAST(status AS CHAR), COALESCE(weight, 0), COALESCE(models, ''), COALESCE(`group`, ''), COALESCE(priority, 0)\n" +
		`FROM channels
ORDER BY id ASC
LIMIT ?`
}

func normalizeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "enabled", "enable", "active":
		return "enabled"
	case "2", "disabled", "disable", "manual_disabled":
		return "disabled"
	case "3", "auto_disabled":
		return "auto_disabled"
	case "":
		return "unknown"
	default:
		return strings.TrimSpace(value)
	}
}
