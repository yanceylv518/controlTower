package billing

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type LogRecord struct {
	UserID           int64
	Username         string
	ModelName        string
	GroupName        string
	PromptTokens     int64
	CompletionTokens int64
	CacheTokens      int64
	Quota            int64
}

type DetailedLogRecord struct {
	ID               int64
	CreatedAt        time.Time
	RequestID        string
	UserID           int64
	Username         string
	ModelName        string
	GroupName        string
	PromptTokens     int64
	CompletionTokens int64
	CacheTokens      int64
	Quota            int64
}

type Source interface {
	Logs(ctx context.Context, instanceID string, start, end time.Time) ([]LogRecord, error)
	RatioSnapshot(ctx context.Context, instanceID string) (string, error)
	Balances(ctx context.Context, instanceID string) (map[int64]int64, error)
}

type RollupStore interface {
	ListBillingPrices(ctx context.Context, instanceID string) ([]PriceRecord, error)
	ReplaceBillingDay(ctx context.Context, instanceID string, day time.Time, rows []DailyRow) error
	PutBillingRatioSnapshot(ctx context.Context, instanceID string, day time.Time, raw string) error
	PutBillingBalanceSnapshots(ctx context.Context, instanceID string, day time.Time, balances map[int64]int64) error
}

type Sleeper func(context.Context, time.Duration) error

type RollupService struct {
	Source       Source
	Store        RollupStore
	SegmentPause time.Duration
	DayPause     time.Duration
	Sleep        Sleeper
	Now          func() time.Time
}

type RollupResult struct {
	InstanceID string
	Day        time.Time
	Rows       int
}

func (s RollupService) RollupDay(ctx context.Context, instanceID string, day time.Time) (RollupResult, error) {
	result := RollupResult{InstanceID: instanceID, Day: dateOnly(day)}
	if s.Source == nil || s.Store == nil {
		return result, fmt.Errorf("billing rollup dependencies are not configured")
	}
	prices, err := s.Store.ListBillingPrices(ctx, instanceID)
	if err != nil {
		return result, fmt.Errorf("list prices: %w", err)
	}
	byModel := make(map[string][]Price)
	for _, price := range prices {
		byModel[price.ModelName] = append(byModel[price.ModelName], price.Price)
	}
	type key struct {
		userID int64
		model  string
		group  string
		tier   int64
	}
	aggregated := make(map[key]DailyRow)
	startOfDay := dateOnly(day)
	for hour := 0; hour < 24; hour++ {
		start := startOfDay.Add(time.Duration(hour) * time.Hour)
		end := start.Add(time.Hour)
		logs, queryErr := s.Source.Logs(ctx, instanceID, start, end)
		if queryErr != nil {
			return result, fmt.Errorf("query hour %02d: %w", hour, queryErr)
		}
		for _, log := range logs {
			tier := int64(0)
			if price, ok := SelectPrice(byModel[log.ModelName], startOfDay, log.PromptTokens); ok {
				tier = price.TierFrom
			}
			k := key{userID: log.UserID, model: log.ModelName, group: log.GroupName, tier: tier}
			row := aggregated[k]
			row.InstanceID, row.UserID, row.Username = instanceID, log.UserID, log.Username
			row.ModelName, row.GroupName, row.TierFrom, row.Day = log.ModelName, log.GroupName, tier, startOfDay
			row.RequestCount++
			row.PromptTokens += log.PromptTokens
			row.CompletionTokens += log.CompletionTokens
			row.CacheTokens += log.CacheTokens
			row.Quota += log.Quota
			row.UpdatedAt = s.now().UTC()
			aggregated[k] = row
		}
		if hour < 23 {
			if err = s.sleep(ctx, s.segmentPause()); err != nil {
				return result, err
			}
		}
	}
	rows := make([]DailyRow, 0, len(aggregated))
	for _, row := range aggregated {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].UserID != rows[j].UserID {
			return rows[i].UserID < rows[j].UserID
		}
		if rows[i].ModelName != rows[j].ModelName {
			return rows[i].ModelName < rows[j].ModelName
		}
		if rows[i].GroupName != rows[j].GroupName {
			return rows[i].GroupName < rows[j].GroupName
		}
		return rows[i].TierFrom < rows[j].TierFrom
	})
	if err = s.Store.ReplaceBillingDay(ctx, instanceID, startOfDay, rows); err != nil {
		return result, fmt.Errorf("replace day: %w", err)
	}
	raw, err := s.Source.RatioSnapshot(ctx, instanceID)
	if err != nil {
		return result, fmt.Errorf("ratio snapshot: %w", err)
	}
	if err = s.Store.PutBillingRatioSnapshot(ctx, instanceID, startOfDay, raw); err != nil {
		return result, fmt.Errorf("save ratio snapshot: %w", err)
	}
	balances, err := s.Source.Balances(ctx, instanceID)
	if err != nil {
		return result, fmt.Errorf("balance snapshot: %w", err)
	}
	if err = s.Store.PutBillingBalanceSnapshots(ctx, instanceID, startOfDay, balances); err != nil {
		return result, fmt.Errorf("save balance snapshot: %w", err)
	}
	MonthlySummaryCache.InvalidateInstance(instanceID)
	result.Rows = len(rows)
	return result, nil
}

// RollupSites isolates failures: one unavailable site's read-only database
// never prevents the remaining sites from completing their daily bill.
func (s RollupService) RollupSites(ctx context.Context, instanceIDs []string, day time.Time) map[string]error {
	errorsBySite := make(map[string]error)
	for i, instanceID := range instanceIDs {
		_, err := s.RollupDay(ctx, instanceID, day)
		if err != nil {
			errorsBySite[instanceID] = err
		}
		if i < len(instanceIDs)-1 {
			_ = s.sleep(ctx, s.dayPause())
		}
	}
	return errorsBySite
}

func (s RollupService) segmentPause() time.Duration {
	if s.SegmentPause > 0 {
		return s.SegmentPause
	}
	return 200 * time.Millisecond
}
func (s RollupService) dayPause() time.Duration {
	if s.DayPause > 0 {
		return s.DayPause
	}
	return 500 * time.Millisecond
}
func (s RollupService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s RollupService) sleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	if s.Sleep != nil {
		return s.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}
