<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, siteOf } from '@ct/shared'
import { client } from '../api'
import AppShell from '../components/AppShell.vue'
import { useFiltersStore } from '../stores/filters'
import { dateToWall, isValidZone, parseWallTime, rezoneRange, zoneLabel } from '../utils/zoned'

interface Query { keyword?: string; source_id: string; container: string; from: string; to: string; request_id?: string; error_code?: string }
interface Result { total_scanned_bytes?: number; next_cursor?: string; complete?: boolean; phase?: string; indexed_bytes?: number; note?: string; files_scanned?: number; scanned_bytes?: number; status: string; lines: string[]; truncated?: boolean; error?: string }
interface Task { id: string; instance_id: string; agent_id: string; actor: string; actor_name: string; query: Query; result: Result; created_at: string }
interface Source { id: string; container: string; log_dir: string; timezone: string; available: boolean; reason?: string }
interface Target { sources: Source[]; discovery_error?: string; instance_id: string; agent_id: string; containers: string[]; seen_at: string }
const filters = useFiltersStore()
const targets = ref<Target[]>([]), tasks = ref<Task[]>([]), current = ref<Task[]>([])
const requestID = ref(''), errorCode = ref('')
const expanded = ref(false), historyOpen = ref(false), keyword = ref('')
const activeFilters = computed(() => [requestID.value.trim(), errorCode.value.trim()].filter(Boolean).length)
// The picker holds wall-clock strings in the log source's zone (not the
// browser's), so what the operator types matches the timestamps in the logs.
const DEFAULT_ZONE = 'Asia/Shanghai'
const defaultRange = (tz: string): [string, string] => [dateToWall(new Date(Date.now() - 15 * 60000), tz), dateToWall(new Date(), tz)]
const range = ref<[string, string]>(defaultRange(DEFAULT_ZONE))
const logZone = ref(DEFAULT_ZONE), zoneNotice = ref('')
const submitting = ref(false), loading = ref(false), error = ref('')
const key = (t: Target) => JSON.stringify([t.instance_id, t.agent_id])
const siteIDs = computed(() => new Set(filters.instances.filter(i => siteOf(i) === filters.site_id).map(i => i.instance_id)))
const available = computed(() => targets.value.filter(t => siteIDs.value.has(t.instance_id)))
const sources = computed(() => available.value.flatMap(target => (target.sources || []).filter(source => source.available).map(source => ({ target, source }))))
const sourceZone = computed(() => { const tz = sources.value[0]?.source.timezone || DEFAULT_ZONE; return isValidZone(tz) ? tz : DEFAULT_ZONE })
const mixedZones = computed(() => new Set(sources.value.map(s => s.source.timezone || DEFAULT_ZONE)).size > 1)
const zoneText = computed(() => zoneLabel(logZone.value))
const history = computed(() => tasks.value.filter(t => siteIDs.value.has(t.instance_id)))
const pending = (task: Task) => ['pending', 'running'].includes(task.result.status)
const busy = computed(() => current.value.some(pending))
const phases: Record<string,string> = { indexing:'建立时间索引', querying:'按索引查询', archive:'分批解压归档', complete:'扫描结束' }
const submissionError = ref('')
const missing = computed(() => filters.instances.filter(i => siteIDs.value.has(i.instance_id) && !available.value.some(t => t.instance_id === i.instance_id)))
const output = computed(() => current.value.filter(t => t.result.lines?.length).map(t => `[${t.agent_id} / ${t.query.container}]\n${t.result.lines.join('\n')}`).join('\n\n'))
const labels: Record<string, string> = { pending: '等待 Agent', running: '查询中', succeeded: '已完成', failed: '失败', timed_out: '已超时' }
const format = (s: string) => dateToWall(new Date(s), logZone.value)
const targetLabel = (t: Target) => `${filters.instances.find(i => i.instance_id === t.instance_id)?.name || t.instance_id} · ${t.agent_id}`
let timer: ReturnType<typeof setInterval> | undefined
let refreshing = false, selection = 0, disposed = false
watch(() => filters.site_id, () => { selection++; current.value = []; loading.value = false; error.value = ''; submissionError.value = '' })
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
    const [a, b] = await Promise.all([
      client.request<{ items: Target[] }>('/api/dashboard/container-log-targets'),
      client.request<{ items: Task[] }>('/api/dashboard/container-log-tasks'),
    ])
    if (disposed) return
    targets.value = a.items || []; tasks.value = b.items || []
    if (busy.value) {
      const generation = selection
      const updates = await Promise.allSettled(current.value.filter(pending).map(t => client.request<Task>(`/api/dashboard/container-log-tasks/${t.id}`)))
      if (!disposed && generation === selection) {
        for (const update of updates) if (update.status === 'fulfilled') current.value = current.value.map(t => t.id === update.value.id ? update.value : t)
        if (updates.some(r => r.status === 'rejected')) throw new Error('result refresh failed')
      }
    }
    error.value = ''
  } catch (e) { if (!disposed) error.value = failure(e) } finally { refreshing = false }
}
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
  if (keyword.value.trim()) query.keyword = keyword.value.trim()
  if (requestID.value.trim()) query.request_id = requestID.value.trim()
  if (errorCode.value.trim()) query.error_code = errorCode.value.trim()
  if ([query.request_id, query.error_code].some(v => v && !/^[a-zA-Z0-9_.:-]{1,128}$/.test(v))) { ElMessage.warning('请求 ID 和错误码仅支持字母、数字及 _ . : -，最多 128 位'); return }
  const chosen = [...sources.value], generation = ++selection
  submitting.value = true; current.value = []; submissionError.value = ''
  let failed = 0
  try {
    for (const { target, source } of chosen) {
      if (disposed || generation !== selection) break
      try {
        const task = await client.request<Task>('/api/dashboard/container-log-tasks', { method: 'POST', body: JSON.stringify({ instance_id: target.instance_id, agent_id: target.agent_id, query: { ...query, source_id: source.id, container: source.container } }) })
        if (!disposed && generation === selection) current.value.push(task)
      } catch { failed++ }
    }
    if (generation === selection) {
      if (failed) submissionError.value = `${failed} 个日志来源提交失败，本次结果不完整。请稍后重新查询。`
      if (current.value.length) ElMessage.success(`已向当前站点的 ${current.value.length} 个日志来源提交查询`)
      await refresh()
    }
  } finally { submitting.value = false }
}
async function show(task: Task) {
  historyOpen.value = false
  const generation = ++selection; loading.value = true
  try { const value = await client.request<Task>(`/api/dashboard/container-log-tasks/${task.id}`); if (generation === selection) current.value = [value] }
  catch { ElMessage.error('结果不存在或已超过 24 小时保留期') }
  finally { if (generation === selection) loading.value = false }
}
async function copy() { try { await navigator.clipboard.writeText(output.value); ElMessage.success('已复制') } catch { ElMessage.warning('当前浏览器不支持直接复制，可选中日志手动复制') } }
onMounted(async () => { try { await filters.loadInstances(); await refresh() } catch { error.value = '无法加载实例' }; if (!disposed) timer = setInterval(() => { if (document.visibilityState === 'visible') void refresh() }, 3000) })
onBeforeUnmount(() => { disposed = true; selection++; if (timer) clearInterval(timer) })
</script>

<template>
  <AppShell title="容器日志">
    <div class="container-logs">
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <section class="query-panel">
        <form class="toolbar" @submit.prevent="submit">
          <el-date-picker v-model="range" class="toolbar-time" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" start-placeholder="开始时间" end-placeholder="结束时间" :clearable="false" aria-label="查询时间区间" />
          <el-tooltip :content="mixedZones ? '站点内日志来源时区不一致，查询按此处标注时区解释' : '查询按此处标注时区解释；来源变化时保留已选实际时间'" placement="bottom"><span class="toolbar-zone" :class="{ mixed: mixedZones }">{{ zoneText }}</span></el-tooltip>
          <el-input v-model="keyword" class="toolbar-keyword" maxlength="128" placeholder="关键词，匹配日志原文" aria-label="关键词" clearable />
          <el-button :type="expanded || activeFilters ? 'primary' : 'default'" plain :aria-expanded="expanded" @click="expanded = !expanded">更多筛选{{ activeFilters ? `（${activeFilters}）` : '' }}</el-button>
          <el-button type="primary" native-type="submit" :loading="submitting" :disabled="!sources.length || busy">查询</el-button>
          <el-button @click="historyOpen = true">历史记录</el-button>
        </form>
        <div v-show="expanded" class="extra-filters">
          <label>Request ID<el-input v-model="requestID" maxlength="128" placeholder="完整请求 ID" clearable /></label>
          <label>错误码<el-input v-model="errorCode" maxlength="128" placeholder="例如 429、insufficient_quota" clearable /></label>
          <el-button link @click="requestID = ''; errorCode = ''">清空附加条件</el-button>
          <span class="query-note">所有条件同时满足，收起后仍生效</span>
        </div>
        <div class="target-status"><span>{{ filters.site_id || '未选择站点' }} · {{ sources.length }} 个可查询来源</span><el-tooltip content="查询当前站点全部在线可用来源。最近 3 天内，单次跨度最多 1 小时；每批最多处理 256 个文件、64 MiB，返回 2,000 行 / 512 KiB；未结束时后台自动继续，无需重复提交。首次建立时间索引，后续复用；服务重启后需重建。关键词按原文包含匹配，不支持正则或命令。" placement="bottom"><button type="button" class="help-button" aria-label="查询范围及限制">ⓘ 查询说明</button></el-tooltip></div>
        <el-alert v-if="zoneNotice" :title="zoneNotice" type="warning" :closable="false" />
        <el-alert v-if="!sources.length" title="当前站点暂无可查询日志，请检查日志读取服务是否已接入。" type="info" :closable="false" />
        <el-alert v-if="missing.length" :title="`${missing.length} 个站点实例尚未接入或已离线，本次查询无法覆盖这些实例。`" type="warning" :closable="false" />
        <template v-for="t in available" :key="key(t)">
          <el-alert v-if="t.discovery_error" :title="`${targetLabel(t)}：${t.discovery_error}`" type="warning" :closable="false" />
          <p v-for="s in (t.sources || []).filter(s => !s.available)" :key="s.container" class="query-note">{{ targetLabel(t) }} / {{ s.container }}：{{ s.reason || '日志不可用' }}，本次查询不包含此来源。</p>
          <p v-if="!t.sources?.length" class="query-note">{{ targetLabel(t) }}：尚未发现日志来源，本次查询不包含此服务。</p>
        </template>

      </section>
      <section v-loading="loading" class="result-panel">
        <div class="query-heading"><h2>查询结果 <el-tag v-if="current.length" type="info">{{ current.filter(t => !pending(t)).length }} / {{ current.length }} 已返回</el-tag></h2><el-button :disabled="!output" @click="copy">复制结果</el-button></div>
        <el-alert v-if="submissionError" :title="submissionError" type="error" :closable="false" />
        <div v-for="item in current" :key="item.id" class="source-result">
          <h3>{{ item.agent_id }} / {{ item.query.container }} <el-tag type="info">{{ labels[item.result.status] }}</el-tag></h3>
          <p class="query-note">{{ item.query.container }} · {{ format(item.query.from) }} 至 {{ format(item.query.to) }} · 发起人 {{ item.actor_name || item.actor }} <span v-if="item.query.keyword"> · 关键词：{{ item.query.keyword }}</span><span v-if="item.query.request_id"> · Request ID：{{ item.query.request_id }}</span><span v-if="item.query.error_code"> · 错误码：{{ item.query.error_code }}</span></p>
          <div v-if="item.result.phase" class="query-note">{{ phases[item.result.phase] }} · 本次索引处理 {{ ((item.result.indexed_bytes || 0) / 1048576).toFixed(2) }} MiB<span v-if="item.result.complete"> · 本次文件快照已扫描结束</span></div>
          <el-alert v-if="item.result.truncated" title="部分目录或记录未能处理，结果可能不完整，请查看下方说明。" type="warning" :closable="false" />
          <el-alert v-if="item.result.note" :title="item.result.note" type="warning" :closable="false" />
          <p v-if="item.result.files_scanned" class="query-note">累计读取 {{ ((item.result.total_scanned_bytes ?? item.result.scanned_bytes ?? 0) / 1048576).toFixed(2) }} MiB</p>
          <el-alert v-if="item.result.error" :title="item.result.error" type="error" :closable="false" />
          <pre v-if="item.result.lines?.length" class="log-output">{{ item.result.lines.join('\n') }}</pre>
          <el-empty v-else :description="pending(item) ? '正在等待查询结果…' : item.result.truncated ? '存在未处理记录，不能判断该时段没有日志' : item.result.status === 'succeeded' ? '在本次扫描范围内没有匹配日志' : '本次查询没有返回日志'" :image-size="70" />
        </div>
        <el-empty v-if="!current.length" description="设置时间范围后点击查询" :image-size="70" />
      </section>
      <el-drawer v-model="historyOpen" title="当前站点 · 我的查询记录" size="min(900px, 96vw)"><p class="query-note">保留 24 小时，显示最近 50 次</p><el-table :data="history" @row-click="show" class="history-table"><el-table-column label="提交时间" width="180"><template #default="s">{{ format(s.row.created_at) }}</template></el-table-column><el-table-column prop="agent_id" label="服务器 / Agent" /><el-table-column prop="query.container" label="容器" /><el-table-column label="状态" width="130"><template #default="s">{{ labels[s.row.result.status] }}</template></el-table-column><el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click.stop="show(s.row)">查看结果</el-button></template></el-table-column></el-table></el-drawer>
    </div>
  </AppShell>
</template>

<style scoped>
.container-logs{display:grid;gap:12px}.query-panel,.result-panel{background:white;border:1px solid var(--el-border-color-light);border-radius:10px;padding:16px}.query-panel{padding:12px 16px}.result-panel{min-height:calc(100vh - 230px)}.toolbar{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.toolbar :deep(.el-button){margin-left:0}.toolbar-time{flex:0 1 380px!important;width:380px!important;min-width:0}.toolbar-zone{font-size:12px;color:var(--el-text-color-secondary);white-space:nowrap;cursor:help}.toolbar-zone.mixed{color:var(--el-color-warning)}.toolbar-keyword{flex:1 1 220px;min-width:160px}.extra-filters{display:flex;align-items:center;gap:16px;flex-wrap:wrap;padding-top:12px}.extra-filters label{display:flex;align-items:center;gap:8px;font-size:12px;white-space:nowrap}.extra-filters :deep(.el-input){width:230px}.target-status{display:flex;align-items:center;gap:12px;margin-top:8px;color:var(--el-text-color-secondary);font-size:12px}.help-button{border:0;background:none;color:inherit;font:inherit;padding:0;cursor:help}.query-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:18px}.query-heading h2{font-size:16px;margin:0;display:flex;gap:12px;align-items:center}.query-heading p,.query-note{font-size:12px;color:var(--el-text-color-secondary);line-height:1.7;margin:8px 0 0}.query-fields{display:grid;grid-template-columns:minmax(0, 640px);gap:16px}.query-fields :deep(.el-select),.time-field :deep(.el-date-editor){width:100%;min-width:0}.optional-fields{grid-template-columns:repeat(2,minmax(0,312px))}.query-actions{display:flex;align-items:center;gap:12px;margin-bottom:16px}.query-actions>span{font-size:12px;color:var(--el-text-color-secondary)}.query-actions>.el-button{margin-left:auto}.log-output{background:#101827;color:#dce7f7;font:12px/1.8 Consolas,monospace;white-space:pre-wrap;overflow-wrap:anywhere;max-height:540px;overflow:auto;border-radius:8px;padding:18px;tab-size:4}.source-result{border-top:1px solid var(--el-border-color-light);padding-top:16px;margin-top:16px}.source-result h3{font-size:14px;display:flex;align-items:center;gap:12px}.history-table{cursor:pointer}@media(max-width:1050px){.query-fields{grid-template-columns:1fr 1fr}.time-field{grid-column:1/-1}}@media(max-width:650px){.query-fields{grid-template-columns:1fr}.query-panel,.result-panel{padding:14px}.query-actions{flex-wrap:wrap}}
</style>
