package auth

import (
	"controltower/server/internal/storage"
	"net/http"
	"strings"
)

type Permission struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

var PermissionCatalog = []Permission{
	{"logs.query", "容器日志", "查询已配置容器的日志，查看本人查询任务及结果"},
	{"overview.read", "运行总览", "查看运行总览和实例汇总"},
	{"monitor.customers", "客户监控", "查看客户指标及客户维度明细"},
	{"monitor.channels", "渠道监控", "查看渠道指标、快照及渠道维度明细"},
	{"monitor.models", "模型监控", "查看模型指标及模型维度明细"},
	{"monitor.runtime", "系统状态", "查看 Agent、服务器、健康检查与容器状态"},
	{"monitor.samples", "样本分析", "查询错误和慢请求样本"},
	{"monitor.latency", "延时分诊", "查看 Nginx 耗时及慢样本"},
	{"data.usage", "用量统计", "查看用量统计"},
	{"data.users", "用户管理", "查询用户信息"},
	{"data.logs", "使用日志", "查询使用日志明细"},
	{"billing.users", "用户账单", "查看、导出和维护用户账单"},
	{"billing.channels", "上游账单", "查看、导出和维护上游账单"},
	{"billing.tasks", "账单任务", "管理用户及上游账单生成任务"},
	{"models.manage", "模型管理", "模型、价格和分组倍率维护"},
	{"upstreams.manage", "上游管理", "上游配置查询与维护"},
	{"discounts.manage", "渠道折扣", "渠道折扣查询与维护"},
	{"tuning.manage", "调权中心", "调权策略维护和渠道指令下发"},
	{"alerts.manage", "告警中心", "告警查看、确认、清理和用户余额告警设置"},
	{"notifications.manage", "通知设置", "通知渠道维护、投递记录和重新发送"},
	{"instances.manage", "实例管理", "实例创建、配置修改和令牌轮换"},
	{"audits.read", "操作审计", "查看所有操作审计记录"},
	{"settings.manage", "系统设置", "系统设置查询与修改"},
	{"accounts.manage", "账号管理", "创建账号、分配自身拥有的权限、停用和重置密码；含客户选择所需的用户查询"},
}

// Legacy bundles remain valid in storage, while editors receive individual menu keys.
var legacyPermissions = map[string][]string{
	"monitor.read":   {"overview.read", "monitor.customers", "monitor.channels", "monitor.models", "monitor.runtime", "monitor.samples", "monitor.latency"},
	"data.read":      {"data.usage", "data.users", "data.logs"},
	"billing.manage": {"billing.users", "billing.channels", "billing.tasks"},
}

func ExpandPermissions(permissions []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, key := range permissions {
		keys, old := legacyPermissions[key]
		if !old {
			keys = []string{key}
		}
		for _, k := range keys {
			if !seen[k] {
				out = append(out, k)
				seen[k] = true
			}
		}
	}
	return out
}

func HasPermission(u storage.User, key string) bool {
	if u.Role != "admin" {
		return false
	}
	if storage.IsFullAdmin(u) {
		return true
	}
	if children, old := legacyPermissions[key]; old {
		for _, child := range children {
			if !HasPermission(u, child) {
				return false
			}
		}
		return true
	}
	for _, p := range ExpandPermissions(u.Permissions) {
		if p == key {
			return true
		}
	}
	return false
}

func canGrant(actor storage.User, permissions []string) bool {
	for _, p := range ExpandPermissions(permissions) {
		known := p == "*"
		for _, entry := range PermissionCatalog {
			if entry.Key == p {
				known = true
				break
			}
		}
		if !known || !HasPermission(actor, p) {
			return false
		}
	}
	return true
}

func canManage(actor, target storage.User) bool {
	if !HasPermission(actor, "accounts.manage") {
		return false
	}
	if target.Role == "viewer" {
		return true
	}
	if storage.IsFullAdmin(target) {
		return storage.IsFullAdmin(actor)
	}
	return canGrant(actor, target.Permissions)
}

// Fail closed: new endpoints are unavailable to restricted admins until explicitly mapped.
func allowAdminRequest(u storage.User, r *http.Request) bool {
	if u.Role != "admin" {
		return false
	}
	if storage.IsFullAdmin(u) {
		return true
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/dashboard/")
	if strings.HasPrefix(path, "container-log-") {
		return HasPermission(u, "logs.query")
	}
	read := r.Method == http.MethodGet
	any := func(keys ...string) bool {
		for _, k := range keys {
			if HasPermission(u, k) {
				return true
			}
		}
		return false
	}
	if path == "instances" && read {
		return true
	} // Shared site selector; response excludes credentials.
	if strings.HasPrefix(path, "instances/") || path == "instances" {
		return any("instances.manage")
	}
	if strings.HasPrefix(path, "tuning/") || path == "channel-commands" || (strings.HasPrefix(path, "channels/") && strings.HasSuffix(path, "/commands")) {
		return any("tuning.manage")
	}
	if path == "operation-audits" {
		return read && any("audits.read")
	}
	if path == "settings" {
		return any("settings.manage")
	}
	if strings.HasPrefix(path, "notification-") {
		return any("notifications.manage")
	}
	if path == "balance-alert-users" {
		return any("alerts.manage")
	}
	if path == "alerts" || strings.HasPrefix(path, "alerts/") {
		return any("alerts.manage") || (read && any("overview.read", "tuning.manage"))
	}
	if path == "passthrough/users" {
		return read && any("data.users", "accounts.manage", "billing.users", "billing.tasks", "alerts.manage")
	}
	if strings.HasPrefix(path, "passthrough/logs") {
		return read && any("data.logs", "billing.users")
	}
	if strings.HasPrefix(path, "billing/") {
		switch strings.TrimPrefix(path, "billing/") {
		case "models", "prices", "group-ratios", "import-prices":
			return any("models.manage") || (read && any("billing.users", "billing.channels", "billing.tasks", "tuning.manage"))
		case "upstreams":
			return any("upstreams.manage") || (read && any("billing.channels", "billing.tasks", "discounts.manage"))
		case "discounts":
			return any("discounts.manage") || (read && any("billing.users", "billing.channels", "billing.tasks"))
		case "jobs":
			if r.Method == http.MethodPost {
				return any("billing.tasks")
			}
			return any("billing.users", "billing.channels", "billing.tasks")
		case "statements", "statements/result":
			return any("billing.users", "billing.channels", "billing.tasks") // Job type checked by the handler.
		case "jobs/steps", "backfill":
			return any("billing.tasks")
		case "upstream-channels", "upstream-channels/detail", "upstream-channels/requests", "channels":
			return any("billing.channels")
		case "overview", "files", "user-days", "user-token-days", "range-workbook", "anomalies", "summary", "detail", "reconciliation", "reconciliation/requests", "tokens", "tokens/daily", "user-settings":
			return any("billing.users")
		case "verification":
			return any("billing.tasks")
		default:
			return false
		}
	}
	if path == "usage" {
		return read && any("data.usage", "overview.read")
	}
	switch path {
	case "overview":
		return read && any("overview.read")
	case "metrics", "metric-history":
		if !read {
			return false
		}
		if any("tuning.manage") {
			return true
		}
		switch r.URL.Query().Get("dimension_type") {
		case "instance":
			return any("overview.read")
		case "instance_user", "instance_user_model":
			return any("monitor.customers")
		case "instance_channel", "instance_channel_model":
			return any("monitor.channels")
		case "instance_model", "instance_model_user":
			return any("monitor.models")
		default:
			return false
		}
	case "channel-snapshots":
		return read && any("monitor.channels", "tuning.manage")
	case "agents", "server-metrics", "health-checks", "docker-statuses":
		return read && any("monitor.runtime")
	case "nginx-timing", "nginx-timing/slow-samples":
		return read && any("monitor.latency")
	case "log-samples", "logs":
		return read && any("monitor.samples")
	}
	return false
}
