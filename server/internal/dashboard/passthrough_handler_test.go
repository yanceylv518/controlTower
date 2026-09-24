package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unconfiguredReadonlyStore struct{}

func (unconfiguredReadonlyStore) ReadonlyDSNForSite(string) (string, error) { return "", nil }
func (unconfiguredReadonlyStore) UpdateReadonlyDSNForSite(string, string, time.Time) error {
	return nil
}

func TestBillingLogsPageProjectionIncludesTokenWithoutChangingOtherProjection(t *testing.T) {
	query, _ := billingLogsPageQuery(7, 0, 3)
	for _, want := range []string{"COALESCE(l.token_id,0)", "COALESCE(l.token_name,'')", "AND COALESCE(l.token_id,0)=?", billingOtherProjection} {
		if !strings.Contains(query, want) {
			t.Fatalf("missing %q in %s", want, query)
		}
	}
}

func TestPassthroughPageLimits(t *testing.T) {
	r := httptest.NewRequest("GET", "/?limit=999&offset=-3", nil)
	limit, offset := queryPage(r, 100)
	if limit != 100 || offset != 0 {
		t.Fatalf("queryPage = %d,%d", limit, offset)
	}
}

func TestPassthroughWindowRejectsMoreThan31Days(t *testing.T) {
	end := time.Now().UTC()
	start := end.Add(-32 * 24 * time.Hour)
	r := httptest.NewRequest("GET", "/?start_time="+start.Format(time.RFC3339)+"&end_time="+end.Format(time.RFC3339), nil)
	if _, _, err := queryWindow(r); err == nil {
		t.Fatal("expected oversized time window to fail")
	}
}

func TestPassthroughWindowAcceptsRC35SecondTimestamps(t *testing.T) {
	r := httptest.NewRequest("GET", "/?start_timestamp=1760000000&end_timestamp=1760003600", nil)
	start, end, err := queryWindow(r)
	require.NoError(t, err)
	assert.Equal(t, int64(1760000000), start.Unix())
	assert.Equal(t, int64(1760003600), end.Unix())
}

func TestPassthroughSummaryRedactsAndTruncates(t *testing.T) {
	value := "email alice@example.com from 192.168.1.2\n" + strings.Repeat("x", 250)
	redacted := redactSummary(value)
	if strings.Contains(redacted, "alice@example.com") || strings.Contains(redacted, "192.168.1.2") {
		t.Fatalf("sensitive values remain: %q", redacted)
	}
	if len([]rune(redacted)) > 201 {
		t.Fatalf("summary is too long: %d", len([]rune(redacted)))
	}
}

func TestPassthroughAdminScopeAllowsAllUsers(t *testing.T) {
	r := httptest.NewRequest("GET", "/?site=cn", nil)
	site, ids, err := passthroughScope(r)
	if err != nil || site != "cn" || len(ids) != 0 {
		t.Fatalf("scope = %q,%v error = %v", site, ids, err)
	}
}

func TestPassthroughUnconfiguredSiteDegradesGracefully(t *testing.T) {
	h := PassthroughHandler{Config: unconfiguredReadonlyStore{}}
	r := httptest.NewRequest("GET", "/?site=cn&user_ids=12", nil)
	w := httptest.NewRecorder()
	h.Users(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Configured {
		t.Fatal("unconfigured site reported configured")
	}
}

func TestPassthroughPoolAndTimeoutGuards(t *testing.T) {
	db, err := sql.Open("mysql", "unused:unused@tcp(127.0.0.1:1)/unused")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureReadonlyDB(db)
	if got := db.Stats().MaxOpenConnections; got != readonlyDBMaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d", got)
	}
	if readonlyQueryTimeout != 5*time.Second {
		t.Fatalf("readonlyQueryTimeout = %s", readonlyQueryTimeout)
	}
	if readonlyLogQueryTimeout != 120*time.Second {
		t.Fatalf("readonlyLogQueryTimeout = %s", readonlyLogQueryTimeout)
	}
	if readonlyLogCountTimeout != 120*time.Second {
		t.Fatalf("readonlyLogCountTimeout = %s", readonlyLogCountTimeout)
	}
}

func TestReadonlyLogsListQueryUsesTimeIndexOrder(t *testing.T) {
	if !strings.Contains(readonlyLogsListQuery, "l.created_at>=? AND l.created_at<?") {
		t.Fatalf("list query must use a half-open time range: %s", readonlyLogsListQuery)
	}
	if strings.Contains(readonlyLogsListQuery, "BETWEEN") {
		t.Fatalf("list query must not use an inclusive time range: %s", readonlyLogsListQuery)
	}
	if readonlyLogsListOrder != " ORDER BY l.created_at DESC,l.id DESC LIMIT ? OFFSET ?" {
		t.Fatalf("list query order does not match idx_created_at_id: %s", readonlyLogsListOrder)
	}
}

// 速率统计必须限制在最近一分钟的半开区间，避免没有上界时扫描未来记录。
func TestReadonlyLogRateQueryUsesBoundedWindow(t *testing.T) {
	if !strings.Contains(readonlyLogRateQuery, "l.created_at>=? AND l.created_at<?") {
		t.Fatalf("rate query must use a bounded half-open time range: %s", readonlyLogRateQuery)
	}
}

// 聚合计数为零时必须回源完整区间，避免重灌日志复用 ID 后静默少计。
func TestReadonlyLogCountFallsBackWhenRollupIsEmpty(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"if local.RequestCount == 0",
		"queryRawCount(ctx, db, start, end, where, args[2:])",
		"直接查完整区间可避免把头尾零头与聚合结果重复相加",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("missing empty-rollup fallback contract %q", required)
		}
	}
}

func TestBillingLogsPageQueryUsesChannelTimeKeyset(t *testing.T) {
	if billingLogsPageTimeout != 2*time.Minute {
		t.Fatalf("large billing pages need a bounded two-minute query window: %s", billingLogsPageTimeout)
	}
	query, idCursor := billingLogsPageQuery(0, 23, -1)
	if idCursor {
		t.Fatal("channel export must use a time cursor")
	}
	for _, required := range []string{
		"l.created_at>=? AND l.created_at<?",
		"AND (l.created_at>? OR (l.created_at=? AND l.id>?))",
		"AND l.channel_id=?",
		"ORDER BY l.created_at,l.id LIMIT ?",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("billing page query missing %q: %s", required, query)
		}
	}
	if strings.Contains(query, "OFFSET") || strings.Contains(query, "BETWEEN") {
		t.Fatalf("billing page query must use a half-open keyset range: %s", query)
	}
}

func TestBillingLogsPageQueryUsesUserTimeKeyset(t *testing.T) {
	query, idCursor := billingLogsPageQuery(7, 0, -1)
	if idCursor {
		t.Fatal("user export must use a bounded time cursor")
	}
	if !strings.Contains(query, "AND l.user_id=?") || strings.Contains(query, "AND l.channel_id=?") ||
		strings.Contains(query, "FORCE INDEX") ||
		!strings.Contains(query, "AND (l.created_at>? OR (l.created_at=? AND l.id>?))") ||
		!strings.Contains(query, "ORDER BY l.created_at,l.id LIMIT ?") {
		t.Fatalf("query filters do not match user export: %s", query)
	}
	if got := strings.Count(query, "?"); got != 7 {
		t.Fatalf("user query placeholders=%d want=7: %s", got, query)
	}
}

func TestBillingLogsPageQueryKeepsTimeKeysetForUnfilteredJobs(t *testing.T) {
	query, idCursor := billingLogsPageQuery(0, 0, -1)
	if idCursor {
		t.Fatal("unfiltered billing jobs must keep the time cursor")
	}
	if !strings.Contains(query, "(l.created_at>? OR (l.created_at=? AND l.id>?))") ||
		!strings.Contains(query, "ORDER BY l.created_at,l.id LIMIT ?") {
		t.Fatalf("unfiltered query does not use the time keyset: %s", query)
	}
}

func TestBillingChannelsLogsPageQueryScansTimeRangeOnce(t *testing.T) {
	query := billingChannelsLogsPageQuery(3)
	for _, required := range []string{
		"l.created_at>=? AND l.created_at<?",
		"(l.created_at>? OR (l.created_at=? AND l.id>?))",
		"l.channel_id IN (?,?,?)",
		"ORDER BY l.created_at,l.id LIMIT ?",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("multi-channel billing query missing %q: %s", required, query)
		}
	}
	if strings.Contains(query, "OFFSET") || strings.Contains(query, "BETWEEN") || strings.Contains(query, "FORCE INDEX") {
		t.Fatalf("multi-channel query must use the existing time index without forcing a site-specific index: %s", query)
	}
	if got := strings.Count(query, "?"); got != 9 {
		t.Fatalf("multi-channel query placeholders=%d want=9: %s", got, query)
	}
}

func TestUniquePositiveIDs(t *testing.T) {
	got := uniquePositiveIDs([]int64{7, 0, 7, -2, 9})
	if !reflect.DeepEqual(got, []int64{7, 9}) {
		t.Fatalf("uniquePositiveIDs=%v", got)
	}
}

// rc35 参数别名必须和 CT 旧参数一起解析，并让所有读取接口得到同一组条件。
func TestReadonlyLogFiltersSupportRC35Aliases(t *testing.T) {
	r := httptest.NewRequest("GET", "/?type=5&channel=18&page_size=25&p=3&username=alice&model_name=gpt-4o&group=vip&request_id=req-1&upstream_request_id=up-1", nil)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), []int64{7, 9}, false)
	require.NoError(t, err)
	assert.Contains(t, filters.where, "l.user_id IN (?,?)")
	assert.Contains(t, filters.where, "l.type = ?")
	assert.Contains(t, filters.where, "l.channel_id = ?")
	assert.Contains(t, filters.where, "l.`group` = ?")
	assert.Contains(t, filters.where, "l.upstream_request_id = ?")
	assert.Equal(t, "alice", filters.username)
	assert.Equal(t, "gpt-4o", filters.modelName)
	assert.Equal(t, "vip", filters.group)
	assert.Equal(t, "req-1", filters.requestID)
	assert.Equal(t, "up-1", filters.upstreamRequestID)
	assert.NotNil(t, filters.logType)
	assert.NotNil(t, filters.channelID)
	limit, offset := queryPage(r, 100)
	assert.Equal(t, 25, limit)
	assert.Equal(t, 50, offset)
}

func TestReadonlyLogFiltersSupportStatusCodeAndEmptyOutput(t *testing.T) {
	r := httptest.NewRequest("GET", "/?status_code=429&empty_output=1", nil)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), nil, false)
	require.NoError(t, err)
	assert.NotNil(t, filters.statusCode)
	assert.Equal(t, 429, *filters.statusCode)
	assert.True(t, filters.emptyOutput)
	assert.True(t, filters.hasRawFilter)
	assert.NotNil(t, filters.logType)
	assert.Equal(t, 5, *filters.logType)
	assert.Contains(t, filters.where, "l.content REGEXP ?")
	assert.Contains(t, filters.where, "l.other REGEXP ?")
	assert.Contains(t, filters.where, "l.completion_tokens = 0 OR l.completion_tokens IS NULL")
	foundStatusPattern := false
	for _, value := range filters.args {
		if pattern, ok := value.(string); ok && strings.Contains(pattern, "429") {
			foundStatusPattern = true
			break
		}
	}
	assert.True(t, foundStatusPattern)
}

func TestReadonlyLogFiltersDefaultsEmptyOutputToConsumption(t *testing.T) {
	filters, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?empty_output=true", nil).URL.Query(), nil, false)
	require.NoError(t, err)
	assert.True(t, filters.emptyOutput)
	assert.NotNil(t, filters.logType)
	assert.Equal(t, 2, *filters.logType)
	assert.Contains(t, filters.where, "l.completion_tokens = 0 OR l.completion_tokens IS NULL")
}

func TestReadonlyLogFiltersRejectInvalidStatusCodeAndTypeCombination(t *testing.T) {
	for _, value := range []string{"99", "600", "abc"} {
		_, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?status_code="+value, nil).URL.Query(), nil, false)
		assert.EqualError(t, err, "invalid_status_code")
	}
	_, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?status_code=429&type=2", nil).URL.Query(), nil, false)
	assert.EqualError(t, err, "invalid_filter_combination")
	_, err = parseReadonlyLogFilters(httptest.NewRequest("GET", "/?empty_output=maybe", nil).URL.Query(), nil, false)
	assert.EqualError(t, err, "invalid_empty_output")
}

func TestReadonlyLogFiltersViewerKeepsFinalRequestOnly(t *testing.T) {
	r := httptest.NewRequest("GET", "/?request_id=req-1", nil)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), []int64{7}, true)
	require.NoError(t, err)
	assert.Contains(t, filters.where, "NOT EXISTS")
	assert.Contains(t, filters.where, "newer_logs.created_at > l.created_at")
	assert.Contains(t, filters.where, "newer_logs.created_at = l.created_at")
	assert.Contains(t, filters.where, "newer_logs.id > l.id")
	assert.True(t, filters.hasRequestFilter)
	assert.False(t, filters.hasLike)
}

func TestReadonlyLogFiltersAdminFallbackFinalOnly(t *testing.T) {
	r := httptest.NewRequest("GET", "/?fallback_final_only=1", nil)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), nil, false)
	require.NoError(t, err)
	assert.True(t, filters.fallbackFinalOnly)
	assert.True(t, filters.hasRawFilter)
	assert.Contains(t, filters.where, "NOT EXISTS")
	assert.Contains(t, filters.where, "newer_logs.user_id = l.user_id")

	viewer, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/", nil).URL.Query(), nil, true)
	require.NoError(t, err)
	assert.True(t, viewer.fallbackFinalOnly)
	assert.True(t, viewer.hasRawFilter)

	disabled, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?fallback_final_only=0", nil).URL.Query(), nil, false)
	require.NoError(t, err)
	assert.False(t, disabled.fallbackFinalOnly)
	assert.NotContains(t, disabled.where, "newer_logs")
}

func TestReadonlyLogFiltersRejectUnsafeFuzzyPatterns(t *testing.T) {
	_, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?model_name=%25a", nil).URL.Query(), nil, false)
	require.EqualError(t, err, "invalid_model_name_filter")
	_, err = parseReadonlyLogFilters(httptest.NewRequest("GET", "/?username=alice%25", nil).URL.Query(), nil, false)
	require.NoError(t, err)
}

func TestProjectReadonlyLogOtherSeparatesRoles(t *testing.T) {
	raw := `{"public_id":9007199254740993,"reject_reason":"blocked","use_channel":["141","148"],"fallback_channels":["141","148"],"upstream_model_name":"deepseek-v4-flash-0731","is_model_mapped":true,"admin_info":{"admin_id":9},"root_info":{"generation":42}}`
	viewer := projectReadonlyLogOther(raw, true)
	assert.Contains(t, viewer, `"public_id":9007199254740993`)
	assert.NotContains(t, viewer, "admin_info")
	assert.NotContains(t, viewer, "root_info")
	assert.NotContains(t, viewer, "reject_reason")
	assert.NotContains(t, viewer, "use_channel")
	assert.NotContains(t, viewer, "fallback_channels")
	assert.NotContains(t, viewer, "upstream_model_name")
	assert.NotContains(t, viewer, "is_model_mapped")

	admin := projectReadonlyLogOther(raw, false)
	assert.Contains(t, admin, `"public_id":9007199254740993`)
	assert.Contains(t, admin, "admin_info")
	assert.Contains(t, admin, "reject_reason")
	assert.Contains(t, admin, "upstream_model_name")
	assert.NotContains(t, admin, "root_info")
	assert.Equal(t, "{}", projectReadonlyLogOther("null", true))
	assert.Equal(t, "{}", projectReadonlyLogOther("not-json", false))
}

// fallback 事实既可能来自新日志的显式标志，也可能只能从旧日志 use_channel 链路推断。
func TestReadonlyLogFallbackInfoSupportsLegacyAndScopedFields(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback bool
		channels []string
	}{
		{name: "admin channel chain", raw: `{"admin_info":{"use_channel":["26","78"]}}`, fallback: true, channels: []string{"26", "78"}},
		{name: "explicit admin flag", raw: `{"admin_info":{"fallback":true,"use_channel":["26"]}}`, fallback: true, channels: []string{"26"}},
		{name: "legacy text chain", raw: `{"use_channel":"26->78"}`, fallback: true, channels: []string{"26", "78"}},
		{name: "explicit top level flag", raw: `{"fallback_flag":true}`, fallback: true},
		{name: "single attempt", raw: `{"admin_info":{"use_channel":["26"]}}`, fallback: false, channels: []string{"26"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fallback, channels := readonlyLogFallbackInfo(tt.raw)
			assert.Equal(t, tt.fallback, fallback)
			assert.Equal(t, tt.channels, channels)
		})
	}
}

// fallback 字段是只读 API 的稳定布尔合同，渠道链为空时不应序列化成误导性的空数组。
func TestPassthroughLogFallbackJSONContract(t *testing.T) {
	value, err := json.Marshal(PassthroughLog{Fallback: true, FallbackChannels: []string{"26", "78"}, FallbackIndex: 1, FallbackTotal: 2})
	require.NoError(t, err)
	var encoded map[string]any
	require.NoError(t, json.Unmarshal(value, &encoded))
	assert.Equal(t, true, encoded["fallback"])
	assert.Equal(t, []any{"26", "78"}, encoded["fallback_channels"])
	assert.Equal(t, float64(1), encoded["fallback_index"])
	assert.Equal(t, float64(2), encoded["fallback_total"])
	empty, err := json.Marshal(PassthroughLog{})
	require.NoError(t, err)
	var emptyEncoded map[string]any
	require.NoError(t, json.Unmarshal(empty, &emptyEncoded))
	assert.Equal(t, false, emptyEncoded["fallback"])
	assert.NotContains(t, emptyEncoded, "fallback_channels")
	assert.NotContains(t, emptyEncoded, "fallback_index")
	assert.NotContains(t, emptyEncoded, "fallback_total")
}

func TestReadonlyFallbackChainPropagatesCompletePathAndCurrentAttempt(t *testing.T) {
	records := []readonlyFallbackRecord{
		{id: 3, requestID: "request-a", userID: 7, typeID: 2, channelID: 148, createdAt: 30, other: `{"admin_info":{"use_channel":["141","191","148"]}}`},
		{id: 1, requestID: "request-a", userID: 7, typeID: 5, channelID: 141, createdAt: 10, other: `{"admin_info":{"use_channel":["141"]}}`},
		{id: 2, requestID: "request-a", userID: 7, typeID: 5, channelID: 191, createdAt: 20, other: `{"admin_info":{"use_channel":["141","191"]}}`},
	}
	chain := readonlyFallbackChainFor(records)
	assert.Equal(t, []string{"141", "191", "148"}, chain.channels)
	assert.Equal(t, map[int64]int{1: 1, 2: 2, 3: 3}, chain.indexByID)
}

func TestReadonlyFallbackChainPreservesRepeatedChannelAttempts(t *testing.T) {
	records := []readonlyFallbackRecord{
		{id: 1, requestID: "request-b", userID: 9, typeID: 5, channelID: 141, createdAt: 10, other: `{"use_channel":["141"]}`},
		{id: 2, requestID: "request-b", userID: 9, typeID: 5, channelID: 141, createdAt: 20, other: `{"use_channel":["141","141"]}`},
		{id: 3, requestID: "request-b", userID: 9, typeID: 2, channelID: 148, createdAt: 30, other: `{"use_channel":["141","141","148"]}`},
	}
	chain := readonlyFallbackChainFor(records)
	assert.Equal(t, []string{"141", "141", "148"}, chain.channels)
	assert.Equal(t, map[int64]int{1: 1, 2: 2, 3: 3}, chain.indexByID)
}

func TestReadonlyFallbackChainSupportsMoreThanThreeAttempts(t *testing.T) {
	channels := make([]string, 0, 12)
	records := make([]readonlyFallbackRecord, 0, 12)
	for attempt := 1; attempt <= 12; attempt++ {
		channels = append(channels, strconv.Itoa(attempt))
		records = append(records, readonlyFallbackRecord{
			id:        int64(attempt),
			requestID: "request-many-attempts",
			userID:    9,
			typeID:    5,
			channelID: int64(attempt),
			createdAt: int64(attempt),
			other:     `{"use_channel":["` + strings.Join(channels, `","`) + `"]}`,
		})
	}

	chain := readonlyFallbackChainFor(records)
	assert.Equal(t, channels, chain.channels)
	assert.Len(t, chain.indexByID, 12)
	assert.Equal(t, 10, chain.indexByID[10])
	assert.Equal(t, 12, chain.indexByID[12])
}

// 使用真实隔离 MySQL 只执行日志查询，验证 rc35 投影在目标方言上可解析。
func TestReadonlyLogsListQueryRunsAgainstConfiguredMySQL(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_MYSQL_TEST_DSN to run the read-only MySQL contract check")
	}
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now().UTC().Add(-time.Hour).Unix()
	end := time.Now().UTC().Add(time.Hour).Unix()
	rows, err := db.QueryContext(ctx, readonlyLogsListQuery+readonlyLogsListOrder, start, end, 1, 0)
	require.NoError(t, err)
	assert.NoError(t, rows.Close())
	assert.NoError(t, rows.Err())
	filters, err := parseReadonlyLogFilters(httptest.NewRequest("GET", "/?username=alice&model_name=gpt-4o&token_name=key&group=vip&request_id=req-1&upstream_request_id=up-1&channel=18&type=2", nil).URL.Query(), nil, false)
	require.NoError(t, err)
	args := append([]any{start, end}, filters.args...)
	args = append(args, 1, 0)
	rows, err = db.QueryContext(ctx, readonlyLogsListQuery+filters.where+readonlyLogsListOrder, args...)
	require.NoError(t, err)
	assert.NoError(t, rows.Close())
	assert.NoError(t, rows.Err())
}
