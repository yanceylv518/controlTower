package archivejob

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"strings"
)

const summaryGroupBatch = 128
const summaryStatementBytes = 1 << 20

// Read and lock only this page's groups, sum with the existing arbitrary-precision
// arithmetic, then write multiple groups per statement. The caller commits these
// writes and the source cursor together under the engine's existing writer lock.
func writeAggregateBatch(ctx context.Context, tx *sql.Tx, version, date string, groups map[string]aggregate) error {
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for start := 0; start < len(keys); start += summaryGroupBatch {
		end := start + summaryGroupBatch
		if end > len(keys) {
			end = len(keys)
		}
		chunk := keys[start:end]
		args := []any{version}
		for _, key := range chunk {
			args = append(args, key)
		}
		rows, err := tx.QueryContext(ctx, "SELECT group_hash,amounts FROM log_archive_daily_stats WHERE version_id=? AND group_hash IN ("+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+") ORDER BY group_hash FOR UPDATE", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			var raw []byte
			if err = rows.Scan(&key, &raw); err != nil {
				rows.Close()
				return err
			}
			old := map[string]string{}
			if json.Unmarshal(raw, &old) != nil {
				rows.Close()
				return errors.New("invalid_summary_json")
			}
			for k, v := range old {
				if err = add(groups[key].Amounts, k, v); err != nil {
					rows.Close()
					return err
				}
			}
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		var values []string
		args = nil
		bytes := 0
		flush := func() error {
			if len(values) == 0 {
				return nil
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO log_archive_daily_stats (version_id,group_hash,log_date,dimensions,amounts) VALUES "+strings.Join(values, ",")+" ON DUPLICATE KEY UPDATE amounts=VALUES(amounts)", args...)
			values, args, bytes = nil, nil, 0
			return err
		}
		for _, key := range chunk {
			a := groups[key]
			for _, value := range a.Amounts {
				n, ok := new(big.Int).SetString(value, 10)
				if !ok || n.Sign() < 0 {
					return errors.New("invalid_summary_delta")
				}
			}
			dimensions, err := json.Marshal(a.Dimensions)
			if err != nil {
				return err
			}
			amounts, err := json.Marshal(a.Amounts)
			if err != nil {
				return err
			}
			rowBytes := len(dimensions) + len(amounts) + len(version) + len(key) + len(date) + 128
			// A single large group is sent alone, preserving the existing row limit.
			if bytes+rowBytes > summaryStatementBytes {
				if err = flush(); err != nil {
					return err
				}
			}
			values = append(values, "(?,?,?,?,?)")
			args = append(args, version, key, date, string(dimensions), string(amounts))
			bytes += rowBytes
		}
		if err = flush(); err != nil {
			return err
		}
	}
	return nil
}
