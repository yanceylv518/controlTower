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
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	return h.logsPageForBilling(ctx, site, 0, 0, -1, start, end, cursor, limit)
}
func (h *PassthroughHandler) DetailedLogsPageForBilling(ctx context.Context, site string, userID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, userID, 0, -1, start, end, cursor, limit)
}
func (h *PassthroughHandler) TokenDetailedLogsPageForBilling(ctx context.Context, site string, userID, tokenID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, userID, 0, tokenID, start, end, cursor, limit)
}
func (h *PassthroughHandler) ChannelLogsPageForBilling(ctx context.Context, site string, channelID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return h.logsPageForBilling(ctx, site, 0, channelID, -1, start, end, cursor, limit)
}

func (h *PassthroughHandler) DetailedChannelsLogsPageForBilling(ctx context.Context, site string, channelIDs []int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	channelIDs = uniquePositiveIDs(channelIDs)
	if len(channelIDs) == 0 {
		return []billing.PagedLogRecord{}, nil
	}
	return h.logsPageForBillingChannels(ctx, site, channelIDs, start, end, cursor, limit)
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (h *PassthroughHandler) ValidateBillingIndexes(ctx context.Context, site string, _ bool) error {
	db, configured, err := h.database(site)
	if err != nil {
		return err
	}
	if !configured {
		return fmt.Errorf("readonly database is not configured for %s", site)
	}
	queryCtx, cancel := context.WithTimeout(ctx, readonlyQueryTimeout)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT INDEX_NAME,SEQ_IN_INDEX,COLUMN_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='logs' ORDER BY INDEX_NAME,SEQ_IN_INDEX`)
	if err != nil {
		return fmt.Errorf("billing index inspection site=%s: %w", site, err)
	}
	defer rows.Close()
	indexes := map[string][]string{}
	for rows.Next() {
		var name, column string
		var sequence int
		if err = rows.Scan(&name, &sequence, &column); err != nil {
			return err
		}
		indexes[name] = append(indexes[name], strings.ToLower(column))
	}
	hasPrefix := func(prefix ...string) bool {
		for _, columns := range indexes {
			if len(columns) < len(prefix) {
				continue
			}
			matched := true
			for i := range prefix {
				matched = matched && columns[i] == prefix[i]
			}
			if matched {
				return true
			}
		}
		return false
	}
	// InnoDB secondary indexes contain the primary key, so NewAPI's existing
	// created_at-leading index can also support the (created_at,id) keyset. CT
	// must not require customers to alter the NewAPI schema for billing.
	if !hasPrefix("created_at") {
		return fmt.Errorf("newapi logs requires an existing index beginning with created_at")
	}
	return rows.Err()
}

const billingLogsPageTimeout = 2 * time.Minute

func (h *PassthroughHandler) logsPageForBilling(ctx context.Context, site string, userID, channelID, tokenID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
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
	// `other` can contain large provider diagnostics. Billing only needs the
	// cache-usage fields below, so project a compact JSON value inside MySQL
	// instead of transferring the complete payload over the RDS connection.
	query, _ := billingLogsPageQuery(userID, channelID, tokenID)
	args := []any{start.Unix(), end.Unix()}
	args = append(args, cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID)
	if userID > 0 {
		args = append(args, userID)
	}
	if channelID > 0 {
		args = append(args, channelID)
	}
	if tokenID >= 0 {
		args = append(args, tokenID)
	}
	args = append(args, limit)
	// Large users and channels can still need more than 30 seconds for the
	// first indexed page on a remote new-api database. Keep every page bounded,
	// but use the same ceiling as the existing detailed billing-log read path.
	queryCtx, cancel := context.WithTimeout(ctx, billingLogsPageTimeout)
	rows, err := db.QueryContext(queryCtx, query, args...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("billing logs page site=%s user=%d channel=%d cursor=%d/%d: %w", site, userID, channelID, cursor.CreatedUnix, cursor.ID, err)
	}
	defer cancel()
	defer rows.Close()
	out := make([]billing.PagedLogRecord, 0, limit)
	return scanBillingLogRows(rows, out)
}

func (h *PassthroughHandler) logsPageForBillingChannels(ctx context.Context, site string, channelIDs []int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
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
	query := billingChannelsLogsPageQuery(len(channelIDs))
	args := make([]any, 0, 6+len(channelIDs))
	args = append(args, start.Unix(), end.Unix(), cursor.CreatedUnix, cursor.CreatedUnix, cursor.ID)
	for _, id := range channelIDs {
		args = append(args, id)
	}
	args = append(args, limit)
	queryCtx, cancel := context.WithTimeout(ctx, billingLogsPageTimeout)
	rows, err := db.QueryContext(queryCtx, query, args...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("billing channel logs page site=%s channels=%d cursor=%d/%d: %w", site, len(channelIDs), cursor.CreatedUnix, cursor.ID, err)
	}
	defer cancel()
	defer rows.Close()
	return scanBillingLogRows(rows, make([]billing.PagedLogRecord, 0, limit))
}

func scanBillingLogRows(rows *sql.Rows, out []billing.PagedLogRecord) ([]billing.PagedLogRecord, error) {
	for rows.Next() {
		var v billing.PagedLogRecord
		var other string
		if err := rows.Scan(&v.ID, &v.CreatedUnix, &v.RequestID, &v.UpstreamRequestID, &v.UserID, &v.Username, &v.TokenID, &v.TokenName, &v.ChannelID, &v.ChannelName, &v.ModelName, &v.GroupName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &other); err != nil {
			return nil, err
		}
		cache := normalizeChannelTestBillingUsage(v.TokenName, parseBillingCacheUsage(other))
		v.SourcePromptTokens = v.PromptTokens
		if v.PromptTokens.Valid {
			cache = resolveBillingCacheSemantic(cache, v.PromptTokens.Int64)
		}
		v.CacheTokens, v.CacheWriteTokens = cache.Read, cache.Write
		v.CacheWrite5mTokens, v.CacheWrite1hTokens = cache.Write5m, cache.Write1h
		v.UsageSemantic = cache.Semantic
		v.ModelPrice, v.ModelRatio, v.CompletionRatio = cache.ModelPrice, cache.ModelRatio, cache.CompletionRatio
		v.CacheRatio, v.CacheCreationRatio, v.GroupRatio = cache.CacheRatio, cache.CacheCreationRatio, cache.GroupRatio
		v.CacheCreationRatio5m, v.CacheCreationRatio1h = cache.CacheCreationRatio5m, cache.CacheCreationRatio1h
		v.ImageRatio = cache.ImageRatio
		v.BillingMode, v.ExprBase64, v.MatchedTier, v.RequestRules = cache.BillingMode, cache.ExprBase64, cache.MatchedTier, cache.RequestRules
		v.ToolSurcharges = cache.ToolSurcharges
		v.ImageInputTokens, v.ImageOutputTokens = cache.ImageInput, cache.ImageOutput
		v.AudioInputTokens, v.AudioOutputTokens = cache.AudioInput, cache.AudioOutput
		if v.PromptTokens.Valid {
			v.PromptTokens.Int64, v.ContextTokens = normalizedBillingPromptTokens(v.PromptTokens.Int64, cache)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// NewAPI's channel-test controller records provider cache diagnostics in the
// log, but settleTestQuota bills the complete prompt and does not apply cache
// ratios. Keeping those diagnostics as billing lanes makes CT undercharge
// every test request that reports cached tokens.
func normalizeChannelTestBillingUsage(tokenName string, usage billingCacheUsage) billingCacheUsage {
	if strings.TrimSpace(tokenName) == "模型测试" {
		usage.Read, usage.Write, usage.Write5m, usage.Write1h = 0, 0, 0, 0
	}
	return usage
}

func normalizedBillingPromptTokens(rawPrompt int64, usage billingCacheUsage) (int64, int64) {
	prompt, contextTokens := rawPrompt, rawPrompt
	if usage.Semantic == "anthropic" {
		contextTokens += usage.Read + usage.Write
	} else {
		prompt -= usage.Read + usage.Write
	}
	// NewAPI removes image input from the base prompt lane and adds it back
	// with image_ratio. Clamp before charging because cache plus image usage can
	// overlap and exceed the source prompt total.
	prompt -= usage.ImageInput
	if prompt < 0 {
		prompt = 0
	}
	return prompt, contextTokens
}

func billingLogsPageQuery(userID, channelID, tokenID int64) (string, bool) {
	from := ` FROM logs l`
	query := `SELECT l.id,l.created_at,COALESCE(l.request_id,''),COALESCE(l.upstream_request_id,''),l.user_id,COALESCE(l.username,''),COALESCE(l.token_id,0),COALESCE(l.token_name,''),COALESCE(l.channel_id,0),COALESCE(c.name,''),COALESCE(l.model_name,''),COALESCE(l.` + "`group`" + `,''),l.prompt_tokens,l.completion_tokens,l.quota,` + billingOtherProjection + from + ` LEFT JOIN channels c ON c.id=l.channel_id WHERE l.type=2 AND l.created_at>=? AND l.created_at<?`
	// Keep pagination aligned with the date range and NewAPI's existing
	// created_at-leading index. Paging by user_id/id alone makes the first page
	// scan a user's entire history before reaching the requested billing period.
	query += ` AND (l.created_at>? OR (l.created_at=? AND l.id>?))`
	if userID > 0 {
		query += ` AND l.user_id=?`
	}
	if channelID > 0 {
		query += ` AND l.channel_id=?`
	}
	if tokenID >= 0 {
		query += ` AND COALESCE(l.token_id,0)=?`
	}
	return query + ` ORDER BY l.created_at,l.id LIMIT ?`, false
}

func billingChannelsLogsPageQuery(channelCount int) string {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", channelCount), ",")
	return `SELECT l.id,l.created_at,COALESCE(l.request_id,''),COALESCE(l.upstream_request_id,''),l.user_id,COALESCE(l.username,''),COALESCE(l.token_id,0),COALESCE(l.token_name,''),COALESCE(l.channel_id,0),COALESCE(c.name,''),COALESCE(l.model_name,''),COALESCE(l.` + "`group`" + `,''),l.prompt_tokens,l.completion_tokens,l.quota,` + billingOtherProjection +
		` FROM logs l LEFT JOIN channels c ON c.id=l.channel_id WHERE l.type=2 AND l.created_at>=? AND l.created_at<? AND (l.created_at>? OR (l.created_at=? AND l.id>?)) AND l.channel_id IN (` + placeholders + `) ORDER BY l.created_at,l.id LIMIT ?`
}

const billingOtherProjection = `CASE WHEN JSON_VALID(l.other) THEN JSON_OBJECT(` +
	`'usage_semantic',JSON_EXTRACT(l.other,'$.usage_semantic'),` +
	`'usage_billing_path',JSON_EXTRACT(l.other,'$.admin_info.usage_billing_path'),` +
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
	`'claude_cache_creation_1_h_tokens',JSON_EXTRACT(l.other,'$.claude_cache_creation_1_h_tokens'),` +
	`'model_price',JSON_EXTRACT(l.other,'$.model_price'),` +
	`'model_ratio',JSON_EXTRACT(l.other,'$.model_ratio'),` +
	`'completion_ratio',JSON_EXTRACT(l.other,'$.completion_ratio'),` +
	`'cache_ratio',JSON_EXTRACT(l.other,'$.cache_ratio'),` +
	`'cache_creation_ratio',JSON_EXTRACT(l.other,'$.cache_creation_ratio'),` +
	`'cache_creation_ratio_5m',JSON_EXTRACT(l.other,'$.cache_creation_ratio_5m'),` +
	`'cache_creation_ratio_1h',JSON_EXTRACT(l.other,'$.cache_creation_ratio_1h'),` +
	`'group_ratio',JSON_EXTRACT(l.other,'$.group_ratio'),` +
	`'billing_mode',JSON_EXTRACT(l.other,'$.billing_mode'),` +
	`'expr_b64',JSON_EXTRACT(l.other,'$.expr_b64'),` +
	`'matched_tier',JSON_EXTRACT(l.other,'$.matched_tier'),` +
	`'request_rules',JSON_EXTRACT(l.other,'$.request_rules'),` +
	`'tool_surcharges',JSON_EXTRACT(l.other,'$.tool_surcharges'),` +
	`'image_ratio',JSON_EXTRACT(l.other,'$.image_ratio'),` +
	`'image_tokens',JSON_EXTRACT(l.other,'$.image_tokens'),` +
	`'image_input',JSON_EXTRACT(l.other,'$.image_input'),` +
	`'image_output',JSON_EXTRACT(l.other,'$.image_output'),` +
	`'image_output_tokens',JSON_EXTRACT(l.other,'$.image_output_tokens'),` +
	`'audio_input_token_count',JSON_EXTRACT(l.other,'$.audio_input_token_count'),` +
	`'audio_input',JSON_EXTRACT(l.other,'$.audio_input'),` +
	`'audio_output',JSON_EXTRACT(l.other,'$.audio_output')) ELSE '{}' END`

func (h *PassthroughHandler) RatioSnapshotForBilling(ctx context.Context, site string) (string, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return "", err
	}
	if !configured {
		return "", fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, "SELECT `key`,value FROM options WHERE `key` IN ('ModelRatio','CompletionRatio','CacheRatio','CreateCacheRatio','GroupRatio','QuotaPerUnit','USDExchangeRate','DisplayInCurrencyEnabled','general_setting','general_setting.quota_display_type','general_setting.custom_currency_symbol','general_setting.custom_currency_exchange_rate')")
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
		values["ct.quota_per_unit_source"] = "newapi_builtin_default_500000"
	} else {
		values["ct.quota_per_unit_source"] = "newapi_options"
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
	rows, err := db.QueryContext(ctx, `SELECT id,COALESCE(name,''),status,COALESCE(models,'') FROM channels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []billing.ConfiguredChannel{}
	for rows.Next() {
		var item billing.ConfiguredChannel
		if err = rows.Scan(&item.ChannelID, &item.ChannelName, &item.Status, &item.Models); err != nil {
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

// new-api multi-key channel values are intentionally fingerprinted and tailed as
// one stored bundle. Never split or return the plaintext key from this query.
const upstreamChannelMappingsQuery = "SELECT id,COALESCE(name,''),COALESCE(base_url,''),SHA2(CONCAT(COALESCE(base_url,''),'|',COALESCE(`key`,'')),256),RIGHT(COALESCE(`key`,''),4) FROM channels"

func (h *PassthroughHandler) UpstreamChannelMappingsForBilling(ctx context.Context, site string) ([]billing.UpstreamChannelMapping, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return nil, err
	}
	if !configured {
		return nil, fmt.Errorf("readonly database is not configured for %s", site)
	}
	queryCtx, cancel := context.WithTimeout(ctx, readonlyQueryTimeout)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, upstreamChannelMappingsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []billing.UpstreamChannelMapping{}
	for rows.Next() {
		var v billing.UpstreamChannelMapping
		v.InstanceID = site
		v.UpdatedAt = time.Now().UTC()
		if err = rows.Scan(&v.ChannelID, &v.ChannelName, &v.BaseURL, &v.UpstreamFP, &v.KeyTail); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s BillingReadonlySource) LogsPage(ctx context.Context, site string, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.LogsPageForBilling(ctx, site, start, end, cursor, limit)
}
func (s BillingReadonlySource) ValidateBillingIndexes(ctx context.Context, site string, userScoped bool) error {
	return s.Handler.ValidateBillingIndexes(ctx, site, userScoped)
}
func (s BillingReadonlySource) DetailedLogsPage(ctx context.Context, site string, userID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.DetailedLogsPageForBilling(ctx, site, userID, start, end, cursor, limit)
}
func (s BillingReadonlySource) TokenDetailedLogsPage(ctx context.Context, site string, userID, tokenID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.TokenDetailedLogsPageForBilling(ctx, site, userID, tokenID, start, end, cursor, limit)
}
func (s BillingReadonlySource) UpstreamChannelMappings(ctx context.Context, site string) ([]billing.UpstreamChannelMapping, error) {
	return s.Handler.UpstreamChannelMappingsForBilling(ctx, site)
}
func (s BillingReadonlySource) ChannelLogsPage(ctx context.Context, site string, channelID int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.ChannelLogsPageForBilling(ctx, site, channelID, start, end, cursor, limit)
}
func (s BillingReadonlySource) DetailedChannelsLogsPage(ctx context.Context, site string, channelIDs []int64, start, end time.Time, cursor billing.LogCursor, limit int) ([]billing.PagedLogRecord, error) {
	return s.Handler.DetailedChannelsLogsPageForBilling(ctx, site, channelIDs, start, end, cursor, limit)
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
	Read, Write, Write5m, Write1h                                           int64
	ImageInput, ImageOutput, AudioInput, AudioOutput                        int64
	Semantic                                                                string
	ModelPrice, ModelRatio, CompletionRatio, CacheRatio, CacheCreationRatio string
	CacheCreationRatio5m, CacheCreationRatio1h, GroupRatio, ImageRatio      string
	BillingMode, ExprBase64, MatchedTier, RequestRules, ToolSurcharges      string
	UsageBillingPath                                                        string
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
	decimal := func(key string) string {
		value, ok := values[key]
		if !ok || value == nil {
			return ""
		}
		switch typed := value.(type) {
		case float64:
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case json.Number:
			return typed.String()
		case string:
			if _, err := strconv.ParseFloat(typed, 64); err == nil {
				return typed
			}
		}
		return ""
	}
	usageBillingPath := ""
	if raw, ok := values["usage_billing_path"].(string); ok {
		usageBillingPath = strings.ToLower(strings.TrimSpace(raw))
	} else if admin, ok := values["admin_info"].(map[string]any); ok {
		usageBillingPath = strings.ToLower(strings.TrimSpace(stringValue(admin, "usage_billing_path")))
	}
	imageInput := number("image_input", "image_tokens")
	imageOutput := number("image_output_tokens")
	// NewAPI writes PromptTokensDetails.ImageTokens to the misleading
	// `image_output` log key. Whether those tokens were split out of the
	// ordinary prompt lane and billed with image_ratio depends on the NewAPI
	// version, not on which billing path produced the usage: every version
	// that stamps admin_info.usage_billing_path bills the image lane on all
	// paths (upstream passthrough and the openai/gemini conversion paths
	// alike), while older versions without that key logged `image_output` as
	// diagnostic metadata and kept the tokens in the ordinary prompt lane.
	// Explicit image input/output fields remain authoritative.
	if imageInput == 0 && imageOutput == 0 && usageBillingPath != "" {
		imageInput = number("image_output")
	}
	return billingCacheUsage{
		Read: read, Write: write, Write5m: write5m, Write1h: write1h, Semantic: semantic,
		ImageInput: imageInput, ImageOutput: imageOutput,
		AudioInput: number("audio_input", "audio_input_token_count"), AudioOutput: number("audio_output"),
		ModelPrice: decimal("model_price"), ModelRatio: decimal("model_ratio"), CompletionRatio: decimal("completion_ratio"),
		CacheRatio: decimal("cache_ratio"), CacheCreationRatio: decimal("cache_creation_ratio"),
		CacheCreationRatio5m: decimal("cache_creation_ratio_5m"), CacheCreationRatio1h: decimal("cache_creation_ratio_1h"),
		ImageRatio: decimal("image_ratio"),
		GroupRatio: decimal("group_ratio"), BillingMode: stringValue(values, "billing_mode"), ExprBase64: stringValue(values, "expr_b64"),
		MatchedTier: stringValue(values, "matched_tier"), RequestRules: jsonValue(values, "request_rules"),
		ToolSurcharges: jsonValue(values, "tool_surcharges"), UsageBillingPath: usageBillingPath,
	}
}

func stringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func jsonValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Legacy text-only logs can omit the Anthropic marker while reporting cache
// outside prompt_tokens, so cache exceeding prompt remains a compatibility
// hint only when the row has no per-request pricing snapshot. Modern NewAPI
// rows can report cached tokens slightly above prompt_tokens while still using
// OpenAI semantics; treating those rows as Anthropic charges prompt_tokens a
// second time. Explicit markers always win.
func resolveBillingCacheSemantic(cache billingCacheUsage, promptTokens int64) billingCacheUsage {
	modernSnapshot := cache.ModelPrice != "" || cache.ModelRatio != "" || cache.CompletionRatio != "" || cache.CacheRatio != "" || cache.CacheCreationRatio != "" || cache.CacheCreationRatio5m != "" || cache.CacheCreationRatio1h != "" || cache.GroupRatio != "" || cache.ImageRatio != "" || cache.BillingMode != "" || cache.ExprBase64 != "" || cache.MatchedTier != "" || cache.RequestRules != "" || cache.ToolSurcharges != ""
	if cache.Semantic != "anthropic" && !modernSnapshot && cache.ImageInput == 0 && cache.ImageOutput == 0 && cache.AudioInput == 0 && cache.AudioOutput == 0 && cache.Read+cache.Write > promptTokens {
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
	// 日志页会并发请求列表、统计和总数，连接池至少要覆盖这三个只读请求。
	readonlyDBMaxOpenConns = 3
	readonlyLogsListQuery  = `SELECT l.id,l.user_id,l.created_at,l.type,COALESCE(l.username,''),COALESCE(l.model_name,''),COALESCE(l.channel_id,0),COALESCE(l.token_id,0),COALESCE(l.token_name,''),COALESCE(l.prompt_tokens,0),COALESCE(l.completion_tokens,0),COALESCE(l.quota,0),COALESCE(l.use_time,0),COALESCE(l.request_id,''),COALESCE(l.upstream_request_id,''),COALESCE(l.content,''),COALESCE(l.` + "`group`" + `,''),COALESCE(l.ip,''),COALESCE(l.is_stream,0),COALESCE(l.other,'') FROM logs l WHERE l.created_at>=? AND l.created_at<?`
	readonlyLogsListOrder  = ` ORDER BY l.created_at DESC,l.id DESC LIMIT ? OFFSET ?`
	readonlyLogRateQuery   = `SELECT COUNT(*),COALESCE(SUM(l.prompt_tokens),0)+COALESCE(SUM(l.completion_tokens),0) FROM logs l WHERE l.created_at>=? AND l.created_at<?`
)

func configureReadonlyDB(db *sql.DB) {
	db.SetMaxOpenConns(readonlyDBMaxOpenConns)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
}

type PassthroughHandler struct {
	Config       ReadonlyConfigStore
	Audit        PassthroughAuditStore
	Rollups      ReadonlyLogRollupStore
	SecretKey    string
	mu           sync.Mutex
	pools        map[string]passthroughPool
	summaryCache *readonlyQueryCache
	rawSummaries readonlyRawSummaryGroup
	summarySlots map[*sql.DB]chan struct{}
	channelNames map[readonlyChannelKey]readonlyChannelName
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

// ListUserBalances is the background-runner counterpart of Users. It performs
// one bounded, read-only query for a site and deliberately omits pagination so
// alert evaluation has a consistent snapshot.
func (h *PassthroughHandler) ListUserBalances(ctx context.Context, site string) ([]PassthroughUser, error) {
	db, configured, err := h.database(site)
	if err != nil || !configured {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, readonlyQueryTimeout)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, `SELECT id,username,COALESCE(display_name,''),quota,used_quota,status,COALESCE(created_at,0),COALESCE(last_login_at,0) FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PassthroughUser{}
	for rows.Next() {
		var v PassthroughUser
		if err := rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.Quota, &v.UsedQuota, &v.Status, &v.CreatedAt, &v.LastLoginAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

type PassthroughLog struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	CreatedAt         time.Time `json:"created_at"`
	Type              int       `json:"type"`
	Username          string    `json:"username"`
	ModelName         string    `json:"model_name"`
	ChannelID         int64     `json:"channel_id"`
	Channel           int64     `json:"channel"`
	ChannelName       string    `json:"channel_name"`
	TokenID           int64     `json:"token_id"`
	TokenName         string    `json:"token_name"`
	PromptTokens      int64     `json:"prompt_tokens"`
	CompletionTokens  int64     `json:"completion_tokens"`
	Quota             int64     `json:"quota"`
	UseTime           int64     `json:"use_time"`
	RequestID         string    `json:"request_id"`
	UpstreamRequestID string    `json:"upstream_request_id"`
	Content           string    `json:"content"`
	ContentSummary    string    `json:"content_summary"`
	Group             string    `json:"group"`
	IP                string    `json:"ip"`
	IsStream          bool      `json:"is_stream"`
	// Fallback 表示同一请求是否实际尝试过多个渠道；它不依赖 admin_info 投影。
	FallbackChecked  bool     `json:"fallback_checked"`
	Fallback         bool     `json:"fallback"`
	FallbackChannels []string `json:"fallback_channels,omitempty"`
	// FallbackIndex/Total 标识当前日志在完整链路中的尝试序号；viewer 只接收布尔事实。
	FallbackIndex int    `json:"fallback_index,omitempty"`
	FallbackTotal int    `json:"fallback_total,omitempty"`
	Other         string `json:"other"`
}

// readonlyRequestKey 用请求 ID 和用户 ID 组成批量标记的稳定键，避免不同用户复用
// 非规范请求 ID 时被误判为同一条 fallback 链路。
func readonlyRequestKey(requestID string, userID int64) string {
	return requestID + "\x00" + strconv.FormatInt(userID, 10)
}

func appendReadonlyFallbackChannel(channels []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return channels
	}
	for _, current := range channels {
		if current == value {
			return channels
		}
	}
	return append(channels, value)
}

// readonlyStringList 兼容 use_channel 的数字数组、字符串数组和旧版链路文本。
// 仅保留非空的渠道标识，避免把损坏的 JSON 变成一个假 fallback。
func readonlyStringList(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		channels := make([]string, 0, len(values))
		for _, item := range values {
			var text string
			if json.Unmarshal(item, &text) == nil {
				channels = appendReadonlyFallbackChannel(channels, text)
				continue
			}
			var number json.Number
			if json.Unmarshal(item, &number) == nil {
				channels = appendReadonlyFallbackChannel(channels, number.String())
			}
		}
		return channels
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	// 历史日志可能保存为 "1->2"、"1 → 2" 或逗号分隔文本。
	text = strings.NewReplacer("->", " ", "→", " ", ",", " ").Replace(text)
	parts := strings.Fields(text)
	channels := make([]string, 0, len(parts))
	for _, part := range parts {
		channels = appendReadonlyFallbackChannel(channels, part)
	}
	return channels
}

// readonlyStringSequence 保留 use_channel 中的重复渠道。相同渠道的连续重试
// 仍是不同尝试，不能像对外展示的去重列表一样合并。
func readonlyStringSequence(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		sequence := make([]string, 0, len(values))
		for _, item := range values {
			var text string
			if json.Unmarshal(item, &text) == nil {
				sequence = appendReadonlySequenceValue(sequence, text)
				continue
			}
			var number json.Number
			if json.Unmarshal(item, &number) == nil {
				sequence = appendReadonlySequenceValue(sequence, number.String())
			}
		}
		return sequence
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return nil
	}
	text = strings.NewReplacer("->", " ", "→", " ", ",", " ").Replace(text)
	parts := strings.Fields(text)
	sequence := make([]string, 0, len(parts))
	for _, part := range parts {
		sequence = appendReadonlySequenceValue(sequence, part)
	}
	return sequence
}

func appendReadonlySequenceValue(sequence []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return sequence
	}
	return append(sequence, value)
}

// readonlyLogAttemptChannels 提取原始 use_channel 顺序；旧日志没有该字段时回退到 fallback_channels。
func readonlyLogAttemptChannels(value string) []string {
	var values map[string]json.RawMessage
	if value == "" || json.Unmarshal([]byte(value), &values) != nil || values == nil {
		return nil
	}
	keys := []string{"use_channel", "fallback_channels"}
	for _, key := range keys {
		if sequence := readonlyStringSequence(values[key]); len(sequence) > 0 {
			return sequence
		}
	}
	if adminRaw, ok := values["admin_info"]; ok {
		var adminInfo map[string]json.RawMessage
		if json.Unmarshal(adminRaw, &adminInfo) == nil && adminInfo != nil {
			for _, key := range keys {
				if sequence := readonlyStringSequence(adminInfo[key]); len(sequence) > 0 {
					return sequence
				}
			}
		}
	}
	return nil
}

func readonlyBool(raw json.RawMessage) bool {
	var value bool
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "1", "true", "yes", "y":
			return true
		}
	}
	return false
}

// readonlyLogFallbackInfo 从显式标志或 use_channel 链路提取可公开的 fallback 事实。
// admin_info 仍按原角色规则投影；这里只返回是否发生过 fallback 以及管理员可用的渠道链。
func readonlyLogFallbackInfo(value string) (bool, []string) {
	var values map[string]json.RawMessage
	if value == "" || json.Unmarshal([]byte(value), &values) != nil || values == nil {
		return false, nil
	}
	channels := readonlyStringList(values["fallback_channels"])
	if len(channels) == 0 {
		channels = readonlyStringList(values["use_channel"])
	}
	if adminRaw, ok := values["admin_info"]; ok {
		var adminInfo map[string]json.RawMessage
		if json.Unmarshal(adminRaw, &adminInfo) == nil && adminInfo != nil {
			if len(channels) == 0 {
				channels = readonlyStringList(adminInfo["fallback_channels"])
			}
			if len(channels) == 0 {
				channels = readonlyStringList(adminInfo["use_channel"])
			}
			for _, key := range []string{"fallback", "is_fallback", "fallback_flag"} {
				if readonlyBool(adminInfo[key]) {
					return true, channels
				}
			}
		}
	}
	for _, key := range []string{"fallback", "is_fallback", "fallback_flag"} {
		if readonlyBool(values[key]) {
			return true, channels
		}
	}
	return len(channels) > 1, channels
}

type readonlyFallbackRecord struct {
	id        int64
	requestID string
	userID    int64
	typeID    int
	channelID int64
	createdAt int64
	other     string
}

type readonlyFallbackChain struct {
	channels  []string
	indexByID map[int64]int
}

func readonlySequencePrefix(prefix, full []string) bool {
	if len(prefix) > len(full) {
		return false
	}
	for index, value := range prefix {
		if value != full[index] {
			return false
		}
	}
	return true
}

func readonlyFallbackChainFor(records []readonlyFallbackRecord) readonlyFallbackChain {
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].createdAt != records[j].createdAt {
			return records[i].createdAt < records[j].createdAt
		}
		return records[i].id < records[j].id
	})
	paths := make([][]string, 0, len(records))
	sequences := make([][]string, len(records))
	for i, record := range records {
		sequences[i] = readonlyLogAttemptChannels(record.other)
		if path := sequences[i]; len(path) > 0 {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return readonlyFallbackChain{}
	}
	path := append([]string(nil), paths[0]...)
	for _, candidate := range paths[1:] {
		if len(candidate) > len(path) {
			path = append([]string(nil), candidate...)
		}
	}
	for _, candidate := range paths {
		if !readonlySequencePrefix(candidate, path) {
			return readonlyFallbackChain{}
		}
	}
	if len(path) <= 1 {
		return readonlyFallbackChain{}
	}

	indexByID := make(map[int64]int, len(records))
	used := make([]bool, len(path))
	for i, record := range records {
		sequence := sequences[i]
		index := -1
		if len(sequence) > 0 && readonlySequencePrefix(sequence, path) && path[len(sequence)-1] == strconv.FormatInt(record.channelID, 10) {
			index = len(sequence) - 1
		}
		if index < 0 {
			matches := make([]int, 0, 1)
			channel := strconv.FormatInt(record.channelID, 10)
			for position, value := range path {
				if value == channel {
					matches = append(matches, position)
				}
			}
			if len(matches) == 1 {
				index = matches[0]
			}
		}
		if index < 0 || index >= len(path) || used[index] {
			channel := strconv.FormatInt(record.channelID, 10)
			for position, value := range path {
				if !used[position] && value == channel {
					index = position
					break
				}
			}
		}
		if index >= 0 && index < len(path) && !used[index] {
			used[index] = true
			indexByID[record.id] = index + 1
		}
	}
	return readonlyFallbackChain{channels: path, indexByID: indexByID}
}

// readonlyRequestPairs 限定当前页的请求和用户组合，避免同名请求带入其它用户的链路。
func readonlyRequestPairs(items []PassthroughLog, only map[string]struct{}) (string, []any) {
	conditions := make([]string, 0, len(items))
	args := make([]any, 0, len(items)*2)
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item.RequestID == "" {
			continue
		}
		key := readonlyRequestKey(item.RequestID, item.UserID)
		if only != nil {
			if _, ok := only[key]; !ok {
				continue
			}
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		conditions = append(conditions, "(request_id = ? AND user_id = ?)")
		args = append(args, item.RequestID, item.UserID)
	}
	return strings.Join(conditions, " OR "), args
}

// markReadonlyFallbackRequests 批量检查请求的其它尝试，并为管理员补齐完整渠道链路。
func markReadonlyFallbackRequests(ctx context.Context, tx *sql.Tx, items []PassthroughLog, viewer bool) {
	where, args := readonlyRequestPairs(items, nil)
	if where == "" {
		return
	}
	query := `SELECT request_id,user_id,COUNT(*) FROM logs WHERE (` + where + `) GROUP BY request_id,user_id`
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
	detailWhere, detailArgs := readonlyRequestPairs(items, fallbackKeys)
	if detailWhere == "" {
		return
	}
	otherProjection := "COALESCE(other,'')"
	if viewer {
		// Still consume the lookup and check for errors before setting checked,
		// but do not transfer or parse chain metadata that viewer never receives.
		otherProjection = "''"
	}
	detailRows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(request_id,''),COALESCE(user_id,0),COALESCE(type,0),COALESCE(channel_id,0),COALESCE(created_at,0),`+otherProjection+` FROM logs WHERE (`+detailWhere+`) ORDER BY request_id,user_id,created_at,id`, detailArgs...)
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
		if _, ok := fallbackKeys[key]; ok && !viewer {
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

type readonlyLogFilters struct {
	where             string
	args              []any
	username          string
	modelName         string
	tokenName         string
	group             string
	requestID         string
	upstreamRequestID string
	logType           *int
	channelID         *int64
	statusCode        *int
	emptyOutput       bool
	fallbackFinalOnly bool
	hasLike           bool
	hasRequestFilter  bool
	hasRawFilter      bool
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
		delete(h.summarySlots, old.db)
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

func readonlyViewer(r *http.Request) bool {
	u, ok := ctauth.CurrentUser(r)
	return ok && u.Role == "viewer"
}

func placeholders(n int) string { return strings.TrimRight(strings.Repeat("?,", n), ",") }

// firstQueryValue 同时兼容 rc35 参数名和 Control Tower 旧参数名，空值会继续读取下一个别名。
func firstQueryValue(values url.Values, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(values.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

// readonlyLikePattern 保持 rc35 的显式通配符语义，同时限制模式复杂度，避免远端日志库被恶意模式拖垮。
func readonlyLikePattern(value, param string) (string, error) {
	if !strings.Contains(value, "%") {
		return value, nil
	}
	if strings.Contains(value, "%%") || strings.Count(value, "%") > 2 {
		return "", fmt.Errorf("invalid_%s_filter", param)
	}
	if utf8.RuneCountInString(strings.ReplaceAll(value, "%", "")) < 2 {
		return "", fmt.Errorf("invalid_%s_filter", param)
	}
	value = strings.ReplaceAll(value, "!", "!!")
	return strings.ReplaceAll(value, "_", "!_"), nil
}

func appendStatusCodeFilter(filters *readonlyLogFilters, code int) {
	// Factor the shared boundaries and separator rather than repeating six
	// complete alternatives at every text position. Keep both fields separate
	// so NULLs and cross-column text cannot change the matching semantics.
	field := `(status_code|statusCode|status[[:space:]]+code|error_code|["']code["'])`
	pattern := `(^|[^[:alnum:]_])(` + field + `[[:space:]]*[:=][[:space:]]*["']?|HTTP[[:space:]]+)` + strconv.Itoa(code) + `([^[:digit:]]|$)`
	filters.where += " AND (l.content REGEXP ? OR l.other REGEXP ?)"
	filters.args = append(filters.args, pattern, pattern)
}

func forceReadonlyLogType(filters *readonlyLogFilters, logType int) error {
	if filters.logType != nil {
		if *filters.logType != logType {
			return fmt.Errorf("invalid_filter_combination")
		}
		return nil
	}
	filters.where += " AND l.type = ?"
	filters.args = append(filters.args, logType)
	filters.logType = &logType
	return nil
}

func parseReadonlyBoolean(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "off", "no":
		return false, nil
	case "1", "true", "on", "yes":
		return true, nil
	default:
		return false, fmt.Errorf("invalid_empty_output")
	}
}

// parseReadonlyLogFilters 构造三类读取接口共用的 WHERE 条件，确保列表、COUNT 和统计不会出现筛选分叉。
func parseReadonlyLogFilters(values url.Values, userIDs []int64, viewer bool) (readonlyLogFilters, error) {
	return parseReadonlyLogFiltersMode(values, userIDs, viewer, false)
}

// cursorIdentity keeps the established cursor hash independent of SQL-only
// optimizations. Its predicates are used for hashing, never database execution.
func parseReadonlyLogFiltersMode(values url.Values, userIDs []int64, viewer, cursorIdentity bool) (readonlyLogFilters, error) {
	filters := readonlyLogFilters{}
	if len(userIDs) > 0 {
		filters.where += " AND l.user_id IN (" + placeholders(len(userIDs)) + ")"
		for _, id := range userIDs {
			filters.args = append(filters.args, id)
		}
	}

	addText := func(param, column string, fuzzy bool) error {
		value := strings.TrimSpace(values.Get(param))
		if value == "" {
			return nil
		}
		if fuzzy && strings.Contains(value, "%") {
			pattern, err := readonlyLikePattern(value, param)
			if err != nil {
				return err
			}
			filters.where += " AND l." + column + " LIKE ? ESCAPE '!'"
			filters.args = append(filters.args, pattern)
			filters.hasLike = true
			return nil
		}
		filters.where += " AND l." + column + " = ?"
		filters.args = append(filters.args, value)
		return nil
	}

	filters.username = strings.TrimSpace(values.Get("username"))
	filters.modelName = strings.TrimSpace(values.Get("model_name"))
	filters.tokenName = strings.TrimSpace(values.Get("token_name"))
	filters.group = strings.TrimSpace(values.Get("group"))
	filters.requestID = strings.TrimSpace(values.Get("request_id"))
	filters.upstreamRequestID = strings.TrimSpace(values.Get("upstream_request_id"))
	if err := addText("username", "username", true); err != nil {
		return filters, err
	}
	if err := addText("model_name", "model_name", true); err != nil {
		return filters, err
	}
	if err := addText("token_name", "token_name", false); err != nil {
		return filters, err
	}
	if err := addText("group", "`group`", false); err != nil {
		return filters, err
	}
	if err := addText("request_id", "request_id", false); err != nil {
		return filters, err
	}
	if err := addText("upstream_request_id", "upstream_request_id", false); err != nil {
		return filters, err
	}

	channelValue := firstQueryValue(values, "channel_id", "channel")
	if channelValue != "" {
		channelID, err := strconv.ParseInt(channelValue, 10, 64)
		if err != nil || channelID < 0 {
			return filters, fmt.Errorf("invalid_channel_id")
		}
		if channelID > 0 {
			filters.where += " AND l.channel_id = ?"
			filters.args = append(filters.args, channelID)
			filters.channelID = &channelID
		}
	}

	logTypeValue := firstQueryValue(values, "log_type", "type")
	if logTypeValue != "" {
		logType, err := strconv.Atoi(logTypeValue)
		if err != nil || logType < 0 {
			return filters, fmt.Errorf("invalid_log_type")
		}
		if logType > 0 {
			filters.where += " AND l.type = ?"
			filters.args = append(filters.args, logType)
			filters.logType = &logType
		}
	}

	statusCodeValue := firstQueryValue(values, "status_code")
	if statusCodeValue != "" {
		statusCode, parseErr := strconv.Atoi(statusCodeValue)
		if parseErr != nil || statusCode < 100 || statusCode > 599 {
			return filters, fmt.Errorf("invalid_status_code")
		}
		if err := forceReadonlyLogType(&filters, 5); err != nil {
			return filters, err
		}
		filters.statusCode = &statusCode
		if cursorIdentity {
			appendReadonlyCursorStatusCode(&filters, statusCode)
		} else {
			appendStatusCodeFilter(&filters, statusCode)
		}
		filters.hasRawFilter = true
	}

	emptyOutput, parseErr := parseReadonlyBoolean(values.Get("empty_output"))
	if parseErr != nil {
		return filters, parseErr
	}
	if emptyOutput {
		filters.emptyOutput = true
		// 未指定类型时，空输出按消费请求处理；显式指定错误类型时保留组合筛选能力。
		if filters.logType == nil {
			if err := forceReadonlyLogType(&filters, 2); err != nil {
				return filters, err
			}
		}
		if cursorIdentity {
			filters.where += " AND COALESCE(l.completion_tokens,0) = 0"
		} else {
			filters.where += " AND (l.completion_tokens = 0 OR l.completion_tokens IS NULL)"
		}
		filters.hasRawFilter = true
	}

	filters.hasRequestFilter = filters.requestID != "" || filters.upstreamRequestID != ""
	fallbackFinalOnly, parseErr := parseReadonlyBoolean(values.Get("fallback_final_only"))
	if parseErr != nil {
		return filters, parseErr
	}
	filters.fallbackFinalOnly = viewer || fallbackFinalOnly
	if filters.fallbackFinalOnly {
		// 与 rc35 自助日志一致：只保留同一用户同一请求按日志时间排序的最后一次尝试。
		// created_at 可能因异步写入与自增 ID 顺序不一致，因此用时间和 ID 做稳定的
		// 字典序比较；空请求 ID 没有可靠的链路键，仍逐条保留。该条件必须走原始
		// 日志查询，不能复用不包含链路关系的聚合表。
		if cursorIdentity {
			filters.where += readonlyCursorFinalPredicate
		} else {
			filters.where += ` AND NOT EXISTS (
			SELECT 1 FROM logs AS newer_logs
			WHERE l.request_id IS NOT NULL AND l.request_id <> ''
			  AND newer_logs.request_id = l.request_id
			  AND newer_logs.user_id = l.user_id
			  AND (newer_logs.created_at > l.created_at OR
				(newer_logs.created_at = l.created_at AND newer_logs.id > l.id))
		)`
		}
		filters.hasRawFilter = true
	}
	return filters, nil
}

// projectReadonlyLogOther 按访问角色投影 other，避免只读整库账号把 root/admin 元数据带到页面。
func projectReadonlyLogOther(value string, viewer bool) string {
	if value == "" {
		return ""
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &values); err != nil {
		return "{}"
	}
	if values == nil {
		return "{}"
	}
	changed := false
	if viewer {
		for _, key := range []string{"admin_info", "root_info", "audit_info", "channel_id", "channel_name", "channel_type", "reject_reason", "use_channel", "fallback_channels", "upstream_model_name", "is_model_mapped"} {
			if _, ok := values[key]; ok {
				delete(values, key)
				changed = true
			}
		}
	} else {
		if _, ok := values["root_info"]; ok {
			delete(values, "root_info")
			changed = true
		}
		// 旧版日志把拒绝原因写在顶层，管理员视图统一收进 admin_info。
		if reject, ok := values["reject_reason"]; ok {
			admin := map[string]json.RawMessage{}
			if raw, exists := values["admin_info"]; exists {
				_ = json.Unmarshal(raw, &admin)
			}
			if admin == nil {
				admin = map[string]json.RawMessage{}
			}
			if _, exists := admin["reject_reason"]; !exists {
				admin["reject_reason"] = reject
			}
			if raw, err := json.Marshal(admin); err == nil {
				values["admin_info"] = raw
				delete(values, "reject_reason")
				changed = true
			}
		}
	}
	if !changed {
		return value
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// hydrateReadonlyChannelNames 只为管理员批量补全渠道名称；channels 不可读时保留日志结果并显示 ID。
func hydrateReadonlyChannelNames(ctx context.Context, tx *sql.Tx, items []PassthroughLog) bool {
	ids := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.ChannelID <= 0 {
			continue
		}
		if _, ok := seen[item.ChannelID]; ok {
			continue
		}
		seen[item.ChannelID] = struct{}{}
		ids = append(ids, item.ChannelID)
	}
	if len(ids) == 0 {
		return false
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,COALESCE(name,'') FROM channels WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return false
	}
	defer rows.Close()
	names := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var name string
		if rows.Scan(&id, &name) != nil {
			return false
		}
		names[id] = name
	}
	if rows.Err() != nil {
		return false
	}
	for i := range items {
		items[i].ChannelName = names[items[i].ChannelID]
	}
	return true
}
func queryWindow(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	// rc35 默认查看最近一小时并预留未来一小时，避免跨日后默认窗口突然跳到当天零点。
	end := now.Add(time.Hour)
	start := now.Add(-time.Hour)
	values := r.URL.Query()
	if value := firstQueryValue(values, "start_time", "start_timestamp"); value != "" {
		parsed, err := parseReadonlyTime(value)
		if err != nil {
			return start, end, err
		}
		start = parsed
	}
	if value := firstQueryValue(values, "end_time", "end_timestamp"); value != "" {
		parsed, err := parseReadonlyTime(value)
		if err != nil {
			return start, end, err
		}
		end = parsed
	}
	if !start.Before(end) || end.Sub(start) > 31*24*time.Hour {
		return start, end, fmt.Errorf("invalid_time_range")
	}
	return start, end, nil
}

func parseReadonlyTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func queryPage(r *http.Request, max int) (int, int) {
	values := r.URL.Query()
	limitValue := firstQueryValue(values, "limit", "page_size")
	limit, _ := strconv.Atoi(limitValue)
	if limit <= 0 {
		limit = 50
	}
	if limit > max {
		limit = max
	}
	offsetValue := strings.TrimSpace(values.Get("offset"))
	offset, _ := strconv.Atoi(offsetValue)
	if offsetValue == "" {
		page, err := strconv.Atoi(strings.TrimSpace(values.Get("p")))
		if err == nil && page > 1 {
			if page-1 > int(^uint(0)>>1)/limit {
				offset = int(^uint(0) >> 1)
			} else {
				offset = (page - 1) * limit
			}
		}
	}
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
	h.auditStatus(r, site, operation, summary, "succeeded")
}
func (h *PassthroughHandler) auditStatus(r *http.Request, site, operation string, summary any, status string) {
	if h.Audit == nil {
		return
	}
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	body, _ := json.Marshal(summary)
	_ = h.Audit.InsertOperationAudit(storage.OperationAudit{ID: hex.EncodeToString(raw), InstanceID: site, OperationType: operation, TargetType: "newapi_readonly", TargetID: site, ActorID: ctauth.Actor(r), AfterSummary: string(body), Status: status, CreatedAt: time.Now().UTC()})
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
		writeDashboardJSON(w, 200, map[string]any{"items": []PassthroughLog{}, "configured": false, "total": 0, "page": 1, "page_size": 0, "summary": PassthroughLogSummary{}})
		return
	}
	limit, offset := queryPage(r, 100)
	viewer := readonlyViewer(r)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), ids, viewer)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	args := append([]any{start.Unix(), end.Unix()}, filters.args...)
	scope, err := readonlyLogCursorScope(site, viewer, r.URL.Query(), ids, start, end)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	cursor, err := parseReadonlyPageCursor(r.URL.Query().Get("cursor"), scope)
	if err != nil {
		writeDashboardError(w, 400, "invalid_cursor")
		return
	}
	listSQL, pageArgs := readonlyPageSQL(filters.where, args, limit, offset, cursor)
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogQueryTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, listSQL, pageArgs...)
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
		if rows.Scan(&v.ID, &v.UserID, &created, &v.Type, &v.Username, &v.ModelName, &v.ChannelID, &v.TokenID, &v.TokenName, &v.PromptTokens, &v.CompletionTokens, &v.Quota, &v.UseTime, &v.RequestID, &v.UpstreamRequestID, &content, &v.Group, &v.IP, &v.IsStream, &v.Other) != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		v.CreatedAt = time.Unix(created, 0).UTC()
		v.ContentSummary = redactSummary(content)
		v.Content = v.ContentSummary
		v.Channel = v.ChannelID
		v.Fallback, v.FallbackChannels = readonlyLogFallbackInfo(v.Other)
		v.FallbackChecked = v.Fallback
		v.Other = projectReadonlyLogOther(v.Other, viewer)
		if viewer {
			v.ChannelName = ""
		}
		items = append(items, v)
	}
	if err := rows.Err(); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	// MySQL 同一事务不能在结果集未关闭时再发起批量关联查询；显式关闭后才标记
	// fallback，避免驱动返回 commands out of sync 后被降级逻辑静默忽略。
	if err := rows.Close(); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	// Optional lookup cannot hold the list for the full query timeout.
	enrichCtx, enrichCancel := context.WithTimeout(ctx, time.Second)
	markReadonlyFallbackRequests(enrichCtx, tx, items, viewer)
	enrichCancel()
	_ = tx.Rollback()
	if viewer {
		for i := range items {
			// viewer 只收到事实标志，不暴露其它尝试渠道的运营链路。
			items[i].FallbackChannels = nil
			items[i].FallbackIndex = 0
			items[i].FallbackTotal = 0
		}
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if cursor != nil && cursor.Previous {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		hasMore = true
	}
	if !viewer {
		h.hydrateCachedChannelNames(ctx, db, items)
	}
	nextCursor, previousCursor := "", ""
	if len(items) > 0 {
		if hasMore {
			nextCursor = readonlyPageToken(items[len(items)-1], false, scope)
		}
		if offset > 0 {
			previousCursor = readonlyPageToken(items[0], true, scope)
		}
	}
	h.audit(r, site, "passthrough.logs", map[string]any{"user_ids": ids, "start_time": start, "end_time": end, "limit": limit, "offset": offset})
	writeDashboardJSON(w, 200, map[string]any{"items": items, "configured": true, "total": offset + len(items), "page": offset/limit + 1, "page_size": limit, "has_more": hasMore, "next_cursor": nextCursor, "previous_cursor": previousCursor})
}

func (h *PassthroughHandler) logStat(w http.ResponseWriter, r *http.Request) {
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
	filters, err := parseReadonlyLogFilters(r.URL.Query(), ids, readonlyViewer(r))
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	where, args := filters.where, filters.args
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogQueryTimeout)
	defer cancel()
	var rawSummary *readonlyRawSummary
	if filters.hasRawFilter {
		if result, err := h.sharedReadonlyRawSummary(ctx, db, site, readonlyViewer(r), start, end, filters); err == nil {
			rawSummary = &result
		}
		// On a shared-query failure, use the original endpoint-specific read.
		// COUNT and SUM failures must not become coupled (for example overflow).
	}
	release, err := h.acquireReadonlySummary(ctx, db)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer release()
	var summary PassthroughLogSummary
	quotaWhere := where
	if filters.logType == nil {
		// rc35 的统计默认只计算消费日志；显式类型筛选时则统计该类型。
		quotaWhere += " AND l.type=2"
	}
	quotaFrom, quotaTo, useRollup := completeHourWindow(start, end)
	if rawSummary != nil {
		summary.Quota = rawSummary.Quota
	} else if useRollup && !filters.hasRequestFilter && !filters.hasLike && !filters.hasRawFilter && h.readonlyRollupReady(ctx, site, quotaFrom) {
		logType := filters.logType
		if logType == nil {
			consumeType := 2
			logType = &consumeType
		}
		queryValues := map[string]string{"username": filters.username, "model_name": filters.modelName, "token_name": filters.tokenName, "group": filters.group}
		local, localErr := h.Rollups.QueryReadonlyLogRollup(ctx, readonlyRollupFilter(site, ids, quotaFrom, quotaTo, queryValues, logType, filters.channelID))
		if localErr != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		summary.Quota = local.QuotaSum
		if start.Before(quotaFrom) {
			value, rawErr := queryRawQuota(ctx, db, start, minTime(end, quotaFrom), quotaWhere, args)
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			summary.Quota += value
		}
		if quotaTo.Before(end) {
			value, rawErr := queryRawQuota(ctx, db, maxTime(start, quotaTo), end, quotaWhere, args)
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			summary.Quota += value
		}
	} else if value, rawErr := queryRawQuota(ctx, db, start, end, quotaWhere, args); rawErr != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	} else {
		summary.Quota = value
	}
	// 速率窗口使用 [now-60s, now)，既保持最近一分钟语义，也避免未来时间戳扩大扫描范围。
	rateEnd := time.Now().UTC()
	rateArgs := []any{rateEnd.Add(-60 * time.Second).Unix(), rateEnd.Unix()}
	rateQuery := readonlyLogRateQuery
	// 显式类型已经由公共 filters.where 注入，只有默认查询需要补充消费类型。
	if filters.logType == nil {
		rateQuery += " AND l.type=2"
	}
	rateArgs = append(rateArgs, args...)
	if err := db.QueryRowContext(ctx, rateQuery+where, rateArgs...).Scan(&summary.RPM, &summary.TPM); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	writeDashboardJSON(w, 200, map[string]any{"configured": true, "summary": summary})
}

func (h *PassthroughHandler) logCount(w http.ResponseWriter, r *http.Request) {
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
	viewer := readonlyViewer(r)
	filters, err := parseReadonlyLogFilters(r.URL.Query(), ids, viewer)
	if err != nil {
		writeDashboardError(w, 400, err.Error())
		return
	}
	where := filters.where
	args := append([]any{start.Unix(), end.Unix()}, filters.args...)
	ctx, cancel := context.WithTimeout(r.Context(), readonlyLogCountTimeout)
	defer cancel()
	if filters.hasRawFilter {
		if result, err := h.sharedReadonlyRawSummary(ctx, db, site, viewer, start, end, filters); err == nil {
			writeDashboardJSON(w, 200, map[string]any{"configured": true, "total": result.Count})
			return
		}
	}
	release, err := h.acquireReadonlySummary(ctx, db)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer release()
	var total int64
	rollupFrom, rollupTo, useRollup := completeHourWindow(start, end)
	if useRollup && !viewer && !filters.hasRequestFilter && !filters.hasLike && !filters.hasRawFilter && h.readonlyRollupReady(ctx, site, rollupFrom) {
		queryValues := map[string]string{"username": filters.username, "model_name": filters.modelName, "token_name": filters.tokenName, "group": filters.group}
		local, localErr := h.Rollups.QueryReadonlyLogRollup(ctx, readonlyRollupFilter(site, ids, rollupFrom, rollupTo, queryValues, filters.logType, filters.channelID))
		if localErr != nil {
			writeDashboardError(w, 502, "readonly_query_failed")
			return
		}
		if local.RequestCount == 0 {
			// 聚合为空时回源整段计数，兼容源库重灌或聚合暂未覆盖当前时间桶的场景。
			// 直接查完整区间可避免把头尾零头与聚合结果重复相加。
			value, rawErr := queryRawCount(ctx, db, start, end, where, args[2:])
			if rawErr != nil {
				writeDashboardError(w, 502, "readonly_query_failed")
				return
			}
			total = value
		} else {
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
		}
	} else if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l WHERE l.created_at>=? AND l.created_at<?`+where, args...).Scan(&total); err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
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
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(l.quota),0) FROM logs l WHERE l.created_at>=? AND l.created_at<?`+where, args...).Scan(&value)
	return value, err
}

func queryRawCount(ctx context.Context, db *sql.DB, start, end time.Time, where string, filterArgs []any) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	args := append([]any{start.Unix(), end.Unix()}, filterArgs...)
	var value int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs l WHERE l.created_at>=? AND l.created_at<?`+where, args...).Scan(&value)
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
