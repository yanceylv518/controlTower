package dashboard

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadonlyLegacyCursorSurvivesQueryOptimization(t *testing.T) {
	for _, input := range []string{"", "status_code=429", "empty_output=1", "fallback_final_only=1", "status_code=503&empty_output=1&fallback_final_only=1"} {
		t.Run(input, func(t *testing.T) {
			values, err := url.ParseQuery(input)
			require.NoError(t, err)
			values.Set("model_name", "model-a")
			oldFilters, err := legacyReadonlySpecialFilters(values, []int64{7}, false)
			require.NoError(t, err)
			oldArgs := append([]any{int64(100), int64(200)}, oldFilters.args...)
			scope := readonlyPageScope("a", false, oldFilters.where, oldArgs)
			for _, previous := range []bool{false, true} {
				calls := 0
				db := sql.OpenDB(reviewConnector{func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
					calls++
					// The old anchor must actually reach the list SQL, not be ignored.
					require.Equal(t, int64(150), args[len(args)-5].Value)
					require.Equal(t, int64(42), args[len(args)-3].Value)
					return &reviewRows{columns: []string{"id"}}, nil
				}})
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
				values.Set("site", "a")
				values.Set("user_ids", "7")
				values.Set("start_time", time.Unix(100, 0).UTC().Format(time.RFC3339))
				values.Set("end_time", time.Unix(200, 0).UTC().Format(time.RFC3339))
				values.Set("cursor", readonlyPageToken(PassthroughLog{ID: 42, CreatedAt: time.Unix(150, 0)}, previous, scope))
				w := httptest.NewRecorder()
				h.Logs(w, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
				require.Equal(t, 200, w.Code, w.Body.String())
				require.Equal(t, 1, calls)
			}
		})
	}
}

func TestReadonlyFallbackCollationEquivalenceMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	requests := []string{"Case", "case", "CASE", "café", "cafe", "cafe\u0301", "padded", "padded ", "padded  ", "", " ", "  ", "nul\x00suffix"}
	var all []PassthroughLog
	for user := int64(1); user <= 2; user++ {
		for _, request := range requests {
			for attempt := 0; attempt < 2; attempt++ {
				id := int64(len(all) + 1)
				_, err := db.Exec("INSERT INTO logs (id,user_id,created_at,type,request_id,channel_id,other) VALUES (?,?,?,5,?,?,?)", id, user, int64(100)+id, request, attempt+10, `{}`)
				require.NoError(t, err)
				all = append(all, PassthroughLog{ID: id, UserID: user, RequestID: request})
			}
		}
	}
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			_, err := db.Exec("ALTER TABLE logs CONVERT TO CHARACTER SET utf8mb4 COLLATE " + collation)
			require.NoError(t, err)
			pages := [][]PassthroughLog{all}
			for _, item := range all {
				pages = append(pages, []PassthroughLog{item})
			}
			for index, page := range pages {
				for _, viewer := range []bool{false, true} {
					before, after := append([]PassthroughLog(nil), page...), append([]PassthroughLog(nil), page...)
					tx, err := db.Begin()
					require.NoError(t, err)
					legacyReadonlyFallbackRequests(context.Background(), tx, before)
					require.NoError(t, tx.Rollback())
					tx, err = db.Begin()
					require.NoError(t, err)
					markReadonlyFallbackRequests(context.Background(), tx, after, viewer)
					require.NoError(t, tx.Rollback())
					if viewer {
						for i := range before {
							before[i].FallbackChannels, before[i].FallbackIndex, before[i].FallbackTotal = nil, 0, 0
						}
					}
					require.Equal(t, before, after, "page=%d viewer=%v requests=%v", index, viewer, page)
				}
			}
		})
	}
}

func TestReadonlyRawSummaryOverflowPreservesCountMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	_, err := db.Exec(`INSERT INTO logs (id,user_id,created_at,type,request_id,completion_tokens,quota) VALUES
(1,1,110,2,'first',0,9223372036854775807),(2,1,120,2,'second',0,1)`)
	require.NoError(t, err)
	h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
	values := url.Values{"site": {"a"}, "empty_output": {"1"}, "start_time": {time.Unix(100, 0).UTC().Format(time.RFC3339)}, "end_time": {time.Unix(200, 0).UTC().Format(time.RFC3339)}}
	count, stat := httptest.NewRecorder(), httptest.NewRecorder()
	h.LogCount(count, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
	h.LogStat(stat, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
	require.Equal(t, 200, count.Code, count.Body.String())
	require.JSONEq(t, `{"configured":true,"total":2}`, count.Body.String())
	require.Equal(t, 502, stat.Code, stat.Body.String())
	// After correcting the source data the failed statistic must not be cached.
	_, err = db.Exec("UPDATE logs SET quota=-1 WHERE id=2")
	require.NoError(t, err)
	stat = httptest.NewRecorder()
	h.LogStat(stat, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
	require.Equal(t, 200, stat.Code, stat.Body.String())
	require.JSONEq(t, fmt.Sprintf(`{"configured":true,"summary":{"quota":%d,"rpm":0,"tpm":0}}`, int64(9223372036854775806)), stat.Body.String())
}

func TestReadonlyCursorStillRejectsChangedScope(t *testing.T) {
	values := url.Values{"status_code": {"429"}, "empty_output": {"1"}, "fallback_final_only": {"1"}, "model_name": {"model-a"}}
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	scope, err := readonlyLogCursorScope("a", false, values, []int64{7}, start, end)
	require.NoError(t, err)
	oldToken := readonlyPageToken(PassthroughLog{ID: 42, CreatedAt: time.Unix(150, 0)}, false, scope)
	for _, changed := range []string{"site", "viewer", "users", "start", "end", "status_code", "empty_output", "fallback_final_only", "model_name", "request_id", "channel_id", "log_type"} {
		t.Run(changed, func(t *testing.T) {
			q, _ := url.ParseQuery(values.Encode())
			site, viewer, ids, from, to := "a", false, []int64{7}, start, end
			switch changed {
			case "site":
				site = "b"
			case "viewer":
				viewer = true
			case "users":
				ids = []int64{8}
			case "start":
				from = from.Add(time.Second)
			case "end":
				to = to.Add(time.Second)
			case "status_code":
				q.Set(changed, "503")
			case "empty_output", "fallback_final_only":
				q.Set(changed, "0")
			case "model_name":
				q.Set(changed, "model-b")
			case "request_id":
				q.Set(changed, "retry")
			case "channel_id":
				q.Set(changed, "11")
			case "log_type":
				q.Set(changed, "2")
			}
			changedScope, err := readonlyLogCursorScope(site, viewer, q, ids, from, to)
			if changed == "log_type" {
				require.EqualError(t, err, "invalid_filter_combination")
				return
			}
			require.NoError(t, err)
			_, err = parseReadonlyPageCursor(oldToken, changedScope)
			require.EqualError(t, err, "invalid_cursor")
		})
	}
}

func TestReadonlyAllStatusCodesEquivalentMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	// Include NULLs, each supported format in either column, code boundaries,
	// mixed codes and fragments that must not match across columns.
	corpus := func(code int) [][2]any {
		value := strconv.Itoa(code)
		return [][2]any{
			{"status_code=" + value, nil}, {nil, "statusCode: '" + value + "'"},
			{"status\ncode=\t" + value, ""}, {"", "error_code=" + value},
			{`{"code":` + value + `}`, nil}, {nil, "HTTP " + value},
			{"prefix_status_code=" + value, "statusCode=" + value + "0"},
			{"status_code=" + value + "abc", "HTTP 0" + value},
			{"状态STATUS_CODE=" + value, "HTTP\u00a0" + value},
			{`{"status_code":` + value + `}`, "HTTP\n" + value},
			{"status_code=", value}, {nil, nil},
			{"status_code=429 HTTP 503", "error_code=599"},
		}
	}
	base, err := legacyReadonlySpecialFilters(url.Values{"status_code": {"100"}}, nil, false)
	require.NoError(t, err)
	current, err := parseReadonlyLogFilters(url.Values{"status_code": {"100"}}, nil, false)
	require.NoError(t, err)
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			rowSQL := "SELECT 5 AS type,CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE " + collation + " AS content,CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE " + collation + " AS other,? AS fallback_match"
			rows := make([]string, len(corpus(100)))
			for i := range rows {
				rows[i] = rowSQL
			}
			query := "SELECT COUNT(*) FROM (" + strings.Join(rows, " UNION ALL ") + ") l WHERE NOT ((1=1" + base.where + ") <=> (1=1" + current.where + ")) OR COALESCE((1=1" + current.where + "),0) <> fallback_match"
			stmt, err := db.Prepare(query)
			require.NoError(t, err)
			defer stmt.Close()
			for code := 100; code <= 599; code++ {
				values := url.Values{"status_code": {strconv.Itoa(code)}}
				old, err := legacyReadonlySpecialFilters(values, nil, false)
				require.NoError(t, err)
				current, err := parseReadonlyLogFilters(values, nil, false)
				require.NoError(t, err)
				var args []any
				matcher := newReadonlyStatusMatcher(code)
				for _, pair := range corpus(code) {
					args = append(args, pair[0], pair[1])
					args = append(args, matcher.match(fmt.Sprint(pair[0]), collation) || matcher.match(fmt.Sprint(pair[1]), collation))
				}
				args = append(args, old.args...)
				args = append(args, current.args...)
				args = append(args, current.args...)
				var mismatches int
				require.NoError(t, stmt.QueryRow(args...).Scan(&mismatches), "code=%d", code)
				require.Zero(t, mismatches, "code=%d", code)
			}
		})
	}
}

func TestReadonlyHandlerPaginationAccuracyMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	seedReadonlyEquivalence(t, db)
	for _, input := range []string{"", "status_code=429", "empty_output=1", "fallback_final_only=1", "status_code=429&empty_output=1&fallback_final_only=1"} {
		t.Run(input, func(t *testing.T) {
			values, err := url.ParseQuery(input)
			require.NoError(t, err)
			legacy, err := legacyReadonlySpecialFilters(values, []int64{1}, false)
			require.NoError(t, err)
			rows, err := db.Query("SELECT l.id FROM logs l WHERE l.created_at>=? AND l.created_at<?"+legacy.where+" ORDER BY l.created_at DESC,l.id DESC", append([]any{int64(100), int64(200)}, legacy.args...)...)
			require.NoError(t, err)
			var expected []int64
			for rows.Next() {
				var id int64
				require.NoError(t, rows.Scan(&id))
				expected = append(expected, id)
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			values.Set("site", "a")
			values.Set("user_ids", "1")
			values.Set("start_time", "100")
			values.Set("end_time", "200")
			values.Set("page_size", "5")
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			type response struct {
				Items    []PassthroughLog `json:"items"`
				Next     string           `json:"next_cursor"`
				Previous string           `json:"previous_cursor"`
			}
			read := func() response {
				w := httptest.NewRecorder()
				h.Logs(w, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
				require.Equal(t, 200, w.Code, w.Body.String())
				var page response
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
				return page
			}
			var actual []int64
			var previousPage []int64
			for number := 1; ; number++ {
				require.Less(t, number, 100, "pagination must terminate")
				values.Set("p", strconv.Itoa(number))
				page := read()
				var ids []int64
				for _, item := range page.Items {
					require.Equal(t, int64(1), item.UserID)
					ids = append(ids, item.ID)
				}
				actual = append(actual, ids...)
				if number > 1 {
					require.NotEmpty(t, page.Previous)
					values.Set("cursor", page.Previous)
					values.Set("p", strconv.Itoa(number-1))
					var backIDs []int64
					for _, item := range read().Items {
						backIDs = append(backIDs, item.ID)
					}
					require.Equal(t, previousPage, backIDs)
				}
				if page.Next == "" {
					break
				}
				values.Set("cursor", page.Next)
				previousPage = ids
			}
			require.Equal(t, expected, actual, "full result must have no omissions, additions or duplicates")
			w := httptest.NewRecorder()
			h.LogCount(w, httptest.NewRequest("GET", "/?"+values.Encode(), nil))
			require.Equal(t, 200, w.Code, w.Body.String())
			require.JSONEq(t, fmt.Sprintf(`{"configured":true,"total":%d}`, len(expected)), w.Body.String())
		})
	}
}
