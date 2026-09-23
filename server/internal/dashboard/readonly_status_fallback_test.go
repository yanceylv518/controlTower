package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestReadonlyStatusFallbackOnlyOnRegexLimit(t *testing.T) {
	require.True(t, readonlyRegexpLimit(fmt.Errorf("wrapped: %w", &mysql.MySQLError{Number: 3699})))
	for _, err := range []error{nil, context.Canceled, context.DeadlineExceeded, errors.New("read failed"), &mysql.MySQLError{Number: 1045}} {
		require.False(t, readonlyRegexpLimit(err))
	}
	matcher := newReadonlyStatusMatcher(500)
	for _, sample := range []struct {
		text  string
		match bool
	}{
		{strings.Repeat(" ", 1_000_000) + "unrelated-500", false},
		{strings.Repeat("status_code=429; ", 100_000) + "500", false},
		{"status_code=" + strings.Repeat(" ", 1_000_000) + "500", true},
		{strings.Repeat("status_code=", 100_000) + "500", true},
	} {
		require.Equal(t, sample.match, matcher.match(sample.text, "utf8mb4_unicode_ci"))
	}
}

func TestReadonlyStatusFallbackCancelAndOverflowMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	_, err := db.Exec("INSERT INTO logs(id,user_id,created_at,type,quota,content) VALUES (1,1,150,5,9223372036854775807,'HTTP 500'),(2,1,150,5,1,'HTTP 500')")
	require.NoError(t, err)
	filters, err := parseReadonlyLogFilters(url.Values{"status_code": {"500"}}, nil, false)
	require.NoError(t, err)
	start, end := time.Unix(100, 0), time.Unix(200, 0)
	_, _, err = fallbackReadonlyStatusSummary(context.Background(), db, start, end, filters, "quota")
	require.ErrorContains(t, err, "overflow")
	count, _, err := fallbackReadonlyStatusSummary(context.Background(), db, start, end, filters, "count")
	require.NoError(t, err)
	require.Equal(t, int64(2), count.Count)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = fallbackReadonlyStatusSummary(ctx, db, start, end, filters, "count")
	require.ErrorIs(t, err, context.Canceled)
}

func TestReadonlyStatusFallbackMatcherMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			for _, code := range []int{100, 429, 500, 599} {
				matcher := newReadonlyStatusMatcher(code)
				value := fmt.Sprint(code)
				var terms []string
				for range statusCodeQueryPatterns(code) {
					terms = append(terms, "CAST(? AS CHAR CHARACTER SET utf8mb4) COLLATE "+collation+" REGEXP ?")
				}
				stmt, err := db.Prepare("SELECT " + strings.Join(terms, " OR "))
				require.NoError(t, err)
				defer stmt.Close()
				for _, key := range []string{"status_code=", "statusCode:", "status code=", "error_code=", `"code":`, "'code':", "HTTP ", "STATUS_CODE=", "ſtatus_code=", "ﬆatus_code=", "ﬅatus_code=", "status\ncode=", "http\u00a0", "status_code＝"} {
					for _, prefix := range []string{"", "x", "_", "中", "\u0345", "\u2160", "\u00b2", "\u0300", "💡 "} {
						for _, suffix := range []string{"", "0", "a", "\u0660"} {
							text := prefix + key + value + suffix
							var args []any
							for _, pattern := range statusCodeQueryPatterns(code) {
								args = append(args, text, pattern)
							}
							var expected bool
							require.NoError(t, stmt.QueryRow(args...).Scan(&expected))
							require.Equal(t, expected, matcher.match(text, collation), "text=%q collation=%s", text, collation)
						}
					}
				}
			}
		})
	}
}

func TestReadonlyRC132RegexFallbackMySQL(t *testing.T) {
	db := readonlyEquivalenceDB(t)
	_, err := db.Exec("ALTER TABLE logs MODIFY content LONGTEXT NULL, MODIFY other LONGTEXT NULL")
	require.NoError(t, err)
	now := time.Now().Unix() - 10
	// More than one candidate batch, mostly false positives. OFFSET must skip
	// matched records, never raw candidates. Include matches in both fields.
	for id := 1; id <= 140; id++ {
		content, other := "unrelated 500", ""
		if id%10 == 0 {
			content = "status_code=500"
		}
		if id == 130 {
			content, other = "unrelated 500", "HTTP 500"
		}
		if id == 139 {
			content = strings.Repeat(" ", 1_000_000) + "unrelated-500"
		}
		_, err := db.Exec("INSERT INTO logs(id,user_id,created_at,type,request_id,quota,prompt_tokens,completion_tokens,content,other) VALUES (?,1,?,5,?,10,20,3,?,?)", id, now, fmt.Sprint(id), content, other)
		require.NoError(t, err)
	}
	for _, collation := range []string{"utf8mb4_0900_ai_ci", "utf8mb4_unicode_ci", "utf8mb4_bin"} {
		t.Run(collation, func(t *testing.T) {
			_, err := db.Exec("ALTER TABLE logs CONVERT TO CHARACTER SET utf8mb4 COLLATE " + collation)
			require.NoError(t, err)
			filters, err := parseReadonlyLogFilters(url.Values{"status_code": {"500"}}, nil, false)
			require.NoError(t, err)
			// Prove this dataset fails on the actual rc132 expression, not only
			// on the already-withdrawn combined regex from the previous fix.
			var n int
			err = db.QueryRow("SELECT COUNT(*) FROM logs l WHERE 1=1"+filters.where, filters.args...).Scan(&n)
			require.True(t, readonlyRegexpLimit(err), "expected rc132 error 3699, got %v", err)
			h := &PassthroughHandler{Config: customerQueryConfig{}, pools: map[string]passthroughPool{"a": {encrypted: "test-pool", db: db}}}
			query := fmt.Sprintf("/?site=a&start_time=%d&end_time=%d&status_code=500&limit=3&offset=0", now-1, now+1)
			list, count, stat := httptest.NewRecorder(), httptest.NewRecorder(), httptest.NewRecorder()
			h.Logs(list, httptest.NewRequest("GET", query, nil))
			h.LogCount(count, httptest.NewRequest("GET", query, nil))
			h.LogStat(stat, httptest.NewRequest("GET", query, nil))
			require.Equal(t, 200, list.Code, list.Body.String())
			require.Equal(t, 200, count.Code, count.Body.String())
			require.Equal(t, 200, stat.Code, stat.Body.String())
			var listed struct {
				Items []PassthroughLog `json:"items"`
			}
			require.NoError(t, json.Unmarshal(list.Body.Bytes(), &listed))
			require.Equal(t, []int64{140, 130, 120}, []int64{listed.Items[0].ID, listed.Items[1].ID, listed.Items[2].ID})
			require.JSONEq(t, `{"configured":true,"total":14}`, count.Body.String())
			require.JSONEq(t, `{"configured":true,"summary":{"quota":140,"rpm":14,"tpm":322}}`, stat.Body.String())
			for _, page := range []struct {
				offset int
				cursor *readonlyPageCursor
				want   []int64
			}{
				{4, nil, []int64{100, 90, 80, 70}},
				{0, &readonlyPageCursor{Time: now, ID: 80}, []int64{70, 60, 50, 40}},
				{0, &readonlyPageCursor{Time: now, ID: 80, Previous: true}, []int64{90, 100, 110, 120}},
			} {
				tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
				require.NoError(t, err)
				items, err := fallbackReadonlyLogPage(context.Background(), tx, time.Unix(now-1, 0), time.Unix(now+1, 0), filters, 3, page.offset, page.cursor, false)
				require.NoError(t, tx.Rollback())
				require.NoError(t, err)
				var ids []int64
				for _, item := range items {
					ids = append(ids, item.ID)
				}
				require.Equal(t, page.want, ids)
			}
		})
	}
}
