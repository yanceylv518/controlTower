package archivereader

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// One bounded day and one consistent read: totals do not depend on log pagination.
// Empty output, missing output and type-5 errors remain separate categories.
func anomalySQL(q JobQuery) (string, []any) {
	d, _ := time.ParseInLocation("2006-01-02", q.Date, time.FixedZone("Beijing", 28800))
	return anomalyRangeSQL(q, d.Unix(), d.AddDate(0, 0, 1).Unix())
}

func anomalyRangeSQL(q JobQuery, from, to int64) (string, []any) {
	d, _ := time.ParseInLocation("2006-01-02", q.Date, time.FixedZone("Beijing", 28800))
	index := ""
	if q.timeIndex != "" {
		index = " FORCE INDEX (`" + strings.ReplaceAll(q.timeIndex, "`", "``") + "`)"
	}
	query := `SELECT /*+ MAX_EXECUTION_TIME(3000) */ FLOOR((created_at-?)/3600) AS hour,
COUNT(*) AS log_rows,
SUM(CASE WHEN type=2 THEN 1 ELSE 0 END) AS consumption,
SUM(CASE WHEN type=2 AND completion_tokens=0 THEN 1 ELSE 0 END) AS empty_output,
SUM(CASE WHEN type=2 AND completion_tokens IS NULL THEN 1 ELSE 0 END) AS missing_output,
SUM(CASE WHEN type=5 THEN 1 ELSE 0 END) AS error,
SUM(CASE WHEN type=2 AND completion_tokens=0 AND quota>0 THEN 1 ELSE 0 END) AS charged_empty_output
FROM ` + "`logs_" + d.Format("200601") + "`" + index + " WHERE created_at>=? AND created_at<?"
	args := []any{d.Unix(), from, to}
	for _, filter := range []struct{ column, value string }{{"user_id", q.UserID}, {"model_name", q.Model}, {q.channelSQL(), q.ChannelID}} {
		if filter.value != "" {
			query += " AND " + filter.column + "=?"
			args = append(args, filter.value)
		}
	}
	return query + " GROUP BY hour ORDER BY hour LIMIT 24", args
}

var anomalyMetrics = []string{"log_rows", "consumption", "empty_output", "missing_output", "error", "charged_empty_output"}

// Keep the same repeatable-read transaction for every partition. No partial
// total escapes when a later partition fails or the request is cancelled.
func readAnomalyDay(ctx context.Context, tx *sql.Tx, q JobQuery) (JobPage, error) {
	d, _ := time.ParseInLocation("2006-01-02", q.Date, time.FixedZone("Beijing", 28800))
	items, err := aggregateAnomalyDay(ctx, d.Unix(), func(from, to int64) ([]map[string]any, error) {
		query, args := anomalyRangeSQL(q, from, to)
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			values := make([]string, 7)
			dest := make([]any, 7)
			for i := range dest {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return nil, err
			}
			item := map[string]any{"hour": values[0]}
			for i, key := range anomalyMetrics {
				item[key] = values[i+1]
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		return items, nil
	})
	if err != nil {
		return JobPage{}, readFailure(err)
	}
	if err := tx.Commit(); err != nil {
		return JobPage{}, readFailure(err)
	}
	return JobPage{Items: items}, nil
}

func aggregateAnomalyDay(ctx context.Context, start int64, read func(int64, int64) ([]map[string]any, error)) ([]map[string]any, error) {
	hours := map[int]map[string]*big.Int{}
	var window func(int64, int64) error
	window = func(from, to int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		items, err := read(from, to)
		if err != nil {
			var dbErr *mysql.MySQLError
			if errors.As(err, &dbErr) && dbErr.Number == 3024 && to-from > 60 {
				mid := from + (to-from)/2
				if err := window(from, mid); err != nil {
					return err
				}
				return window(mid, to)
			}
			return err
		}
		for _, item := range items {
			rawHour, _ := item["hour"].(string)
			hour, err := strconv.Atoi(rawHour)
			if err != nil || hour < 0 || hour > 23 {
				return ErrUnavailable
			}
			if hours[hour] == nil {
				hours[hour] = map[string]*big.Int{}
			}
			for _, key := range anomalyMetrics {
				raw, _ := item[key].(string)
				value, ok := new(big.Int).SetString(raw, 10)
				if !ok || value.Sign() < 0 {
					return ErrUnavailable
				}
				if hours[hour][key] == nil {
					hours[hour][key] = new(big.Int)
				}
				hours[hour][key].Add(hours[hour][key], value)
			}
		}
		return nil
	}
	for hour := int64(0); hour < 24; hour++ {
		if err := window(start+hour*3600, start+(hour+1)*3600); err != nil {
			return nil, err
		}
	}
	items := []map[string]any{}
	for hour := 0; hour < 24; hour++ {
		if totals := hours[hour]; totals != nil {
			item := map[string]any{"hour": strconv.Itoa(hour)}
			for _, key := range anomalyMetrics {
				item[key] = totals[key].String()
			}
			items = append(items, item)
		}
	}
	return items, nil
}
