package storage

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrUnsupportedOperationAudit = errors.New("unsupported operation audit type")

var auditSecretAssignment = regexp.MustCompile(`(?i)\b(password|passwd|token|secret|api[_-]?key|authorization|dsn)\s*([:=])\s*(?:Bearer\s+)?("[^"]*"|'[^']*'|[^\s,;]+)`)
var auditBearerCredential = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]+=*`)
var auditURLCredential = regexp.MustCompile(`(?i)(https?://)[^/@\s]+@`)

type ChannelCommandQuery struct {
	InstanceID string
	Status     string
	Limit      int
	Offset     int
}

type OperationAuditQuery struct {
	// ListOnly avoids the historical exact count. CountOnly ignores pagination.
	ListOnly      bool
	CountOnly     bool
	BeforeTime    time.Time
	BeforeID      string
	ActorOptions  bool
	ActorExact    bool
	InstanceID    string
	SiteID        string
	OperationType string
	Actor         string
	RequestID     string
	CorrelationID string
	Status        string
	Source        string
	Trigger       string
	Search        string
	From          time.Time
	To            time.Time
	Limit         int
	Offset        int
}

type OperationAuditPage struct {
	HasMore        bool
	Actors         []string
	Items          []OperationAudit
	Total          int64
	OperationTypes []string
}

var operationAuditHTTPFilterSources = []string{
	"alerts",
	"archive",
	"billing",
	"channel_group_presets",
	"channels",
	"container_log_tasks",
	"control_tower",
	"identity",
	"log_archives",
	"notification_channels",
	"notification_deliveries",
	"system",
	"tuning",
}

var configurationAuditOperationTypes = []string{
	"settings.update",
	"settings.balance_alert_user_update",
	"menu_visibility.update",
	"instance.create",
	"instance.update",
	"instance.delete",
	"instance.token_rotate",
	"tuning.base_update",
	"tuning.base_priority_sync",
	"tuning.manual_execute",
	"tuning.policy_update",
	"tuning.group_presets_update",
	"logs.query",
	"auth.account_create",
	"auth.account_update",
	"auth.password_reset",
	"auth.password_change",
	"billing.price_update",
	"billing.group_ratio_update",
	"billing.model_metadata_update",
	"billing.models_sync",
	"billing.backfill",
	"billing.discount.create",
	"billing.discount.update",
	"billing.discount.delete",
	"billing.upstream.create",
	"billing.upstream.update",
	"billing.upstream.delete",
	"billing.channel_setting.update",
	"billing.user_setting.update",
	"channel.update",
}

var configurationAuditOperationSet = func() map[string]struct{} {
	operations := make(map[string]struct{}, len(configurationAuditOperationTypes))
	for _, operation := range configurationAuditOperationTypes {
		operations[operation] = struct{}{}
	}
	return operations
}()

// IsConfigurationAuditOperation keeps operation history focused on explicit
// configuration changes. Runtime activity, reads, probes and logins are not
// configuration changes and must not grow the audit table.
func IsConfigurationAuditOperation(operation string) bool {
	_, ok := configurationAuditOperationSet[operation]
	return ok
}

func IsExcludedOperationAudit(operation string) bool {
	switch operation {
	case "auth.viewer_login", "channel.probe", "channel.verify", "passthrough.users", "passthrough.logs":
		return true
	default:
		return false
	}
}

func IsManualOperationAudit(v OperationAudit) bool {
	if v.OperationType == "tuning.auto_execute" || v.ActorType == "system" || v.TriggerType == "automatic" {
		return false
	}
	// 已验证的网页登录身份优先于历史系统账号命名，防止同名管理员被错误排除。
	if v.ActorType == "human" && (v.ActorRole == "admin" || v.ActorRole == "viewer") && (v.AuthMethod == "session" || v.AuthMethod == "web_session") {
		return true
	}
	return v.ActorID != "system" && v.ActorID != "agent" &&
		!strings.HasPrefix(v.ActorID, "system:") && !strings.HasPrefix(v.ActorID, "agent:")
}

// OperationAuditFilterTypes 返回固定的操作类型筛选目录，不依赖审计表中的历史数据。
func OperationAuditFilterTypes() []string {
	operations := append([]string(nil), configurationAuditOperationTypes...)
	operations = append(operations, "auth.login", "auth.logout")
	for _, source := range operationAuditHTTPFilterSources {
		operations = append(operations, "http."+source+".*")
	}
	return operations
}

// OperationAuditTypeFilterPrefix 返回模块级 HTTP 类型筛选对应的精确前缀。
func OperationAuditTypeFilterPrefix(filter string) (string, bool) {
	for _, source := range operationAuditHTTPFilterSources {
		prefix := "http." + source + "."
		if filter == prefix+"*" {
			return prefix, true
		}
	}
	return "", false
}

// OperationAuditTypeMatchesFilter 同时支持业务类型精确匹配和固定模块类型筛选。
func OperationAuditTypeMatchesFilter(operation, filter string) bool {
	if prefix, ok := OperationAuditTypeFilterPrefix(filter); ok {
		return strings.HasPrefix(operation, prefix)
	}
	return operation == filter
}

func IsSupportedOperationAudit(operation string) bool {
	return operation == "auth.login" || operation == "auth.logout" || IsConfigurationAuditOperation(operation) || strings.HasPrefix(operation, "http.")
}

func NormalizeOperationAudit(v OperationAudit) OperationAudit {
	v.ErrorSummary = RedactAuditError(v.ErrorSummary)
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = v.CreatedAt
	}
	if v.Status == "" {
		v.Status = "unknown"
	}
	if v.ActorType == "" || v.ActorType == "unknown" {
		switch {
		case strings.HasPrefix(v.ActorID, "system:") || strings.HasPrefix(v.ActorID, "agent:"):
			v.ActorType = "system"
		case v.ActorID == "token":
			v.ActorType = "service_token"
		case v.ActorID != "" && v.ActorID != "unknown" && v.ActorID != "legacy-admin":
			v.ActorType = "human"
		default:
			v.ActorType = "unknown"
		}
	}
	if v.TriggerType == "" || v.TriggerType == "unknown" {
		if v.ActorType == "system" {
			v.TriggerType = "automatic"
		} else {
			v.TriggerType = "manual"
		}
	}
	if v.AuthMethod == "" {
		switch v.ActorType {
		case "human":
			v.AuthMethod = "session"
		case "service_token":
			v.AuthMethod = "bearer"
		}
	}
	if v.SourceComponent == "" {
		if component, _, ok := strings.Cut(v.OperationType, "."); ok {
			v.SourceComponent = component
		} else {
			v.SourceComponent = "control_tower"
		}
	}
	return v
}

func RedactAuditError(value string) string {
	value = auditSecretAssignment.ReplaceAllString(value, "$1$2[redacted]")
	value = auditBearerCredential.ReplaceAllString(value, "Bearer [redacted]")
	value = auditURLCredential.ReplaceAllString(value, "${1}[redacted]@")
	value = strings.ToValidUTF8(value, "�")
	if len(value) > 1000 {
		value = value[:1000]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func NormalizeCommandPagination(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
