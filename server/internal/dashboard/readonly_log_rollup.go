package dashboard

import (
	"context"
	"crypto/sha256"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"controltower/server/internal/storage"
)

const (
	readonlyLogRollupBatchSize = 5000
	readonlyLogRollupBackfill  = 7 * 24 * time.Hour
	// Rows younger than this lag are left for the next pass. Auto-increment
	// ids become visible out of commit order, so chasing the live head can
	// advance the cursor past a not-yet-committed lower id and lose that row
	// forever. Bounding each pass to a settled head id closes that gap for
	// any insert that commits within the lag.
	readonlyLogRollupSafetyLag = 10 * time.Second
)

type ReadonlyLogRollupStore interface {
	ListInstances() ([]storage.Instance, error)
	ReadonlyLogRollupCursor(context.Context, string) (storage.ReadonlyLogRollupCursor, error)
	InitializeReadonlyLogRollupCursor(context.Context, string, int64, time.Time, time.Time) error
	ApplyReadonlyLogRollups(context.Context, string, int64, []storage.ReadonlyLogRollup, time.Time) error
	MarkReadonlyLogRollupCaughtUp(context.Context, string, time.Time) error
	RecordReadonlyLogRollupError(context.Context, string, string, time.Time) error
	QueryReadonlyLogRollup(context.Context, storage.ReadonlyLogRollupFilter) (storage.ReadonlyLogRollupSummary, error)
}

type readonlySourceLog struct {
	ID               int64
	CreatedAt        int64
	LogType          int
	UserID           int64
	Username         string
	ChannelID        int64
	ModelName        string
	TokenName        string
	GroupName        string
	PromptTokens     int64
	CompletionTokens int64
	Quota            int64
}

// readonlyLogLastIDBefore returns the highest log id whose created_at is
// strictly before cutoff (0 when none). Used both for the initial cursor and
// as the settled-head bound of each sync pass.
func (h *PassthroughHandler) readonlyLogLastIDBefore(ctx context.Context, site string, cutoff time.Time) (int64, error) {
	db, configured, err := h.database(site)
	if err != nil || !configured {
		return 0, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, readonlyLogQueryTimeout)
	defer cancel()
	var id int64
	err = db.QueryRowContext(queryCtx, `SELECT COALESCE((SELECT id FROM logs WHERE created_at<? ORDER BY created_at DESC,id DESC LIMIT 1),0)`, cutoff.Unix()).Scan(&id)
	return id, err
}

func (h *PassthroughHandler) readonlyLogBatch(ctx context.Context, site string, afterID, maxID int64, limit int) ([]readonlySourceLog, error) {
	db, configured, err := h.database(site)
	if err != nil || !configured {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, readonlyLogQueryTimeout)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT id,created_at,type,user_id,COALESCE(username,''),COALESCE(channel_id,0),COALESCE(model_name,''),COALESCE(token_name,''),COALESCE(`+"`group`"+`,''),prompt_tokens,completion_tokens,quota FROM logs WHERE id>? AND id<=? ORDER BY id LIMIT ?`, afterID, maxID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]readonlySourceLog, 0, limit)
	for rows.Next() {
		var item readonlySourceLog
		if err = rows.Scan(&item.ID, &item.CreatedAt, &item.LogType, &item.UserID, &item.Username, &item.ChannelID, &item.ModelName, &item.TokenName, &item.GroupName, &item.PromptTokens, &item.CompletionTokens, &item.Quota); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type readonlyLogSource interface {
	readonlyLogLastIDBefore(ctx context.Context, site string, cutoff time.Time) (int64, error)
	readonlyLogBatch(ctx context.Context, site string, afterID, maxID int64, limit int) ([]readonlySourceLog, error)
}

type ReadonlyLogRollupRunner struct {
	Source   readonlyLogSource
	Store    ReadonlyLogRollupStore
	Interval time.Duration
}

func (r ReadonlyLogRollupRunner) Run(ctx context.Context) error {
	if r.Source == nil || r.Store == nil {
		return errors.New("readonly log rollup runner is not configured")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for {
		r.runOnce(ctx)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r ReadonlyLogRollupRunner) runOnce(ctx context.Context) {
	instances, err := r.Store.ListInstances()
	if err != nil {
		log.Printf("readonly log rollup instances failed: %v", err)
		return
	}
	sites := map[string]bool{}
	for _, instance := range instances {
		if instance.Enabled && instance.LogsReadonlyDSN != "" {
			sites[siteOf(instance)] = true
		}
	}
	for site := range sites {
		if ctx.Err() != nil {
			return
		}
		if err = r.syncSite(ctx, site); err != nil {
			message := err.Error()
			if len(message) > 1000 {
				message = message[:1000]
			}
			_ = r.Store.RecordReadonlyLogRollupError(ctx, site, message, time.Now().UTC())
			log.Printf("readonly log rollup site=%s failed: %v", site, err)
		}
	}
}

func (r ReadonlyLogRollupRunner) syncSite(ctx context.Context, site string) error {
	cursor, err := r.Store.ReadonlyLogRollupCursor(ctx, site)
	if err != nil {
		return err
	}
	if !cursor.Initialized {
		coverageFrom := time.Now().UTC().Add(-readonlyLogRollupBackfill)
		initial, initErr := r.Source.readonlyLogLastIDBefore(ctx, site, coverageFrom)
		if initErr != nil {
			return initErr
		}
		if err = r.Store.InitializeReadonlyLogRollupCursor(ctx, site, initial, coverageFrom, time.Now().UTC()); err != nil {
			return err
		}
		cursor.LastLogID = initial
	}
	safeHead, err := r.Source.readonlyLogLastIDBefore(ctx, site, time.Now().Add(-readonlyLogRollupSafetyLag))
	if err != nil {
		return err
	}
	if safeHead <= cursor.LastLogID {
		return r.Store.MarkReadonlyLogRollupCaughtUp(ctx, site, time.Now().UTC())
	}
	// Bound each pass so a historical backfill cannot monopolize the readonly
	// pool. Subsequent passes continue from the durable source-id cursor.
	for batch := 0; batch < 10; batch++ {
		items, batchErr := r.Source.readonlyLogBatch(ctx, site, cursor.LastLogID, safeHead, readonlyLogRollupBatchSize)
		if batchErr != nil {
			return batchErr
		}
		if len(items) == 0 {
			return r.Store.MarkReadonlyLogRollupCaughtUp(ctx, site, time.Now().UTC())
		}
		rollups := aggregateReadonlyLogs(site, items)
		lastID := items[len(items)-1].ID
		if err = r.Store.ApplyReadonlyLogRollups(ctx, site, lastID, rollups, time.Now().UTC()); err != nil {
			return err
		}
		cursor.LastLogID = lastID
		if len(items) < readonlyLogRollupBatchSize {
			return r.Store.MarkReadonlyLogRollupCaughtUp(ctx, site, time.Now().UTC())
		}
	}
	return nil
}

func aggregateReadonlyLogs(site string, items []readonlySourceLog) []storage.ReadonlyLogRollup {
	values := map[[32]byte]*storage.ReadonlyLogRollup{}
	for _, item := range items {
		hour := time.Unix(item.CreatedAt, 0).UTC().Truncate(time.Hour)
		keyText := strings.Join([]string{site, hour.Format(time.RFC3339), strconv.Itoa(item.LogType), strconv.FormatInt(item.UserID, 10), item.Username, strconv.FormatInt(item.ChannelID, 10), item.ModelName, item.TokenName, item.GroupName}, "\x00")
		hash := sha256.Sum256([]byte(keyText))
		value := values[hash]
		if value == nil {
			value = &storage.ReadonlyLogRollup{DimensionHash: hash[:], SiteID: site, HourStart: hour, LogType: item.LogType, UserID: item.UserID, Username: item.Username, ChannelID: item.ChannelID, ModelName: item.ModelName, TokenName: item.TokenName, GroupName: item.GroupName}
			values[hash] = value
		}
		value.RequestCount++
		value.PromptTokens += item.PromptTokens
		value.CompletionTokens += item.CompletionTokens
		value.QuotaSum += item.Quota
	}
	result := make([]storage.ReadonlyLogRollup, 0, len(values))
	for _, value := range values {
		result = append(result, *value)
	}
	return result
}

func readonlyRollupFilter(site string, ids []int64, start, end time.Time, rQuery map[string]string, logType *int, channelID *int64) storage.ReadonlyLogRollupFilter {
	return storage.ReadonlyLogRollupFilter{SiteID: site, Start: start, End: end, LogType: logType, UserIDs: ids, Username: rQuery["username"], ChannelID: channelID, ModelName: rQuery["model_name"], TokenName: rQuery["token_name"], GroupName: rQuery["group"]}
}

func completeHourWindow(start, end time.Time) (time.Time, time.Time, bool) {
	first := start.UTC().Truncate(time.Hour)
	if !start.UTC().Equal(first) {
		first = first.Add(time.Hour)
	}
	last := end.UTC().Truncate(time.Hour)
	return first, last, first.Before(last)
}
