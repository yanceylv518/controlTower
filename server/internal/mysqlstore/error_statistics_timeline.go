package mysqlstore

import (
	"context"
	es "controltower/internal/errorstats"
	"database/sql"
	"strings"
	"time"
)

// Aggregate the long tail in SQL, keeping result size independent of log volume.
func (s Store) queryErrorTimeline(ctx context.Context, tx *sql.Tx, q es.Query, out es.Result, where string, args []any) (es.Result, error) {
	codes := []es.Count{}
	for _, c := range out.Codes {
		if c.Key != "zero_output" {
			codes = append(codes, c)
		}
	}
	out.Codes = codes
	if out.Truncated {
		var sum int64
		for _, c := range codes {
			sum += c.Count
		}
		if remaining := out.TotalErrors - sum; remaining > 0 {
			out.Codes = append(out.Codes, es.Count{Key: "other", Count: remaining})
		}
	}
	expression := "error_code"
	params := []any{}
	if q.Code != "" {
		where += " AND error_code=?"
		args = append(args, q.Code)
	} else {
		top := codes[:min(5, len(codes))]
		if len(top) > 0 {
			marks := make([]string, len(top))
			for i, c := range top {
				marks[i] = "?"
				params = append(params, c.Key)
			}
			expression = "CASE WHEN error_code IN (" + strings.Join(marks, ",") + ") THEN error_code ELSE 'other' END"
		} else {
			expression = "'other'"
		}
	}
	where += " AND error_code<>'zero_output'"
	step := q.BucketSeconds
	if step != 300 {
		step = 60
	}
	params = append(params, args...)
	// Parameters for the expression occur before the WHERE parameters.
	rows, err := tx.QueryContext(ctx, "SELECT FLOOR(TIMESTAMPDIFF(SECOND,'1970-01-01',bucket_time)/"+map[int64]string{60: "60", 300: "300"}[step]+")*"+map[int64]string{60: "60", 300: "300"}[step]+", "+expression+", SUM(record_count) FROM error_statistics_minutes WHERE "+where+" GROUP BY 1,2 ORDER BY 1,2", params...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var unix, n int64
		var code string
		if err = rows.Scan(&unix, &code, &n); err != nil {
			rows.Close()
			return out, err
		}
		at := time.Unix(unix, 0).UTC()
		if len(out.Buckets) == 0 || !out.Buckets[len(out.Buckets)-1].Time.Equal(at) {
			out.Buckets = append(out.Buckets, es.Bucket{Time: at, Counts: map[string]int64{}})
		}
		out.Buckets[len(out.Buckets)-1].Counts[code] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func errorStatisticsWindow(start, covered time.Time, lost *time.Time, since, until time.Time, step time.Duration) (time.Time, time.Time) {
	first := start.Truncate(step)
	if first.Before(start) {
		first = first.Add(step)
	}
	if lost != nil {
		afterLoss := lost.Truncate(step).Add(step)
		if afterLoss.After(first) {
			first = afterLoss
		}
	}
	if since.Before(first) {
		since = first
	}
	last := covered.Truncate(step)
	if until.After(last) {
		until = last
	}
	return since, until
}
