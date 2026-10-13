import type { ChannelSchedule, ChannelScheduleExecution } from '@ct/shared'

const weekdays = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const timeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
})
export function scheduleTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : timeFormatter.format(date)
}
export function scheduleDays(days: number[]) {
  const sorted = [...days].sort((a, b) => a - b)
  if (sorted.join() === '1,2,3,4,5,6,7') return '每天'
  if (sorted.join() === '1,2,3,4,5') return '周一至周五'
  if (sorted.join() === '6,7') return '周末'
  return sorted.map(day => weekdays[day - 1]).join('、')
}
export function scheduleSummary(plan: ChannelSchedule) {
  if (!plan.enabled) return '计划已停用'
  const window = plan.windows[0]
  return `${scheduleDays(plan.days)} ${window.start}–${window.end}${window.end < window.start ? '次日' : ''}${plan.windows.length > 1 ? ` +${plan.windows.length - 1}` : ''}`
}
export function effectiveWindow(start: number, end: number) {
  if (end - start === 10080) return '整周开启'
  const label = (minute: number) => {
    const day = Math.floor(minute / 1440)
    const clock = `${String(Math.floor(minute % 1440 / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
    return `${day >= 7 ? '下' : ''}${weekdays[day % 7]} ${clock}`
  }
  return `${label(start)} — ${label(end)}`
}
export function effectiveWindows(windows: { start_minute: number; end_minute: number }[]) {
  const grouped = new Map<string, { days: number[]; start: number; end: number }>()
  const labels: string[] = []
  for (const window of windows) {
    if (window.end_minute - window.start_minute > 1440) { labels.push(effectiveWindow(window.start_minute, window.end_minute)); continue }
    const start = window.start_minute % 1440, end = start + window.end_minute - window.start_minute
    const key = `${start}:${end}`, group = grouped.get(key) || { days: [], start, end }
    group.days.push(Math.floor(window.start_minute / 1440) % 7 + 1); grouped.set(key, group)
  }
  const clock = (minute: number) => `${String(Math.floor(minute % 1440 / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`
  for (const group of grouped.values()) labels.push(`${scheduleDays(group.days)} ${clock(group.start)} — ${group.end >= 1440 ? '次日 ' : ''}${clock(group.end)}`)
  return labels
}
// 把按周分钟的有效区间切成 7 行 24 小时色带；跨周的部分折回周一。
export function weekStrip(windows: { start_minute: number; end_minute: number }[]) {
  const rows = weekdays.map(label => ({ label, segments: [] as { left: number; width: number }[] }))
  for (const window of windows) {
    for (let start = Math.max(0, window.start_minute); start < window.end_minute;) {
      const day = Math.floor(start / 1440), end = Math.min(window.end_minute, (day + 1) * 1440)
      rows[day % 7].segments.push({ left: (start - day * 1440) / 14.4, width: (end - start) / 14.4 })
      start = end
    }
  }
  return rows
}
export function scheduleResult(run?: ChannelScheduleExecution) {
  if (!run) return '尚未处理'
  const action = run.target === 1 ? '开启' : '关闭'
  const labels: Record<string, string> = {
    pending: '待处理', running: '处理中', retry: '等待重试', waiting_recovery: '等待恢复', waiting_control: '等待其他操作',
    succeeded: `${action}成功`, unchanged: '无需变更', skipped: `本次未${action}`,
    failed: `${action}失败`, unknown: '结果待确认', superseded: '已取消',
  }
  return labels[run.status] || '结果待确认'
}
export function scheduleTone(run?: ChannelScheduleExecution): 'success' | 'warning' | 'danger' | 'info' {
  if (!run) return 'info'
  if (run.status === 'succeeded' || run.status === 'unchanged') return 'success'
  if (run.status === 'failed' || run.status === 'unknown') return 'danger'
  if (['retry', 'waiting_recovery', 'waiting_control', 'skipped', 'pending', 'running'].includes(run.status)) return 'warning'
  return 'info'
}
export function scheduleFollowup(run?: ChannelScheduleExecution) {
  if (!run) return ''
  if (['failed', 'skipped'].includes(run.status)) return '本时点不再重试'
  if (run.status === 'unknown') return '核对状态后重新同步'
  if (run.status === 'retry') {
    const at = new Date(new Date(run.updated_at).getTime() + 2 ** Math.min(run.attempts, 4) * 30000)
    return `下次重试 ${scheduleTime(at.toISOString())}`
  }
  return ''
}
