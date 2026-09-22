package logarchive

import (
	"context"
	af "controltower/internal/archivecontract"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type rawInventoryMonth struct {
	Checked time.Time
	Days    map[string]af.RawDayCount
}
type rawInventoryCache struct{ Months map[string]*rawInventoryMonth }

// refreshRawInventory is serialized by the Agent poller. It reads at most one
// physical month per poll, uses a covering timestamp index and a server-side
// execution limit, and caches results for five minutes. It never reads source
// logs or derives totals from partially rebuilt statistics. Failed refreshes
// retain the previous count with its original observation time.
func (w *Worker) refreshRawInventory(ctx context.Context) error {
	if w.rawInventory.Months == nil {
		w.rawInventory.Months = map[string]*rawInventoryMonth{}
	}
	rows, err := w.target.QueryContext(ctx, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME REGEXP '^logs_[0-9]{6}$' ORDER BY TABLE_NAME`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, table)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	present := map[string]bool{}
	for _, table := range tables {
		present[table] = true
	}
	for table := range w.rawInventory.Months {
		if !present[table] {
			delete(w.rawInventory.Months, table)
		}
	}
	// Oldest observation first prevents a slow or broken month starving others.
	sort.SliceStable(tables, func(i, j int) bool {
		a, b := w.rawInventory.Months[tables[i]], w.rawInventory.Months[tables[j]]
		if a == nil {
			return b != nil
		}
		if b == nil {
			return false
		}
		return a.Checked.Before(b.Checked)
	})
	now := time.Now().UTC()
	for _, table := range tables {
		month, e := time.Parse("200601", strings.TrimPrefix(table, "logs_"))
		if e != nil {
			continue
		}
		cache := w.rawInventory.Months[table]
		if cache != nil && now.Sub(cache.Checked) < 5*time.Minute {
			continue
		}
		if cache == nil {
			cache = &rawInventoryMonth{Days: map[string]af.RawDayCount{}}
			w.rawInventory.Months[table] = cache
		}
		cache.Checked = now
		countCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		counts, e := w.countRawMonth(countCtx, table, month)
		cancel()
		if e == nil {
			cache.Days = counts
			return nil
		}
		code := Diagnose(e).Code
		for day := month; day.Month() == month.Month(); day = day.AddDate(0, 0, 1) {
			date := day.Format("2006-01-02")
			v := cache.Days[date]
			v.ErrorCode = code
			cache.Days[date] = v
		}
		return nil // count errors belong to counts, not the archive task
	}
	return nil
}

func (w *Worker) countRawMonth(ctx context.Context, table string, month time.Time) (map[string]af.RawDayCount, error) {
	if !writerMonthlyName.MatchString(table) || table == "logs_undated" {
		return nil, ErrFoundationSchema
	}
	var index string
	err := w.target.QueryRowContext(ctx, `SELECT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND SEQ_IN_INDEX=1 AND COLUMN_NAME='created_at' AND SUB_PART IS NULL AND INDEX_TYPE='BTREE' ORDER BY INDEX_NAME LIMIT 1`, table).Scan(&index)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &scanError{code: "archive_count_index_missing"}
		}
		return nil, err
	}
	var clock string
	if err = w.target.QueryRowContext(ctx, `SELECT DATE_FORMAT(UTC_TIMESTAMP(6),'%Y-%m-%d %H:%i:%s.%f')`).Scan(&clock); err != nil {
		return nil, err
	}
	observed, err := time.Parse("2006-01-02 15:04:05.999999", clock)
	if err != nil {
		return nil, err
	}
	// Arithmetic uses the existing Beijing date contract and is independent of
	// the connection's timezone. No raw row payload leaves the target database.
	rows, err := w.target.QueryContext(ctx, fmt.Sprintf("SELECT /*+ MAX_EXECUTION_TIME(2000) */ FLOOR((created_at+28800)/86400),COUNT(*) FROM %s FORCE INDEX (%s) GROUP BY FLOOR((created_at+28800)/86400) LIMIT 33", quote(table), quote(index)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]af.RawDayCount{}
	for day := month; day.Month() == month.Month(); day = day.AddDate(0, 0, 1) {
		zero := "0"
		t := observed
		counts[day.Format("2006-01-02")] = af.RawDayCount{Rows: &zero, ObservedAt: &t}
	}
	for rows.Next() {
		var epoch int64
		var count string
		if err = rows.Scan(&epoch, &count); err != nil {
			return nil, err
		}
		date := time.Unix(epoch*86400, 0).UTC().Format("2006-01-02")
		if _, ok := counts[date]; !ok {
			return nil, &scanError{code: "archive_count_date_mismatch"}
		}
		t := observed
		counts[date] = af.RawDayCount{Rows: &count, ObservedAt: &t}
	}
	return counts, rows.Err()
}

func (w *Worker) mergeRawInventory(p *af.WorkflowDayPage, after string) {
	days := map[string]af.WorkflowDay{}
	for _, day := range p.Days {
		days[day.Date] = day
	}
	for _, month := range w.rawInventory.Months {
		for date, raw := range month.Days {
			if date <= after || date > p.ObservedAt.In(archiveLocation).Format("2006-01-02") {
				continue
			}
			d, ok := days[date]
			if !ok {
				d = af.WorkflowDay{Date: date, State: "unknown", ObservedAt: p.ObservedAt}
			}
			copy := raw
			d.Raw = &copy
			days[date] = d
		}
	}
	ordered := make([]string, 0, len(days))
	for date := range days {
		ordered = append(ordered, date)
	}
	sort.Strings(ordered)
	more := p.NextAfter != "" || len(ordered) > af.WorkflowDayPageSize
	if len(ordered) > af.WorkflowDayPageSize {
		ordered = ordered[:af.WorkflowDayPageSize]
	}
	p.Days = nil
	for _, date := range ordered {
		p.Days = append(p.Days, days[date])
	}
	p.NextAfter = ""
	if more && len(ordered) > 0 {
		p.NextAfter = ordered[len(ordered)-1]
	}
}
