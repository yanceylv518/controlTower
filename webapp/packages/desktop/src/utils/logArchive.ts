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
  active_dataset_id?: string
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
    prepare_phase?: string
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

export const archiveWorkflowLabels: Record<string, string> = {
  reset_state: '重建贡献记录', reset_daily: '重建日统计', reset_monthly: '重建月统计',
  import_target: '接入已有归档', source_scan: '切换逐日推进', live: '持续归档 / 调度日期',
  backfill: '补齐并比较', verify: '归档库核验', seal: '封存版本',
}

/** Describe reported evidence; a heartbeat is not proof that a batch is advancing. */
export function archiveExecution(item: ArchiveItem, fullHistory: boolean, now: number, readFailed = false) {
  const s = item.status
  const mode = item.config.full_history ? '全量逐日归档' : s.prepare_phase ? '新版归档初始化' : item.active_dataset_id ? '数据集增量归档' : '增量归档'
  const result = (title: string, detail: string, attention = false) => ({ mode, title, detail, attention })
  if (readFailed) return result('状态读取失败', '当前内容是上次成功读取的记录，暂时无法确认任务是否继续运行。', true)
  if (!item.config.agent_id) return result('尚未选择执行节点', '请在任务与策略中配置执行 Agent。', true)
  const seen = Date.parse(item.seen_at || '')
  if (!Number.isFinite(seen)) return result('尚未收到 Agent 上报', '开启开关只是提交运行要求，还没有收到执行确认。', true)
  if (now - seen >= 90000) return result('Agent 上报已中断', '以下为最后一次上报内容，不能据此确认当前任务仍在运行。', true)
  if (item.config.version !== s.applied_version) return result('等待 Agent 应用配置', `期望版本 v${item.config.version}，Agent 已应用 v${s.applied_version}。`, true)
  const preparing: Record<string, [string, string]> = {
    checking: ['正在检查归档库', '正在识别已有数据集身份和归档表。'],
    waiting_lease: ['等待旧归档退出', '旧写入租约结束后自动准备新版表结构，已有数据保留。'],
    waiting_authorization: ['等待自动初始化授权', '等待服务端授权建表；若持续等待，请确认 Server 与 Agent 均已更新。'],
    migrating: ['正在准备归档表结构', '正在自动检查并迁移表结构，完成后接入已有月表并进入新版归档。'],
    registering: ['正在绑定归档数据集', '表结构已准备完成，正在等待服务端确认数据集身份。'],
  }
  if (s.error.startsWith('archive_prepare_')) {
    const reasons: Record<string, string> = {
      archive_prepare_identity_mismatch: '站点或数据集身份不一致，请核对归档目标配置。',
      archive_prepare_registration_conflict: '数据集身份或来源指纹与已登记信息冲突，请核对连接的数据库。',
      archive_prepare_tasks_pending: '已有独立补齐、核验或封存任务尚未完成，完成原任务后才能切换全量归档。',
      archive_prepare_connection_configuration_invalid: '归档连接配置不可用，请检查 Agent 配置。',
      archive_prepare_schema_mismatch: '归档结构与预期不一致，请检查表结构或迁移记录。',
      archive_prepare_version_unsupported: '归档库版本不受当前 Agent 支持，请核对配套版本。',
    }
    return result('归档初始化失败', `${reasons[s.error] || '请检查数据库连接、迁移权限及写入租约。'}（${s.error}）重试会保留同一数据集身份和已有数据。`, true)
  }
  if (s.error) return result('执行异常', s.error, true)
  const preparation = preparing[s.prepare_phase || '']
  if (preparation) return result(preparation[0], preparation[1])
  if (!s.configured) return result('归档目标尚未就绪', 'Agent 尚未通过目标连接或归档结构检查，请查看任务与策略。', true)
  if (!item.enabled) return result('站点未启用', '当前站点未启用，不能仅凭上次运行状态判断仍在执行。', true)
  if (!item.config.running) return result(s.state === 'paused' ? '已暂停' : '等待暂停确认', '已有进度会保留；恢复后按原位置继续。')
  if (s.state === 'waiting') return result('等待执行授权', 'Agent 已收到配置，尚未确认可执行；请核对任务与策略中的节点及配置状态。', true)
  if (s.state !== 'running') return result('等待运行确认', '尚未收到本次运行的有效执行状态。', true)
  if (item.config.reconcile_id) return result('按日对账', s.reconciliation ? `日期 ${s.reconciliation.date}；状态 ${s.reconciliation.state}。` : '尚未收到本次对账进度。')
  if (fullHistory && item.config.full_history && !s.workflow) return result('尚未收到全量任务进度', '运行开关已开启，但没有阶段上报；不能判断正在扫描、核验或等待。请核对数据集绑定与 Agent 版本。', true)
  if (s.workflow) {
    const stage = archiveWorkflowLabels[s.workflow.phase] || `未知阶段（${s.workflow.phase}）`
    return result(stage, `${s.workflow.date ? `最近处理日期 ${s.workflow.date}。` : '尚未上报处理日期。'} 阶段来自最近一次 Agent 上报，不代表此刻有数据库查询正在执行。`, !!s.workflow.error_code || /^[1-9]\d*$/.test(s.workflow.blocked_days))
  }
  return result('增量任务已启用', '当前上报无法区分批次执行、批次间隔或等待新日志；请结合最近提交时间和已提交日志 ID 观察推进。')
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
