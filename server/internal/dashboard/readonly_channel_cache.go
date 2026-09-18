package dashboard

import (
	"context"
	"database/sql"
	"time"
)

type readonlyChannelKey struct {
	DB *sql.DB
	ID int64
}
type readonlyChannelName struct {
	Name    string
	Expires time.Time
}

// Cache only successful reads, including confirmed missing IDs. Connection
// identity prevents old names surviving a site's DSN replacement.
func (h *PassthroughHandler) hydrateCachedChannelNames(ctx context.Context, db *sql.DB, items []PassthroughLog) {
	now := time.Now()
	missing := []PassthroughLog{}
	seen := map[int64]bool{}
	h.mu.Lock()
	if h.channelNames == nil {
		h.channelNames = make(map[readonlyChannelKey]readonlyChannelName)
	}
	for key, value := range h.channelNames {
		if !now.Before(value.Expires) {
			delete(h.channelNames, key)
		}
	}
	for i := range items {
		key := readonlyChannelKey{db, items[i].ChannelID}
		if value, ok := h.channelNames[key]; ok {
			items[i].ChannelName = value.Name
		} else if key.ID > 0 && !seen[key.ID] {
			seen[key.ID] = true
			missing = append(missing, items[i])
		}
	}
	h.mu.Unlock()
	if len(missing) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return
	}
	defer tx.Rollback()
	if !hydrateReadonlyChannelNames(ctx, tx, missing) {
		return
	}
	names := map[int64]string{}
	h.mu.Lock()
	if len(h.channelNames)+len(missing) > 2048 {
		clear(h.channelNames)
	}
	for _, item := range missing {
		names[item.ChannelID] = item.ChannelName
		h.channelNames[readonlyChannelKey{db, item.ChannelID}] = readonlyChannelName{item.ChannelName, time.Now().Add(time.Minute)}
	}
	h.mu.Unlock()
	for i := range items {
		if name, ok := names[items[i].ChannelID]; ok {
			items[i].ChannelName = name
		}
	}
}
