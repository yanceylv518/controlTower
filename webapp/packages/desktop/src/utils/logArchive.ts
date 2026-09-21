export interface ArchiveConfig {
	full_history?: boolean
	history_immutable?: boolean
  reconcile_id?: string
  reconcile_date?: string
  version: number
  instance_id: string
  agent_id: string
  running: boolean
  batch_size: number
  interval_seconds: number
  delay_seconds: number
}

export interface ArchiveTarget {
  instance_id: string
  name: string
  agent_id: string
  configured: boolean
  seen_at: string
}

export interface ArchiveReportedDay {
  date: string
  archived_rows: string
  request_rows: string
  error_rows: string
  last_id: string
  verified_at: string
}

export interface ArchiveReconciliation {
  id: string
  date: string
  state: string
  source_rows: number
  target_rows: number
  finished_at?: string
  error?: string
}

export interface ArchiveItem {
  site_id: string
  name: string
  enabled: boolean
  config: ArchiveConfig
  targets: ArchiveTarget[]
  days: ArchiveReportedDay[]
  seen_at?: string
  status: {
	workflow?: { phase: string; date?: string; first_date?: string; imported_rows: string; completed_days: string; blocked_days: string; error_code?: string; issues?: {date: string; code: string}[] }
    supports_daily_check?: boolean
    reconciliation?: ArchiveReconciliation
    agent_id: string
    configured: boolean
    applied_version: number
    state: string
    last_id: string
    last_success?: string
    verified_at?: string
    verified_rows: number
    last_batch_rows: number
    error: string
  }
}

export interface ArchiveResponse {
  items: ArchiveItem[]
  month: string
  // Missing capabilities on a legacy server must be read as false, never inferred from counts.
  capabilities?: {
    full_history?: boolean
    day_versions: boolean
    archive_billing: boolean
    date_backfill: boolean
    coverage_catalog: boolean
  }
}

export interface ArchiveCalendarDay {
  date: string
  reported?: ArchiveReportedDay
  kind: 'future' | 'today' | 'reported' | 'unknown' | 'matched' | 'mismatched' | 'failed' | 'checking'
  label: string
  tagType: 'info' | 'warning' | 'danger' | 'success'
  reason: string
  reconciliation?: ArchiveReconciliation
}

const dayMilliseconds = 86_400_000
const beijingOffset = 8 * 60 * 60 * 1000

function daysInMonth(month: string): number {
  if (!/^\d{4}-(0[1-9]|1[0-2])$/.test(month)) return 0
  const year = Number(month.slice(0, 4))
  if (year === 0) return 0
  const number = Number(month.slice(5))
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  return [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][number - 1]
}

function beijingDayStart(date: string): number {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return NaN
  const day = Number(date.slice(8))
  if (day < 1 || day > daysInMonth(date.slice(0, 7))) return NaN
  return Date.parse(`${date}T00:00:00+08:00`)
}

/** The archive uses a fixed UTC+08:00 business day, independent of the browser timezone. */
export function beijingDate(now: number = Date.now()): string {
  const shifted = new Date(now + beijingOffset)
  return Number.isFinite(shifted.getTime()) ? shifted.toISOString().slice(0, 10) : ''
}

/** Legacy cumulative reports and a recent comparison cannot establish sealed coverage. */
export function buildArchiveDays(
  month: string,
  reported: ArchiveReportedDay[],
  today: string,
  reconciliation?: ArchiveReconciliation,
): ArchiveCalendarDay[] {
  const count = daysInMonth(month)
  if (!count || !Number.isFinite(beijingDayStart(today))) return []
  const reports = new Map(reported.map(day => [day.date, day]))
  return Array.from({ length: count }, (_, index): ArchiveCalendarDay => {
    const date = `${month}-${String(index + 1).padStart(2, '0')}`
    const report = reports.get(date)
    const base = { date, ...(report ? { reported: report } : {}) }
    if (date > today) return {
      ...base, kind: 'future', label: '未来日期', tagType: 'info',
      reason: '日期尚未开始，不计入历史归档覆盖。',
    }
    if (date === today) return {
      ...base, kind: 'today', label: '今日 · 未结束', tagType: 'info',
      reason: '当前日期尚未结束，累计上报不代表该日数据已完整。',
    }
    const result = reconciliation?.date === date ? reconciliation : undefined
    if (result) {
      const checked = { ...base, reconciliation: result }
      if (result.state === 'matched') return {
        ...checked, kind: 'matched', label: '最近对账一致', tagType: 'success',
        reason: '仅表示最近一次扫描的源与目标明细一致，不代表已封存、历史完整或已核实零业务。',
      }
      if (result.state === 'mismatched') return {
        ...checked, kind: 'mismatched', label: '最近对账有差异', tagType: 'danger',
        reason: '最近一次源与目标明细对账存在差异，尚未自动补齐或修复。',
      }
      if (result.state === 'failed') return {
        ...checked, kind: 'failed', label: '最近对账失败', tagType: 'danger',
        reason: '最近一次对账未能完成，不能据此判断数据完整性或零业务。',
      }
      if (result.state === 'running') return {
        ...checked, kind: 'checking', label: '对账中', tagType: 'warning',
        reason: '最近一次对账仍在进行，尚无完整性结论。',
      }
    }
    if (report) return {
      ...base, kind: 'reported', label: '已上报', tagType: 'info',
      reason: '仅为已归档数据的累计上报，不代表该日完整或已封存。',
    }
    return {
      ...base, kind: 'unknown', label: '无上报记录', tagType: 'warning',
      reason: '尚无该日累计上报，无法判断是零业务还是未归档。',
    }
  })
}

export function formatArchiveCount(value: string | undefined): string {
  return typeof value === 'string' && /^\d+$/.test(value)
    ? value.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
    : '—'
}

/** Match the server's strict day-end + delay < now rule; now is Unix milliseconds. */
export function canCheckArchiveDay(date: string, today: string, delaySeconds: number, now: number): boolean {
  const start = beijingDayStart(date)
  if (!Number.isFinite(start) || !Number.isFinite(beijingDayStart(today)) || date >= today) return false
  if (!Number.isInteger(delaySeconds) || delaySeconds < 0 || !Number.isFinite(now)) return false
  return start + dayMilliseconds + delaySeconds * 1000 < now
}
