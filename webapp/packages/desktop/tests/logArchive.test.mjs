import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/logArchive.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { beijingDate, buildArchiveDays, buildWorkflowDays, formatArchiveCount, canCheckArchiveDay, archiveExecution, archivePreparationActivity, archiveWorkflowOperation, archiveWorkflowSteps, archiveOperationActivity, archiveDiagnosticReason } = await import(
  `data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`
)

const report = (date, archivedRows = '12') => ({
  date, archived_rows: archivedRows, request_rows: '10', error_rows: '2',
  last_id: '9007199254740993', verified_at: '2026-09-20T00:00:00Z',
})
const comparison = (date, state = 'matched') => ({
  id: 'reconcile-1', date, state, source_rows: 0, target_rows: 0,
})
const byDate = (rows, date) => rows.find(row => row.date === date)

test('remote without daily reports means missing status, not missing raw logs', () => {
  const item = {days:[report('2026-09-01','999999')],status:{workflow:{phase:'import_target',imported_rows:'5951524'}}}
  const days = buildWorkflowDays('2026-09',item,'2026-09-22')
  const day = byDate(days,'2026-09-01')
  assert.equal(day.label,'处理状态待上报')
  assert.match(day.reason,/归档月表可能已经有日志/)
  assert.match(day.reason,/不代表无数据/)
  assert.equal(day.counts,undefined)
  assert.equal(byDate(days,'2026-09-22').label,'今日 · 状态待上报')
  assert.equal(byDate(days,'2026-09-23').kind,'future')
})

test('workflow daily view preserves partial counts and does not reuse legacy receipts', () => {
  const item = {days:[report('2026-09-01','999999')], workflow_days:[{date:'2026-09-02',state:'rebuilding',counts:{log_rows:'9007199254740993',request_rows:'1',error_rows:'0'},observed_at:'2026-09-21T12:00:00Z'}],status:{workflow:{phase:'import_target'}}}
  let days=buildWorkflowDays('2026-09',item,'2026-09-21')
  assert.equal(days.length,30)
  assert.equal(byDate(days,'2026-09-01').counts,undefined)
  assert.equal(byDate(days,'2026-09-01').kind,'unknown')
  assert.equal(byDate(days,'2026-09-02').counts.log_rows,'9007199254740993')
  assert.match(byDate(days,'2026-09-02').reason,/已重建部分/)
  assert.equal(byDate(days,'2026-09-22').kind,'future')
  item.status.workflow.phase='reset_state'
  days=buildWorkflowDays('2026-09',item,'2026-09-21')
  assert.equal(byDate(days,'2026-09-02').counts,undefined)
  assert.equal(byDate(days,'2026-09-02').kind,'preparing')
})

test('workflow daily view displays active empty date, blocked reason and completed seals', () => {
  const item={workflow_days:[{date:'2026-09-01',state:'sealed',counts:{log_rows:'0',request_rows:'0',error_rows:'0'}}],status:{workflow:{phase:'verify',date:'2026-09-03',issues:[{date:'2026-09-02',code:'source_history_unconfirmed'}]}}}
  const days=buildWorkflowDays('2026-09',item,'2026-09-21')
  assert.equal(byDate(days,'2026-09-01').kind,'sealed')
  assert.equal(byDate(days,'2026-09-01').counts.log_rows,'0')
  assert.equal(byDate(days,'2026-09-02').kind,'blocked')
  assert.match(byDate(days,'2026-09-02').reason,/确认源历史/)
  assert.equal(byDate(days,'2026-09-03').kind,'verify')
  assert.equal(byDate(days,'2026-09-03').counts,undefined)
  assert.equal(byDate(buildWorkflowDays('2026-08',item,'2026-09-21'),'2026-08-01').counts,undefined)
})

const executionNow = Date.parse('2026-09-21T01:00:00Z')
const executionItem = () => ({
  enabled: true, seen_at: new Date(executionNow - 1000).toISOString(),
  config: {agent_id: 'agent-a', version: 2, running: true},
  status: {configured: true, applied_version: 2, state: 'running', error: '', last_id: '9007199254740993'},
})

test('daily workflow loop never implies all dates have passed earlier daily steps', () => {
  const item=executionItem();item.status.workflow={phase:'seal'}
  let steps=archiveWorkflowSteps(item)
  assert.equal(steps[5].current,true)
  assert.equal(steps[3].passed,false)
  assert.equal(steps[4].passed,false)
  item.status.workflow.phase='live'
  steps=archiveWorkflowSteps(item)
  assert.equal(steps[3].current,true)
  assert.equal(steps[5].passed,false)
  item.status.workflow.phase='new_unknown_phase'
  assert.equal(archiveWorkflowSteps(item).some(step=>step.current||step.passed),false)
})

test('operation snapshots cannot override pause, stale heartbeat, config change or failure', () => {
  const item=executionItem();item.status.operation={phase:'import_target',state:'executing',code:'read_existing_logs'}
  assert.match(archiveOperationActivity(item,executionNow),/正在执行/)
  item.config.running=false;item.status.state='paused'
  assert.match(archiveOperationActivity(item,executionNow),/已暂停/)
  assert.match(archiveOperationActivity(item,executionNow,true),/过期/)
  item.config.running=true;item.status.state='running';item.config.version++
  assert.match(archiveOperationActivity(item,executionNow),/等待配置/)
  item.status.applied_version++;item.status.error='database_permission_denied'
  assert.match(archiveOperationActivity(item,executionNow),/执行异常/)
})

test('diagnostics distinguish permission, duplicate, locks and unknown causes', () => {
  assert.match(archiveDiagnosticReason('database_permission_denied').reason,/拒绝/)
  assert.match(archiveDiagnosticReason('database_duplicate_key').action,/同一日志 ID/)
  assert.match(archiveDiagnosticReason('database_lock_timeout').action,/长事务/)
  assert.match(archiveDiagnosticReason('new_unknown_code').reason,/无法据此断定/)
  const item=executionItem();item.status.error='batch failed';item.status.diagnostic={code:'database_permission_denied'}
  assert.match(archiveExecution(item,true,executionNow).detail,/拒绝/)
})

test('legacy running status does not claim an active batch or caught-up position', () => {
  const result = archiveExecution(executionItem(), false, executionNow)
  assert.equal(result.mode, '增量归档')
  assert.equal(result.title, '增量任务已启用')
  assert.match(result.detail, /无法区分/)
})

test('missing workflow cannot appear as a running scan or zero completed days', () => {
  const item = executionItem(); item.config.full_history = true
  const result = archiveExecution(item, true, executionNow)
  assert.equal(result.title, '尚未收到全量任务进度')
  assert.equal(result.attention, true)
  assert.equal(formatArchiveCount(item.status.workflow?.completed_days), '—')
})

test('supporting full history does not mean the operator enabled it', () => {
  const item = executionItem(); item.active_dataset_id = 'dataset'
  const result = archiveExecution(item, true, executionNow)
  assert.equal(result.mode, '数据集增量归档')
  assert.equal(result.title, '增量任务已启用')
})

test('read failures and offline status take precedence over stale workflow progress', () => {
  const item = executionItem(); item.config.full_history = true
  item.status.workflow = {phase: 'backfill', date: '2026-09-01', blocked_days: '0'}
  assert.equal(archiveExecution(item, true, executionNow, true).title, '状态读取失败')
  item.seen_at = new Date(executionNow - 90000).toISOString()
  assert.equal(archiveExecution(item, true, executionNow).title, 'Agent 上报已中断')
  item.seen_at = ''
  assert.equal(archiveExecution(item, true, executionNow).title, '尚未收到 Agent 上报')
})

test('configuration mismatch, target errors, pause and authorization waits remain distinct', () => {
  const item = executionItem()
  item.config.version = 3
  assert.equal(archiveExecution(item, false, executionNow).title, '等待 Agent 应用配置')
  item.status.applied_version = 3; item.status.configured = false; item.status.error = 'archive identity/schema/checkpoint preflight failed'
  assert.match(archiveExecution(item, false, executionNow).detail, /尚未归类/)
  item.status.error = ''
  assert.equal(archiveExecution(item, false, executionNow).title, '归档目标尚未就绪')
  item.status.configured = true; item.config.running = false; item.status.state = 'paused'
  assert.equal(archiveExecution(item, false, executionNow).title, '已暂停')
  item.config.running = true; item.status.state = 'waiting'
  assert.equal(archiveExecution(item, false, executionNow).title, '等待执行授权')
})

test('reported workflow distinguishes verification from copying and preserves blocked evidence', () => {
  const item = executionItem(); item.config.full_history = true
  item.status.workflow = {phase: 'verify', date: '2026-09-01', blocked_days: '2'}
  let result = archiveExecution(item, true, executionNow)
  assert.equal(result.title, '归档库核验')
  assert.match(result.detail, /2026-09-01/)
  assert.equal(result.attention, true)
  item.status.workflow.phase = 'future_phase'
  result = archiveExecution(item, true, executionNow)
  assert.match(result.title, /未知阶段.*future_phase/)
})

const preparationItem = () => {
  const item = executionItem()
  item.config.full_history = true
  item.config.interval_seconds = 2
  item.status.workflow = { phase: 'reset_state', imported_rows: '0', completed_days: '0', blocked_days: '0', preparation: {
    phase: 'reset_state', table: 'archive_log_state', after_id: '0', processed_rows: '9007199254740993', last_batch_rows: '1000', committed_batches: '9',
    recorded_since: new Date(executionNow - 60000).toISOString(), last_committed_at: new Date(executionNow - 2000).toISOString(),
  } }
  return item
}

test('preparation explains actual cleanup and shows recorded counters without inferring a total', () => {
  const item = preparationItem()
  const result = archivePreparationActivity(item, executionNow)
  assert.equal(result.attention, false)
  assert.match(result.message, /近期有批次提交/)
  assert.equal(result.progress.processed_rows, '9007199254740993')
  const execution = archiveExecution(item, true, executionNow)
  assert.equal(execution.title, '清理旧贡献记录')
  assert.match(execution.detail, /原始日志月表保留/)
  assert.match(archiveWorkflowOperation('reset_state').target, /archive_log_state/)
  assert.match(archiveWorkflowOperation('import_target').detail, /不读取源库历史/)
})

test('legacy Agent missing detailed progress is unknown rather than zero', () => {
  const item = preparationItem(); delete item.status.workflow.preparation
  const result = archivePreparationActivity(item, executionNow)
  assert.equal(result.progress, undefined)
  assert.match(result.message, /尚未上报批次明细/)
  assert.equal(formatArchiveCount(result.progress?.processed_rows), '—')
})

test('fresh heartbeat cannot make old preparation commits appear to be advancing', () => {
  const item = preparationItem()
  item.status.workflow.preparation.last_committed_at = new Date(executionNow - 120000).toISOString()
  let result = archivePreparationActivity(item, executionNow)
  assert.equal(result.attention, true)
  assert.match(result.message, /未收到新的批次提交/)
  assert.doesNotMatch(result.message, /卡死|已停止/)
  item.config.interval_seconds = 300
  result = archivePreparationActivity(item, executionNow)
  assert.equal(result.attention, false, 'respect long configured batch intervals')
})

test('pause, error, offline, config change and read failure override recent commit evidence', () => {
  const item = preparationItem()
  assert.match(archivePreparationActivity(item, executionNow, true).message, /状态读取失败/)
  item.config.running = false
  assert.match(archivePreparationActivity(item, executionNow).message, /暂停/)
  item.config.running = true; item.status.error = 'timeout'
  assert.match(archivePreparationActivity(item, executionNow).message, /执行异常/)
  item.status.error = ''; item.config.version++
  assert.match(archivePreparationActivity(item, executionNow).message, /等待 Agent 确认/)
  item.seen_at = new Date(executionNow - 90000).toISOString()
  assert.match(archivePreparationActivity(item, executionNow).message, /上报中断/)
})

test('phase transitions label previous counters and finished preparation does not masquerade as live progress', () => {
  const item = preparationItem(); item.status.workflow.phase = 'reset_daily'
  assert.match(archivePreparationActivity(item, executionNow).message, /上一阶段最后记录/)
  item.status.workflow.phase = 'backfill'
  assert.equal(archivePreparationActivity(item, executionNow), undefined)
})

test('unusable or future commit timestamp cannot prove recent activity', () => {
  for (const date of ['', 'invalid', new Date(executionNow + 60000).toISOString()]) {
    const item = preparationItem(); item.status.workflow.preparation.last_committed_at = date
    assert.equal(archivePreparationActivity(item, executionNow).attention, true)
  }
})

test('archive business date changes at Beijing midnight, including the year boundary', () => {
  assert.equal(beijingDate(Date.parse('2026-09-19T15:59:59.999Z')), '2026-09-19')
  assert.equal(beijingDate(Date.parse('2026-09-19T16:00:00.000Z')), '2026-09-20')
  assert.equal(beijingDate(Date.parse('2026-12-31T16:00:00.000Z')), '2027-01-01')
})

test('automatic preparation distinguishes draining, migration, registration and failure', () => {
  const item = executionItem(); item.status.state = 'waiting'
  for (const [code, title] of Object.entries({
    waiting_lease: '等待旧归档退出',
    migrating: '正在准备归档表结构',
    registering: '正在绑定归档数据集',
  })) {
    item.status.prepare_phase = code
    const result = archiveExecution(item, false, executionNow)
    assert.equal(result.title, title)
    assert.equal(result.attention, false)
    assert.equal(result.mode, '新版归档初始化')
  }
  item.status.error = 'archive_prepare_identity_mismatch'
  const failed = archiveExecution(item, false, executionNow)
  assert.equal(failed.title, '归档初始化失败')
  assert.equal(failed.attention, true)
  assert.match(failed.detail, /archive_prepare_identity_mismatch/)
})

test('initialization errors show actionable reasons while preserving the error code', () => {
  const item = executionItem(); item.status.state = 'error'; item.status.prepare_phase = 'failed'
  for (const [code, reason] of Object.entries({
    archive_prepare_connection_configuration_invalid: '连接配置不可用',
    archive_prepare_registration_conflict: '来源指纹',
    archive_prepare_tasks_pending: '原任务',
  })) {
    item.status.error = code
    const result = archiveExecution(item, false, executionNow)
    assert.equal(result.title, '归档初始化失败')
    assert.ok(result.detail.includes(reason))
    assert.ok(result.detail.includes(code))
    assert.equal(result.attention, true)
  }
})

test('calendar includes every date and follows leap-year century rules', () => {
  for (const [month, count, last] of [
    ['2024-02', 29, '2024-02-29'], ['2026-02', 28, '2026-02-28'],
    ['2000-02', 29, '2000-02-29'], ['1900-02', 28, '1900-02-28'],
    ['2026-04', 30, '2026-04-30'], ['2026-12', 31, '2026-12-31'],
  ]) {
    const rows = buildArchiveDays(month, [], '2027-01-01')
    assert.equal(rows.length, count)
    assert.equal(rows[0].date, `${month}-01`)
    assert.equal(rows.at(-1).date, last)
    assert.equal(new Set(rows.map(row => row.date)).size, count)
  }
})

test('invalid year/month and invalid current date cannot produce a misleading calendar', () => {
  for (const month of ['', '2026-00', '2026-13', '2026-9', '26-09', '0000-01', '2026-09-01', '2026-09 ']) {
    assert.deepEqual(buildArchiveDays(month, [], '2026-09-20'), [])
  }
  for (const today of ['', '2026-02-29', '2026-09-00', '2026-13-01']) {
    assert.deepEqual(buildArchiveDays('2026-09', [], today), [])
  }
})

test('today and future dates take precedence over reports and stale comparison results', () => {
  for (const date of ['2026-09-20', '2026-09-21']) {
    const row = byDate(buildArchiveDays('2026-09', [report(date)], '2026-09-20', comparison(date)), date)
    assert.equal(row.kind, date.endsWith('20') ? 'today' : 'future')
    assert.equal(row.reconciliation, undefined)
    assert.doesNotMatch(row.label, /已封存|对账一致/)
  }
})

test('no report remains unknown; an explicit zero report is still only a cumulative report', () => {
  const rows = buildArchiveDays('2026-09', [report('2026-09-02', '0')], '2026-09-20')
  const unknown = byDate(rows, '2026-09-01')
  assert.equal(unknown.kind, 'unknown')
  assert.equal(unknown.reported, undefined)
  assert.equal(formatArchiveCount(unknown.reported?.archived_rows), '—')
  const zero = byDate(rows, '2026-09-02')
  assert.equal(zero.kind, 'reported')
  assert.equal(zero.label, '已上报')
  assert.equal(zero.reported.archived_rows, '0')
  assert.match(zero.reason, /不代表.*完整.*封存/)
})

test('matched applies only to its date and never certifies sealing, completeness, or zero business', () => {
  const result = comparison('2026-09-02')
  const rows = buildArchiveDays('2026-09', [report('2026-09-01')], '2026-09-20', result)
  const matched = byDate(rows, '2026-09-02')
  assert.equal(matched.kind, 'matched')
  assert.equal(matched.label, '最近对账一致')
  assert.equal(matched.reconciliation, result)
  assert.equal(matched.reported, undefined)
  assert.match(matched.reason, /不代表.*封存.*完整.*零业务/)
  assert.equal(byDate(rows, '2026-09-01').kind, 'reported')
  assert.equal(rows.filter(row => row.reconciliation).length, 1)
  assert.equal(buildArchiveDays('2026-08', [], '2026-09-20', result).some(row => row.reconciliation), false)
})

test('running, mismatched and failed comparisons remain distinct from a successful report', () => {
  for (const [state, kind] of [['running', 'checking'], ['mismatched', 'mismatched'], ['failed', 'failed']]) {
    const row = byDate(buildArchiveDays('2026-09', [report('2026-09-02')], '2026-09-20', comparison('2026-09-02', state)), '2026-09-02')
    assert.equal(row.kind, kind)
    assert.equal(row.reconciliation.state, state)
    assert.notEqual(row.tagType, 'success')
  }
  const row = byDate(buildArchiveDays('2026-09', [], '2026-09-20', comparison('2026-09-02', 'unsupported')), '2026-09-02')
  assert.equal(row.kind, 'unknown')
})

test('undated, out-of-month and impossible reports are not included or silently relabeled', () => {
  const valid = Object.freeze(report('2026-09-01'))
  const reports = Object.freeze([valid, report('undated'), report('2026-08-31'), report('2026-09-31')])
  const rows = buildArchiveDays('2026-09', reports, '2026-09-20')
  assert.equal(rows.length, 30)
  assert.deepEqual(rows.filter(row => row.reported).map(row => row.date), ['2026-09-01'])
  assert.equal(rows[0].reported, valid)
})

test('count formatting keeps every large integer digit and distinguishes unknown from zero', () => {
  assert.equal(formatArchiveCount('90071992547409931234567890123456789012'), '90,071,992,547,409,931,234,567,890,123,456,789,012')
  assert.equal(formatArchiveCount('0'), '0')
  assert.equal(formatArchiveCount('1000'), '1,000')
  for (const value of [undefined, '', 'NaN', '1.2', '-1', '1e9']) assert.equal(formatArchiveCount(value), '—')
})

test('day check requires strict passage beyond Beijing day end plus the configured delay', () => {
  const boundary = Date.parse('2026-09-20T00:05:00+08:00')
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary - 1), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary + 1), true)
  assert.equal(canCheckArchiveDay('2026-09-20', '2026-09-20', 300, boundary + 1), false)
  assert.equal(canCheckArchiveDay('2026-09-21', '2026-09-20', 300, boundary + 1), false)
})

test('day check handles leap/year boundaries and a full day of delay without local timezone arithmetic', () => {
  assert.equal(canCheckArchiveDay('2024-02-29', '2024-03-01', 300, Date.parse('2024-03-01T00:05:00.001+08:00')), true)
  assert.equal(canCheckArchiveDay('2026-12-31', '2027-01-01', 300, Date.parse('2027-01-01T00:05:00.001+08:00')), true)
  const boundary = Date.parse('2026-09-20T00:00:00+08:00')
  assert.equal(canCheckArchiveDay('2026-09-18', '2026-09-20', 86400, boundary), false)
  assert.equal(canCheckArchiveDay('2026-09-18', '2026-09-20', 86400, boundary + 1), true)
})

test('invalid dates, delay values and clocks cannot enable a check', () => {
  const now = Date.parse('2026-09-20T12:00:00+08:00')
  for (const date of ['2026-02-29', '2026-09-31', '2026-09-00', '2026-9-01', 'undated', '']) {
    assert.equal(canCheckArchiveDay(date, '2026-09-20', 300, now), false)
  }
  for (const delay of [-1, 0.5, Infinity, NaN]) assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', delay, now), false)
  assert.equal(canCheckArchiveDay('2026-09-19', 'invalid', 300, now), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, NaN), false)
})

 test('raw monthly counts stay independent of partial statistics and pipeline overrides',()=>{
 const observed='2026-09-22T01:00:00Z'
 const item={config:{pipeline:{}},status:{workflow:{phase:'reset_state'}},workflow_days:[{date:'2026-09-01',state:'changed',counts:{log_rows:'5',request_rows:'4',error_rows:'1'},raw:{rows:'9007199254740993',observed_at:observed,error_code:'operation_timeout'},observed_at:observed}]}
 const day=byDate(buildWorkflowDays('2026-09',item,'2026-09-22'),'2026-09-01')
 assert.equal(day.kind,'changed');assert.equal(day.raw.rows,'9007199254740993');assert.equal(day.counts.log_rows,'5')
 assert.equal(day.raw.error_code,'operation_timeout');assert.equal(day.raw.observed_at,observed)
 item.workflow_days[0].raw={error_code:'archive_count_index_missing'}
 assert.equal(byDate(buildWorkflowDays('2026-09',item,'2026-09-22'),'2026-09-01').raw.rows,undefined)
 })
