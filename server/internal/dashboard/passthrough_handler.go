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
		v.CacheTokens = billingCacheTokens(other)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (h *PassthroughHandler) RatioSnapshotForBilling(ctx context.Context, site string) (string, error) {
	db, configured, err := h.database(site)
	if err != nil {
		return "", err
	}
	if !configured {
		return "", fmt.Errorf("readonly database is not configured for %s", site)
	}
	rows, err := db.QueryContext(ctx, "SELECT `key`,value FROM options WHERE `key` IN ('ModelRatio','CompletionRatio','CacheRatio','GroupRatio','QuotaPerUnit')")
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
	raw, err := json.Marshal(values)
	return string(raw), err
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

func (s BillingReadonlySource) Logs(ctx context.Context, site string, start, end time.Time) ([]billing.LogRecord, error) {
	return s.Handler.LogsForBilling(ctx, site, start, end)
}
func (s BillingReadonlySource) RatioSnapshot(ctx context.Context, site string) (string, error) {
	return s.Handler.RatioSnapshotForBilling(ctx, site)
}
func (s BillingReadonlySource) Balances(ctx context.Context, site string) (map[int64]int64, error) {
	return s.Handler.BalancesForBilling(ctx, site)
}

func billingCacheTokens(other string) int64 {
	if strings.TrimSpace(other) == "" {
		return 0
	}
	var values map[string]any
	if json.Unmarshal([]byte(other), &values) != nil {
		return 0
	}
	for _, key := range []string{"cache_tokens", "cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens"} {
		if value, ok := values[key]; ok {
			switch typed := value.(type) {
			case float64:
				return int64(typed)
			case json.Number:
				parsed, _ := typed.Int64()
				return parsed
			case string:
				parsed, _ := strconv.ParseInt(typed, 10, 64)
				return parsed
			}
		}
	}
	return 0
}

type passthroughPool struct {
	encrypted string
	db        *sql.DB
}

const readonlyQueryTimeout = 5 * time.Second

func configureReadonlyDB(db *sql.DB) {
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)
}

type PassthroughHandler struct {
	Config    ReadonlyConfigStore
	Audit     PassthroughAuditStore
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
	rows, err := tx.QueryContext(ctx, `SELECT id,username,COALESCE(display_name,''),quota,used_quota,status FROM users`+where+` ORDER BY id LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer rows.Close()
	items := []PassthroughUser{}
	for rows.Next() {
		var v PassthroughUser
		if rows.Scan(&v.ID, &v.Username, &v.DisplayName, &v.Quota, &v.UsedQuota, &v.Status) != nil {
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
		writeDashboardJSON(w, 200, map[string]any{"items": []PassthroughLog{}, "configured": false, "total": 0})
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
			filters += " AND " + spec.column + " LIKE ?"
			args = append(args, "%"+value+"%")
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
	args = append(args, limit, offset)
	ctx, cancel := context.WithTimeout(r.Context(), readonlyQueryTimeout)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		writeDashboardError(w, 502, "readonly_query_failed")
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,created_at,type,COALESCE(username,''),COALESCE(model_name,''),channel_id,COALESCE(token_name,''),prompt_tokens,completion_tokens,quota,use_time,COALESCE(request_id,''),COALESCE(content,''),COALESCE(`+"`group`"+`,''),COALESCE(ip,''),COALESCE(is_stream,0),COALESCE(other,'') FROM logs WHERE created_at BETWEEN ? AND ?`+userFilter+filters+` ORDER BY id DESC LIMIT ? OFFSET ?`, args...)
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
	h.audit(r, site, "passthrough.logs", map[string]any{"user_ids": ids, "start_time": start, "end_time": end, "limit": limit, "offset": offset})
	writeDashboardJSON(w, 200, map[string]any{"items": items, "configured": true, "total": offset + len(items)})
}
