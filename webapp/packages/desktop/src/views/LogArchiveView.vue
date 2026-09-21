<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError } from '@ct/shared'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { can } from '../permissions'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import {
  beijingDate, buildArchiveDays, canCheckArchiveDay, formatArchiveCount, archiveExecution, archiveWorkflowLabels,
  type ArchiveCalendarDay, type ArchiveConfig, type ArchiveItem, type ArchiveResponse,
} from '../utils/logArchive'

const filters = useFiltersStore(), auth = useAuthStore()
const item = ref<ArchiveItem>(), loading = ref(false), failure = ref(''), saving = ref(false)
const tab = ref('overview'), now = ref(Date.now()), month = ref(beijingDate().slice(0, 7))
const loadedMonth = ref(''), readAt = ref(''), dayFilter = ref('all'), detailDate = ref('')
const emptyCapabilities = () => ({ full_history: false, day_versions: false, archive_billing: false, date_backfill: false, coverage_catalog: false })
const capabilities = ref(emptyCapabilities())
const fullHistory = computed(() => capabilities.value.full_history || item.value?.config.full_history === true)
const execution = computed(() => item.value ? archiveExecution(item.value, fullHistory.value, now.value, !!failure.value) : undefined)

function workflowReason(code: string) {
  const reasons: Record<string, string> = { source_history_unknown: '源历史保留情况尚未确认', source_history_unconfirmed: '历史完整保留与稳定性尚未确认', verification_mismatched: '源库与归档明细不一致，需检查差异', source_cleared: '源历史已清理，原归档记录已保留', source_index_missing: '源日志缺少日期扫描所需索引', source_invalid_date: '源日志存在空或无效日期，需处理后继续按日归档', cohort_incomplete: '跨日期关联范围过大，需检查关联记录', cohort_not_ended: '关联日期尚未结束，稍后可重试', verification_expired: '本轮核验超时，请重试', row_too_large: '单条日志超过读取预算' }
  return `${reasons[code] || '该日期未满足核验或封存条件，请检查归档任务详情'}（${code}）`
}
function retryWorkflow() { if (item.value && writable.value) void save({ ...item.value.config, full_history: true }, item.value.site_id) }
const workflowLabels = archiveWorkflowLabels
const dialog = ref(false), editingSite = ref(''), checkDialog = ref(false), checkSite = ref(''), checkDate = ref('')
const form = reactive<ArchiveConfig>({ version: 0, instance_id: '', agent_id: '', running: false, batch_size: 500, interval_seconds: 30, delay_seconds: 300 })
const today = computed(() => beijingDate(now.value))
const monthReady = computed(() => loadedMonth.value === month.value && !!item.value)
const days = computed(() => monthReady.value ? buildArchiveDays(month.value, item.value?.days || [], today.value, item.value?.status.reconciliation) : [])
const reportedDays = computed(() => days.value.filter(day => day.reported && day.date <= today.value))
const missingDays = computed(() => days.value.filter(day => day.date < today.value && !day.reported))
const isIssue = (day: ArchiveCalendarDay) => day.kind === 'mismatched' || day.kind === 'failed'
const pendingDays = computed(() => days.value.filter(day => day.date < today.value && (isIssue(day) || !day.reported)).sort((a, b) => Number(isIssue(b)) - Number(isIssue(a)) || a.date.localeCompare(b.date)))
const visibleDays = computed(() => days.value.filter(day => dayFilter.value === 'all' || (dayFilter.value === 'reported' ? !!day.reported : day.date < today.value && (isIssue(day) || !day.reported))))
const detail = computed(() => days.value.find(day => day.date === detailDate.value))
const detailOpen = computed({ get: () => !!detail.value, set: (value: boolean) => { if (!value) detailDate.value = '' } })
const calendarOffset = computed(() => {
  const first = new Date(`${month.value}-01T12:00:00+08:00`)
  return Number.isFinite(first.getTime()) ? (first.getUTCDay() + 6) % 7 : 0
})
const siteName = computed(() => item.value?.name || filters.site_id)
const canManage = computed(() => can(auth.user, 'archive.manage'))
const online = computed(() => {
  const seen = Date.parse(item.value?.seen_at || '')
  return Number.isFinite(seen) && now.value - seen < 90000
})
const chosenReady = computed(() => !!item.value?.targets.some(target => target.instance_id === item.value?.config.instance_id && target.agent_id === item.value?.config.agent_id && target.configured))
const writable = computed(() => !!item.value && item.value.site_id === filters.site_id && canManage.value && !failure.value && !loading.value && !saving.value)
const versionApplied = computed(() => !!item.value && item.value.config.version === item.value.status.applied_version)
const checkLabels: Record<string, string> = { running: '对账中', matched: '明细一致', mismatched: '明细存在差异', failed: '对账失败' }
const state = computed<{ text: string; type: 'info' | 'success' | 'warning' | 'danger' }>(() => {
  const current = item.value
  if (!current?.config.agent_id) return { text: '待配置', type: 'info' }
  if (!current.seen_at) return { text: '等待 Agent 确认', type: 'warning' }
  if (!online.value) return { text: '离线 · 等待确认', type: 'warning' }
  if (!versionApplied.value) return { text: current.config.running ? '启动 / 配置待确认' : '正在暂停 / 应用配置', type: 'warning' }
  if (!current.status.configured) return { text: '目标未配置', type: 'warning' }
  if (current.status.prepare_phase && execution.value) return { text: execution.value.title, type: execution.value.attention ? 'danger' : 'warning' }
  if (current.status.error) return { text: '异常 · 等待重试', type: 'danger' }
  if (current.config.reconcile_id && current.config.running && current.status.state === 'running') {
    return { text: current.status.reconciliation?.id === current.config.reconcile_id ? (checkLabels[current.status.reconciliation.state] || '等待对账') : '等待对账', type: 'info' }
  }
  if (current.config.running && current.status.state === 'running') return { text: '归档中', type: 'success' }
  if (!current.config.running && current.status.state === 'paused') return { text: '已暂停', type: 'info' }
  return { text: '等待 Agent 确认', type: 'warning' }
})
const checkReason = computed(() => {
  const current = item.value
  if (!canManage.value) return '需要归档管理权限'
  if (failure.value) return '状态读取失败，请先刷新后操作'
  if (loading.value || saving.value) return '正在读取或提交状态，请稍候'
  if (!current) return '当前没有归档配置'
  if (!current.enabled) return '当前站点未启用'
  if (!online.value) return '执行 Agent 离线，等待恢复连接'
  if (!chosenReady.value || !current.status.configured) return '所选 Agent 的归档目标尚未就绪'
  if (!current.status.supports_daily_check) return '当前 Agent 尚不支持按日对账，需要升级'
  if (current.config.reconcile_id) return '请先结束当前对账模式'
  if (current.config.running || current.status.state !== 'paused' || !versionApplied.value) return '请先暂停归档，并等待 Agent 确认配置版本'
  return ''
})
const validCheckDate = computed(() => !!item.value && canCheckArchiveDay(checkDate.value, today.value, item.value.config.delay_seconds, now.value))
const checkCanSubmit = computed(() => !checkReason.value && validCheckDate.value && checkSite.value === filters.site_id)
const selection = computed({
  get: () => JSON.stringify([form.instance_id, form.agent_id]),
  set: (value: string) => { const [instance, agent] = JSON.parse(value); form.instance_id = instance; form.agent_id = agent },
})
const selectedTargetExists = computed(() => item.value?.targets.some(target => target.agent_id === form.agent_id && target.instance_id === form.instance_id))
const toggleLabel = computed(() => item.value?.config.reconcile_id ? (item.value.config.running ? '暂停对账' : '继续对账') : (item.value?.config.running ? '暂停归档' : '启用归档'))
const toggleDisabled = computed(() => !writable.value || (!item.value?.config.running && (!chosenReady.value || !item.value?.enabled)))
const shortLabels: Record<ArchiveCalendarDay['kind'], string> = { future: '未开始', today: '今日', reported: '已上报', unknown: '未上报', matched: '对账一致', mismatched: '有差异', failed: '对账失败', checking: '对账中' }
function displayDate(value?: string) {
  if (!value) return '—'
  const parsed = new Date(value)
  return Number.isFinite(parsed.getTime()) ? parsed.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false }) : '—'
}
function numberCount(value: number | undefined) { return Number.isSafeInteger(value) && (value ?? -1) >= 0 ? formatArchiveCount(String(value)) : '—' }
function errorMessage(error: unknown) {
  if (error instanceof ApiError) {
    if (error.status === 409) return '配置已变化、执行节点不属于当前站点或旧执行授权未结束，请刷新后重试'
    if (error.status === 403) return '没有归档管理权限，请联系管理员'
  }
  return '请求失败，请检查网络、权限及服务端状态'
}
function showDay(day: ArchiveCalendarDay) { if (day.kind !== 'future') detailDate.value = day.date }
function showPending() { dayFilter.value = 'pending'; tab.value = 'daily' }
function pickerDate(value: Date) { return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}` }
function disabledCheckDate(value: Date) { return !item.value || !canCheckArchiveDay(pickerDate(value), today.value, item.value.config.delay_seconds, now.value) }

let sequence = 0, siteEpoch = 0, saveSequence = 0, disposed = false
async function load() {
  const site = filters.site_id, requestedMonth = month.value, epoch = siteEpoch, ticket = ++sequence
  const current = () => !disposed && ticket === sequence && epoch === siteEpoch && site === filters.site_id && requestedMonth === month.value
  if (!site) { item.value = undefined; loading.value = false; return }
  loading.value = true
  try {
    const result = await client.request<ArchiveResponse>(`/api/dashboard/log-archives?site_id=${encodeURIComponent(site)}&month=${encodeURIComponent(requestedMonth)}`)
    if (!current()) return
    const received = (Array.isArray(result.items) ? result.items : []).find(entry => entry.site_id === site)
    if (result.month && result.month !== requestedMonth) throw new Error('archive_month_mismatch')
    item.value = received ? { ...received, targets: Array.isArray(received.targets) ? received.targets : [], days: Array.isArray(received.days) ? received.days : [] } : undefined
    capabilities.value = {
      full_history: result.capabilities?.full_history === true,
      day_versions: result.capabilities?.day_versions === true,
      archive_billing: result.capabilities?.archive_billing === true,
      date_backfill: result.capabilities?.date_backfill === true,
      coverage_catalog: result.capabilities?.coverage_catalog === true,
    }
    loadedMonth.value = requestedMonth
    readAt.value = new Date().toISOString()
    failure.value = ''
  } catch (error) {
    if (current()) failure.value = errorMessage(error)
  } finally {
    if (current()) { loading.value = false; now.value = Date.now() }
  }
}
function edit() {
  if (!item.value || !writable.value) return
  editingSite.value = item.value.site_id
  Object.assign(form, { reconcile_id: '', reconcile_date: '', full_history: false, history_immutable: false }, item.value.config)
  dialog.value = true
}
async function save(config: ArchiveConfig, site: string): Promise<boolean> {
  if (!writable.value || site !== filters.site_id) return false
  const epoch = siteEpoch, ticket = ++saveSequence
  const current = () => !disposed && epoch === siteEpoch && ticket === saveSequence && site === filters.site_id
  saving.value = true
  try {
    await client.request(`/api/dashboard/log-archives/${encodeURIComponent(site)}`, { method: 'PUT', body: JSON.stringify(config) })
    if (!current()) return false
    ElMessage.success('站点配置已保存，等待 Agent 确认')
    await load()
    return current()
  } catch (error) {
    if (current()) ElMessage.error(errorMessage(error))
    return false
  } finally {
    if (current()) saving.value = false
  }
}
async function savePolicy() { if (await save({ ...form }, editingSite.value)) dialog.value = false }
function toggle() { if (item.value && !toggleDisabled.value) void save({ ...item.value.config, running: !item.value.config.running, full_history: fullHistory.value, reconcile_id: fullHistory.value ? '' : item.value.config.reconcile_id, reconcile_date: fullHistory.value ? '' : item.value.config.reconcile_date }, item.value.site_id) }
function exitCheck() { if (item.value && writable.value) void save({ ...item.value.config, running: false, reconcile_id: '', reconcile_date: '' }, item.value.site_id) }
function openCheck(date = '') {
  if (checkReason.value) return
  checkSite.value = filters.site_id
  checkDate.value = date
  checkDialog.value = true
}
async function startCheck() {
  if (!item.value || !checkCanSubmit.value) return
  const config = { ...item.value.config, running: true, reconcile_date: checkDate.value, reconcile_id: crypto.randomUUID().replaceAll('-', '') }
  if (await save(config, checkSite.value)) checkDialog.value = false
}
function changeMonth() {
  detailDate.value = ''
  loadedMonth.value = ''
  if (item.value) item.value.days = []
  failure.value = ''
  void load()
}
watch(() => filters.site_id, () => {
  siteEpoch++; sequence++; saveSequence++
  dialog.value = false; checkDialog.value = false; detailDate.value = ''; checkDate.value = ''
  editingSite.value = ''; checkSite.value = ''; item.value = undefined; saving.value = false
  loadedMonth.value = ''; readAt.value = ''; month.value = beijingDate().slice(0, 7); dayFilter.value = 'all'
  capabilities.value = emptyCapabilities(); failure.value = ''
  void load()
}, { flush: 'sync' })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(async () => {
  try { await filters.loadInstances(); if (!disposed) await load() } catch (error) { if (!disposed) failure.value = errorMessage(error) }
  if (!disposed) timer = setInterval(() => { now.value = Date.now(); if (!document.hidden && !loading.value && !saving.value) void load() }, 15000)
})
onUnmounted(() => { disposed = true; sequence++; siteEpoch++; saveSequence++; if (timer) clearInterval(timer) })
</script>

<template>
 <AppShell title="日志归档">
  <div class="archive-page">
    <div class="archive-navigation">
      <el-tabs v-if="item" v-model="tab" class="archive-tabs" aria-label="归档工作区"><el-tab-pane label="归档总览" name="overview" /><el-tab-pane v-if="!fullHistory" label="每日数据" name="daily" /><el-tab-pane label="任务与策略" name="tasks" /></el-tabs>
      <span v-else class="secondary">按北京时间查看归档上报与对账结果</span>
      <div class="navigation-actions">
        <div v-if="item" class="archive-state-group" aria-live="polite"><el-tooltip :content="`最近非空批次：${displayDate(item.status.last_success)}；点击查看任务与策略`" placement="bottom"><button type="button" class="archive-state" :aria-label="`归档状态：${failure ? '状态待刷新' : state.text}，查看任务与策略`" @click="tab = 'tasks'"><el-tag :type="failure ? 'warning' : state.type">{{ failure ? '状态待刷新' : state.text }}</el-tag></button></el-tooltip><span v-if="!item.enabled" class="secondary">站点未启用</span></div>
        <div v-if="item && tab !== 'tasks' && !fullHistory" class="month-control"><el-date-picker v-model="month" type="month" value-format="YYYY-MM" format="YYYY 年 MM 月" :clearable="false" aria-label="归档月份" @change="changeMonth" /></div>
        <el-button :loading="loading" :disabled="!filters.site_id || saving" :title="readAt ? `上次读取于 ${displayDate(readAt)}` : '刷新归档状态'" aria-describedby="archive-read-time" @click="load">刷新状态</el-button><span id="archive-read-time" class="read-time">{{ readAt ? `上次读取于 ${displayDate(readAt)}` : '尚未读取归档状态' }}</span>
      </div>
    </div>
    <el-alert v-if="failure" type="error" :closable="false" show-icon title="暂时无法读取归档状态"><p>{{ failure }}。{{ readAt ? `上次读取于 ${displayDate(readAt)}；当前展示旧数据，管理操作已停用。` : '尚未读取到当前站点数据，请重试。' }}</p></el-alert>
    <el-empty v-if="!filters.site_id" description="请选择一个站点查看日志归档" />
    <el-empty v-else-if="!item && !loading && !failure" description="当前站点没有可用的归档配置" />
    <div v-else-if="!item && loading" v-loading="true" class="panel loading-panel" aria-label="正在读取归档状态" />
    <template v-if="item">
      <section v-if="execution" class="panel execution-summary" aria-label="归档运行情况">
        <div class="section-title"><div><h3>{{ execution.title }}</h3><p class="secondary">{{ execution.detail }}</p></div><el-tag :type="execution.attention ? 'warning' : 'info'">{{ execution.mode }}</el-tag></div>
        <div class="execution-grid">
          <div><span>执行 Agent</span><b>{{ item.config.agent_id || '未选择' }}</b><small>{{ item.config.instance_id || '尚未配置执行节点' }}</small></div>
          <div><span>最近 Agent 上报</span><b>{{ displayDate(item.seen_at) }}</b><small>{{ online ? '上报连接正常；不代表批次已推进' : '尚无近期上报' }}</small></div>
          <div><span>最近非空批次提交</span><b>{{ displayDate(item.status.last_success) }}</b><small>没有新日志或处于核验阶段时，时间可能不变</small></div>
          <div v-if="!fullHistory"><span>已提交日志 ID</span><b>{{ item.status.last_success ? item.status.last_id : '—' }}</b><small>比较两次上报的位置，不代表历史已完整</small></div>
          <div v-else><span>最近处理日期</span><b>{{ item.status.workflow?.date || '尚未上报' }}</b><small>{{ item.status.workflow ? '已收到阶段记录' : '尚无阶段记录' }}</small></div>
        </div>
        <p class="secondary">每批最多 {{ item.config.batch_size }} 条，间隔 {{ item.config.interval_seconds }} 秒，归档延迟 {{ item.config.delay_seconds }} 秒。页面每 15 秒刷新；刷新页面不会启动新批次。</p>
        <p v-if="!fullHistory && !item.active_dataset_id && item.status.prepare_phase" class="secondary">新版归档由 Agent 和服务端自动准备表结构并绑定数据集，已有月表数据保留，无需手动执行初始化脚本。</p>
        <p v-else-if="!fullHistory && !item.active_dataset_id" class="secondary">当前未收到新版数据集信息，无法确认全量任务是否已准备好。若仍使用旧版增量模式，需完成归档库准备、数据集注册及配套 Agent 升级后使用全量任务。</p>
        <el-button v-if="tab !== 'tasks'" link type="primary" @click="tab = 'tasks'">查看执行详情与策略</el-button>
      </section>
      <el-alert v-if="item.status.error" :title="item.status.error" type="error" :closable="false" show-icon />
      <section v-if="fullHistory" class="panel">
        <div class="section-title"><div><h3>全量归档任务</h3><p class="secondary">从最早日期分批归档并回读校验；当天结束后补齐一次，再核对归档库并封存。</p></div><el-button :type="item.config.running ? 'default' : 'primary'" :disabled="toggleDisabled" :loading="saving" @click="toggle">{{ item.config.running ? '暂停归档' : item.status.workflow ? '继续归档' : '启动归档' }}</el-button></div>
        <div class="execution-grid"><div><span>最近上报阶段</span><b>{{ item.status.workflow ? workflowLabels[item.status.workflow.phase] || item.status.workflow.phase : '尚未上报' }}</b></div><div><span>已复用历史日志</span><b>{{ formatArchiveCount(item.status.workflow?.imported_rows) }}</b></div><div><span>已封存日期</span><b>{{ formatArchiveCount(item.status.workflow?.completed_days) }} 天</b></div><div><span>受阻日期</span><b>{{ formatArchiveCount(item.status.workflow?.blocked_days) }} 天</b></div></div>
        <p v-if="item.status.workflow?.date" class="secondary">当前处理日期：{{ item.status.workflow.date }}；已发现最早日期：{{ item.status.workflow.first_date || '扫描中' }}。范围来自可读取数据，不代表源库从未清理。</p>
        <p v-if="item.status.workflow?.error_code" class="error-text">任务需关注：{{ workflowReason(item.status.workflow.error_code) }}</p>
        <div v-if="item.status.workflow?.issues?.length"><p v-for="issue in item.status.workflow.issues" :key="issue.date" class="secondary">{{ issue.date }} · {{ workflowReason(issue.code) }}</p><p class="secondary">显示最早 20 个受阻日期。处理原因后重试，其他日期继续归档。</p><el-button :disabled="!writable" :loading="saving" @click="retryWorkflow">重试受阻日期</el-button></div>
        <p v-if="!item.config.history_immutable" class="secondary">尚未声明源历史完整保留且稳定：仍会扫描、复用和补齐，不自动封存。确认实际保留策略后可在任务与策略中设置。</p>
      </section>
      <template v-if="tab === 'overview' && !fullHistory">
        <div class="panel metrics">
          <section class="metric"><span class="label">所选月已上报</span><strong>{{ monthReady ? reportedDays.length : '—' }} <small>天</small></strong><span class="secondary">累计记录，完整性待核验</span></section>
          <button class="metric metric-action" type="button" :disabled="!monthReady" @click="showPending"><span class="label">已结束但未上报</span><strong>{{ monthReady ? missingDays.length : '—' }} <small>天</small></strong><span class="secondary">状态未知，不等于漏采 <span aria-hidden="true">↗</span></span></button>
          <section class="metric"><span class="label">归档出账</span><strong class="metric-status">尚未开放</strong><span class="secondary">待接入封存版本与金额配置</span></section>
        </div>
        <div class="overview-grid">
          <section class="panel calendar-panel" v-loading="loading && !monthReady"><div class="section-title"><div><h3>每日日志覆盖</h3><p class="secondary">{{ month }} · 北京时间，点击日期查看已有证据</p></div><el-button link type="primary" @click="tab = 'daily'">查看列表</el-button></div>
            <div class="calendar" aria-label="每日日志上报日历"><span v-for="weekday in ['一', '二', '三', '四', '五', '六', '日']" :key="weekday" class="weekday">{{ weekday }}</span><span v-for="blank in calendarOffset" :key="`blank-${blank}`" aria-hidden="true" /><button v-for="day in days" :key="day.date" type="button" class="calendar-day" :class="[`day-${day.kind}`, { 'day-selected': detailDate === day.date }]" :disabled="day.kind === 'future'" :title="`${day.date} · ${day.label}；${day.reason}`" :aria-label="`${day.date}，${day.label}，${day.reported ? '已有累计上报' : '无累计上报'}。查看详情`" @click="showDay(day)"><b>{{ Number(day.date.slice(8)) }}</b><span>{{ shortLabels[day.kind] }}</span></button></div>
            <div class="legend"><span><i class="dot dot-report" />已上报</span><span><i class="dot dot-unknown" />未上报</span><span><i class="dot dot-error" />对账异常</span><span><i class="dot dot-today" />今日未结束</span></div><p class="secondary calendar-note">无上报日期保留为未知；当前接口没有可信覆盖起点，不计算完整率。</p>
          </section>
          <section class="panel attention-panel"><div class="section-title"><div><h3>优先查看</h3><p class="secondary">已结束日期中的对账异常与未上报记录</p></div></div><div v-if="!monthReady" class="empty-note">等待该月份数据</div><div v-else-if="!pendingDays.length" class="empty-note">没有未上报或对账异常的已结束日期。已有上报仍需后续完整性核验。</div><button v-for="day in pendingDays.slice(0, 5)" :key="day.date" type="button" class="attention-day" @click="showDay(day)"><span><b>{{ day.date }}</b><span class="secondary">{{ isIssue(day) ? day.label : '无累计上报，需确认覆盖' }}</span></span><span aria-hidden="true">›</span></button><el-button v-if="pendingDays.length" link type="primary" class="attention-more" @click="showPending">查看全部 {{ pendingDays.length }} 天</el-button><div class="capability-note"><b>下一步：建立可信日期覆盖</b><p>按日对账可比较当前源与目标明细；封存、指定日期补齐和归档账单仍需后端支持。</p></div></section>
        </div>
      </template>
      <section v-if="tab === 'daily'" class="panel daily-panel"><div class="section-title daily-heading"><div><h3>每日数据</h3><p class="secondary">全月日期均保留，条数为已归档累计值</p></div><el-radio-group v-model="dayFilter" size="small" aria-label="日期筛选"><el-radio-button value="all">全部</el-radio-button><el-radio-button value="reported">已上报</el-radio-button><el-radio-button value="pending">未上报 / 异常</el-radio-button></el-radio-group></div><p v-if="dayFilter === 'pending'" class="secondary">只显示已结束且无累计上报，或最近对账异常的日期。</p>
        <el-table v-mobile-cards v-loading="loading && !monthReady" :data="visibleDays" row-key="date" empty-text="当前月份没有符合筛选条件的日期">
          <el-table-column label="日志日期" prop="date" min-width="114"><template #default="{ row }"><span class="date-cell">{{ row.date }}</span></template></el-table-column>
          <el-table-column label="累计上报条数" min-width="126"><template #default="{ row }"><span>{{ formatArchiveCount(row.reported?.archived_rows) }} <small class="secondary">原始</small></span><div class="secondary">请求 {{ formatArchiveCount(row.reported?.request_rows) }} · 错误 {{ formatArchiveCount(row.reported?.error_rows) }}</div></template></el-table-column>
          <el-table-column label="日志状态" min-width="146"><template #default="{ row }"><el-tag :type="row.tagType" size="small">{{ row.label }}</el-tag></template></el-table-column>
          <el-table-column label="归档出账条件" min-width="120"><template #default="{ row }"><span class="secondary">{{ row.kind === 'future' ? '—' : row.kind === 'today' ? '等待日期结束' : '尚不能校验' }}</span></template></el-table-column>
          <el-table-column label="操作" width="70"><template #default="{ row }"><el-button link type="primary" :disabled="row.kind === 'future'" @click="showDay(row)">详情</el-button></template></el-table-column>
        </el-table><p class="secondary">“—”表示没有可信上报计数；上报为 0 或最近对账一致，均不表示已核实零业务、完成封存或可出账。</p>
      </section>
      <template v-if="tab === 'tasks'">
        <section class="panel"><div class="section-title"><div><h3>当前执行</h3><p class="secondary">{{ fullHistory ? '同一任务自动衔接扫描、补齐、核验和封存' : '使用现有增量归档与按日对账能力' }}</p></div><div class="actions"><el-button v-if="item.config.reconcile_id" :disabled="!writable" @click="exitCheck">结束对账模式</el-button><el-button v-if="!fullHistory" :disabled="!!checkReason" :title="checkReason || '比较一个历史日期的源与目标明细'" @click="openCheck()">按日对账</el-button><el-button :type="item.config.running ? 'default' : 'primary'" :disabled="toggleDisabled" :loading="saving" @click="toggle">{{ toggleLabel }}</el-button></div></div><p v-if="checkReason && !fullHistory" class="secondary">按日对账：{{ checkReason }}。</p>
          <div class="execution-grid"><div><span>执行 Agent</span><b>{{ item.config.agent_id || '未选择' }}</b><small>{{ item.config.instance_id || '请先配置执行节点' }}</small></div><div><span>已提交日志 ID</span><b>{{ item.status.last_id || '—' }}</b><small>源日志 ID 增量位置，不代表历史已完整</small></div><div><span>最近一批</span><b>{{ numberCount(item.status.last_batch_rows) }} 条</b><small>单批处理量，没有全量进度分母</small></div><div><span>最近写入校验</span><b>{{ item.status.verified_at ? `${numberCount(item.status.verified_rows)} 条通过` : '尚无结果' }}</b><small>{{ displayDate(item.status.verified_at) }}</small></div></div><p class="secondary">最近非空批次提交：{{ displayDate(item.status.last_success) }}。没有新日志时此时间不会前进，不能用它计算数据延迟。</p>
        </section>
        <section v-if="!fullHistory" class="panel"><div class="section-title"><div><h3>最近一次按日对账</h3><p class="secondary">仅保留当前上报结果，尚无持久任务历史或重试队列</p></div><el-tag v-if="item.status.reconciliation" :type="item.status.reconciliation.state === 'matched' ? 'success' : item.status.reconciliation.state === 'running' ? 'info' : 'warning'">{{ checkLabels[item.status.reconciliation.state] || item.status.reconciliation.state }}</el-tag></div><template v-if="item.status.reconciliation"><div class="reconcile-summary"><b>{{ item.status.reconciliation.date }}</b><span>源库 {{ numberCount(item.status.reconciliation.source_rows) }} 条</span><span>目标库 {{ numberCount(item.status.reconciliation.target_rows) }} 条</span><span class="secondary">完成于 {{ displayDate(item.status.reconciliation.finished_at) }}</span></div><p v-if="item.status.reconciliation.error" class="error-text">{{ item.status.reconciliation.error }}</p></template><p v-else class="empty-note">尚无按日对账结果。</p><p class="secondary">对账逐批比较原始明细，不自动修复差异或重算统计，也不产生封存版本。对账结束后，退出对账模式再启用归档。</p></section>
        <section class="panel"><div class="section-title"><div><h3>站点归档策略</h3><p class="secondary">每批结束后应用配置，暂停和恢复均需 Agent 确认</p></div><el-button :disabled="!writable" @click="edit">编辑策略</el-button></div><div class="version-line"><el-tag :type="versionApplied && online ? 'info' : 'warning'">{{ versionApplied && online ? 'Agent 已确认当前版本' : '等待 Agent 确认' }}</el-tag><span>期望版本 v{{ item.config.version }}</span><span>Agent 已应用 v{{ item.status.applied_version }}</span></div><div class="policy"><div><span>每批上限</span><b>{{ item.config.batch_size }} 条</b></div><div><span>批次间隔</span><b>{{ item.config.interval_seconds }} 秒</b></div><div><span>归档延迟</span><b>{{ item.config.delay_seconds }} 秒</b></div><div><span>存储方式</span><b>月度明细 · 日 / 月统计</b></div></div><details class="technical-details"><summary>执行节点与配置说明</summary><p>同一站点只授权选定 Agent 归档，源库和归档目标必须属于本站点。更换节点前请先暂停，等待旧授权结束。</p><p>Agent 需开启 <code>CT_LOG_ARCHIVE_MANAGED=true</code> 与 <code>CT_LOG_ARCHIVE_ENABLED=true</code>，目标连接保留在 Agent 本地。当前发现 {{ item.targets.length }} 个本站点在线 Agent。</p><p>最近 Agent 上报：{{ displayDate(item.seen_at) }}。{{ item.status.configured ? 'Agent 上报归档目标已配置。' : 'Agent 尚未确认归档目标可用。' }}</p></details></section>
        <section v-if="!fullHistory" class="panel planned-panel"><h3>后续归档能力</h3><p class="secondary">当前操作不创建固定日期版本，也不生成归档账单。相关能力接入后再开放对应操作。</p><div class="planned-list"><span>可信覆盖目录 <b>{{ capabilities.coverage_catalog ? '等待页面联调' : '尚未开放' }}</b></span><span>日期封存与历史版本 <b>{{ capabilities.day_versions ? '等待页面联调' : '尚未开放' }}</b></span><span>指定日期补齐 <b>{{ capabilities.date_backfill ? '等待页面联调' : '尚未开放' }}</b></span><span>金额配置与归档出账 <b>{{ capabilities.archive_billing ? '等待页面联调' : '尚未开放' }}</b></span></div></section>
      </template>
    </template>
  </div>
  <el-dialog v-model="detailOpen" :title="`${detailDate} · 日志详情`" width="min(680px, calc(100vw - 24px))" class="archive-detail-dialog">
    <template v-if="detail"><div class="detail-title"><span>{{ siteName }}</span><el-tag :type="detail.tagType">{{ detail.label }}</el-tag></div><p class="dialog-note">{{ detail.reason }}</p><div class="detail-counts"><div><span>已上报原始日志</span><b>{{ formatArchiveCount(detail.reported?.archived_rows) }}</b></div><div><span>请求日志</span><b>{{ formatArchiveCount(detail.reported?.request_rows) }}</b></div><div><span>错误请求</span><b>{{ formatArchiveCount(detail.reported?.error_rows) }}</b></div></div>
      <section class="detail-section"><h3>已有核验记录</h3><dl class="detail-facts"><div><dt>该日已提交 ID</dt><dd>{{ detail.reported?.last_id || '—' }}</dd></div><div><dt>最近写入校验</dt><dd>{{ displayDate(detail.reported?.verified_at) }}</dd></div></dl><p class="secondary">写入校验检查已提交批次，不能证明源库整个日期已归档。</p><template v-if="detail.reconciliation"><p><el-tag :type="detail.tagType" size="small">{{ detail.label }}</el-tag></p><p>源库 {{ numberCount(detail.reconciliation.source_rows) }} 条 · 目标库 {{ numberCount(detail.reconciliation.target_rows) }} 条</p><p class="secondary">完成于 {{ displayDate(detail.reconciliation.finished_at) }}</p><p v-if="detail.reconciliation.error" class="error-text">{{ detail.reconciliation.error }}</p></template><p v-else class="secondary">当前上报没有该日的按日对账结果。</p></section><section class="detail-section"><h3>归档出账条件：尚不能校验</h3><p class="secondary">固定日期版本、历史金额配置和完整性目录尚未接入。已有累计计数或明细一致结果不能作为可出账结论。</p><p class="secondary">指定日期补齐与版本修订尚未开放，当前不会自动修复差异。</p></section>
    </template><template #footer><el-button @click="detailOpen = false">关闭</el-button><el-button :disabled="!!checkReason || !detail || !canCheckArchiveDay(detail.date, today, item?.config.delay_seconds ?? 0, now)" :title="checkReason || '仅支持已结束且超过归档延迟窗口的日期'" @click="openCheck(detailDate)">按日对账</el-button></template>
  </el-dialog>
  <el-dialog v-model="checkDialog" title="按日对账" width="min(540px, calc(100vw - 24px))" :close-on-click-modal="false" append-to-body>
    <p class="dialog-note">站点：{{ checkSite }}。读取所选日期的源库及目标月表，比较原始明细；不自动修复差异。</p><el-form label-position="top" @submit.prevent><el-form-item label="日志日期（北京时间）"><el-date-picker v-model="checkDate" type="date" value-format="YYYY-MM-DD" :disabled-date="disabledCheckDate" placeholder="选择已结束的日志日期" style="max-width:100%" /></el-form-item></el-form><p class="secondary">需先暂停并等待 Agent 确认；日期结束后还需超过 {{ item?.config.delay_seconds ?? '—' }} 秒归档延迟。仅适用于日志保持不变的历史日期，源库需有 created_at 开头的索引。</p><p v-if="checkReason" class="error-text">{{ checkReason }}</p><p v-else-if="checkDate && !validCheckDate" class="error-text">所选日期尚未结束或仍在归档延迟窗口内。</p><template #footer><el-button :disabled="saving" @click="checkDialog = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!checkCanSubmit" @click="startCheck">开始对账</el-button></template>
  </el-dialog>
  <el-dialog v-model="dialog" title="站点归档策略" width="min(540px, calc(100vw - 24px))" :close-on-click-modal="false">
    <p class="dialog-note">站点：{{ editingSite }} · 基于配置 v{{ form.version }}，保存后等待 Agent 确认。</p><el-form label-position="top" @submit.prevent><el-form-item v-if="fullHistory" label="历史数据声明"><el-checkbox v-model="form.history_immutable" :disabled="item?.config.running">确认源历史完整保留且日志仅追加，超过延迟窗口不再补写，未归档日志不清理</el-checkbox><p class="secondary">仅在符合实际运维策略时勾选；修改前请暂停。</p></el-form-item><el-form-item label="执行 Agent"><el-select v-model="selection" placeholder="选择本站点在线 Agent" style="width:100%"><el-option v-for="target in item?.targets || []" :key="JSON.stringify([target.instance_id, target.agent_id])" :value="JSON.stringify([target.instance_id, target.agent_id])" :label="`${target.name || target.instance_id} · ${target.agent_id}${target.configured ? '' : '（目标未配置）'}`" :disabled="!target.configured" /><el-option v-if="form.agent_id && !selectedTargetExists" :value="selection" :label="`${form.agent_id}（离线 / 不可用）`" disabled /></el-select></el-form-item><div class="form-grid"><el-form-item label="每批最多日志数"><el-input-number v-model="form.batch_size" :min="1" :max="5000" :step="100" /></el-form-item><el-form-item label="批次间隔（秒）"><el-input-number v-model="form.interval_seconds" :min="2" :max="3600" /></el-form-item><el-form-item label="归档延迟（秒）"><el-input-number v-model="form.delay_seconds" :min="60" :max="86400" :step="60" /></el-form-item></div></el-form><p class="secondary">更换执行节点前请先暂停，并等待旧授权结束。新节点需配置相同源库和目标归档库。{{ form.reconcile_id ? '当前处于对账模式，保存策略将保留该模式。' : '' }}</p><template #footer><el-button :disabled="saving" @click="dialog = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!writable || !form.agent_id || !form.instance_id || editingSite !== filters.site_id" @click="savePolicy">保存站点策略</el-button></template>
  </el-dialog>
 </AppShell>
</template>

<style scoped>
:global(body:has(.archive-page)){min-width:0}
.archive-page{min-width:0;display:grid;gap:12px;color:var(--ct-ink)}
.archive-page .panel{min-height:0}.archive-page .loading-panel{min-height:180px}
.section-title,.archive-navigation,.actions,.detail-title,.version-line,.reconcile-summary,.navigation-actions,.archive-state-group{display:flex;align-items:center;gap:12px}
.section-title,.archive-navigation{justify-content:space-between}.section-title p{margin:0}.navigation-actions{min-width:0;margin-left:auto;gap:10px;flex-wrap:wrap;justify-content:flex-end}.navigation-actions>.el-button{flex-shrink:0;margin-left:0}.archive-state-group{min-width:0;gap:6px;flex-wrap:wrap}.archive-state{padding:0;min-height:36px;max-width:100%;border:0;background:transparent;cursor:pointer}.archive-state :deep(.el-tag){height:auto;min-height:24px;white-space:normal;line-height:1.6}.archive-state:focus-visible{outline:2px solid var(--ct-accent);outline-offset:2px;border-radius:4px}.read-time{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
.panel{min-width:0;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:10px;padding:18px}.loading-panel{min-height:180px}.label{font-size:13px;color:var(--ct-ink-2)}.secondary{font-size:12px;line-height:1.8;color:var(--ct-ink-3);overflow-wrap:anywhere}
.archive-page h3,.detail-section h3{font-size:15px;line-height:1.5;margin:0 0 4px;font-weight:600}.archive-navigation{gap:20px;min-width:0;flex-wrap:wrap}.archive-tabs{min-width:0;flex:1}.archive-tabs :deep(.el-tabs__header){margin:0}.archive-tabs :deep(.el-tabs__nav-wrap::after){height:1px;background:var(--ct-line)}.archive-tabs :deep(.el-tabs__content){display:none}.archive-tabs :deep(.el-tabs__item){padding:0 18px}.archive-tabs :deep(.el-tabs__item:nth-child(2)){padding-left:0}.month-control{display:flex;align-items:center;gap:10px}.month-control :deep(.el-date-editor){width:164px}
.archive-page .metrics{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));padding:0;gap:0}.metric{min-width:0;display:grid;grid-template-columns:1fr auto;align-items:center;gap:3px 12px;padding:10px 16px;text-align:left;font:inherit;color:inherit;background:transparent;border:0}.metric+.metric{border-left:1px solid var(--ct-line)}.metric .secondary{grid-column:1/-1}.metric strong{font-size:22px;line-height:1.35;font-weight:600;font-variant-numeric:tabular-nums;white-space:nowrap}.metric strong.metric-status{font-size:16px;line-height:1.85}.metric small{font-size:12px;font-weight:400;color:var(--ct-ink-3)}.metric-action{cursor:pointer}.metric-action:hover{background:var(--ct-surface-2)}.metric-action:disabled{cursor:default}.metric-action:focus-visible{outline-offset:-3px}
.overview-grid{display:grid;grid-template-columns:minmax(0,1.65fr) minmax(230px,1fr);gap:16px}.section-title{margin-bottom:16px;min-width:0;flex-wrap:wrap}.section-title>div{min-width:0}.calendar{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:6px}.weekday{text-align:center;font-size:12px;color:var(--ct-ink-3);padding:4px 0 7px}.calendar-day{min-width:0;min-height:69px;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:7px;border:1px solid var(--ct-line);border-radius:7px;background:var(--ct-surface-2);color:var(--ct-ink);font:inherit;cursor:pointer}.calendar-day b{font-size:15px;font-weight:600;font-variant-numeric:tabular-nums}.calendar-day span{font-size:11px;white-space:nowrap}.calendar-day:hover:not(:disabled),.calendar-day.day-selected{border-color:var(--ct-accent);box-shadow:inset 0 0 0 1px var(--ct-accent)}.calendar-day:focus-visible,.attention-day:focus-visible,.metric-action:focus-visible{outline:2px solid var(--ct-accent);outline-offset:3px}.day-unknown{background:var(--ct-warn-weak);color:var(--ct-warn);border-color:transparent}.day-mismatched,.day-failed{background:var(--ct-crit-weak);color:var(--ct-crit);border-color:transparent}.day-matched{background:var(--ct-ok-weak);color:var(--ct-ok);border-color:transparent}.day-today,.day-checking{color:var(--ct-accent);background:var(--ct-accent-weak);border-color:var(--ct-accent)}.day-future{opacity:.52;cursor:default;background:transparent;border-color:transparent}
.legend{display:flex;flex-wrap:wrap;gap:9px 13px;margin-top:18px;color:var(--ct-ink-3);font-size:11px}.legend>span{display:inline-flex;align-items:center;gap:5px}.dot{width:7px;height:7px;border-radius:50%;display:inline-block;background:var(--ct-line-strong)}.dot-report{background:var(--ct-ink-3)}.dot-unknown{background:var(--ct-warn)}.dot-error{background:var(--ct-crit)}.dot-today{background:var(--ct-accent)}.calendar-note{margin:12px 0 0}.attention-panel{display:flex;flex-direction:column}.attention-day{display:flex;justify-content:space-between;align-items:center;width:100%;text-align:left;border:0;border-bottom:1px solid var(--ct-line);background:transparent;color:var(--ct-ink);padding:13px 0;font:inherit;cursor:pointer;gap:12px}.attention-day>span:first-child{display:flex;flex-direction:column;gap:4px}.attention-day b{font-size:13px;font-weight:500}.attention-day:hover{color:var(--ct-accent)}.attention-more{align-self:flex-start;margin-top:15px}.capability-note{margin-top:auto;padding-top:20px;font-size:12px;line-height:1.8}.capability-note b{color:var(--ct-ink-2);font-weight:500}.capability-note p{color:var(--ct-ink-3);margin:5px 0 0}.empty-note{font-size:13px;line-height:1.9;color:var(--ct-ink-3);padding:12px 0}
.daily-panel :deep(.el-table){--el-table-border-color:var(--ct-line)}.date-cell{font-variant-numeric:tabular-nums;font-size:13px}.daily-panel>p:last-child{margin-bottom:0}.actions{flex-wrap:wrap;gap:8px}.actions :deep(.el-button)+.el-button{margin-left:0}.execution-grid,.policy{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:18px}.execution-grid>div,.policy>div{display:flex;flex-direction:column;gap:8px;min-width:0}.execution-grid span,.policy span,.detail-counts span{font-size:12px;color:var(--ct-ink-3)}.execution-grid b,.policy b{font-size:14px;font-weight:500;overflow-wrap:anywhere}.execution-grid small{color:var(--ct-ink-3);font-size:12px;line-height:1.7;overflow-wrap:anywhere}.reconcile-summary,.version-line{flex-wrap:wrap;font-size:13px;line-height:1.8}.version-line{padding-bottom:18px;color:var(--ct-ink-2)}.reconcile-summary b{font-weight:600}.error-text{color:var(--ct-crit);font-size:12px;line-height:1.8;overflow-wrap:anywhere}.technical-details{border-top:1px solid var(--ct-line);margin-top:20px;padding-top:14px;font-size:12px;color:var(--ct-ink-3);line-height:1.8}.technical-details summary{cursor:pointer;color:var(--ct-ink-2)}.technical-details p{margin:10px 0 0}.technical-details code{overflow-wrap:anywhere;font-family:Consolas,monospace;font-size:11px}.planned-list{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin-top:16px;font-size:13px}.planned-list>span{display:flex;justify-content:space-between;gap:12px;color:var(--ct-ink-2)}.planned-list b{color:var(--ct-ink-3);font-size:12px;font-weight:400;white-space:nowrap}
.detail-title{flex-wrap:wrap;color:var(--ct-ink);font-size:13px;margin-bottom:12px}.dialog-note{font-size:13px;line-height:1.8;color:var(--ct-ink-2);margin:0 0 20px;overflow-wrap:anywhere}.detail-counts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:12px;padding:16px;background:var(--ct-surface-2);border-radius:8px}.detail-counts>div{min-width:0;display:flex;flex-direction:column;gap:8px}.detail-counts b{font-size:20px;color:var(--ct-ink);font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.detail-section{border-top:1px solid var(--ct-line);margin-top:20px;padding-top:18px;color:var(--ct-ink-2);font-size:13px}.detail-facts{margin:12px 0}.detail-facts>div{display:flex;justify-content:space-between;gap:12px;margin:10px 0}.detail-facts dt{color:var(--ct-ink-3)}.detail-facts dd{margin:0;text-align:right;overflow-wrap:anywhere}.form-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:0 16px}.form-grid :deep(.el-input-number){width:100%}
@media(max-width:1100px){.panel{padding:15px}.overview-grid{grid-template-columns:minmax(0,1.6fr) minmax(215px,1fr);gap:12px}.calendar{gap:4px}.execution-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.daily-heading{align-items:flex-start}}
@media(max-width:700px){.archive-page{gap:10px}.navigation-actions{width:100%;gap:8px;justify-content:flex-end}.navigation-actions>.el-button{padding:8px 10px}.archive-state-group{margin-right:auto}.archive-navigation{gap:8px}.archive-tabs{flex-basis:100%}.month-control{width:auto}.month-control :deep(.el-date-editor){width:146px}.archive-page .metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.metric{padding:9px 12px;gap:3px 8px}.metric>.label{font-size:12px}.metric strong{font-size:21px}.metrics>.metric:last-child{grid-column:1/-1;border-left:0;border-top:1px solid var(--ct-line);grid-template-columns:auto 1fr;gap:3px 10px}.metrics>.metric:last-child strong{font-size:15px;line-height:1.5}.metrics>.metric:last-child .secondary{grid-column:1/-1}.overview-grid{grid-template-columns:minmax(0,1fr)}.calendar-day{min-height:61px}.calendar-day span{font-size:10px}.attention-day{padding:12px 0}.capability-note{padding-top:18px}.daily-heading{gap:12px}.daily-heading :deep(.el-radio-button__inner){padding:8px 10px}.policy,.execution-grid{grid-template-columns:repeat(2,minmax(0,1fr));gap:18px 12px}.planned-list{grid-template-columns:1fr}.section-title>.actions{width:100%}.form-grid{grid-template-columns:1fr}.detail-counts{gap:8px;padding:12px}.detail-counts b{font-size:18px}.detail-facts>div{flex-direction:column;gap:4px}.detail-facts dd{text-align:left}.detail-title>span:first-child{overflow-wrap:anywhere}.archive-tabs :deep(.el-tabs__item){padding:0 15px;font-size:14px}}
</style>
