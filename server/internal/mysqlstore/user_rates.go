package mysqlstore

import (
	"context"
	"controltower/server/internal/aggregator"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

func applyUserRates(ctx context.Context, tx *sql.Tx, instance string, metrics []aggregator.Metric) error {
	values := []string{}
	args := []any{}
	count := 0
	flush := func() error {
		if len(values) == 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO user_rate_seconds(instance_id,user_id,bucket_time,request_count,tokens) VALUES `+strings.Join(values, ",")+` ON DUPLICATE KEY UPDATE request_count=request_count+VALUES(request_count),tokens=tokens+VALUES(tokens)`, args...)
		values = nil
		args = nil
		return err
	}
	for _, m := range metrics {
		if m.DimensionType != "user_rate_second" {
			continue
		}
		id, err := strconv.ParseInt(m.DimensionKey, 10, 64)
		if err != nil || id < 0 || m.TPM < 0 || m.RequestCount < 0 {
			continue
		}
		values = append(values, "(?,?,?,?,?)")
		args = append(args, instance, id, m.BucketTime, m.RequestCount, m.TPM)
		count++
		if len(values) == 100 {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if count > 0 {
		_, err := tx.ExecContext(ctx, "DELETE FROM user_rate_seconds WHERE bucket_time<? LIMIT ?", time.Now().UTC().Add(-15*time.Minute), max(1000, count))
		return err
	}
	return nil
}
