export type ArchiveTask = 'migration' | 'organization' | 'verification' | 'collection'
export type ArchiveTaskGroup = 'collection' | 'history'

// Preserve the other task when pausing. Resuming a single task from a global
// pause must not unexpectedly resume the other task's retained enabled flags.
export function toggleArchiveGroup(config: ArchiveConfig, group: ArchiveTaskGroup): ArchiveConfig {
  if (!config.pipeline) return config
  const pipeline = { ...config.pipeline }
  const enabled = group === 'collection' ? pipeline.collection : pipeline.organization || pipeline.verification
  const start = !config.running || !enabled
  if (!config.running) {
    pipeline.collection = false
    pipeline.organization = false
    pipeline.verification = false
  }
  if (group === 'collection') pipeline.collection = start
  else { pipeline.organization = start; pipeline.verification = start }
  return { ...config, pipeline, running: config.running || start }
}

export function archiveDataLabel(kind: string): string {
  if (kind === 'sealed') return '已封存'
  if (['blocked', 'failed', 'mismatched'].includes(kind)) return '处理失败'
  if (['preparing','rebuilding','organization','verification','backfill','verify','seal','checking'].includes(kind)) return '处理中'
  if (['pending','collected','organized','changed','waiting_migration'].includes(kind)) return '待处理'
  if (kind === 'collecting') return '采集中'
  // Missing evidence is not a sixth lifecycle state and must not imply ready.
  if (kind === 'future') return '未来日期'
  return '状态待确认'
}
export type ArchivePipelineSettings = Record<ArchiveTask, boolean> & { collection_from?: string; collection_through?: string; collection_newest_first?: boolean; retry_token?: string }
export interface ArchivePipelineStatus {
 collection_done?: boolean; collection_date?: string; migration_done: boolean; cutoff: string; settings: ArchivePipelineSettings
 active: Partial<Record<ArchiveTask, {task: ArchiveTask; date?: string; token: string; revision: string}>>
 errors?: Partial<Record<ArchiveTask, string>>
 progress?: Partial<Record<ArchiveTask, {operation?: ArchiveOperation; diagnostic?: ArchiveDiagnostic; after_id: string; rows: string; updated_at: string}>>
}
export interface ArchiveConfig {
 pipeline?: ArchivePipelineSettings
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

export interface ArchiveWorkflowDay {
  diagnostic?: ArchiveDiagnostic
  raw?: { rows?: string; observed_at?: string; error_code?: string }
  date: string
  state: 'collecting' | 'waiting_migration' | 'organization' | 'verification' | 'organized' | 'changed' | 'collected' | 'unknown' | 'preparing' | 'pending' | 'rebuilding' | 'backfill' | 'verify' | 'seal' | 'sealed' | 'blocked'
  counts?: { log_rows: string; request_rows: string; error_rows: string }
  error_code?: string
  updated_at?: string
  observed_at: string
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
  workflow_days?: ArchiveWorkflowDay[]
  seen_at?: string
  status: {
    calendar_origin?: {date: string; source: 'archive' | 'source' | 'empty'; observed_at: string}
    calendar_origin_error?: string
    raw_position?: { table: string; id: string; log_time?: string; observed_at: string }
    raw_position_error?: string
    pipeline?: ArchivePipelineStatus
    operation?: ArchiveOperation
    diagnostic?: ArchiveDiagnostic
    workflow_daily?: { task_id: string; observed_at: string }
    workflow_daily_error?: string
	workflow?: { phase: string; date?: string; first_date?: string; imported_rows: string; completed_days: string; blocked_days: string; error_code?: string; issues?: {date: string; code: string}[]; preparation?: ArchivePreparationProgress }
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

export interface ArchivePreparationProgress {
  phase: string
  table?: string
  after_id: string
  processed_rows: string
  last_batch_rows: string
  committed_batches: string
  recorded_since: string
  last_committed_at: string
}

export interface ArchiveOperation {
  phase: string; code: string; database: 'source' | 'archive' | 'both' | 'control'; table?: string; date?: string
  state: 'executing' | 'completed' | 'failed'; started_at: string; finished_at?: string
}
export interface ArchiveDiagnostic {
  code: string; operation?: ArchiveOperation; mysql_number?: number; sql_state?: string
  row_id?: string; row_bytes?: string; occurred_at: string; retry_at?: string
}

export const archiveOperationNames: Record<string, string> = {
  write_raw_logs: '写入原始日志月表并回读检查', save_raw_ledger: '保存采集进度及原始记录指纹', prepare_date_index: '为归档月表建立日期索引，便于按日读取和计数',
  ensure_archive_table: '确认归档表存在，按需创建',
  read_seal_logs: '读取该日期日志，构建或审计封存版本',
  acquire_writer: '确认本节点的归档写入权限', advance_workflow: '读取任务位置并决定下一步',
  check_statistics_schema: '检查统计表是否就绪', check_month_table: '检查已有日志月表',
  read_existing_logs: '读取已归档的历史日志', begin_archive_batch: '开启本批事务并确认写入权限',
  clear_legacy_statistics: '分批清理旧统计依据，保留日志明细', rebuild_contributions: '重建每条日志的统计依据',
  update_date_catalog: '记录受影响的日期', compare_imported_logs: '回读比较本批历史日志',
  rebuild_daily_statistics: '汇总本批日志的每日统计', rebuild_monthly_statistics: '汇总本批日志的每月统计',
  save_workflow: '保存任务位置', commit_archive_batch: '提交本批数据与进度', select_month_table: '寻找下一张已有日志月表',
  check_source_index: '检查源日志能否按日期高效读取', select_source_date: '查找源日志的待归档日期',
  select_archive_date: '查找归档库中的待处理日期', prepare_date_scan: '恢复该日期的归档位置',
  read_source_clock: '读取源库时间，计算归档延迟窗口', read_source_logs: '分批读取这一天的源日志',
  write_archive_logs: '写入归档日志、更新统计并回读比较', verify_archive: '对比归档日志与本轮保存的源证据',
  build_sealed_version: '构建并审计不可变日期版本', read_progress: '读取已提交的任务进度',
  verify_archive_page: '逐批比较归档日志与源证据', save_verification: '保存本批校验结果',
}

export function archiveDiagnosticReason(code: string) {
  const reasons: Record<string, [string, string]> = {
    verification_field_mismatch: ['回读的日志字段与本批原始数据不一致', '按日志 ID 检查字段类型、触发器及并发写入；该批不会以校验通过提交。'],
    verification_missing_rows: ['回读校验发现本批日志缺失', '检查目标表写入结果、触发器和并发修改；保留提交记录进行定位。'],
    verification_unexpected_row: ['回读校验发现未预期或重复的日志', '检查主键约束及本批记录，定位重复或多余数据。'],
    verification_read_failed: ['回读归档日志时失败', '结合数据库编号检查处理表的查询权限、连接与锁等待。'],
    schema_check_failed: ['读取表结构或索引信息失败', '检查数据库连接及元数据读取权限，结合数据库编号定位。'],
    schema_provision_failed: ['归档表创建失败', '检查目标库建表权限和模板结构，结合数据库编号定位。'],
    writer_commit_uncertain: ['无法确认本批事务是否提交成功', '等待恢复时核对目标检查点与批次回执；不要直接重置游标或重复累计统计。'],
    database_permission_denied: ['数据库拒绝当前账号执行这项操作', '核对出错数据库、表和操作对应的授权；按缺失权限补齐后重试。'],
    database_authentication_failed: ['数据库账号认证失败', '核对该连接的用户名、密码及允许连接的主机。'],
    database_missing: ['连接指定的数据库不存在', '核对 Agent 连接配置中的数据库名。'],
    database_table_missing: ['本次操作需要的表不存在', '核对连接的归档库、源库和迁移是否完成；不要清空现有进度。'],
    database_column_missing: ['查询需要的字段不存在', '核对表结构与 Agent 版本，检查是否存在未完成的迁移。'],
    database_index_conflict: ['目标表存在同名但不符合要求的索引', '检查该表 idx_archive_created_id 的列定义；修正索引冲突后重试迁移'],
    database_duplicate_key: ['写入发生唯一键冲突', '核对该操作的主键或唯一索引；旧月表接入时重点检查同一日志 ID 是否出现在不同月表。不要直接删除冲突数据。'],
    database_lock_timeout: ['等待数据库锁超时', '检查该表的长事务或并发写入，释放阻塞后等待重试。'],
    database_deadlock: ['数据库检测到事务死锁', '本次事务被数据库中止；若反复出现，检查并发写入与锁顺序。'],
    database_connection_limit: ['数据库可用连接数不足', '检查当前连接数和账号连接限制。'],
    database_write_restricted: ['数据库当前限制写入', '核对是否连接了只读副本，或数据库启用了限制写入的运行选项。'],
    database_table_full: ['数据库无法继续扩展该表', '检查磁盘空间、表空间及存储限制。'],
    database_connection_lost: ['数据库连接在操作中断开', '检查数据库重启、网络和连接超时；提交时断开可能无法确认结果，恢复后以持久化进度为准。'],
    database_connection_failed: ['无法建立或保持数据库连接', '检查出错连接对应的网络、地址、端口和数据库可用性。'],
    database_connection_timeout: ['连接数据库超时', '检查网络连通性、数据库负载与连接超时设置。'],
    database_error: ['数据库返回了未归类的错误', '使用下方 MySQL 编号、SQLSTATE 和发生操作定位原因；当前证据不足以断定具体故障。'],
    operation_timeout: ['本次操作超过执行时间预算', '结合处理表检查慢查询、锁等待和批量大小；这不等于表中没有数据。'],
    operation_cancelled: ['操作被取消', '核对是否正在暂停、退出或更新 Agent；恢复后从已提交位置继续。'],
    source_index_missing: ['源日志缺少日期扫描所需索引', '检查 logs 的 created_at、id 索引是否满足扫描要求，并确认索引可见。'],
    archive_count_index_missing: ['归档月表缺少按日期计数所需索引', '检查归档月表是否有 created_at 开头的完整 BTREE 索引；系统不会为显示数量自动扫描整张宽表或自动建索引。'],
    archive_count_date_mismatch: ['归档月表中存在不属于该月的日期', '核对原始日志时间和月表归属；本次数量不会作为可信快照发布。'],
    source_index_check_failed: ['读取源日志索引信息失败', '核对源库连接及元数据读取权限；不能据此认定索引不存在。'],
    source_query_failed: ['读取日志数据失败', '结合数据库编号与发生操作检查连接、查询权限和表结构。'],
    row_too_large: ['单条日志超过读取字节预算', '按下方日志 ID 和字节数定位该行，再检查读取预算；任务不会跳过该行。'],
    invalid_source_row: ['日志字段不符合归档要求', '检查对应记录的 ID、数值字段与表结构；不要通过跳过游标掩盖问题。'],
    invalid_timestamp: ['日志时间字段无法解析', '按日志 ID 检查 created_at 的值和类型。'],
    source_invalid_date: ['源日志存在空值或无效日期', '检查 created_at 为空或小于等于 0 的日志，处理后继续。'],
    source_history_unknown: ['尚未确认源历史保留范围', '仅在符合实际保留策略时确认历史声明；不能把未知历史当成完整数据。'],
    source_history_unconfirmed: ['尚未确认历史完整保留且稳定', '归档可以继续；核对真实保留策略后再启用核验及封存条件。'],
    source_cleared: ['所需源历史已超出保留范围', '已有归档保留，但缺少源证据，不能自动认定完整。'],
    verification_mismatched: ['归档日志与本轮源证据不一致', '查看该日期的差异记录，区分缺失、变化或多余日志；修复后重新核验。'],
    verification_mismatch: ['归档日志与核验依据不一致', '查看该日期的差异记录，完成修复后再封存。'],
    writer_lease: ['写入授权已过期或被其他节点接替', '等待新授权；持续发生时核对执行节点、控制连接和时钟。'],
    checkpoint_conflict: ['保存的游标与提交记录不一致', '检查归档检查点和批次回执；不要手动重置游标或删除台账。'],
    receipt_conflict: ['同一批次的回执与数据不一致', '检查是否存在并发写入或重复批次身份；保留原始回执以便定位。'],
    date_frozen: ['该日期正在封存，暂时不能修改', '等待该日期的封存或写入授权结束后重试。'],
    schema_changed: ['表结构与归档要求不一致', '检查结构指纹、迁移记录与数据库引擎，不要仅修改版本号。'],
    repair_unscoped_target: ['日志所在月表与其记录日期不一致', '按日志日期核对月表归属，修复后重新接入。'],
    cohort_incomplete: ['跨日期关联范围超过本轮允许范围', '检查关联记录涉及的日期，待关联范围满足条件后重试。'],
    cohort_not_ended: ['关联日期尚未结束', '等待关联日期结束且超过延迟窗口后重试。'],
    verification_expired: ['校验证据已过期', '重新获取该日期的有效证据并核验。'],
    budget_exhausted: ['本批达到执行预算', '等待下批接续；如果始终没有提交，检查单条数据大小和查询耗时。'],
  }
  const [reason, action] = reasons[code] || ['当前错误尚未归类，无法据此断定原因', '保留错误代码、发生时间、处理表和 Agent 日志进行定位。']
  return { reason, action }
}

export function archiveWorkflowSteps(item: ArchiveItem) {
  const current = item.status.operation
  const phase = current?.state === 'executing' && current.phase !== 'acquire' ? current.phase : item.status.workflow?.phase
  const index = item.status.prepare_phase ? 0 : ({reset_state:1, reset_daily:1, reset_monthly:1, import_target:2, source_scan:3, live:3, backfill:3, verify:4, seal:5} as Record<string, number>)[phase || '']
  return [
    ['准备归档库', '检查连接、建表和绑定'], ['整理旧统计', '保留明细，清理旧统计依据'],
    ['接入历史归档', '读取已有月表，重新汇总'], ['按日归档', '读取源日志，补齐与回读'],
    ['校验完整性', '归档日志与源证据比较'], ['封存日期', '生成不可变版本，继续下一天'],
  ].map(([title, detail], i) => ({ title, detail, current:index === i, passed:index !== undefined && i < index && i < 3, daily:i >= 3 }))
}

export function archiveOperationActivity(item: ArchiveItem, now: number, readFailed = false) {
  const o = item.status.operation
  const seen = Date.parse(item.seen_at || '')
  if (readFailed || !Number.isFinite(seen) || now - seen >= 90000) return '状态已过期，仅保留最后上报的操作'
  if (item.config.version !== item.status.applied_version) return '等待配置生效，以下为最近上报操作'
  if (!item.config.running || !item.enabled) return item.status.state === 'paused' ? '已暂停，以下为暂停前的操作' : '等待暂停确认，可能仍有一批尚未结束'
  if (item.status.error) return '执行异常，查看下方原因与处理建议'
  if (!o) return '尚无具体操作上报；当前只能确认流程阶段'
  if (!item.status.configured || item.status.state !== 'running') return '等待执行确认，以下为最近上报操作'
  return o.state === 'executing' ? '最近上报：正在执行此操作' : o.state === 'failed' ? '最近一次操作失败，等待执行状态更新' : '最近一批已返回，等待下一次调度'
}

export const archiveWorkflowLabels: Record<string, string> = {
  reset_state: '清理旧贡献记录', reset_daily: '清理旧日统计', reset_monthly: '清理旧月统计',
  import_target: '接入已有归档', source_scan: '切换逐日推进', live: '持续归档 / 调度日期',
  backfill: '补齐并比较', verify: '归档库核验', seal: '封存版本',
}

const preparationPhases = ['reset_state', 'reset_daily', 'reset_monthly', 'import_target']

export function archiveWorkflowOperation(phase: string) {
  const operations: Record<string, { detail: string; next: string; target: string }> = {
    reset_state: { detail: '分批清理旧归档的统计依据，原始日志月表保留。这一步尚未读取源日志，也不会增加“已复用历史日志”。', next: '清理旧日统计 → 清理旧月统计 → 读取已有月表并重建统计 → 逐日补齐', target: '旧贡献记录（archive_log_state）' },
    reset_daily: { detail: '分批清理旧日统计，随后从已有归档日志重新计算；原始日志月表保留。', next: '清理旧月统计 → 读取已有月表并重建统计 → 逐日补齐', target: '旧日统计（log_daily_stats）' },
    reset_monthly: { detail: '分批清理旧月统计，随后从已有归档日志重新计算；原始日志月表保留。', next: '读取已有月表并重建统计 → 逐日补齐', target: '旧月统计（log_monthly_stats）' },
    import_target: { detail: '按日志 ID 分批读取已有归档月表，重建贡献记录和日/月统计，并回读比较原始日志。此阶段不读取源库历史。', next: '按最早日期补齐源日志 → 核验归档库 → 满足条件后封存', target: '已有归档月表' },
    source_scan: { detail: '恢复旧任务状态并切换为按日期推进，保存已有归档进度。', next: '选择最早待处理日期', target: '任务状态' },
    live: { detail: '选择下一待处理日期；历史处理后继续采集当天超过归档延迟窗口的日志。', next: '分批补齐所选日期', target: '待处理日期' },
    backfill: { detail: '按日期分批读取源日志，补入缺失记录、修复变化记录，并回读比较归档数据；当天仅处理超过延迟窗口的日志。', next: '日期结束后收尾补齐；满足历史声明条件后核验', target: '源日志与对应归档月表' },
    verify: { detail: '读取归档月表，逐条比较本轮补齐保存的源记录证据，检查缺失、变化和多余记录。', next: '核验通过后封存；存在差异则记录受阻原因', target: '归档月表与本轮源证据' },
    seal: { detail: '分批构建并校验不可变归档版本，完成后发布该日期及关联日期的封存结果。', next: '处理下一日期', target: '归档版本' },
  }
  return operations[phase] || { detail: `尚无法解释 Agent 上报的阶段 ${phase}。`, next: '等待受支持的阶段上报', target: '尚未上报' }
}

/** Progress timestamps come from committed work, never from the Agent heartbeat. */
export function archivePreparationActivity(item: ArchiveItem, now: number, readFailed = false) {
  const workflow = item.status.workflow
  if (!workflow || !preparationPhases.includes(workflow.phase)) return undefined
  const progress = workflow.preparation
  const result = (message: string, attention = false) => ({ progress, message, attention })
  if (readFailed) return result('状态读取失败，以下保留最近一次记录，无法确认是否继续推进。', true)
  const seen = Date.parse(item.seen_at || '')
  if (!Number.isFinite(seen) || now - seen >= 90000) return result('Agent 上报中断，以下为历史提交记录。', true)
  if (item.status.error) return result('任务执行异常，以下保留最后成功提交的记录。', true)
  if (!item.enabled || !item.config.running) return result('任务已停用或正在暂停，已提交进度保留。')
  if (item.config.version !== item.status.applied_version || !item.status.configured || item.status.state !== 'running') return result('等待 Agent 确认执行，以下为最近提交记录。', true)
  if (!progress) return result('当前 Agent 尚未上报批次明细，无法判断已处理量；配套升级后开始记录，已有任务接续执行。', true)
  const committed = Date.parse(progress.last_committed_at)
  if (!Number.isFinite(committed) || committed > now + 30000) return result('批次提交时间不可用或存在时钟差异，无法判断最近推进情况。', true)
  if (now - committed > Math.max(90000, (item.config.interval_seconds * 3 + 60) * 1000)) return result('已有一段时间未收到新的批次提交；可能正在执行、等待或重试，不能仅凭心跳认定正常。', true)
  if (progress.phase !== workflow.phase) return result('上一阶段已完成，等待当前阶段首批提交；下方为上一阶段最后记录。')
  return result('近期有批次提交。页面每 15 秒更新，可比较累计处理量和提交时间确认推进。')
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
  if (s.error) return result('执行异常', archiveDiagnosticReason(s.diagnostic?.code || s.error).reason, true)
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
    return result(stage, `${archiveWorkflowOperation(s.workflow.phase).detail}${s.workflow.date ? ` 最近处理日期 ${s.workflow.date}。` : ''}`, !!s.workflow.error_code || /^[1-9]\d*$/.test(s.workflow.blocked_days))
  }
  return result('增量任务已启用', '当前上报无法区分批次执行、批次间隔或等待新日志；请结合最近提交时间和已提交日志 ID 观察推进。')
}

export interface ArchiveResponse {
  items: ArchiveItem[]
  month: string
  // Missing capabilities on a legacy server must be read as false, never inferred from counts.
  capabilities?: {
    four_tasks?: boolean
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
  kind: 'future' | 'today' | 'reported' | 'unknown' | 'matched' | 'mismatched' | 'failed' | 'checking' | ArchiveWorkflowDay['state']
  workflow?: ArchiveWorkflowDay
  counts?: ArchiveWorkflowDay['counts']
  raw?: ArchiveWorkflowDay['raw']
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
    const base = { date, ...(report ? { reported: report, counts: { log_rows: report.archived_rows, request_rows: report.request_rows, error_rows: report.error_rows } } : {}) }
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

export function archiveDayReason(code: string): string {
  const reasons: Record<string, string> = { source_history_unconfirmed: '已完成本轮补齐，待确认源历史完整保留且稳定后才能封存', source_history_unknown: '源历史保留情况尚未确认', verification_mismatched: '核验发现差异，需检查并重试', source_cleared: '源历史已清理，已有归档保留', source_index_missing: '源日志缺少日期扫描索引', source_invalid_date: '源日志存在无效日期', cohort_incomplete: '关联日期范围过大', cohort_not_ended: '等待关联日期结束', verification_expired: '核验已超时，需重试', row_too_large: '单条日志超过读取预算' }
  const detail = archiveDiagnosticReason(code)
  return `${reasons[code] || detail.reason}；${detail.action}（${code}）`
}

export function buildWorkflowDays(month: string, item: ArchiveItem, today: string): ArchiveCalendarDay[] {
  const reports = new Map((item.workflow_days || []).map(day => [day.date, day]))
  const workflow = item.config?.pipeline ? undefined : item.status.workflow
  const labels: Record<ArchiveWorkflowDay['state'], [string, ArchiveCalendarDay['tagType'], string]> = {
    collecting: ['采集中 / 等待跨日信号', 'info', '已有当日日志；尚未观察到下一日期的已提交记录，不能开始历史整理。'],
    waiting_migration: ['等待版本迁移', 'info', '已观察到跨日采集边界，等待一次性统计迁移结束。'],
    collected: ['采集完成 · 待整理', 'info', '已观察到下一日期的记录，可以整理；迟到写入仍会使结果失效。'],
    organization: ['历史整理中', 'info', '读取当天月表，重建统计依据和日、月统计，不读取源库。'],
    organized: ['整理完成 · 待校验', 'info', '该日期当前修订的统计已整理，等待校验任务；校验与采集互斥。'],
    verification: ['校验与封存', 'info', '补齐并比较源记录，核验归档后生成日期版本；采集暂时让出源库。'],
    changed: ['数据变化 · 待重整', 'warning', '封存后又有日志写入或变更；旧版本保留，当前结果已失效，重新整理后再校验。'],
    unknown: ['处理状态待上报' , 'info', '月表数量与处理状态独立；尚未收到该日期的处理结果。'],
    preparing: ['准备中', 'info', '正在清理旧统计，暂不展示旧计数；已有原始月表保留。'],
    pending: ['待逐日处理', 'info', '尚未收到该日完整的补齐、核验和封存结果。'],
    rebuilding: ['重建统计中', 'info', '正在接入已有月表；计数仅为已重建部分，不是该日原始日志总量。'],
    backfill: ['补齐并比较', 'info', '最近上报阶段为读取源日志、补齐并回读比较归档。'],
    verify: ['归档库核验', 'info', '最近上报阶段为核对归档与本轮源记录证据。'],
    seal: ['封存中', 'info', '正在构建日期版本，尚未确认封存完成。'],
    sealed: ['已封存', 'success', 'Agent 已上报该日封存完成；归档出账功能仍未开放。'],
    blocked: ['需关注', 'warning', '该日暂未满足核验或封存条件，查看受阻原因。'],
  }
  // Never merge legacy site receipts into the active dataset's daily view.
  return buildArchiveDays(month, [], today).map(day => {
    if (day.kind === 'future') return day
    const report = reports.get(day.date)
    day = { ...day, raw: report?.raw, ...(report ? { workflow: report } : {}) }
    let state = report?.state
    let counts = report?.counts
    let code = report?.error_code
    if (workflow?.phase.startsWith('reset_')) { state = 'preparing'; counts = undefined; code = undefined }
    else if (workflow?.phase === 'import_target') { state = counts ? 'rebuilding' : report ? 'pending' : undefined; code = undefined }
    else {
      const issue = workflow?.issues?.find(issue => issue.date === day.date)
      if (issue) { state = 'blocked'; code = issue.code }
      if (workflow?.date === day.date && ['backfill', 'verify', 'seal'].includes(workflow.phase)) { state = workflow.phase as ArchiveWorkflowDay['state']; code = workflow.error_code }
    }
    if (!state || !(state in labels)) return { ...day, kind: day.kind === 'today' ? 'today' : 'unknown', label: day.kind === 'today' ? '今日 · 状态待上报' : '处理状态待上报', reason: '页面尚未收到该日期的处理状态和统计数量。归档月表可能已经有日志；此处不代表无数据、未采集或采集失败。需由远程 Server 和 Agent 提供每日数据上报后，才能展示真实数量与处理进度。' }
    const [label, tagType, reason] = labels[state]
    return { ...day, kind: state, label, tagType, reason: code ? archiveDayReason(code) : reason, counts, ...(report ? { workflow: report } : {}) }
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
