import type { CurrentUser } from '@ct/shared'

export function can(user: CurrentUser | null, permission: string): boolean {
  return user?.role === 'admin' && Boolean(user.permissions?.includes('*') || user.permissions?.includes(permission))
}

export const permissionPages: Array<[string, string]> = [
  ['/', 'overview.read'], ['/customers', 'monitor.customers'], ['/channels', 'monitor.channels'], ['/models', 'monitor.models'],
  ['/runtime', 'monitor.runtime'], ['/samples', 'monitor.samples'], ['/latency', 'monitor.latency'],
  ['/usage', 'data.usage'], ['/readonly-users', 'data.users'], ['/readonly-logs', 'data.logs'],
  ['/billing', 'billing.users'], ['/billing/tasks', 'billing.tasks'], ['/billing/channels', 'billing.channels'],
  ['/billing/anomalies', 'billing.users'], ['/billing-reconciliation', 'billing.users'],
  ['/models/manage', 'models.manage'], ['/billing/pricing', 'models.manage'],
  ['/billing/upstreams', 'upstreams.manage'], ['/billing/discounts', 'discounts.manage'],
  ['/tuning', 'tuning.manage'], ['/alerts', 'alerts.manage'], ['/notifications', 'notifications.manage'],
  ['/instances', 'instances.manage'], ['/audits', 'audits.read'], ['/settings', 'settings.manage'],
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
