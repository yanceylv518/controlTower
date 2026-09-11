<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, siteOf } from '@ct/shared'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'
import { useAuthStore } from '../stores/auth'
import { dateToWall, isValidZone, parseWallTime, rezoneRange, zoneLabel } from '../utils/zoned'
import { splitLogLine } from '../utils/logLine'
import { groupLogHistory } from '../utils/logHistory'
import { mergeLogTaskRefresh } from '../utils/logTaskRefresh'
import { copyText } from '../utils/copyText'

interface Query { kind?: string; host?: string; path?: string; level?: string; min_duration_ms?: number; batch_id?: string; keyword?: string; source_id: string; container: string; from: string; to: string; request_id?: string; error_code?: string }
interface Result { total_scanned_bytes?: number; next_cursor?: string; complete?: boolean; phase?: string; indexed_bytes?: number; note?: string; files_scanned?: number; scanned_bytes?: number; status: string; lines: string[]; truncated?: boolean; error?: string }
interface Task { id: string; instance_id: string; agent_id: string; actor: string; actor_name: string; query: Query; result: Result; created_at: string }
interface Source { kind?: string; domains?: string[]; fields?: string[]; query_host?: string; id: string; container: string; log_dir: string; timezone: string; available: boolean; reason?: string }
interface Target { sources: Source[]; discovery_error?: string; instance_id: string; agent_id: string; containers: string[]; seen_at: string }
const filters = useFiltersStore()
const targets = ref<Target[]>([]), tasks = ref<Task[]>([]), current = ref<Task[]>([])
const requestID = ref(''), errorCode = ref('')
const logKind = ref('app'), selectedLogSource = ref(''), requestPath = ref(''), logLevel = ref(''), minDuration = ref<number | undefined>()
const logKinds: Record<string, string> = { app: '应用日志', nginx_access: 'Nginx 访问日志', nginx_error: 'Nginx 错误日志' }
const sourceKind = (s: Source) => s.kind || 'app'
const sourceKey = (target: Target, source: Source) => `${target.instance_id}/${target.agent_id}/${source.id}`
function clearExtraFilters() { requestID.value = ''; errorCode.value = ''; requestPath.value = ''; logLevel.value = ''; minDuration.value = undefined }
watch(logKind, () => { selectedLogSource.value = ''; clearExtraFilters() })
watch(selectedLogSource, clearExtraFilters)
const expanded = ref(false), historyOpen = ref(false), keyword = ref('')
const resultDetails = ref(false)
const wrapLogs = ref(true)
const supportsQueryBatch = ref(false)
const supportsHistoryPagination = ref(false)
const auth = useAuthStore()
const canFilterActor = computed(() => auth.user?.role === 'admin' && (auth.user.permissions == null || auth.user.permissions.includes('*')))
interface HistoryGroup { id: string; created_at: string; tasks: Task[] }
const historyGroups = ref<HistoryGroup[]>([]), historyLoading = ref(false), historyError = ref('')
const historyPage = ref(1), historyPageSize = ref(20), historyTotal = ref(0)
const historyRange = ref<[string, string] | null>(null), historyActor = ref(''), historyRequestID = ref('')
let historyGeneration = 0
const partialResult = (result: Result) => result.truncated && (!result.complete || result.note?.includes('目录遍历达到保护限制'))
const activeFilters = computed(() => [requestID.value.trim(), errorCode.value.trim(), requestPath.value.trim(), logLevel.value, minDuration.value].filter(Boolean).length)
// The picker holds wall-clock strings in the log source's zone (not the
// browser's), so what the operator types matches the timestamps in the logs.
const DEFAULT_ZONE = 'Asia/Shanghai'
const defaultRange = (tz: string): [string, string] => [dateToWall(new Date(Date.now() - 15 * 60000), tz), dateToWall(new Date(), tz)]
const range = ref<[string, string]>(defaultRange(DEFAULT_ZONE))
const logZone = ref(DEFAULT_ZONE), zoneNotice = ref('')
const submitting = ref(false), loading = ref(false), error = ref('')
const key = (t: Target) => JSON.stringify([t.instance_id, t.agent_id])
const siteIDs = computed(() => new Set(filters.instances.filter(i => i.enabled && siteOf(i) === filters.site_id).map(i => i.instance_id)))
const available = computed(() => targets.value.filter(t => siteIDs.value.has(t.instance_id)))
const kindSources = computed(() => available.value.flatMap(target => (target.sources || []).filter(source => source.available && sourceKind(source) === logKind.value).map(source => ({ target, source }))))
const sources = computed(() => kindSources.value.filter(({ target, source }) => !selectedLogSource.value || sourceKey(target, source) === selectedLogSource.value))
const supportsFilter = (field: string) => logKind.value === 'app' ? ['request_id', 'status'].includes(field) : sources.value.length > 0 && sources.value.every(s => s.source.fields?.includes(field))
watch(() => ['request_id','status','path','duration','level'].map(supportsFilter).join(','), () => {
  if (!supportsFilter('request_id')) requestID.value = ''
  if (!supportsFilter('status')) errorCode.value = ''
  if (!supportsFilter('path')) requestPath.value = ''
  if (!supportsFilter('duration')) minDuration.value = undefined
  if (!supportsFilter('level')) logLevel.value = ''
})
const sourceZone = computed(() => { const tz = sources.value[0]?.source.timezone || DEFAULT_ZONE; return isValidZone(tz) ? tz : DEFAULT_ZONE })
const mixedZones = computed(() => new Set(sources.value.map(s => s.source.timezone || DEFAULT_ZONE)).size > 1)
const zoneText = computed(() => zoneLabel(logZone.value))
const history = computed(() => historyGroups.value.map(g => ({ ...g, status: g.tasks.some(pending) ? '查询中' : g.tasks.every(t => t.result.status === 'succeeded') ? '已完成' : g.tasks.some(t => t.result.status === 'succeeded') ? '部分失败' : '失败' })))
const pending = (task: Task) => ['pending', 'running'].includes(task.result.status)
const busy = computed(() => current.value.some(pending))
const phases: Record<string,string> = { indexing:'建立时间索引', querying:'正在查询', archive:'分批解压归档', complete:'扫描结束' }
const submissionError = ref('')
const missing = computed(() => filters.instances.filter(i => siteIDs.value.has(i.instance_id) && !available.value.some(t => t.instance_id === i.instance_id)))
const mergedLines = computed(() => current.value.flatMap(t => (t.result.lines || []).map(line => current.value.length > 1 ? `[${t.agent_id} / ${t.query.container}] ${line}` : line)))
const output = computed(() => mergedLines.value.join('\n'))
const displayLines = computed(() => mergedLines.value.map(splitLogLine))
const failedResults = computed(() => current.value.filter(t => ['failed', 'timed_out'].includes(t.result.status)))
const limitedResults = computed(() => current.value.filter(t => partialResult(t.result)))
const skippedResults = computed(() => current.value.filter(t => t.result.truncated && !partialResult(t.result)))
const mergedStatus = computed(() => {
  if (submitting.value || busy.value) return '查询中'
  if (failedResults.value.length === current.value.length && current.value.length) return '查询失败'
  if (failedResults.value.length || limitedResults.value.length || submissionError.value) return '部分结果'
  return '已完成'
})
const mergedNotice = computed(() => {
  if (failedResults.value.length || limitedResults.value.length) return '部分来源查询失败或达到限制，已显示可用结果。'
  if (skippedResults.value.length) return '部分日志条目已跳过。'
  return ''
})
const labels: Record<string, string> = { pending: '等待 Agent', running: '查询中', succeeded: '已完成', failed: '失败', timed_out: '已超时' }
const format = (s: string) => dateToWall(new Date(s), logZone.value)
const targetLabel = (t: Target) => `${filters.instances.find(i => i.instance_id === t.instance_id)?.name || t.instance_id} · ${t.agent_id}`
let timer: ReturnType<typeof setInterval> | undefined
let refreshing = false, selection = 0, disposed = false
watch(() => filters.site_id, () => { selection++; selectedLogSource.value = ''; current.value = []; loading.value = false; error.value = ''; submissionError.value = '' })
// Refreshes and site changes may change the preferred zone, never the selected instants.
watch(sourceZone, tz => {
  if (tz === logZone.value) { zoneNotice.value = ''; return }
  const converted = rezoneRange(range.value, logZone.value, tz)
  if (!converted) {
    zoneNotice.value = `来源时区已变化，当前区间无法无歧义地转换；已保留原时间及 ${logZone.value} 时区。`
    return
  }
  range.value = converted
  logZone.value = tz
  zoneNotice.value = ''
})
function failure(e: unknown) { return e instanceof ApiError && e.status === 409 ? '目标离线、容器不可用或查询队列已满，请刷新后重试' : '加载失败，请检查网络或登录状态' }
async function refresh() {
  if (refreshing) return
  refreshing = true
  try {
    const a = await client.request<{ items: Target[]; supports_query_batch?: boolean; supports_history_pagination?: boolean }>('/api/dashboard/container-log-targets')
    if (disposed) return
    targets.value = a.items || []
    supportsQueryBatch.value = !!a.supports_query_batch
    supportsHistoryPagination.value = !!a.supports_history_pagination
    if (busy.value) {
      const generation = selection
      const requested = current.value.filter(pending)
      const updates = await Promise.allSettled(requested.map(t => client.request<Task>(`/api/dashboard/container-log-tasks/${t.id}`)))
      if (!disposed && generation === selection) {
        const merged = mergeLogTaskRefresh(current.value, requested, updates)
        current.value = merged.tasks
        error.value = merged.warning
        return
      }
    }
    error.value = ''
  } catch (e) { if (!disposed) error.value = failure(e) } finally { refreshing = false }
}
async function loadHistory(page = 1) {
  const generation = ++historyGeneration
  const params = new URLSearchParams({ paged: '1', site: filters.site_id, page: String(page), page_size: String(historyPageSize.value) })
  if (historyRange.value) {
    const start = parseWallTime(historyRange.value[0], logZone.value), end = parseWallTime(historyRange.value[1], logZone.value)
    if (!start.date || !end.date || start.error || end.error || +end.date < +start.date) { historyLoading.value = false; ElMessage.warning('请选择有效的提交时间范围'); return }
    params.set('from', start.date.toISOString()); params.set('to', end.date.toISOString())
  }
  if (canFilterActor.value && historyActor.value.trim()) params.set('actor', historyActor.value.trim())
  if (historyRequestID.value.trim()) params.set('request_id', historyRequestID.value.trim())
  historyLoading.value = true; historyError.value = ''; historyGroups.value = []
  try {
    if (!supportsHistoryPagination.value) {
      const data = await client.request<{ items: Task[] }>('/api/dashboard/container-log-tasks')
      if (generation !== historyGeneration || disposed) return
      tasks.value = data.items || []
      const groups = groupLogHistory(tasks.value.filter(t => filters.instances.some(i => i.instance_id === t.instance_id && siteOf(i) === filters.site_id)))
      historyTotal.value = groups.length; historyPage.value = page
      historyGroups.value = groups.slice((page - 1) * historyPageSize.value, page * historyPageSize.value)
      historyError.value = '当前 CT 尚未升级，仅显示接口返回的近期记录；完整分页和筛选需更新 CT。'
      return
    }
    const data = await client.request<{ items: HistoryGroup[]; total: number; page: number }>(`/api/dashboard/container-log-tasks?${params}`)
    if (generation !== historyGeneration || disposed) return
    historyGroups.value = data.items || []; historyTotal.value = data.total; historyPage.value = data.page
    if (!historyGroups.value.length && page > 1) { void loadHistory(Math.max(1, Math.ceil(data.total / historyPageSize.value))); return }
  } catch { if (generation === historyGeneration) { historyError.value = '查询记录加载失败，请重试'; historyTotal.value = 0 } }
  finally { if (generation === historyGeneration) historyLoading.value = false }
}
function resetHistory() { historyRange.value = null; historyActor.value = ''; historyRequestID.value = ''; void loadHistory() }
watch(historyOpen, open => { if (open) void loadHistory(); else historyGeneration++ })
watch(() => filters.site_id, () => { historyGeneration++; historyGroups.value = []; historyTotal.value = 0; if (historyOpen.value) void loadHistory() })
async function submit() {
  if (submitting.value || !sources.value.length) return
  const start = parseWallTime(range.value?.[0] || '', logZone.value), end = parseWallTime(range.value?.[1] || '', logZone.value)
  const timeError = start.error || end.error
  if (timeError) {
    ElMessage.warning(timeError === 'nonexistent' ? '所选时间因时区切换而不存在，请选择其他时间' : timeError === 'ambiguous' ? '所选时间因时区回拨而出现两次，请选择不重复的时间' : '请选择有效的时间范围')
    return
  }
  const from = start.date!, to = end.date!
  if (+to <= +from || +to - +from > 3600000 || +from < Date.now() - 3 * 86400000 || +to > Date.now() + 60000) { ElMessage.warning('请选择最近 3 天内、跨度不超过 1 小时的时间范围'); return }
  const query: Omit<Query, 'source_id' | 'container'> = { from: from.toISOString(), to: to.toISOString() }
  if (supportsQueryBatch.value) query.batch_id = Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join('')
  if (keyword.value.trim()) query.keyword = keyword.value.trim()
  if (requestID.value.trim()) query.request_id = requestID.value.trim()
  if (errorCode.value.trim()) query.error_code = errorCode.value.trim()
  if (logKind.value !== 'app') {
    query.kind = logKind.value
    if (requestPath.value.trim()) query.path = requestPath.value.trim()
    if (logLevel.value) query.level = logLevel.value
    if (minDuration.value) query.min_duration_ms = minDuration.value
  }
  if ([query.request_id, query.error_code].some(v => v && !/^[a-zA-Z0-9_.:-]{1,128}$/.test(v))) { ElMessage.warning('请求 ID 和错误码仅支持字母、数字及 _ . : -，最多 128 位'); return }
  const chosen = [...sources.value], generation = ++selection
  submitting.value = true; current.value = []; submissionError.value = ''
  let failed = 0
  try {
    for (const { target, source } of chosen) {
      if (disposed || generation !== selection) break
      try {
        const task = await client.request<Task>('/api/dashboard/container-log-tasks', { method: 'POST', body: JSON.stringify({ instance_id: target.instance_id, agent_id: target.agent_id, query: { ...query, ...(source.query_host ? { host: source.query_host } : {}), source_id: source.id, container: source.container } }) })
        if (!disposed && generation === selection) current.value.push(task)
      } catch { failed++ }
    }
    if (generation === selection) {
      if (failed) submissionError.value = `${failed} 个日志来源提交失败，本次结果不完整。请稍后重新查询。`
      if (current.value.length) ElMessage.success(`已获取 ${current.value.length} 个来源的查询任务，完整历史结果直接复用`)
      await refresh()
    }
  } finally { submitting.value = false }
}
async function show(group: { tasks: Task[] }) {
  historyOpen.value = false
  const generation = ++selection; loading.value = true
  current.value = []; submissionError.value = ''
  try {
    const results = await Promise.allSettled(group.tasks.map(task => client.request<Task>(`/api/dashboard/container-log-tasks/${task.id}`)))
    if (generation === selection) {
      current.value = results.flatMap(r => r.status === 'fulfilled' ? [r.value] : [])
      if (results.some(r => r.status === 'rejected')) submissionError.value = '部分来源结果读取失败或已过期，已显示可用结果。'
    }
  }
  catch { ElMessage.error('结果不存在或已超过 24 小时保留期') }
  finally { if (generation === selection) loading.value = false }
}
const manualCopyOpen = ref(false), manualCopyText = ref('')
const manualCopyField = ref<HTMLTextAreaElement>()
function selectManualCopy() { manualCopyField.value?.focus(); manualCopyField.value?.select() }
async function copy() {
  const text = output.value
  if (!text) return
  if (await copyText(text)) ElMessage.success('已复制')
  else { manualCopyText.value = text; manualCopyOpen.value = true }
}
onMounted(async () => { try { await filters.loadInstances(); await refresh() } catch { error.value = '无法加载实例' }; if (!disposed) timer = setInterval(() => { if (document.visibilityState === 'visible') void refresh() }, 3000) })
onBeforeUnmount(() => { disposed = true; selection++; if (timer) clearInterval(timer) })
</script>

<template>
  <AppShell title="容器日志">
    <div class="container-logs">
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <section class="query-panel">
        <form class="toolbar" @submit.prevent="submit">
          <el-select v-model="logKind" style="width: 170px" aria-label="日志类型" :disabled="busy || submitting"><el-option v-for="(label, value) in logKinds" :key="value" :label="label" :value="value" /></el-select>
          <el-date-picker v-model="range" class="toolbar-time" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" start-placeholder="开始时间" end-placeholder="结束时间" :clearable="false" aria-label="查询时间区间" />

          <el-input v-model="keyword" class="toolbar-keyword" maxlength="128" placeholder="关键词（忽略大小写）" aria-label="关键词" clearable />
          <el-button :type="expanded || activeFilters ? 'primary' : 'default'" plain :aria-expanded="expanded" @click="expanded = !expanded">更多筛选{{ activeFilters ? `（${activeFilters}）` : '' }}</el-button>
          <el-button type="primary" native-type="submit" :loading="submitting" :disabled="!sources.length || busy">查询</el-button>
          <el-button @click="historyOpen = true">历史记录</el-button>
        </form>
        <div v-show="expanded" class="extra-filters">
          <label v-if="logKind !== 'app'">日志来源<el-select v-model="selectedLogSource" style="width: 320px" clearable placeholder="全部已发现来源"><el-option v-for="s in kindSources" :key="sourceKey(s.target, s.source)" :value="sourceKey(s.target, s.source)" :label="`${s.target.agent_id} / ${s.source.container} · ${s.source.log_dir.split('/').pop()}`" /></el-select></label>
          <label v-if="supportsFilter('request_id')">Request ID<el-input v-model="requestID" maxlength="128" placeholder="完整请求 ID" clearable /></label>
          <label v-if="supportsFilter('status')">{{ logKind === 'app' ? '错误码' : 'HTTP 状态码' }}<el-input v-model="errorCode" maxlength="128" :placeholder="logKind === 'app' ? '例如 429、insufficient_quota' : '例如 404、502'" clearable /></label>
          <label v-if="supportsFilter('path')">请求路径<el-input v-model="requestPath" maxlength="256" placeholder="例如 /v1/chat/completions" clearable /></label>
          <label v-if="supportsFilter('duration')">耗时 ≥<el-input-number v-model="minDuration" :min="0" :max="3600000" :precision="0" :controls="false" placeholder="毫秒" /> ms</label>
          <label v-if="supportsFilter('level')">错误级别<el-select v-model="logLevel" clearable style="width: 150px"><el-option v-for="level in ['debug','info','notice','warn','error','crit','alert','emerg']" :key="level" :value="level" :label="level" /></el-select></label>
          <el-button link @click="clearExtraFilters">清空附加条件</el-button>
          <span class="query-note">所有条件同时满足，收起后仍生效</span>
        </div>
        <div class="target-status"><el-tooltip :content="mixedZones ? '站点内日志来源时区不一致，查询按此处标注时区解释' : '查询按此处标注时区解释；来源变化时保留已选实际时间'" placement="bottom"><span class="toolbar-zone" :class="{ mixed: mixedZones }">{{ zoneText }}</span></el-tooltip><span class="status-divider" aria-hidden="true">·</span><span>{{ filters.site_id || '未选择站点' }} · {{ sources.length }} 个可查询来源</span><el-tooltip content="查询当前站点全部在线可用来源。最近 3 天内，单次跨度最多 1 小时；每个来源最多返回 2,000 行 / 512 KiB；未结束时后台自动继续，无需重复提交。关键词按原文包含匹配，不支持正则或命令。" placement="bottom"><button type="button" class="help-button" aria-label="查询范围及限制">ⓘ 查询说明</button></el-tooltip></div>
        <el-alert v-if="zoneNotice" :title="zoneNotice" type="warning" :closable="false" />
        <el-alert v-if="!sources.length" title="当前站点暂无可查询日志，请检查日志读取服务是否已接入。" type="info" :closable="false" />
        <el-alert v-if="missing.length" :title="`${missing.length} 个站点实例尚未接入或已离线，本次查询无法覆盖这些实例。`" type="warning" :closable="false" />
        <template v-for="t in available" :key="key(t)">
          <el-alert v-if="t.discovery_error" :title="`${targetLabel(t)}：${t.discovery_error}`" type="warning" :closable="false" />
          <p v-for="s in (t.sources || []).filter(s => !s.available && sourceKind(s) === logKind)" :key="s.id || s.container" class="query-note">{{ targetLabel(t) }} / {{ s.container }}：{{ s.reason || '日志不可用' }}，本次查询不包含此来源。</p>
          <p v-if="!t.sources?.length" class="query-note">{{ targetLabel(t) }}：尚未发现日志来源，本次查询不包含此服务。</p>
        </template>

      </section>
      <section v-loading="loading" class="result-panel">
        <div class="query-heading">
          <div class="result-heading-title"><h2>查询结果</h2><el-tag v-if="current.length" size="small" :type="mergedStatus === '查询失败' ? 'danger' : mergedStatus === '部分结果' ? 'warning' : 'info'">{{ mergedStatus }}</el-tag><span v-if="current.length" class="result-summary">{{ current.filter(t => !pending(t)).length }}/{{ current.length }} 来源已返回<span v-if="mergedLines.length"> · {{ mergedLines.length }} 行</span></span></div>
          <div class="result-actions"><el-button v-if="current.length" link :aria-expanded="resultDetails" aria-controls="merged-query-details" @click="resultDetails = !resultDetails">{{ resultDetails ? '收起详情' : '查询详情' }}</el-button><el-button v-if="output" link :aria-pressed="wrapLogs" @click="wrapLogs = !wrapLogs">{{ wrapLogs ? '关闭换行' : '自动换行' }}</el-button><el-button size="small" :disabled="!output" @click="copy">复制结果</el-button></div>
        </div>
        <el-alert v-if="submissionError" :title="submissionError" type="error" :closable="false" />
        <div v-if="current.length" class="source-result">
          <div v-if="resultDetails" id="merged-query-details" class="result-details">
            <div v-for="item in current" :key="item.id" class="source-detail">
              <strong>{{ item.agent_id }} / {{ item.query.container }} · {{ logKinds[item.query.kind || 'app'] }} · {{ labels[item.result.status] }} · {{ item.result.lines?.length || 0 }} 行</strong>
              <p>时间：{{ format(item.query.from) }} 至 {{ format(item.query.to) }} · 发起人：{{ item.actor_name || item.actor }}</p>
              <p v-if="item.query.keyword || item.query.request_id || item.query.error_code"><span v-if="item.query.keyword">关键词：{{ item.query.keyword }}　</span><span v-if="item.query.request_id">Request ID：{{ item.query.request_id }}　</span><span v-if="item.query.error_code">错误码：{{ item.query.error_code }}</span></p>
              <p v-if="item.query.host">域名：{{ item.query.host }}<span v-if="item.query.path"> · 路径包含：{{ item.query.path }}</span><span v-if="item.query.min_duration_ms"> · 耗时 ≥ {{ item.query.min_duration_ms }} ms</span><span v-if="item.query.level"> · 级别：{{ item.query.level }}</span></p>
              <p v-if="item.result.phase || item.result.files_scanned">{{ phases[item.result.phase || ''] || '' }}<span v-if="item.result.files_scanned"> · 累计读取 {{ ((item.result.total_scanned_bytes ?? item.result.scanned_bytes ?? 0) / 1048576).toFixed(2) }} MiB</span></p>
              <p v-if="item.result.error">{{ item.result.error }}</p>
              <p v-if="item.result.note">{{ item.result.note }}</p>
            </div>
          </div>
          <p v-if="mergedNotice" class="result-notice" role="status"><span>{{ mergedNotice }}</span><button type="button" class="notice-details" :aria-expanded="resultDetails" aria-controls="merged-query-details" @click="resultDetails = !resultDetails">{{ resultDetails ? '收起说明' : '查看说明' }}</button></p>
          <div v-if="output" class="log-output" :class="{ nowrap: !wrapLogs }" tabindex="0" role="region" aria-label="合并日志查询结果"><div v-for="(line, index) in displayLines" :key="index" class="log-line"><span class="log-prefix">{{ line.prefix }}</span><span class="log-body">{{ line.body }}</span></div></div>
          <el-empty v-else :description="busy || submitting ? '正在等待查询结果…' : failedResults.length ? '本次查询没有返回日志，原因见查询详情' : skippedResults.length || limitedResults.length ? '未找到匹配日志，查询说明见详情' : '在本次扫描范围内没有匹配日志'" :image-size="70" />
        </div>
        <el-empty v-if="!current.length" description="设置时间范围后点击查询" :image-size="70" />
      </section>
      <el-drawer v-model="historyOpen" title="当前站点 · 查询记录" size="min(900px, 96vw)"><p class="query-note">保留 24 小时，多个来源按一次查询计数。提交时间使用 {{ zoneText }}。</p>
        <form class="history-filters" @submit.prevent="loadHistory()">
          <el-date-picker v-model="historyRange" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" start-placeholder="提交开始时间" end-placeholder="提交结束时间" :disabled="!supportsHistoryPagination" />
          <el-input v-if="canFilterActor" v-model="historyActor" placeholder="发起人账号或姓名（完整匹配）" clearable :maxlength="128" :disabled="!supportsHistoryPagination" />
          <el-input v-model="historyRequestID" placeholder="完整 Request ID" clearable :maxlength="128" :disabled="!supportsHistoryPagination" />
          <el-button native-type="submit" type="primary" :loading="historyLoading">筛选</el-button><el-button @click="resetHistory">重置</el-button>
        </form>
        <el-alert v-if="historyError" :title="historyError" type="warning" :closable="false" />
        <el-table v-loading="historyLoading" :data="history" @row-click="show" class="history-table"><el-table-column label="提交时间" width="180"><template #default="s">{{ format(s.row.created_at) }}</template></el-table-column><el-table-column label="查询范围" min-width="240"><template #default="s">{{ format(s.row.tasks[0].query.from) }} 至 {{ format(s.row.tasks[0].query.to) }}</template></el-table-column><el-table-column label="发起人" min-width="150"><template #default="s">{{ s.row.tasks[0].actor_name ? `${s.row.tasks[0].actor_name}（${s.row.tasks[0].actor}）` : s.row.tasks[0].actor }}</template></el-table-column><el-table-column label="来源" width="80"><template #default="s">{{ s.row.tasks.length }} 个</template></el-table-column><el-table-column label="状态" width="130"><template #default="s">{{ s.row.status }}</template></el-table-column><el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click.stop="show(s.row)">查看结果</el-button></template></el-table-column></el-table><el-pagination class="history-pagination" :current-page="historyPage" v-model:page-size="historyPageSize" :total="historyTotal" :page-sizes="[20, 50, 100]" layout="total, sizes, prev, pager, next" :disabled="historyLoading" @current-change="loadHistory" @size-change="loadHistory(1)" /></el-drawer>
    </div>
    <el-dialog v-model="manualCopyOpen" title="手动复制日志" width="min(760px, calc(100vw - 32px))" append-to-body @opened="selectManualCopy" @closed="manualCopyText = ''">
      <p>浏览器未允许自动复制，日志已选中，请按 Ctrl+C（Mac 使用 ⌘C）。</p>
      <textarea ref="manualCopyField" :value="manualCopyText" readonly aria-label="待复制日志" style="width:100%;height:300px;font-family:monospace;white-space:pre;" />
      <template #footer><el-button @click="selectManualCopy">全选日志</el-button><el-button @click="manualCopyOpen = false">关闭</el-button></template>
    </el-dialog>
  </AppShell>
</template>

<style scoped>
.container-logs { display: grid; gap: 14px; }
.query-panel, .result-panel { background: var(--el-bg-color); border: 1px solid var(--el-border-color-light); border-radius: 10px; }
.query-panel { padding: 14px 16px 10px; }
.toolbar { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.toolbar :deep(.el-button), .result-actions :deep(.el-button) { margin-left: 0; }
.toolbar-time { flex: 0 1 420px !important; width: 420px !important; min-width: 0; }
.toolbar-keyword { flex: 1 1 240px; min-width: 180px; }
.toolbar-zone { cursor: help; }
.toolbar-zone.mixed { color: var(--el-color-warning); }
.extra-filters { display: flex; align-items: center; gap: 12px 20px; flex-wrap: wrap; padding: 12px 0; margin-top: 12px; border-top: 1px solid var(--el-border-color-lighter); }
.extra-filters label { display: flex; align-items: center; gap: 8px; font-size: 12px; white-space: nowrap; }
.extra-filters :deep(.el-input) { width: 230px; }
.target-status { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-top: 10px; font-size: 12px; color: var(--el-text-color-secondary); }
.status-divider { color: var(--el-border-color); }
.help-button { padding: 0; border: 0; background: none; color: inherit; font: inherit; cursor: help; }
.help-button:hover { color: var(--el-color-primary); }
.query-panel :deep(.el-alert) { margin-top: 8px; padding: 6px 10px; }
.query-note { font-size: 12px; color: var(--el-text-color-secondary); line-height: 1.7; margin: 6px 0 0; }
.extra-filters .query-note { margin: 0; }
.result-panel { padding: 0 16px 16px; min-width: 0; }
.query-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; flex-wrap: wrap; padding: 14px 0; }
.result-heading-title, .result-actions { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.result-actions { margin-left: auto; gap: 14px; }
.query-heading h2 { margin: 0; font-size: 15px; font-weight: 600; }
.result-summary { color: var(--el-text-color-secondary); font-size: 12px; }
.source-result { min-width: 0; }
.result-details { padding: 12px 14px; margin-bottom: 12px; background: var(--el-fill-color-light); border-radius: 8px; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.8; overflow-wrap: anywhere; }
.result-details strong { color: var(--el-text-color-regular); font-weight: 500; }
.result-details p { margin: 2px 0; }
.source-detail + .source-detail { border-top: 1px solid var(--el-border-color-light); margin-top: 10px; padding-top: 10px; }
.result-notice { display: flex; align-items: baseline; flex-wrap: wrap; gap: 4px 8px; margin: 0 0 8px; color: var(--el-text-color-secondary); font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; }
.notice-details { padding: 0; border: 0; background: none; color: var(--el-text-color-regular); font: inherit; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
.notice-details:hover { color: var(--el-color-primary); }
.notice-details:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 3px; }
.log-output { box-sizing: border-box; margin: 0; background: #101827; color: #dce7f7; font: 12px/1.85 Consolas, monospace; white-space: pre-wrap; overflow-wrap: anywhere; min-height: 260px; max-height: max(320px, calc(100vh - 290px)); overflow: auto; border: 1px solid #202d43; border-radius: 8px; padding: 16px; tab-size: 4; scrollbar-color: #48556c #101827; }
.log-line { display: grid; grid-template-columns: max-content minmax(24ch, 1fr); min-height: 1.85em; }
.log-prefix { white-space: pre; }
.log-body { white-space: pre-wrap; overflow-wrap: anywhere; }
.log-output.nowrap { white-space: pre; overflow-wrap: normal; }
.log-output.nowrap .log-line { grid-template-columns: max-content max-content; }
.log-output.nowrap .log-body { white-space: pre; overflow-wrap: normal; }
.log-output:focus-visible { outline: 2px solid var(--el-color-primary); outline-offset: 2px; }
.result-panel :deep(.el-empty) { min-height: 310px; padding: 32px 0; }
.history-table { cursor: pointer; }
.history-filters { display: flex; flex-wrap: wrap; gap: 8px; margin: 14px 0; }
.history-filters :deep(.el-date-editor) { flex: 1 1 100%; width: 100%; }
.history-filters > .el-input { flex: 1 1 220px; }
.history-pagination { margin-top: 16px; overflow-x: auto; }
@media (max-width: 1100px) { .toolbar-time { flex-basis: 380px !important; width: 380px !important; } .toolbar-keyword { flex-basis: 180px; } }
@media (max-width: 650px) {
  .query-panel { padding: 12px; }
  .result-panel { padding: 0 12px 12px; }
  .toolbar-time { flex: 1 1 100% !important; width: 100% !important; }
  .toolbar-keyword { flex-basis: 100%; }
  .extra-filters { gap: 10px; }
  .extra-filters label { width: 100%; justify-content: space-between; }
  .extra-filters :deep(.el-input) { width: min(230px, 72%); }
  .result-actions { margin-left: 0; }
  .result-summary { width: 100%; }
  .log-output { padding: 12px; }
}
</style>
