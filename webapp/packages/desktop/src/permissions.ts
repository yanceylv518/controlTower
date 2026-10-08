import { ApiError, type CurrentUser, type PermissionOption } from '@ct/shared'

export function permissionKeys(values: string[], options: PermissionOption[]): string[] {
  return [...new Set(values.includes('*') ? options.map(option => option.key) : values)].sort()
}

export function samePermissions(left: string[], right: string[]): boolean {
  const a = [...new Set(left)].sort(), b = [...new Set(right)].sort()
  return a.length === b.length && a.every((key, index) => key === b[index])
}

export function permissionChanges(before: string[], after: string[], options: PermissionOption[]) {
  const old = permissionKeys(before, options), next = permissionKeys(after, options)
  return { added: next.filter(key => !old.includes(key)), removed: old.filter(key => !next.includes(key)) }
}

export function permissionError(error: unknown): string {
  const messages: Record<string, string> = {
    forbidden: '权限已变化或超出可管理范围，请刷新后重试',
    invalid_user: '请检查账号是否重复、密码长度及必填信息',
    last_full_admin: '必须保留至少一个启用且拥有全部权限的管理员',
    permission_preset_conflict: '预设已被其他管理员修改，请载入最新版本后重新确认',
    permission_preset_accounts_changed: '所选账号的权限已变化，请刷新差异后重新确认',
    permission_preset_not_found: '预设已被删除，请刷新预设列表',
    permission_preset_name_exists: '预设名称已存在，请使用其他名称',
    permission_preset_limit: '预设数量已达到上限，请先整理现有预设',
    invalid_permission_preset: '请填写有效名称并选择可授予的权限',
    permission_preset_unavailable: '当前服务尚未支持权限预设，请更新 Server 并执行迁移',
  }
  if (error instanceof ApiError) return messages[error.code] || `操作失败（${error.code}），请刷新后重试`
  return '连接中断或请求超时，结果尚未确认；请刷新数据后再操作'
}

export function can(user: CurrentUser | null, permission: string): boolean {
  return user?.role === 'admin' && Boolean(user.permissions?.includes('*') || user.permissions?.includes(permission))
}

export const permissionPages: Array<[string, string]> = [
  ['/trial-followup','monitor.trials'],
  ['/', 'overview.read'], ['/container-logs', 'logs.query'], ['/customers', 'monitor.customers'], ['/channels', 'monitor.channels'], ['/models', 'monitor.models'],
  ['/runtime', 'monitor.runtime'], ['/samples', 'monitor.samples'], ['/latency', 'monitor.latency'],
  ['/usage', 'data.usage'], ['/readonly-users', 'data.users'], ['/readonly-logs', 'data.logs'],
  ['/billing', 'billing.users'], ['/billing/new', 'billing.users'], ['/billing/reports','billing.channels'], ['/billing/upstream-new','billing.channels'], ['/billing/tasks', 'billing.tasks'], ['/billing/channels', 'billing.channels'],
  ['/billing/anomalies', 'billing.users'], ['/billing-reconciliation', 'billing.users'],
  ['/models/manage', 'models.manage'], ['/billing/pricing', 'models.manage'],
  ['/billing/upstreams', 'upstreams.manage'], ['/billing/discounts', 'discounts.manage'], ['/billing/user-discounts', 'discounts.manage'],
  ['/tuning', 'tuning.manage'], ['/alerts', 'alerts.manage'], ['/notifications', 'notifications.manage'],
  ['/instances', 'instances.manage'], ['/log-archives', 'archive.manage'], ['/audits', 'audits.read'], ['/settings', 'settings.manage'],
  ['/access-users', 'accounts.manage'],
]

export function canVisit(user: CurrentUser | null, path: string): boolean {
  const match = [...permissionPages].sort((a, b) => b[0].length - a[0].length)
    .find(([base]) => path === base || (base !== '/' && path.startsWith(`${base}/`)))
  return Boolean(match && can(user, match[1]))
}

export function homeFor(user: CurrentUser | null): string {
  if (user?.role === 'viewer') return '/customers'
  return permissionPages.find(([, permission]) => can(user, permission))?.[0] || '/no-access'
}
