package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// A private schema is required because MySQL cannot self-join a TEMPORARY
// table. The opt-in DSN must name a disposable ct_readonly_query_test_* database;
// the suite creates and removes only its own uniquely named sibling schema.
func readonlyEquivalenceDB(t testing.TB) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CT_READONLY_QUERY_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_READONLY_QUERY_TEST_DSN for MySQL query equivalence tests")
	}
	config, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(config.DBName, "ct_readonly_query_test_"), "use a disposable query-test database")
	admin, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("ct_readonly_query_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE `" + schema + "`")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	config.DBName = schema
	db, err := sql.Open("mysql", config.FormatDSN())
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE logs (
 id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, created_at BIGINT NOT NULL, type INT NOT NULL,
 username VARCHAR(80), model_name VARCHAR(80), channel_id BIGINT, token_id BIGINT, token_name VARCHAR(80),
 prompt_tokens BIGINT, completion_tokens BIGINT NULL, quota BIGINT, use_time BIGINT,
 request_id VARCHAR(120) NULL, upstream_request_id VARCHAR(120), content TEXT NULL,
 ` + "`group`" + ` VARCHAR(80), ip VARCHAR(80), is_stream INT, other TEXT NULL,
 INDEX idx_created_at_id(created_at,id), INDEX idx_created_at_type(created_at,type),
 INDEX idx_logs_user_created_type(user_id,created_at,type), INDEX idx_logs_model_created_at(model_name,created_at),
 INDEX idx_tokenname_createdat(token_name,created_at), INDEX idx_logs_request_id(request_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`)
	require.NoError(t, err)
	return db
}

func seedReadonlyEquivalence(t *testing.T, db *sql.DB) {
	t.Helper()
	insert, err := db.Prepare(`INSERT INTO logs (id,user_id,created_at,type,username,model_name,channel_id,token_id,token_name,prompt_tokens,completion_tokens,quota,use_time,request_id,upstream_request_id,content,` + "`group`" + `,ip,is_stream,other) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	require.NoError(t, err)
	defer insert.Close()
	add := func(id, created, user, kind int64, request, output, content, other any) {
		_, err := insert.Exec(id, user, created, kind, fmt.Sprintf("user-%d", user), "model-a", int64(10+id%3), 7, "token-a", 100, output, id*10, 2, request, fmt.Sprintf("up-%d", id), content, "vip", "127.0.0.1", 1, other)
		require.NoError(t, err)
	}
	add(1, 110, 1, 5, "retry", 0, "status_code=429", `{"admin_info":{"use_channel":[11]}}`)
	add(2, 115, 1, 5, "retry", 0, "HTTP 429", `{"admin_info":{"use_channel":[11,12]}}`)
	add(3, 120, 1, 2, "retry", 10, "", `{"admin_info":{"use_channel":[11,12,10]}}`)
	add(4, 116, 2, 5, "retry", nil, "statusCode: 503", `{"use_channel":[11]}`)
	add(5, 125, 2, 5, "retry", 0, "error_code=429", `{"use_channel":[11,12]}`)
	add(6, 130, 1, 2, nil, nil, nil, nil)
	add(7, 131, 1, 2, "", 0, "", "")
	add(8, 132, 1, 2, "", 5, "", "")
	add(9, 140, 1, 5, "outside", 0, "status_code=429", "")
	add(10, 210, 1, 2, "outside", 10, "", "") // final attempt is outside the requested window
	add(11, 80, 1, 5, "before", 0, "status_code=429", "")
	add(12, 150, 1, 2, "before", 20, "", "")
	add(50, 170, 1, 5, "out-of-order", 0, "status_code=429", "")
	add(13, 180, 1, 2, "out-of-order", 30, "", "") // lower ID but later timestamp
	add(14, 180, 1, 5, "same-time", 0, "status_code=429", "")
	add(15, 180, 1, 6, "same-time", nil, "refund", "") // a non-consumption final row also counts
	add(16, 100, 1, 2, "at-start", 0, "", "")
	add(17, 200, 1, 2, "at-end", 0, "", "")
	add(18, 155, 1, 7, "login", 0, "login", "")
	add(19, 160, 1, 2, "negative", -1, "", "")
	add(20, 161, 1, 2, "spaces", 0, "", "")
	add(21, 162, 1, 2, "   ", 0, "", "")
	texts := []string{
		"status_code=429", "statusCode: '429'", "status code: 429", "error_code=429",
		`{"code":429}`, "HTTP 429", "status_code=4290", "prefix_status_code=429",
		"HTTP 1429", "429", `{"status_code":429}`, `{"http_status_code":429}`,
		"STATUS_CODE=429", "status_code=429; HTTP 503", "status_code=４２９", "状态 status_code=429",
		"status code:\n429", strings.Repeat("long message ", 2000) + " status_code=429",
		"status_code=429abc", "status_code=503", "not json", "",
	}
	for i, text := range texts {
		id := int64(100 + i)
		add(id, int64(140+i), 1, 5, fmt.Sprintf("format-%d", i), 0, text, nil)
		add(id+100, int64(140+i), 1, 5, fmt.Sprintf("other-format-%d", i), nil, nil, text)
	}
	// Same request ID belonging to unrelated users must not enlarge page enrichment.
	for i := int64(0); i < 30; i++ {
		add(300+i, 130+i, 3, 5, "retry", 0, "status_code=429", `{"use_channel":[1,2,3]}`)
	}
}

// Frozen pre-optimization predicates form the oracle. Base filters and validation
// are unchanged; only the three rewritten predicates are reconstructed here.
func legacyReadonlySpecialFilters(values url.Values, ids []int64, viewer bool) (readonlyLogFilters, error) {
	base := make(url.Values)
	for key, value := range values {
		base[key] = append([]string(nil), value...)
	}
	base.Del("status_code")
	base.Del("empty_output")
	base.Del("fallback_final_only")
	f, err := parseReadonlyLogFilters(base, ids, false)
	if err != nil {
		return f, err
	}
	if value := values.Get("status_code"); value != "" {
		code, err := strconv.Atoi(value)
		if err != nil {
			return f, err
		}
		if err := forceReadonlyLogType(&f, 5); err != nil {
			return f, err
		}
		conditions := []string{}
		for _, pattern := range legacyStatusCodeQueryPatterns(code) {
			conditions = append(conditions, "(l.content REGEXP ? OR l.other REGEXP ?)")
			f.args = append(f.args, pattern, pattern)
		}
		f.where += " AND (" + strings.Join(conditions, " OR ") + ")"
	}
	if value, _ := parseReadonlyBoolean(values.Get("empty_output")); value {
		if f.logType == nil {
			if err := forceReadonlyLogType(&f, 2); err != nil {
				return f, err
			}
		}
		f.where += " AND COALESCE(l.completion_tokens,0) = 0"
	}
	final, _ := parseReadonlyBoolean(values.Get("fallback_final_only"))
	if viewer || final {
		f.where += ` AND (l.request_id IS NULL OR l.request_id = '' OR NOT EXISTS (
			SELECT 1 FROM logs AS newer_logs
			WHERE newer_logs.request_id = l.request_id
			  AND newer_logs.user_id = l.user_id
			  AND (newer_logs.created_at > l.created_at OR
				(newer_logs.created_at = l.created_at AND newer_logs.id > l.id))
		))`
	}
	return f, nil
}

func legacyStatusCodeQueryPatterns(code int) []string {
	value := strconv.Itoa(code)
	boundary := "([^[:digit:]]|$)"
	prefix := "(^|[^[:alnum:]_])"
	return []string{
		prefix + "status_code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "statusCode[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "status[[:space:]]+code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "error_code[[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "[\"']code[\"'][[:space:]]*[:=][[:space:]]*[\"']?" + value + boundary,
		prefix + "HTTP[[:space:]]+" + value + boundary,
	}
}

func readonlyEquivalenceIDs(t *testing.T, db *sql.DB, filters readonlyLogFilters, offset int, cursor *readonlyPageCursor) []int64 {
	t.Helper()
	query, args := readonlyPageSQL(filters.where, append([]any{int64(100), int64(200)}, filters.args...), 10, offset, cursor)
	rows, err := db.Query(query, args...)
	require.NoError(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	require.NoError(t, err)
	ids := []int64{}
	for rows.Next() {
		var id int64
		values := make([]any, len(columns))
		values[0] = &id
		for i := 1; i < len(values); i++ {
			values[i] = new(any)
		}
		require.NoError(t, rows.Scan(values...))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func TestReadonlyOptimizedQueriesMatchLegacyMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	seedReadonlyEquivalence(t, db)
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			_, err := db.Exec("ALTER TABLE logs CONVERT TO CHARACTER SET utf8mb4 COLLATE " + collation)
			require.NoError(t, err)
			compareReadonlyQueriesMySQL(t, db)
		})
	}
}

func compareReadonlyQueriesMySQL(t *testing.T, db *sql.DB) {
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	comparisons := 0
	for mask := 0; mask < 8; mask++ {
		for _, kind := range []string{"", "2", "5", "6"} {
			for _, dimension := range []string{"", "model_name=model-a", "model_name=%25model%25", "token_name=token-a", "username=user-1", "request_id=retry", "channel_id=11", "upstream_request_id=up-9"} {
				values, err := url.ParseQuery(dimension)
				require.NoError(t, err)
				if mask&1 != 0 {
					values.Set("status_code", "429")
				}
				if mask&2 != 0 {
					values.Set("empty_output", "1")
				}
				if mask&4 != 0 {
					values.Set("fallback_final_only", "1")
				}
				if kind != "" {
					values.Set("log_type", kind)
				}
				for _, viewer := range []bool{false, true} {
					var scope []int64
					if viewer {
						scope = []int64{1, 2}
					}
					current, err := parseReadonlyLogFilters(values, scope, viewer)
					legacy, oldErr := legacyReadonlySpecialFilters(values, scope, viewer)
					if oldErr != nil {
						require.EqualError(t, err, oldErr.Error())
						continue
					}
					require.NoError(t, err)
					label := fmt.Sprintf("%s viewer=%t", values.Encode(), viewer)
					cursorScope, err := readonlyLogCursorScope("a", viewer, values, scope, start, end)
					require.NoError(t, err)
					require.Equal(t, readonlyPageScope("a", viewer, legacy.where, append([]any{start.Unix(), end.Unix()}, legacy.args...)), cursorScope, "cursor identity: "+label)
					for _, page := range []struct {
						offset int
						cursor *readonlyPageCursor
					}{{0, nil}, {10, nil}, {0, &readonlyPageCursor{Time: 150, ID: 12}}, {10, &readonlyPageCursor{Time: 150, ID: 12, Previous: true}}} {
						require.Equal(t, readonlyEquivalenceIDs(t, db, legacy, page.offset, page.cursor), readonlyEquivalenceIDs(t, db, current, page.offset, page.cursor), label)
					}
					var count, quota int64
					args := append([]any{start.Unix(), end.Unix()}, legacy.args...)
					err = db.QueryRow("SELECT COUNT(*) FROM logs l WHERE l.created_at>=? AND l.created_at<?"+legacy.where, args...).Scan(&count)
					require.NoError(t, err)
					quotaWhere := legacy.where
					if legacy.logType == nil {
						quotaWhere += " AND l.type=2"
					}
					quota, err = queryRawQuota(context.Background(), db, start, end, quotaWhere, legacy.args)
					require.NoError(t, err)
					query, newArgs := readonlyRawSummarySQL(start, end, current)
					var combined readonlyRawSummary
					err = db.QueryRow(query, newArgs...).Scan(&combined.Count, &combined.Quota)
					require.NoError(t, err)
					require.Equal(t, readonlyRawSummary{Count: count, Quota: quota}, combined, label)
					// Freeze the rate clock so old/new recent-minute queries see identical rows.
					rate := func(f readonlyLogFilters) [2]int64 {
						where := f.where
						if f.logType == nil {
							where += " AND l.type=2"
						}
						var result [2]int64
						err := db.QueryRow(readonlyLogRateQuery+where, append([]any{int64(120), int64(180)}, f.args...)...).Scan(&result[0], &result[1])
						require.NoError(t, err)
						return result
					}
					require.Equal(t, rate(legacy), rate(current), label)
					comparisons++
				}
			}
		}
	}
	t.Logf("Compared %d filter/scope combinations including offset, both cursor directions, count, quota and rate", comparisons)
}

// An opt-in benchmark with realistic repeated request IDs and nontrivial text.
// Compare the two original scans against one combined scan on the same data.
func BenchmarkReadonlyQueryStatsMySQL(b *testing.B) {
	db := readonlyEquivalenceDB(b)
	digits := "SELECT 0 n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 UNION ALL SELECT 8 UNION ALL SELECT 9"
	_, err := db.Exec(`INSERT INTO logs (id,user_id,created_at,type,username,model_name,channel_id,token_id,token_name,prompt_tokens,completion_tokens,quota,use_time,request_id,upstream_request_id,content,` + "`group`" + `,ip,is_stream,other)
SELECT n+1,1+MOD(FLOOR(n/4),100),100+MOD(n,3600),IF(MOD(n,4)=3,2,5),CONCAT('user-',1+MOD(FLOOR(n/4),100)),CONCAT('model-',MOD(n,5)),1+MOD(n,4),1,'token',100,IF(MOD(n,4)<3 OR MOD(FLOOR(n/4),5)=0,0,20),IF(MOD(n,4)=3,100,0),2,CONCAT('req-',FLOOR(n/4)),CONCAT('up-',n),IF(MOD(n,4)=3,'',CONCAT(REPEAT('upstream error detail ',20),IF(MOD(n,3)=0,' status_code=429',' HTTP 503'))),'vip','',1,'{"use_channel":[1,2,3,4]}'
FROM (SELECT a.n+10*c.n+100*d.n+1000*e.n+10000*f.n AS n FROM (` + digits + `) a CROSS JOIN (` + digits + `) c CROSS JOIN (` + digits + `) d CROSS JOIN (` + digits + `) e CROSS JOIN (` + digits + `) f) numbers WHERE n<20000`)
	require.NoError(b, err)
	start, end := time.Unix(100, 0), time.Unix(3700, 0)
	for _, input := range []struct{ name, query string }{
		{"status", "status_code=429"}, {"empty", "empty_output=1"}, {"final", "fallback_final_only=1"}, {"combined", "status_code=429&empty_output=1&fallback_final_only=1"},
	} {
		values, err := url.ParseQuery(input.query)
		require.NoError(b, err)
		legacy, err := legacyReadonlySpecialFilters(values, nil, false)
		require.NoError(b, err)
		current, err := parseReadonlyLogFilters(values, nil, false)
		require.NoError(b, err)
		oldRead := func() readonlyRawSummary {
			var result readonlyRawSummary
			args := append([]any{start.Unix(), end.Unix()}, legacy.args...)
			err := db.QueryRow("SELECT COUNT(*) FROM logs l WHERE l.created_at>=? AND l.created_at<?"+legacy.where, args...).Scan(&result.Count)
			require.NoError(b, err)
			where := legacy.where
			if legacy.logType == nil {
				where += " AND l.type=2"
			}
			result.Quota, err = queryRawQuota(context.Background(), db, start, end, where, legacy.args)
			require.NoError(b, err)
			return result
		}
		newRead := func() readonlyRawSummary {
			var result readonlyRawSummary
			query, args := readonlyRawSummarySQL(start, end, current)
			err := db.QueryRow(query, args...).Scan(&result.Count, &result.Quota)
			require.NoError(b, err)
			return result
		}
		require.Equal(b, oldRead(), newRead())
		b.Run(input.name+"/before", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				oldRead()
			}
		})
		b.Run(input.name+"/after", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				newRead()
			}
		})
	}
}

func TestReadonlyScopedFallbackEnrichmentMatchesLegacyMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	seedReadonlyEquivalence(t, db)
	for _, viewer := range []bool{false, true} {
		items := []PassthroughLog{{ID: 1, RequestID: "retry", UserID: 1}, {ID: 3, RequestID: "retry", UserID: 1}, {ID: 5, RequestID: "retry", UserID: 2}, {ID: 18, RequestID: "login", UserID: 1}, {ID: 7, UserID: 1}}
		oldItems := append([]PassthroughLog(nil), items...)
		tx, err := db.Begin()
		require.NoError(t, err)
		legacyReadonlyFallbackRequests(context.Background(), tx, oldItems)
		require.NoError(t, tx.Rollback())
		tx, err = db.Begin()
		require.NoError(t, err)
		markReadonlyFallbackRequests(context.Background(), tx, items, viewer)
		require.NoError(t, tx.Rollback())
		if viewer {
			for i := range oldItems {
				oldItems[i].FallbackChannels = nil
				oldItems[i].FallbackIndex = 0
				oldItems[i].FallbackTotal = 0
			}
		}
		require.Equal(t, oldItems, items)
		require.True(t, items[0].Fallback)
		require.True(t, items[0].FallbackChecked)
		if !viewer {
			require.Equal(t, []string{"11", "12", "10"}, items[0].FallbackChannels)
		}
	}
}

// Frozen baseline for enrichment result comparisons; no production caller.
func legacyReadonlyFallbackRequests(ctx context.Context, tx *sql.Tx, items []PassthroughLog) {
	requestIDs := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.RequestID == "" {
			continue
		}
		if _, ok := seen[item.RequestID]; ok {
			continue
		}
		seen[item.RequestID] = struct{}{}
		requestIDs = append(requestIDs, item.RequestID)
	}
	if len(requestIDs) == 0 {
		return
	}
	args := make([]any, len(requestIDs))
	for i, requestID := range requestIDs {
		args[i] = requestID
	}
	query := `SELECT request_id,user_id,COUNT(*) FROM logs WHERE request_id IN (` + placeholders(len(requestIDs)) + `) GROUP BY request_id,user_id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		// 标记属于展示增强；远端旧库不支持时保留基础日志，不让列表查询失败。
		return
	}
	defer rows.Close()
	fallbackKeys := make(map[string]struct{})
	for rows.Next() {
		var requestID string
		var userID, count int64
		if err := rows.Scan(&requestID, &userID, &count); err != nil {
			return
		}
		if count > 1 {
			fallbackKeys[readonlyRequestKey(requestID, userID)] = struct{}{}
		}
	}
	if rows.Err() != nil {
		return
	}
	for i := range items {
		if _, ok := fallbackKeys[readonlyRequestKey(items[i].RequestID, items[i].UserID)]; ok {
			items[i].Fallback = true
			// 计数只证明存在多条日志；完整链路查询成功后才宣布检查完成。
			items[i].FallbackChecked = false
		} else if items[i].RequestID != "" {
			items[i].FallbackChecked = true
		}
	}
	if len(fallbackKeys) == 0 {
		return
	}

	// 只对确认存在多条日志的请求读取渠道路径，避免普通日志页额外扫描内容字段。
	detailIDs := make([]string, 0, len(fallbackKeys))
	seenDetailID := make(map[string]struct{}, len(fallbackKeys))
	for key := range fallbackKeys {
		requestID := strings.SplitN(key, "\x00", 2)[0]
		if _, ok := seenDetailID[requestID]; ok {
			continue
		}
		seenDetailID[requestID] = struct{}{}
		detailIDs = append(detailIDs, requestID)
	}
	detailArgs := make([]any, len(detailIDs))
	for index, requestID := range detailIDs {
		detailArgs[index] = requestID
	}
	detailRows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(request_id,''),COALESCE(user_id,0),COALESCE(type,0),COALESCE(channel_id,0),COALESCE(created_at,0),COALESCE(other,'') FROM logs WHERE request_id IN (`+placeholders(len(detailIDs))+`) ORDER BY request_id,user_id,created_at,id`, detailArgs...)
	if err != nil {
		return
	}
	defer detailRows.Close()
	grouped := make(map[string][]readonlyFallbackRecord)
	for detailRows.Next() {
		var record readonlyFallbackRecord
		if err := detailRows.Scan(&record.id, &record.requestID, &record.userID, &record.typeID, &record.channelID, &record.createdAt, &record.other); err != nil {
			return
		}
		key := readonlyRequestKey(record.requestID, record.userID)
		if _, ok := fallbackKeys[key]; ok {
			grouped[key] = append(grouped[key], record)
		}
	}
	if detailRows.Err() != nil {
		return
	}
	chains := make(map[string]readonlyFallbackChain, len(grouped))
	for key, records := range grouped {
		chains[key] = readonlyFallbackChainFor(records)
	}
	for i := range items {
		if items[i].RequestID != "" {
			items[i].FallbackChecked = true
		}
		chain, ok := chains[readonlyRequestKey(items[i].RequestID, items[i].UserID)]
		if !ok || len(chain.channels) <= 1 {
			continue
		}
		items[i].Fallback = true
		items[i].FallbackChannels = append([]string(nil), chain.channels...)
		items[i].FallbackTotal = len(chain.channels)
		items[i].FallbackIndex = chain.indexByID[items[i].ID]
	}
}
