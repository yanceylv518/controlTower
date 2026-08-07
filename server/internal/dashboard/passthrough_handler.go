package dashboard

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"controltower/server/internal/billing"
	"controltower/server/internal/storage"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type PassthroughAuditStore interface {
	InsertOperationAudit(storage.OperationAudit) error
}

// Logs implements billing.Source using bounded, read-only hourly queries.
// The rollup service calls this method serially for the 24 hours of one day.
func (h *PassthroughHandler) LogsForBilling(ctx context.Context, site string, start, end time.Time) ([]billing.LogRecord, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT user_id,COALESCE(username,''),COALESCE(model_name,''),COALESCE(`+"`group`"+`,''),prompt_tokens,completion_tokens,quota,COALESCE(other,'') FROM logs WHERE created_at>=? AND created_at<? AND type=2 ORDER BY id`, start.Unix(), end.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []billing.LogRecord
	for rows.Next() {
		var v billing.LogRecord
		var other string
		if err = rows.Scan(&v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &other); err != nil {
			return nil, err
		}
		cache := resolveBillingCacheSemantic(parseBillingCacheUsage(other), v.PromptTokens)
		v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens = cache.Read, cache.Write, cache.Write5m, cache.Write1h
		if cache.Semantic != "anthropic" {
			v.PromptTokens -= cache.Read + cache.Write
			if v.PromptTokens < 0 {
				v.PromptTokens = 0
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *PassthroughHandler) DetailedLogsForBilling(ctx context.Context, site string, userID int64, start, end time.Time) ([]billing.DetailedLogRecord, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT id,created_at,COALESCE(request_id,''),user_id,COALESCE(username,''),COALESCE(model_name,''),COALESCE(`+"`group`"+`,''),prompt_tokens,completion_tokens,quota,COALESCE(other,'') FROM logs WHERE created_at>=? AND created_at<? AND type=2 AND user_id=? ORDER BY id`, start.Unix(), end.Unix(), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.DetailedLogRecord{}
	for rows.Next() {
		var v billing.DetailedLogRecord
		var created int64
		var other string
		if err = rows.Scan(&v.ID, &created, &v.RequestID, &v.UserID, &v.Username, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &other); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(created, 0).UTC()
		cache := resolveBillingCacheSemantic(parseBillingCacheUsage(other), v.PromptTokens)
		v.CacheTokens, v.CacheWriteTokens, v.CacheWrite5mTokens, v.CacheWrite1hTokens = cache.Read, cache.Write, cache.Write5m, cache.Write1h
		if cache.Semantic != "anthropic" {
			v.PromptTokens -= cache.Read + cache.Write
			if v.PromptTokens < 0 {
				v.PromptTokens = 0
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LogsPageForBilling keeps every source query bounded and resumes with a
// stable (created_at,id) keyset. It deliberately avoids OFFSET so later pages
// do not rescan all preceding rows on large new-api log tables.
func (h *PassthroughHandler) LogsPageForBilling(ctx context.Context, site string, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, 0, 0, start, end, cursor, limit)
}
func (h *PassthroughHandler) DetailedLogsPageForBilling(ctx context.Context, site string, userID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, userID, 0, start, end, cursor, limit)
}
func (h *PassthroughHandler) ChannelLogsPageForBilling(ctx context.Context, site string, channelID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, 0, channelID, start, end, cursor, limit)
}
func (h *PassthroughHandler) logsPageForBilling(ctx context.Context, site string, userID, channelID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	if limit <= 0 || limit > 5000 {
		limit = billing.BillingPageSize
	}
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// `other` can contain large provider diagnostics. Billing only needs the
	// cache-usage fields below, so project a compact JSON value inside MySQL
	// instead of transferring the complete payload over the RDS connection.
	query := `SELECT l.id,l.created_at,COALESCE(l.request_id,''),COALESCE(l.upstream_request_id,''),l.user_id,COALESCE(l.username,''),COALESCE(l.channel_id,0),COALESCE(c.name,''),COALESCE(l.model_name,''),COALESCE(l.` + "`group`" + `,''),l.prompt_tokens,l.completion_tokens,l.quota,` + billingOtherProjection + ` FROM logs l LEFT JOIN channels c ON c.id=l.channel_id WHERE l.type=2 AND l.created_at>=? AND l.created_at<? AND (l.created_at>? OR (l.created_at=? AND l.id>?))`
	args := []any{start.Unix(), end.Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID}
	if userID > 0 {
		query += ` AND l.user_id=?`
		args = append(args, userID)
	}
	if channelID > 0 {
		query += ` AND l.channel_id=?`
		args = append(args, channelID)
	}
	query += ` ORDER BY l.created_at,l.id LIMIT ?`
	args = append(args, limit)
	rows, err := db.QueryContext(queryCtx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]billing.PagedLogRecord, 0, limit)
	for rows.Next() {
		var v billing.PagedLogRecord
		var other string
		if err = rows.Scan(&v.ID, &v.CreatedUnix, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &other); err != nil {
			return nil, err
		}
		cache := parseBillingCacheUsage(other)
		v.SourcePromptTokens = v.PromptTokens
		if v.PromptTokens.Valid {
			cache = resolveBillingCacheSemantic(cache, v.PromptTokens.Int64)
		}
		v.CacheTokens, v.CacheWriteTokens = cache.Read, cache.Write
		v.CacheWrite5mTokens, v.CacheWrite1hTokens = cache.Write5m, cache.Write1h
		v.UsageSemantic = cache.Semantic
		if v.PromptTokens.Valid {
			rawPrompt := v.PromptTokens.Int64
			if cache.Semantic == "anthropic" {
				v.ContextTokens = rawPrompt + cache.Read + cache.Write
			} else {
				v.ContextTokens = rawPrompt
				v.PromptTokens.Int64 = rawPrompt - cache.Read - cache.Write
				if v.PromptTokens.Int64 < 0 {
					v.PromptTokens.Int64 = 0
				}
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const billingOtherProjection = `CASE WHEN JSON_VALID(l.other) THEN JSON_OBJECT(` +
	`'usage_semantic',JSON_EXTRACT(l.other,'$.usage_semantic'),` +
	`'claude',JSON_EXTRACT(l.other,'$.claude'),` +
	`'cache_tokens',JSON_EXTRACT(l.other,'$.cache_tokens'),` +
	`'cached_tokens',JSON_EXTRACT(l.other,'$.cached_tokens'),` +
	`'cache_read_input_tokens',JSON_EXTRACT(l.other,'$.cache_read_input_tokens'),` +
	`'prompt_cache_hit_tokens',JSON_EXTRACT(l.other,'$.prompt_cache_hit_tokens'),` +
	`'cache_creation_tokens',JSON_EXTRACT(l.other,'$.cache_creation_tokens'),` +
	`'cache_write_tokens',JSON_EXTRACT(l.other,'$.cache_write_tokens'),` +
	`'cached_creation_tokens',JSON_EXTRACT(l.other,'$.cached_creation_tokens'),` +
	`'cache_creation_tokens_5m',JSON_EXTRACT(l.other,'$.cache_creation_tokens_5m'),` +
	`'claude_cache_creation_5_m_tokens',JSON_EXTRACT(l.other,'$.claude_cache_creation_5_m_tokens'),` +
	`'cache_creation_tokens_1h',JSON_EXTRACT(l.other,'$.cache_creation_tokens_1h'),` +
	`'claude_cache_creation_1_h_tokens',JSON_EXTRACT(l.other,'$.claude_cache_creation_1_h_tokens')) ELSE '{}' END`

func (h *PassthroughHandler) RatioSnapshotForBilling(ctx context.Context, site string) (string, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return "", err
	}
	if !configured {
		return "", fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, "SELECT `key`,value FROM options WHERE `key` IN ('ModelRatio','CompletionRatio','CacheRatio','CreateCacheRatio','GroupRatio','QuotaPerUnit')")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return "", err
		}
		values[key] = value
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	// new-api omits options that still use their built-in defaults. Billing
	// price conversion nevertheless needs the effective QuotaPerUnit value.
	if strings.TrimSpace(values["QuotaPerUnit"]) == "" {
		values["QuotaPerUnit"] = "500000"
	}
	raw, err := json.Marshal(values)
	return string(raw), err
}

func (h *PassthroughHandler) ConfiguredModelsForBilling(ctx context.Context, site string) ([]string, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, "SELECT models FROM channels WHERE status=1 AND models<>''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var models []string
		if json.Unmarshal([]byte(raw), &models) != nil {
			models = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' })
		}
		for _, model := range models {
			model = strings.TrimSpace(model)
			if model != "" {
				seen[model] = true
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func (h *PassthroughHandler) CurrentChannelsForBilling(ctx context.Context, site string) ([]billing.ConfiguredChannel, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(name,''),status FROM channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ConfiguredChannel{}
	for rows.Next() {
		var item billing.ConfiguredChannel
		if err = rows.Scan(&item.ChannelID, &item.ChannelName, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (h *PassthroughHandler) BalancesForBilling(ctx context.Context, site string) (map[int64]int64, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, `SELECT id,quota FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[int64]int64{}
	for rows.Next() {
		var id, balance int64
		if err = rows.Scan(&id, &balance); err != nil {
			return nil, err
		}
		values[id] = balance
	}
	return values, rows.Err()
}

// Adapter names keep PassthroughHandler's HTTP Logs method intact while
// satisfying billing.Source through a small explicit wrapper.
type BillingReadonlySource struct{ Handler *PassthroughHandler }

func (s BillingReadonlySource) LogsPage(ctx context.Context, site string, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.LogsPageForBilling(ctx, site, start, end, cursor, limit)
}
func (s BillingReadonlySource) DetailedLogsPage(ctx context.Context, site string, userID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.DetailedLogsPageForBilling(ctx, site, userID, start, end, cursor, limit)
}
func (s BillingReadonlySource) ChannelLogsPage(ctx context.Context, site string, channelID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.ChannelLogsPageForBilling(ctx, site, channelID, start, end, cursor, limit)
}

func (s BillingReadonlySource) Logs(ctx context.Context, site string, start, end time.Time) ([]billing.LogRecord, error) {
	return s.Handler.LogsForBilling(ctx, site, start, end)
}
func (s BillingReadonlySource) DetailedLogs(ctx context.Context, site string, userID int64, start, end time.Time) ([]billing.DetailedLogRecord, error) {
	return s.Handler.DetailedLogsForBilling(ctx, site, userID, start, end)
}
func (s BillingReadonlySource) RatioSnapshot(ctx context.Context, site string) (string, error) {
	return s.Handler.RatioSnapshotForBilling(ctx, site)
}

func (s BillingReadonlySource) ConfiguredModels(ctx context.Context, site string) ([]string, error) {
	return s.Handler.ConfiguredModelsForBilling(ctx, site)
}
func (s BillingReadonlySource) CurrentChannels(ctx context.Context, site string) ([]billing.ConfiguredChannel, error) {
	return s.Handler.CurrentChannelsForBilling(ctx, site)
}
func (s BillingReadonlySource) Balances(ctx context.Context, site string) (map[int64]int64, error) {
	return s.Handler.BalancesForBilling(ctx, site)
}

func billingCacheTokens(other string) int64 {
	return parseBillingCacheUsage(other).Read
}

type billingCacheUsage struct {
	Read, Write, Write5m, Write1h int64
	Semantic                      string
}

func parseBillingCacheUsage(other string) billingCacheUsage {
	if strings.TrimSpace(other) == "" {
		return billingCacheUsage{Semantic: "openai"}
	}
	var values map[string]any
	if json.Unmarshal([]byte(other), &values) != nil {
		return billingCacheUsage{Semantic: "openai"}
	}
	number := func(keys ...string) int64 {
		for _, key := range keys {
			if value, ok := values[key]; ok {
				switch typed := value.(type) {
				case float64:
					if typed > 0 {
						return int64(typed)
					}
				case json.Number:
					parsed, _ := typed.Int64()
					if parsed > 0 {
						return parsed
					}
				case string:
					parsed, _ := strconv.ParseInt(typed, 10, 64)
					if parsed > 0 {
						return parsed
					}
				}
			}
		}
		return 0
	}
	numberMax := func(keys ...string) int64 {
		var max int64
		for _, key := range keys {
			if value := number(key); value > max {
				max = value
			}
		}
		return max
	}
	read := number("cache_tokens", "cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens")
	write5m := number("cache_creation_tokens_5m", "claude_cache_creation_5_m_tokens")
	write1h := number("cache_creation_tokens_1h", "claude_cache_creation_1_h_tokens")
	write := numberMax("cache_creation_tokens", "cache_write_tokens", "cached_creation_tokens")
	if split := write5m + write1h; split > write {
		write = split
	}
	semantic := "openai"
	if raw, ok := values["usage_semantic"].(string); ok && strings.EqualFold(raw, "anthropic") {
		semantic = "anthropic"
	}
	if claude, ok := values["claude"].(bool); ok && claude {
		semantic = "anthropic"
	}
	return billingCacheUsage{Read: read, Write: write, Write5m: write5m, Write1h: write1h, Semantic: semantic}
}

// OpenAI-style usage counts cache reads inside prompt_tokens, so cache lanes
// can never exceed prompt there. A row whose cache lanes exceed prompt is
// Anthropic-shaped even without an explicit marker — production new-api
// versions emit this shape with no usage_semantic field, and subtracting
// would zero the input lane.
func resolveBillingCacheSemantic(cache billingCacheUsage, promptTokens int64) billingCacheUsage {
	if cache.Semantic != "anthropic" && cache.Read+cache.Write > promptTokens {
		cache.Semantic = "anthropic"
	}
	return cache
}

type passthroughPool struct {
	encrypted string
	db        *sql.DB
}

const (
	readonlyQueryTimeout    = 5 * time.Second
	readonlyLogQueryTimeout = 120 * time.Second
	readonlyLogCountTimeout = 120 * time.Second
	readonlyLogsListQuery   = `SELECT id,user_id,created_at,type,COALESCE(username,''),COALESCE(model_name,''),channel_id,COALESCE(token_name,''),prompt_tokens,completion_tokens,quota,use_time,COALESCE(request_id,''),COALESCE(content,''),COALESCE(` + "`group`" + `,''),COALESCE(ip,''),COALESCE(is_stream,0),COALESCE(other,'') FROM logs WHERE created_at>=? AND created_at<?`
	readonlyLogsListOrder   = ` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?`
)

func configureReadonlyDB(db *sql.DB) {
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
}

type PassthroughHandler struct {
	Config    ReadonlyConfigStore
	Audit     PassthroughAuditStore
	Rollups   ReadonlyLogRollupStore
	SecretKey string
	mu        sync.Mutex
	pools     map[string]passthroughPool
}

type PassthroughUser struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Quota       int64  `json:"quota"`
	UsedQuota   int64  `json:"used_quota"`
	Status      int    `json:"status"`
	CreatedAt   int64  `json:"created_at"`
	LastLoginAt int64  `json:"last_login_at"`
}
type PassthroughLog struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	CreatedAt        time.Time `json:"created_at"`
	Type             int       `json:"type"`
	Username         string    `json:"username"`
	ModelName        string    `json:"model_name"`
	ChannelID        int64     `json:"channel_id"`
	TokenName        string    `json:"token_name"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	Quota            int64     `json:"quota"`
	UseTime          int64     `json:"use_time"`
	RequestID        string    `json:"request_id"`
	ContentSummary   string    `json:"content_summary"`
	Group            string    `json:"group"`
	IP               string    `json:"ip"`
	IsStream         bool      `json:"is_stream"`
	Other            string    `json:"other"`
}
type PassthroughLogSummary struct {
	Quota int64 `json:"quota"`
	RPM   int64 `json:"rpm"`
	TPM   int64 `json:"tpm"`
}

func (h *PassthroughHandler) database(site string) (*sql.DB, bool, error) {
	if h.Config == nil {
		return nil, false, nil
	}
	encrypted, err := h.Config.ReadonlyDSNForSite(site)
	if err != nil || encrypted == "" {
		return nil, false, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pools == nil {
		h.pools = map[string]passthroughPool{}
	}
	if current, ok := h.pools[site]; ok && current.encrypted == encrypted {
		return current.db, true, nil
	}
	dsn, err := decryptSecret(h.SecretKey, encrypted)
	if err != nil {
		return nil, false, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, false, err
	}
	configureReadonlyDB(db)
	if old, ok := h.pools[site]; ok {
		_ = old.db.Close()
	}
	h.pools[site] = passthroughPool{encrypted: encrypted, db: db}
	return db, true, nil
}

func passthroughScope(r *http.Request) (string, []int64, error) {
	u, authenticated := ctauth.CurrentUser(r)
	if authenticated && u.Role == "viewer" {
		if u.ScopeSite == "" || len(u.ScopeUserIDs) == 0 {
			return "", nil, fmt.Errorf("scope_not_configured")
		}
		return u.ScopeSite, append([]int64(nil), u.ScopeUserIDs...), nil
	}
	site := strings.TrimSpace(r.URL.Query().Get("site"))
	if site == "" {
		return "", nil, fmt.Errorf("site_required")
	}
	values := strings.Split(r.URL.Query().Get("user_ids"), ",")
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return "", nil, fmt.Errorf("invalid_user_ids")
		}
		ids = append(ids, id)
	}
	return site, ids, nil
}

func placeholders(n int) string { return strings.TrimRight(strings.Repeat("?,", n), ",") }
func queryWindow(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	end := now
	start := now.Add(-24 * time.Hour)
	var err error
	if value := r.URL.Query().Get("start_time"); value != "" {
		start, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return start, end, err
		}
	}
	if value := r.URL.Query().Get("end_time"); value != "" {
		end, err = time.Parse(time.RFC3339, value)
		if err != nil {
			return start, end, err
		}
	}
	if !start.Before(end) || end.Sub(start) > 31*24*time.Hour {
		return start, end, fmt.Errorf("invalid_time_range")
	}
	return start, end, nil
}
func queryPage(r *http.Request, max int) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > max {
		limit = max
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

func redactSummary(value string) string {
	value = emailPattern.ReplaceAllString(value, "***@***")
	value = ipv4Pattern.ReplaceAllString(value, "***.***.***.***")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	if len([]rune(value)) > 200 {
		value = string([]rune(value)[:200]) + "…"
	}
	return value
}

func (h *PassthroughHandler) audit(r *http.Request, site, operation string, summary any) {
	if h.Audit == nil {
		return
	}
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	body, _ := json.Marshal(summary)
	_ = h.Audit.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(raw), InstanceID: site, OperationType: operation, TargetType: "newapi_readonly", TargetID: site, ActorID: ctauth.Actor(r), AfterSummary: string(body), Status: "succeeded", CreatedAt: time.Now().UTC()})
}

func (h *PassthroughHandler) Users(w http.ResponseWriter, r *http.Request) {
	site, ids, err := passthroughScope(r)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	db, configured, err := h.database(site)
	if err != nil {
		writeDashboardError(w, 502, "readonly_connection_failed")
		return
	}
	if !configured {
		writeDashboardJSON(w, 200, map[string]any{"items": []PassthroughUser{}, "configured": false, "total": 0})
		return
	}
	limit, offset := queryPage(r, 200)
	args := make([]any, 0, len(ids)+4)
	where := " WHERE 1=1"
	if len(ids) > 0 {
		where += " AND id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if keyword := strings.TrimSpace(r.URL.Query().Get("keyword")); keyword != "" {
		where += " AND (username LIKE ? OR display_name LIKE ? OR CAST(id AS CHAR) LIKE ?)"
		like := "%" + keyword + "%"
		args = append(args, like, like, like)
	}
	if rawStatus := strings.TrimSpace(r.URL.Query().Get("status")); rawStatus != "" {
		status, parseErr := strconv.Atoi(rawStatus)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_status")
			return
		}
		where += " AND status = ?"
		args = append(args, status)
	}
	ctx, cancel := context.WithTimeout(r.Context(), readonlyQueryTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer tx.Rollback()
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := tx.QueryContext(ctx, `SELECT id,username,COALESCE(display_name,''),quota,used_quota,status,COALESCE(created_at,0),COALESCE(last_login_at,0) FROM users`+where+` ORDER BY id LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer rows.Close()
	items := []PassthroughUser{}
	for rows.Next() {
		var v PassthroughUser
		if rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.Quota, &v.UsedQuota, &v.Status, &v.CreatedAt, &v.LastLoginAt) != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	h.audit(r, site, "passthrough.users", map[string]any{"user_ids": ids, "limit": limit, "offset": offset})
	writeDashboardJSON(w, 200, map[string]any{"items": items, "configured": true, "total": total})
}

func (h *PassthroughHandler) Logs(w http.ResponseWriter, r *http.Request) {
	site, ids, err := passthroughScope(r)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	start, end, err := queryWindow(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_time_range")
		return
	}
	db, configured, err := h.database(site)
	if err != nil {
		writeDashboardError(w, 502, "readonly_connection_failed")
		return
	}
	if !configured {
		writeDashboardJSON(w, 200, map[string]any{"items": []PassthroughLog{}, "configured": false, "total": 0, "summary": PassthroughLogSummary{}})
		return
	}
	limit, offset := queryPage(r, 100)
	args := make([]any, 0, len(ids)+10)
	args = append(args, start.Unix(), end.Unix())
	userFilter := ""
	if len(ids) > 0 {
		userFilter = " AND user_id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	filters := ""
	for _, spec := range []struct{ param, column string }{{"token_name", "token_name"}, {"username", "username"}, {"group", "`group`"}, {"model_name", "model_name"}, {"request_id", "request_id"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(spec.param)); value != "" {
			filters += " AND " + spec.column + " = ?"
			args = append(args, value)
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("channel_id")); value != "" {
		channelID, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_channel_id")
			return
		}
		filters += " AND channel_id = ?"
		args = append(args, channelID)
	}
	if value := strings.TrimSpace(r.URL.Query().Get("log_type")); value != "" {
		logType, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_log_type")
			return
		}
		filters += " AND type = ?"
		args = append(args, logType)
	}
	pageArgs := append(append([]any{}, args...), limit+1, offset)
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogQueryTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, readonlyLogsListQuery+userFilter+filters+readonlyLogsListOrder, pageArgs...)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer rows.Close()
	items := []PassthroughLog{}
	for rows.Next() {
		var v PassthroughLog
		var created int64
		var content string
		if rows.Scan(&v.ID, &v.UserID, &created, &v.Type, &v.Username, &v.ModelName, &v.ChannelID, &v.TokenName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &v.UseTime, &v.RequestID, &content, &v.Group, &v.IP, &v.IsStream, &v.Other) != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		v.CreatedAt = time.Unix(created, 0).UTC()
		v.ContentSummary = redactSummary(content)
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	h.audit(r, site, "passthrough.logs", map[string]any{"user_ids": ids, "start_time": start, "end_time": end, "limit": limit, "offset": offset})
	writeDashboardJSON(w, 200, map[string]any{"items": items, "configured": true, "total": offset + len(items), "has_more": hasMore})
}

func (h *PassthroughHandler) LogStat(w http.ResponseWriter, r *http.Request) {
	site, ids, err := passthroughScope(r)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	start, end, err := queryWindow(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_time_range")
		return
	}
	db, configured, err := h.database(site)
	if err != nil {
		writeDashboardError(w, 502, "readonly_connection_failed")
		return
	}
	if !configured {
		writeDashboardJSON(w, 200, map[string]any{"configured": false, "summary": PassthroughLogSummary{}})
		return
	}
	where := ""
	args := make([]any, 0, len(ids)+6)
	if len(ids) > 0 {
		where += " AND user_id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	for _, spec := range []struct{ param, column string }{{"token_name", "token_name"}, {"username", "username"}, {"group", "`group`"}, {"model_name", "model_name"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(spec.param)); value != "" {
			where += " AND " + spec.column + " = ?"
			args = append(args, value)
		}
	}
	var channelID *int64
	if value := strings.TrimSpace(r.URL.Query().Get("channel_id")); value != "" {
		parsedChannelID, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_channel_id")
			return
		}
		where += " AND channel_id = ?"
		args = append(args, parsedChannelID)
		channelID = &parsedChannelID
	}
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogQueryTimeout)
	defer cancel()
	var summary PassthroughLogSummary
	quotaFrom, quotaTo, useRollup := completeHourWindow(start, end)
	if useRollup && h.readonlyRollupReady(ctx, site, quotaFrom) {
		consumeType := 2
		queryValues := map[string]string{"username": strings.TrimSpace(r.URL.Query().Get("username")), "model_name": strings.TrimSpace(r.URL.Query().Get("model_name")), "token_name": strings.TrimSpace(r.URL.Query().Get("token_name")), "group": strings.TrimSpace(r.URL.Query().Get("group"))}
		local, localErr := h.Rollups.QueryReadonlyLogRollup(ctx, readonlyRollupFilter(site, ids, quotaFrom, quotaTo, queryValues, &consumeType, channelID))
		if localErr != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		summary.Quota = local.QuotaSum
		if start.Before(quotaFrom) {
			value, rawErr := queryRawQuota(ctx, db, start, minTime(end, quotaFrom), where, args)
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			summary.Quota += value
		}
		if quotaTo.Before(end) {
			value, rawErr := queryRawQuota(ctx, db, maxTime(start, quotaTo), end, where, args)
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			summary.Quota += value
		}
	} else if value, rawErr := queryRawQuota(ctx, db, start, end, where, args); rawErr != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	} else {
		summary.Quota = value
	}
	rateArgs := append([]any{time.Now().Add(-60 * time.Second).Unix()}, args...)
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(prompt_tokens),0)+COALESCE(SUM(completion_tokens),0) FROM logs WHERE created_at>=? AND type=2`+where, rateArgs...).Scan(&summary.RPM, &summary.TPM); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	h.audit(r, site, "passthrough.logs.stat", map[string]any{"user_ids": ids, "start_time": start, "end_time": end})
	writeDashboardJSON(w, 200, map[string]any{"configured": true, "summary": summary})
}

func (h *PassthroughHandler) LogCount(w http.ResponseWriter, r *http.Request) {
	site, ids, err := passthroughScope(r)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	start, end, err := queryWindow(r)
	if err != nil {
		writeDashboardError(w, 400, "invalid_time_range")
		return
	}
	db, configured, err := h.database(site)
	if err != nil {
		writeDashboardError(w, 502, "readonly_connection_failed")
		return
	}
	if !configured {
		writeDashboardJSON(w, 200, map[string]any{"configured": false, "total": 0})
		return
	}
	where := ""
	args := []any{start.Unix(), end.Unix()}
	if len(ids) > 0 {
		where += " AND user_id IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	for _, spec := range []struct{ param, column string }{{"token_name", "token_name"}, {"username", "username"}, {"group", "`group`"}, {"model_name", "model_name"}, {"request_id", "request_id"}} {
		if value := strings.TrimSpace(r.URL.Query().Get(spec.param)); value != "" {
			where += " AND " + spec.column + " = ?"
			args = append(args, value)
		}
	}
	var channelID *int64
	if value := strings.TrimSpace(r.URL.Query().Get("channel_id")); value != "" {
		parsedChannelID, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_channel_id")
			return
		}
		where += " AND channel_id = ?"
		args = append(args, parsedChannelID)
		channelID = &parsedChannelID
	}
	var logType *int
	if value := strings.TrimSpace(r.URL.Query().Get("log_type")); value != "" {
		parsedLogType, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			writeDashboardError(w, 400, "invalid_log_type")
			return
		}
		where += " AND type = ?"
		args = append(args, parsedLogType)
		logType = &parsedLogType
	}
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogCountTimeout)
	defer cancel()
	var total int64
	rollupFrom, rollupTo, useRollup := completeHourWindow(start, end)
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))
	if useRollup && requestID == "" && h.readonlyRollupReady(ctx, site, rollupFrom) {
		queryValues := map[string]string{"username": strings.TrimSpace(r.URL.Query().Get("username")), "model_name": strings.TrimSpace(r.URL.Query().Get("model_name")), "token_name": strings.TrimSpace(r.URL.Query().Get("token_name")), "group": strings.TrimSpace(r.URL.Query().Get("group"))}
		local, localErr := h.Rollups.QueryReadonlyLogRollup(ctx, readonlyRollupFilter(site, ids, rollupFrom, rollupTo, queryValues, logType, channelID))
		if localErr != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		total = local.RequestCount
		if start.Before(rollupFrom) {
			value, rawErr := queryRawCount(ctx, db, start, minTime(end, rollupFrom), where, args[2:])
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			total += value
		}
		if rollupTo.Before(end) {
			value, rawErr := queryRawCount(ctx, db, maxTime(start, rollupTo), end, where, args[2:])
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			total += value
		}
	} else if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE created_at>=? AND created_at<?`+where, args...).Scan(&total); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	h.audit(r, site, "passthrough.logs.count", map[string]any{"user_ids": ids, "start_time": start, "end_time": end, "total": total})
	writeDashboardJSON(w, 200, map[string]any{"configured": true, "total": total})
}

func (h *PassthroughHandler) readonlyRollupReady(ctx context.Context, site string, from time.Time) bool {
	if h.Rollups == nil {
		return false
	}
	cursor, err := h.Rollups.ReadonlyLogRollupCursor(ctx, site)
	return err == nil && cursor.Initialized && cursor.CoverageFrom != nil && !from.Before(*cursor.CoverageFrom) && cursor.CaughtUpAt != nil && time.Since(*cursor.CaughtUpAt) < 2*time.Minute
}

func queryRawQuota(ctx context.Context, db *sql.DB, start, end time.Time, where string, filterArgs []any) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	args := append([]any{start.Unix(), end.Unix()}, filterArgs...)
	var value int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(quota),0) FROM logs WHERE created_at>=? AND created_at<? AND type=2`+where, args...).Scan(&value)
	return value, err
}

func queryRawCount(ctx context.Context, db *sql.DB, start, end time.Time, where string, filterArgs []any) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	args := append([]any{start.Unix(), end.Unix()}, filterArgs...)
	var value int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs WHERE created_at>=? AND created_at<?`+where, args...).Scan(&value)
	return value, err
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
