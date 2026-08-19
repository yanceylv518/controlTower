<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { ReadonlyLog } from '@ct/shared'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { formatNumber } from '../utils/format'
import { timeRangeShortcuts } from '../utils/billingRange'

type LogExtra = Record<string, unknown>
const filters = useFiltersStore(), prefs = usePrefsStore()
const route = useRoute()
const username = ref(''), tokenName = ref(''), modelName = ref(''), requestID = ref('')
const channelID = ref('')
const logType = ref<number>(), offset = ref(0), limit = ref(100)
const defaultTimeRange = (): [Date, Date] => {
  const now = new Date()
  const start = new Date(now)
  start.setHours(0, 0, 0, 0)
  return [start, new Date(now.getTime() + 60 * 60 * 1000)]
}
const timeRange = ref<[Date, Date]>(defaultTimeRange())
const parsedChannelID = computed(() => { const value = Number(channelID.value); return channelID.value.trim() !== '' && Number.isSafeInteger(value) && value >= 0 ? value : undefined })
const params = computed(() => ({ site: filters.site_id, username: username.value, start_time: timeRange.value[0].toISOString(), end_time: timeRange.value[1].toISOString(), token_name: tokenName.value, model_name: modelName.value, request_id: requestID.value, channel_id: parsedChannelID.value, log_type: logType.value, limit: limit.value, offset: offset.value }))
const statParams = computed(() => ({ site: filters.site_id, username: username.value, start_time: timeRange.value[0].toISOString(), end_time: timeRange.value[1].toISOString(), token_name: tokenName.value, model_name: modelName.value, channel_id: parsedChannelID.value }))
const countParams = computed(() => { const { limit: _limit, offset: _offset, ...rest } = params.value; return rest })
const state = useAsyncData(() => passthrough.logs(params.value))
const statState = useAsyncData(() => passthrough.logStat(statParams.value))
const countState = useAsyncData(() => passthrough.logCount(countParams.value))
const extraCache = new WeakMap<object, LogExtra>()
function extra(row: ReadonlyLog): LogExtra { const cached = extraCache.get(row); if (cached) return cached; let value: LogExtra = {}; try { value = row.other ? JSON.parse(row.other) as LogExtra : {} } catch { value = {} }; extraCache.set(row, value); return value }
function first(row: ReadonlyLog, ...keys: string[]) { const data = extra(row); for (const key of keys) { const value = data[key]; if (value !== undefined && value !== null && value !== '') return value } return undefined }
function numberValue(row: ReadonlyLog, ...keys: string[]) { const value = Number(first(row, ...keys)); return Number.isFinite(value) ? value : undefined }
function textValue(row: ReadonlyLog, ...keys: string[]) { const value = first(row, ...keys); if (value === undefined) return ''; return typeof value === 'string' ? value : JSON.stringify(value) }
const search = () => { offset.value = 0; void Promise.allSettled([state.reload(), statState.reload(), countState.reload()]) }
const fallbackTotal = computed(() => offset.value + (state.data.value?.items.length || 0) + (state.data.value?.has_more ? 1 : 0))
const effectiveTotal = computed(() => !countState.loading.value && !countState.error.value && countState.data.value ? countState.data.value.total : fallbackTotal.value)
const currentPage = computed(() => Math.floor(offset.value / limit.value) + 1)
const totalPages = computed(() => Math.max(1, Math.ceil(effectiveTotal.value / limit.value)))
const rangeStart = computed(() => state.data.value?.items.length ? offset.value + 1 : 0)
const rangeEnd = computed(() => offset.value + (state.data.value?.items.length || 0))
const changePage = (page: number) => { offset.value = (page - 1) * limit.value; void state.reload() }
const changePageSize = (size: number) => { limit.value = size; offset.value = 0; void state.reload() }
const reset = () => { username.value = ''; tokenName.value = ''; modelName.value = ''; requestID.value = ''; channelID.value = ''; logType.value = undefined; timeRange.value = defaultTimeRange(); search() }
const typeMeta = (type: number) => ({ 1: ['充值', 'warning'], 2: ['消费', 'success'], 3: ['管理', 'info'], 4: ['系统', 'info'], 5: ['错误', 'danger'], 6: ['退款', 'warning'], 7: ['登录', 'info'] }[type] || [`类型 ${type}`, 'info']) as [string, 'success' | 'warning' | 'info' | 'danger']
const money = (quota: number) => {
  const amount = quota / (prefs.quotaPerUnit || 500000)
  return `${prefs.currencySymbol}${amount >= 1 ? amount.toFixed(2) : amount.toFixed(6)}`
}
const logSummary = computed(() => statState.data.value?.summary)
const billingPrice = (value: number) => `${prefs.currencySymbol}${value.toFixed(6)}`
const initial = (name: string) => name?.trim().slice(0, 1) || '用'
const requestPath = (row: ReadonlyLog) => textValue(row, 'request_path') || '—'
const cacheReadTokens = (row: ReadonlyLog) => numberValue(row, 'cache_tokens') || 0
const cacheWrite5mTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_5m') || 0
const cacheWrite1hTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_1h') || 0
const cacheWriteTokens = (row: ReadonlyLog) => {
  const split = cacheWrite5mTokens(row) + cacheWrite1hTokens(row)
  return split > 0 ? split : numberValue(row, 'cache_creation_tokens') || 0
}
const isConsume = (row: ReadonlyLog) => row.type === 2
function streamFlag(row: ReadonlyLog): boolean | undefined {
  const value = row.is_stream
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value === 1 ? true : value === 0 ? false : undefined
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    if (['1', 'true'].includes(normalized)) return true
    if (['0', 'false'].includes(normalized)) return false
  }
  // Older Control Tower responses omitted is_stream. A stream_status payload is
  // still positive evidence of a streaming request, but its absence is unknown.
  return first(row, 'stream_status') !== undefined ? true : undefined
}
function firstResponse(row: ReadonlyLog): number | undefined {
  // new-api stores `other.frt` in milliseconds and uses negative values
  // (commonly -1000) as the sentinel for requests without a first-token time.
  // A first-token latency only applies to streaming requests.
  if (streamFlag(row) !== true) return undefined
  const milliseconds = numberValue(row, 'frt', 'first_response_time')
  return milliseconds !== undefined && milliseconds > 0 ? milliseconds / 1000 : undefined
}
const streamLabel = (row: ReadonlyLog) => streamFlag(row) === true ? '流' : streamFlag(row) === false ? '非流' : '未知'
function streamStatus(row: ReadonlyLog) {
  const value = first(row, 'stream_status')
  if (value && typeof value === 'object') {
    const status = String((value as LogExtra).status || '')
    const reason = String((value as LogExtra).end_reason || '')
    return status === 'ok' ? `✓ 正常${reason ? ` (${reason})` : ''}` : [status || '异常', reason].filter(Boolean).join('：')
  }
  if (typeof value === 'string' && value) return value
  const flag = streamFlag(row)
  return flag === true ? '流式' : flag === false ? '非流式' : '旧版接口未返回流式标记'
}
const conversion = (row: ReadonlyLog) => textValue(row, 'request_conversion_chain', 'final_request_format') || '原生格式'
function overrideText(row: ReadonlyLog) {
  const value = first(row, 'po', 'parameter_overrides')
  if (Array.isArray(value)) return value.length ? `${value.length} 项操作` : ''
  if (value && typeof value === 'object') return Object.keys(value).length ? `${Object.keys(value).length} 项操作` : ''
  return value === undefined || value === null ? '' : String(value)
}
function effectiveGroupRatio(row: ReadonlyLog) {
  const userRatio = numberValue(row, 'user_group_ratio')
  return userRatio !== undefined && userRatio !== -1 ? userRatio : numberValue(row, 'group_ratio')
}
const isPerCallBilling = (modelPrice: number | undefined) => modelPrice !== undefined && modelPrice > 0
const ratioText = (value: number) => `${Number(value.toFixed(4))}x`
function groupRatioLabel(row: ReadonlyLog) {
  const userRatio = numberValue(row, 'user_group_ratio')
  return userRatio !== undefined && userRatio !== -1 ? '用户专属倍率' : '分组倍率'
}
function billingSummary(row: ReadonlyLog) {
  if (!isConsume(row)) return row.content_summary || '—'
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row)
  const parts: string[] = []
  if (groupRatio !== undefined) parts.push(`${groupRatioLabel(row)} ${ratioText(groupRatio)}`)
  if (isPerCallBilling(modelPrice)) {
    parts.push(`按次 ${billingPrice(modelPrice!)}`)
  } else if (modelRatio !== undefined) {
    const inputPrice = modelRatio * 2
    parts.push(`输入 ${billingPrice(inputPrice)} / 1M tokens`)
    if (completionRatio !== undefined) parts.push(`输出 ${billingPrice(inputPrice * completionRatio)} / 1M tokens`)
    if (cacheReadTokens(row) > 0 && cacheRatio !== undefined && cacheRatio !== 1) parts.push(`缓存读 ${billingPrice(inputPrice * cacheRatio)} / 1M tokens`)
    const cacheWriteRatio = numberValue(row, 'cache_creation_ratio')
    const cacheWrite1hRatio = numberValue(row, 'cache_creation_ratio_1h')
    if (cacheWriteTokens(row) > 0 && cacheWriteRatio !== undefined && cacheWriteRatio !== 1) parts.push(`缓存写 ${billingPrice(inputPrice * cacheWriteRatio)} / 1M tokens`)
    if (cacheWrite1hTokens(row) > 0 && cacheWrite1hRatio !== undefined && cacheWrite1hRatio !== 0) parts.push(`缓存写 1h ${billingPrice(inputPrice * cacheWrite1hRatio)} / 1M tokens`)
  }
  return parts.join('；') || row.content_summary || '—'
}
function logDetail(row: ReadonlyLog) {
  if (!isConsume(row)) return row.content_summary || '—'
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row)
  const values: string[] = []
  if (isPerCallBilling(modelPrice)) {
    values.push(`按次价格 ${billingPrice(modelPrice!)}`)
  } else if (modelRatio !== undefined) {
    const inputPrice = modelRatio * 2
    values.push(`输入价格 ${billingPrice(inputPrice)} / 1M tokens`)
    if (completionRatio !== undefined) values.push(`输出价格 ${billingPrice(inputPrice * completionRatio)} / 1M tokens`)
    if (cacheReadTokens(row) > 0 && cacheRatio !== undefined) values.push(`缓存读取价格 ${billingPrice(inputPrice * cacheRatio)} / 1M tokens`)
  }
  if (groupRatio !== undefined) values.push(`${groupRatioLabel(row)} ${ratioText(groupRatio)}`)
  return values.join('，') || row.content_summary || '—'
}
function billingProcess(row: ReadonlyLog) {
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row) ?? 1
  const groupLabel = groupRatioLabel(row)
  const lines: string[] = []
  if (isPerCallBilling(modelPrice)) {
    lines.push(`按次价格：${billingPrice(modelPrice!)}`)
    lines.push(`${groupLabel}：${ratioText(groupRatio)}`)
  } else if (modelRatio !== undefined) {
    const inputPrice = modelRatio * 2
    const outputPrice = inputPrice * (completionRatio ?? 1)
    const cached = cacheReadTokens(row)
    const uncached = Math.max(0, row.prompt_tokens - cached)
    lines.push(`输入价格：${billingPrice(inputPrice)} / 1M tokens`)
    lines.push(`输出价格：${billingPrice(outputPrice)} / 1M tokens`)
    if (cached > 0 && cacheRatio !== undefined) lines.push(`缓存读取价格：${billingPrice(inputPrice * cacheRatio)} / 1M tokens`)
    const terms = [`输入 ${uncached} tokens / 1M tokens * ${billingPrice(inputPrice)}`]
    if (cached > 0 && cacheRatio !== undefined) terms.push(`缓存 ${cached} tokens / 1M tokens * ${billingPrice(inputPrice * cacheRatio)}`)
    terms.push(`输出 ${row.completion_tokens} tokens / 1M tokens * ${billingPrice(outputPrice)}`)
    lines.push(`(${terms.join(' + ')}) * ${groupLabel} ${Number(groupRatio.toFixed(4))} = ${money(row.quota)}`)
  }
  lines.push('仅供参考，以实际扣费为准')
  return lines.join('\n')
}
function billingMode(row: ReadonlyLog) {
  const value = first(row, 'admin_info')
  return value && typeof value === 'object' && Boolean((value as LogExtra).local_count_tokens) ? '本地计费' : '上游返回'
}
async function load() { try { await Promise.all([filters.loadInstances(), prefs.load()]) } catch { /* content request reports its own error */ } await Promise.allSettled([state.reload(), statState.reload(), countState.reload()]) }
onMounted(() => {
  // Deep links from the billing drawer carry username plus a month or an
  // explicit from/to period matching the billing generation range.
  const qUser = typeof route.query.username === 'string' ? route.query.username : ''
  const qMonth = typeof route.query.month === 'string' ? route.query.month : ''
  const qFrom = typeof route.query.from === 'string' ? route.query.from : ''
  const qTo = typeof route.query.to === 'string' ? route.query.to : ''
  if (qUser) username.value = qUser
  const cap = new Date(Date.now() + 60 * 60 * 1000)
  if (qFrom && qTo && !Number.isNaN(Date.parse(qFrom)) && !Number.isNaN(Date.parse(qTo))) {
    const start = new Date(qFrom); const end = new Date(qTo)
    if (end > start) timeRange.value = [start, end < cap ? end : cap]
  } else if (/^\d{4}-\d{2}$/.test(qMonth)) {
    const start = new Date(`${qMonth}-01T00:00:00`)
    const nextMonth = new Date(start); nextMonth.setMonth(start.getMonth() + 1)
    timeRange.value = [start, nextMonth < cap ? nextMonth : cap]
  }
  void load()
})
watch(() => filters.site_id, (site, previous) => {
  if (site && site !== previous) {
    offset.value = 0
    void Promise.allSettled([state.reload(), statState.reload(), countState.reload()])
  }
})
</script>
<template><AppShell title="使用日志">
  <div class="summary-bar">
    <span class="summary-chip quota">消耗额度：{{money(logSummary?.quota || 0)}}</span>
    <span class="summary-chip rpm">RPM：{{formatNumber(logSummary?.rpm || 0)}}</span>
    <span class="summary-chip tpm">TPM：{{formatNumber(logSummary?.tpm || 0)}}</span>
  </div>
  <el-alert v-if="statState.error.value" title="统计数据加载失败，日志列表仍可正常查询" type="warning" show-icon :closable="false"><el-button link type="primary" @click="statState.reload">重新加载统计</el-button></el-alert>
  <div class="filter-bar">
    <div class="filter-fields">
      <el-date-picker v-model="timeRange" type="datetimerange" range-separator="~" start-placeholder="开始时间" end-placeholder="结束时间" :shortcuts="timeRangeShortcuts" unlink-panels :clearable="false" class="time-range"/>
      <el-input v-model="modelName" clearable prefix-icon="Search" placeholder="模型名称"/>
      <el-select v-model="logType" clearable placeholder="全部类型"><el-option label="消费" :value="2"/><el-option label="错误" :value="5"/><el-option label="充值" :value="1"/><el-option label="管理" :value="3"/><el-option label="系统" :value="4"/><el-option label="退款" :value="6"/></el-select>
      <el-input v-model="tokenName" clearable prefix-icon="Search" placeholder="令牌名称"/>
      <el-input v-model="username" clearable prefix-icon="Search" placeholder="用户名称"/>
      <el-input v-model="channelID" clearable prefix-icon="Search" placeholder="渠道 ID"/>
      <el-input v-model="requestID" clearable prefix-icon="Search" placeholder="Request ID"/>
    </div>
    <div class="filter-actions">
      <el-button @click="reset">重置</el-button>
      <el-button type="primary" :loading="state.loading.value" @click="search">查询</el-button>
    </div>
  </div>
  <el-alert v-if="state.error.value" :title="state.error.value" type="error" show-icon :closable="false"><el-button link type="primary" @click="state.reload">重新加载</el-button></el-alert>
  <el-alert v-else-if="!state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。配置后可直接在此查询。" type="info" show-icon :closable="false"/>
  <div v-loading="state.loading.value" class="table-panel">
    <el-table :data="state.data.value?.items||[]" height="100%" empty-text="暂无日志数据" row-key="id">
      <el-table-column type="expand" width="36"><template #default="s"><div class="expanded">
        <div class="detail-row"><span>Request ID</span><strong>{{s.row.request_id||'—'}}</strong></div>
        <div class="detail-row"><span>渠道 ID</span><strong>{{s.row.channel_id}}</strong></div>
        <div v-if="isConsume(s.row) && cacheReadTokens(s.row)>0" class="detail-row"><span>缓存 Tokens</span><strong>{{formatNumber(cacheReadTokens(s.row))}}</strong></div>
        <template v-if="isConsume(s.row)">
          <div class="detail-row"><span>日志详情</span><strong>{{logDetail(s.row)}}</strong></div>
          <div class="detail-row"><span>计费过程</span><strong class="detail-multiline">{{billingProcess(s.row)}}</strong></div>
        </template>
        <div v-else class="detail-row"><span>日志详情</span><strong>{{s.row.content_summary||'—'}}</strong></div>
        <div class="detail-row"><span>请求路径</span><strong>{{requestPath(s.row)}}</strong></div>
        <template v-if="isConsume(s.row)">
          <div class="detail-row"><span>流状态</span><strong>{{streamStatus(s.row)}}</strong></div>
          <div class="detail-row"><span>参数覆盖</span><strong>{{overrideText(s.row)||'无'}}</strong></div>
        </template>
        <div class="detail-row"><span>请求转换</span><strong>{{conversion(s.row)}}</strong></div>
        <div class="detail-row"><span>计费模式</span><strong>{{billingMode(s.row)}}</strong></div>
      </div></template></el-table-column>
      <el-table-column label="时间" width="172"><template #default="s">{{new Date(s.row.created_at).toLocaleString()}}</template></el-table-column><el-table-column label="渠道 ID" width="88" prop="channel_id"/><el-table-column label="用户" width="220"><template #default="s"><span class="user-cell"><i>{{initial(s.row.username)}}</i>{{s.row.username||`用户 ${s.row.user_id}`}}</span></template></el-table-column><el-table-column label="令牌" width="180" show-overflow-tooltip><template #default="s"><span class="pill pill-token">{{s.row.token_name||'—'}}</span></template></el-table-column><el-table-column label="类型" width="74"><template #default="s"><el-tag :type="typeMeta(s.row.type)[1]" round size="small">{{typeMeta(s.row.type)[0]}}</el-tag></template></el-table-column><el-table-column label="模型" width="190" show-overflow-tooltip><template #default="s"><span class="pill pill-model">{{s.row.model_name||'—'}}</span></template></el-table-column>
      <el-table-column label="用时/首字" width="150"><template #default="s"><div class="timing-cell"><span class="metric-pill">{{s.row.use_time}} s</span><span v-if="firstResponse(s.row)!==undefined" class="metric-pill warm">{{firstResponse(s.row)?.toFixed(1)}} s</span><span class="stream-label" :class="{ unknown: streamFlag(s.row)===undefined }">{{streamLabel(s.row)}}</span></div></template></el-table-column><el-table-column label="输入" width="128"><template #default="s"><div>{{formatNumber(s.row.prompt_tokens)}}</div><small v-if="cacheReadTokens(s.row)">缓存读 {{formatNumber(cacheReadTokens(s.row))}}</small><small v-if="cacheWriteTokens(s.row)">缓存写 {{formatNumber(cacheWriteTokens(s.row))}}</small></template></el-table-column><el-table-column label="输出" width="84"><template #default="s">{{formatNumber(s.row.completion_tokens)}}</template></el-table-column><el-table-column label="花费" width="112"><template #default="s">{{money(s.row.quota)}}</template></el-table-column><el-table-column label="详情" width="280"><template #default="s"><div class="billing-summary" v-for="part in billingSummary(s.row).split('；')" :key="part">{{part}}</div></template></el-table-column>
    </el-table>
  </div>
  <div class="pager">
    <span style="white-space:nowrap">显示第 {{rangeStart}} 条 - 第 {{rangeEnd}} 条<span v-if="countState.loading.value">，总数统计中…</span><span v-else-if="countState.error.value">，总数暂不可用</span><span v-else>，共 {{formatNumber(effectiveTotal)}} 条</span></span>
    <div class="pager-controls" style="flex:none"><span style="white-space:nowrap">总页数：{{countState.loading.value || countState.error.value ? '—' : formatNumber(totalPages)}}</span><el-pagination background :current-page="currentPage" :page-size="limit" :total="effectiveTotal" layout="prev, pager, next" @current-change="changePage"/><el-select :model-value="limit" style="width:142px;flex:none" @change="changePageSize"><el-option v-for="size in [20,50,100]" :key="size" :label="`每页条数：${size}`" :value="size"/></el-select></div>
  </div>
</AppShell></template>
<style scoped>.summary-bar{display:flex;align-items:center;gap:8px;padding:10px 12px;margin-bottom:10px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.summary-chip{padding:5px 11px;border-radius:8px;font-size:13px;white-space:nowrap}.summary-chip.quota{background:#e7f0ff;color:#2266bd}.summary-chip.rpm{background:#ffe6ef;color:#b51f56}.summary-chip.tpm{background:#f2f3f5;color:#303133}.filter-bar{padding:12px 16px;margin-bottom:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.filter-fields{display:grid;grid-template-columns:minmax(360px,1.8fr) repeat(6,minmax(130px,1fr));align-items:center;gap:8px}.time-range{width:100%}.filter-actions{display:flex;justify-content:flex-end;align-items:center;gap:8px;margin-top:10px;padding-top:10px;border-top:1px solid var(--el-border-color-lighter)}.table-panel{height:calc(100vh - 350px);min-height:380px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden}.pager{display:flex;justify-content:space-between;align-items:center;gap:18px;min-height:48px;padding:8px 12px;color:#606266;font-size:13px;background:#fff;border:1px solid var(--el-border-color-light);border-top:0;border-radius:0 0 8px 8px;box-sizing:border-box}.pager-controls{display:flex;align-items:center;gap:14px;min-width:0}.pager :deep(.el-pagination){flex-wrap:nowrap}.expanded{margin:0 12px 0 18px;padding:10px 18px;background:#f5f7fa}.detail-row{display:grid;grid-template-columns:100px minmax(0,1fr);gap:16px;min-height:28px;align-items:start;color:#303133}.detail-row>span{color:#73767a;text-align:right}.detail-row strong{font-weight:400;word-break:break-word}.detail-multiline{white-space:pre-line;line-height:1.55}.billing-summary{line-height:1.45}.pill{display:inline-block;max-width:100%;padding:3px 9px;border-radius:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.pill-token{background:#edf0f2;color:#303133}.pill-model{background:#f1e8fb;color:#7134a3}.user-cell{display:flex;align-items:center;gap:7px}.user-cell i{display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:50%;background:#43c6b9;color:#fff;font-style:normal}.timing-cell{display:flex;align-items:center;gap:4px;white-space:nowrap}.metric-pill{display:inline-block;padding:2px 7px;border-radius:10px;background:#dff2dd;color:#237b34;white-space:nowrap}.metric-pill.warm{background:#fff0d5;color:#a55d00}.stream-label{display:inline-block;padding:2px 7px;border-radius:10px;background:#dce9ff;color:#2468c9;font-size:12px;line-height:18px;white-space:nowrap}.stream-label.unknown{background:#f0f2f5;color:#909399}.table-panel small{display:block;color:#909399;white-space:nowrap}@media(max-width:1200px){.filter-fields{grid-template-columns:repeat(2,minmax(180px,1fr))}.time-range{grid-column:span 2}.table-panel{height:calc(100vh - 405px)}.pager{align-items:flex-start;flex-direction:column;height:auto}.pager-controls{width:100%;justify-content:flex-end}}</style>
