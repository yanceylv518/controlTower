package dashboard

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadonlyLogTokenFiltersValidateBounds(t *testing.T) {
	for _, param := range []string{"prompt_tokens_min", "prompt_tokens_max", "completion_tokens_min", "completion_tokens_max"} {
		t.Run(param, func(t *testing.T) {
			for _, invalid := range []string{"-1", "1.5", "1e3", "abc", "9223372036854775808", "0 OR 1=1"} {
				_, err := parseReadonlyLogFilters(url.Values{param: {invalid}}, nil, false)
				require.EqualError(t, err, "invalid_"+param)
			}
			for _, valid := range []string{"0", " 10 ", "9223372036854775807"} {
				filters, err := parseReadonlyLogFilters(url.Values{param: {valid}}, nil, false)
				require.NoError(t, err)
				require.True(t, filters.hasRawFilter)
				require.NotContains(t, filters.where, "COALESCE")
				bound, err := strconv.ParseInt(strings.TrimSpace(valid), 10, 64)
				require.NoError(t, err)
				require.Equal(t, []any{bound}, filters.args)
			}
		})
	}
	for _, column := range []string{"prompt_tokens", "completion_tokens"} {
		_, err := parseReadonlyLogFilters(url.Values{column + "_min": {"10"}, column + "_max": {"0"}}, nil, false)
		require.EqualError(t, err, "invalid_"+column+"_range")
	}
	filters, err := parseReadonlyLogFilters(url.Values{"prompt_tokens_min": {" "}, "completion_tokens_max": {""}}, nil, false)
	require.NoError(t, err)
	require.False(t, filters.hasRawFilter)
	require.Empty(t, filters.where)
}

func TestReadonlyLogTokenFiltersPreserveNullAndScopeSemantics(t *testing.T) {
	filters, err := parseReadonlyLogFilters(url.Values{"prompt_tokens_min": {"100"}, "completion_tokens_min": {"0"}, "completion_tokens_max": {"20"}}, []int64{7}, false)
	require.NoError(t, err)
	require.Contains(t, filters.where, "AND l.prompt_tokens >= ?")
	require.NotContains(t, filters.where, "l.prompt_tokens IS NULL")
	require.Contains(t, filters.where, "AND (l.completion_tokens >= ? AND l.completion_tokens <= ? OR l.completion_tokens IS NULL)")
	require.Equal(t, []any{int64(7), int64(100), int64(0), int64(20)}, filters.args)
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	scope, err := readonlyLogCursorScope("a", false, url.Values{}, []int64{7}, start, end)
	require.NoError(t, err)
	cursor := readonlyPageToken(PassthroughLog{ID: 1, CreatedAt: start}, false, scope)
	for _, param := range []string{"prompt_tokens_min", "prompt_tokens_max", "completion_tokens_min", "completion_tokens_max"} {
		changed, err := readonlyLogCursorScope("a", false, url.Values{param: {"0"}}, []int64{7}, start, end)
		require.NoError(t, err)
		require.NotEqual(t, scope, changed)
		_, err = parseReadonlyPageCursor(cursor, changed)
		require.EqualError(t, err, "invalid_cursor")
	}

	status, err := parseReadonlyLogFilters(url.Values{"status_code": {"429"}, "completion_tokens_max": {"20"}}, []int64{7}, false)
	require.NoError(t, err)
	candidates := readonlyStatusCandidates(status)
	require.Contains(t, candidates.where, "AND l.user_id IN (?)")
	require.Contains(t, candidates.where, "AND (l.completion_tokens <= ? OR l.completion_tokens IS NULL)")
	require.Equal(t, []any{int64(7), int64(20), 5, "429", "429"}, candidates.args)
	require.Equal(t, len(candidates.args), strings.Count(candidates.where, "?"))
}

// 用原先的 COALESCE 条件作独立参照，验证改写没有改变空值、负数或边界口径。
func originalReadonlyTokenFilters(t testing.TB, values url.Values, ids []int64, viewer bool) readonlyLogFilters {
	t.Helper()
	base := make(url.Values)
	for key, value := range values {
		if !strings.HasPrefix(key, "prompt_tokens_") && !strings.HasPrefix(key, "completion_tokens_") {
			base[key] = value
		}
	}
	filters, err := parseReadonlyLogFilters(base, ids, viewer)
	require.NoError(t, err)
	for _, column := range []string{"prompt_tokens", "completion_tokens"} {
		for index, suffix := range []string{"min", "max"} {
			value := strings.TrimSpace(values.Get(column + "_" + suffix))
			if value == "" {
				continue
			}
			bound, err := strconv.ParseInt(value, 10, 64)
			require.NoError(t, err)
			filters.where += " AND COALESCE(l." + column + ",0) " + []string{">=", "<="}[index] + " ?"
			filters.args = append(filters.args, bound)
		}
	}
	return filters
}

func TestReadonlyLogTokenRangesMatchOriginalMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	_, err := db.Exec(`INSERT INTO logs(id,user_id,created_at,type,prompt_tokens,completion_tokens,quota,request_id,content) VALUES
 (1,7,100,2,NULL,NULL,1,NULL,''),(2,7,110,2,0,0,2,'',''),
 (3,7,120,2,100,10,3,'a',''),(4,7,130,2,200,20,4,'b',''),
 (5,7,140,2,-1,-1,5,NULL,''),(6,8,145,2,100,10,6,NULL,''),
 (7,7,150,5,100,0,7,'retry','HTTP 429'),(8,7,160,2,100,30,8,'retry',''),
 (9,7,200,2,100,10,9,NULL,''),(10,7,99,2,100,10,10,NULL,''),
 (11,7,170,5,NULL,NULL,11,NULL,'HTTP 429'),(12,7,180,5,9223372036854775807,40,12,NULL,'')`)
	require.NoError(t, err)
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	for _, query := range []string{
		"", "prompt_tokens_min=0", "prompt_tokens_max=0", "prompt_tokens_min=0&prompt_tokens_max=0",
		"prompt_tokens_min=100&prompt_tokens_max=200", "prompt_tokens_min=9223372036854775807",
		"completion_tokens_min=0", "completion_tokens_max=0", "completion_tokens_min=0&completion_tokens_max=0",
		"completion_tokens_min=10&completion_tokens_max=20", "completion_tokens_min=30",
		"prompt_tokens_min=100&completion_tokens_max=20", "prompt_tokens_max=0&completion_tokens_min=0",
		"completion_tokens_max=0&status_code=429", "completion_tokens_max=0&empty_output=1",
		"completion_tokens_max=0&fallback_final_only=1", "completion_tokens_min=10&type=2",
		"completion_tokens_min=0&completion_tokens_max=0&status_code=429&fallback_final_only=1",
	} {
		for _, viewer := range []bool{false, true} {
			values, err := url.ParseQuery(query)
			require.NoError(t, err)
			filters, err := parseReadonlyLogFilters(values, []int64{7}, viewer)
			require.NoError(t, err)
			original := originalReadonlyTokenFilters(t, values, []int64{7}, viewer)
			for _, page := range []struct {
				offset int
				cursor *readonlyPageCursor
			}{{0, nil}, {2, nil}, {0, &readonlyPageCursor{Time: 150, ID: 7}}, {0, &readonlyPageCursor{Time: 150, ID: 7, Previous: true}}} {
				require.Equal(t, readonlyEquivalenceIDs(t, db, original, page.offset, page.cursor), readonlyEquivalenceIDs(t, db, filters, page.offset, page.cursor), "query=%s viewer=%t", query, viewer)
			}
			var expected, actual readonlyRawSummary
			oldSQL, oldArgs := readonlyRawSummarySQL(start, end, original)
			newSQL, newArgs := readonlyRawSummarySQL(start, end, filters)
			require.NoError(t, db.QueryRow(oldSQL, oldArgs...).Scan(&expected.Count, &expected.Quota))
			require.NoError(t, db.QueryRow(newSQL, newArgs...).Scan(&actual.Count, &actual.Quota))
			require.Equal(t, expected, actual, "summary: query=%s viewer=%t", query, viewer)
			rate := func(f readonlyLogFilters) [2]int64 {
				where := f.where
				if f.logType == nil {
					where += " AND l.type=2"
				}
				var result [2]int64
				require.NoError(t, db.QueryRow(readonlyLogRateQuery+where, append([]any{int64(100), int64(200)}, f.args...)...).Scan(&result[0], &result[1]))
				return result
			}
			require.Equal(t, rate(original), rate(filters), "rate: query=%s viewer=%t", query, viewer)
			if filters.statusCode != nil {
				fallback, _, err := fallbackReadonlyStatusSummary(context.Background(), db, start, end, filters, "quota")
				require.NoError(t, err)
				require.Equal(t, actual, fallback)
				tx, err := db.BeginTx(context.Background(), nil)
				require.NoError(t, err)
				items, readErr := fallbackReadonlyLogPage(context.Background(), tx, start, end, filters, 10, 0, nil, viewer)
				require.NoError(t, tx.Rollback())
				require.NoError(t, readErr)
				got := []int64{}
				for _, item := range items {
					got = append(got, item.ID)
				}
				require.Equal(t, readonlyEquivalenceIDs(t, db, original, 0, nil), got)
			}
		}
	}
	for _, sample := range []struct {
		query  string
		viewer bool
		ids    []int64
	}{
		{"completion_tokens_max=0", false, []int64{11, 7, 5, 2, 1}},
		{"completion_tokens_min=0&completion_tokens_max=0", false, []int64{11, 7, 2, 1}},
		{"prompt_tokens_min=0&prompt_tokens_max=0", false, []int64{11, 2, 1}},
		{"completion_tokens_max=0", true, []int64{11, 5, 2, 1}},
		{"prompt_tokens_min=100&prompt_tokens_max=100&completion_tokens_max=0", false, []int64{7}},
	} {
		values, err := url.ParseQuery(sample.query)
		require.NoError(t, err)
		filters, err := parseReadonlyLogFilters(values, []int64{7}, sample.viewer)
		require.NoError(t, err)
		require.Equal(t, sample.ids, readonlyEquivalenceIDs(t, db, filters, 0, nil))
	}
	t.Log("36 filter/role combinations matched for list, both cursor directions, count, quota, rate and error-code fallback")
}

func BenchmarkReadonlyLogTokenRangesMySQL(b *testing.B) {
	db := readonlyEquivalenceDB(b)
	digits := "(SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9)"
	_, err := db.Exec(`INSERT INTO logs(id,user_id,created_at,type,completion_tokens,quota)
 SELECT n+1,7,100+MOD(n,100),2,IF(MOD(n,1000)=0,NULL,MOD(n,10000)),1 FROM
 (SELECT a.n+10*c.n+100*d.n+1000*e.n+10000*f.n n FROM ` + digits + " a CROSS JOIN " + digits + " c CROSS JOIN " + digits + " d CROSS JOIN " + digits + " e CROSS JOIN " + digits + " f) numbers")
	require.NoError(b, err)
	values := url.Values{"completion_tokens_min": {"9990"}, "completion_tokens_max": {"9999"}}
	filters, err := parseReadonlyLogFilters(values, nil, false)
	require.NoError(b, err)
	original := originalReadonlyTokenFilters(b, values, nil, false)
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	for _, index := range []string{"time_index", "token_index"} {
		if index == "token_index" {
			_, err := db.Exec("ALTER TABLE logs ADD INDEX idx_test_completion_created(completion_tokens,created_at)")
			require.NoError(b, err)
		}
		for _, sample := range []struct {
			name    string
			filters readonlyLogFilters
		}{{"coalesce", original}, {"range", filters}} {
			b.Run(index+"/"+sample.name, func(b *testing.B) {
				query, args := readonlyRawSummarySQL(start, end, sample.filters)
				var raw string
				require.NoError(b, db.QueryRow("EXPLAIN FORMAT=JSON "+query, args...).Scan(&raw))
				var plan struct {
					QueryBlock struct {
						Table struct {
							AccessType string `json:"access_type"`
							Key        string `json:"key"`
							Rows       int64  `json:"rows_examined_per_scan"`
						} `json:"table"`
					} `json:"query_block"`
				}
				require.NoError(b, json.Unmarshal([]byte(raw), &plan))
				table := plan.QueryBlock.Table
				b.Logf("access=%s key=%s estimated_rows=%d", table.AccessType, table.Key, table.Rows)
				stmt, err := db.Prepare(query)
				require.NoError(b, err)
				defer stmt.Close()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var result readonlyRawSummary
					require.NoError(b, stmt.QueryRow(args...).Scan(&result.Count, &result.Quota))
					require.Equal(b, readonlyRawSummary{Count: 100, Quota: 100}, result)
				}
			})
		}
	}
}
