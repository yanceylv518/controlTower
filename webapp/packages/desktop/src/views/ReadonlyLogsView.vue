<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import type { ReadonlyLog } from '@ct/shared'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { formatNumber } from '../utils/format'

type LogExtra = Record<string, unknown>
const auth = useAuthStore(), filters = useFiltersStore(), prefs = usePrefsStore()
const username = ref(''), tokenName = ref(''), modelName = ref(''), requestID = ref('')
const logType = ref<number>(), offset = ref(0), limit = 50
const timeRange = ref<[Date, Date]>([new Date(new Date().setHours(0, 0, 0, 0)), new Date()])
const params = computed(() => ({ site: filters.site_id, username: auth.user?.role === 'admin' ? username.value : undefined, start_time: timeRange.value[0].toISOString(), end_time: timeRange.value[1].toISOString(), token_name: tokenName.value, model_name: modelName.value, request_id: requestID.value, log_type: logType.value, limit, offset: offset.value }))
const state = useAsyncData(() => passthrough.logs(params.value))
const extraCache = new WeakMap<object, LogExtra>()
function extra(row: ReadonlyLog): LogExtra { const cached = extraCache.get(row); if (cached) return cached; let value: LogExtra = {}; try { value = row.other ? JSON.parse(row.other) as LogExtra : {} } catch { value = {} }; extraCache.set(row, value); return value }
function first(row: ReadonlyLog, ...keys: string[]) { const data = extra(row); for (const key of keys) { const value = data[key]; if (value !== undefined && value !== null && value !== '') return value } return undefined }
function numberValue(row: ReadonlyLog, ...keys: string[]) { const value = Number(first(row, ...keys)); return Number.isFinite(value) ? value : undefined }
function textValue(row: ReadonlyLog, ...keys: string[]) { const value = first(row, ...keys); if (value === undefined) return ''; return typeof value === 'string' ? value : JSON.stringify(value) }
const search = () => { offset.value = 0; void state.reload() }
const page = (next: number) => { offset.value = Math.max(0, next); void state.reload() }
const reset = () => { username.value = ''; tokenName.value = ''; modelName.value = ''; requestID.value = ''; logType.value = undefined; timeRange.value = [new Date(new Date().setHours(0, 0, 0, 0)), new Date()]; search() }
const typeMeta = (type: number) => ({ 1: ['充值', 'warning'], 2: ['消费', 'success'], 3: ['管理', 'info'], 4: ['系统', 'info'], 5: ['错误', 'danger'], 6: ['退款', 'warning'], 7: ['登录', 'info'] }[type] || [`类型 ${type}`, 'info']) as [string, 'success' | 'warning' | 'info' | 'danger']
const money = (quota: number) => {
  const amount = quota / (prefs.quotaPerUnit || 500000)
  return `${prefs.currencySymbol}${amount >= 1 ? amount.toFixed(2) : amount.toFixed(6)}`
}
const billingPrice = (value: number) => `${prefs.currencySymbol}${value.toLocaleString('zh-CN', { maximumFractionDigits: 6 })}`
const initial = (name: string) => name?.trim().slice(0, 1) || '用'
const requestPath = (row: ReadonlyLog) => textValue(row, 'request_path') || '—'
const cacheReadTokens = (row: ReadonlyLog) => numberValue(row, 'cache_tokens') || 0
const cacheWrite5mTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_5m') || 0
const cacheWrite1hTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_1h') || 0
const cacheWriteTokens = (row: ReadonlyLog) => {
  const split = cacheWrite5mTokens(row) + cacheWrite1hTokens(row)
  return split > 0 ? split : numberValue(row, 'cache_creation_tokens') || 0
}
const hasCacheTokens = (row: ReadonlyLog) => cacheReadTokens(row) > 0 || cacheWriteTokens(row) > 0
const firstResponse = (row: ReadonlyLog) => numberValue(row, 'frt', 'first_response_time')
const isConsume = (row: ReadonlyLog) => row.type === 2
function streamStatus(row: ReadonlyLog) {
  const value = first(row, 'stream_status')
  if (value && typeof value === 'object') {
    const status = String((value as LogExtra).status || '')
    const reason = String((value as LogExtra).end_reason || '')
    return status === 'ok' ? `✓ 正常${reason ? ` (${reason})` : ''}` : [status || '异常', reason].filter(Boolean).join('：')
  }
  return typeof value === 'string' && value ? value : (row.is_stream ? '流式' : '非流式')
}
const conversion = (row: ReadonlyLog) => textValue(row, 'request_conversion_chain', 'final_request_format') || '原生格式'
const billingMode = (row: ReadonlyLog) => textValue(row, 'billing_mode', 'billing_source', 'billing_preference') || '上游返回'
function overrideText(row: ReadonlyLog) {
  const value = first(row, 'po', 'parameter_overrides')
  if (Array.isArray(value)) return value.length ? `${value.length} 项操作` : ''
  if (value && typeof value === 'object') return Object.keys(value).length ? `${Object.keys(value).length} 项操作` : ''
  return value === undefined || value === null ? '' : String(value)
}
const channelInfo = (row: ReadonlyLog) => `${row.channel_id}${textValue(row, 'channel_name') ? ` - ${textValue(row, 'channel_name')}` : ''}`
function effectiveGroupRatio(row: ReadonlyLog) {
  const userRatio = numberValue(row, 'user_group_ratio')
  return userRatio !== undefined && userRatio !== -1 ? userRatio : numberValue(row, 'group_ratio')
}
function billingSummary(row: ReadonlyLog) {
  if (!isConsume(row)) return row.content_summary || '—'
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row)
  const parts: string[] = []
  if (groupRatio !== undefined) parts.push(`分组倍率 ${groupRatio}x`)
  if (modelPrice !== undefined) {
    parts.push(`按次 ${billingPrice(modelPrice)}`)
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
function billingLines(row: ReadonlyLog) {
  const values: string[] = []
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row)
  if (modelPrice !== undefined) {
    values.push(`按次价格：${billingPrice(modelPrice)}`)
  } else if (modelRatio !== undefined) {
    const inputPrice = modelRatio * 2
    values.push(`输入价格：${billingPrice(inputPrice)} / 1M tokens`)
    if (completionRatio !== undefined) values.push(`输出价格：${billingPrice(inputPrice * completionRatio)} / 1M tokens`)
    if (cacheReadTokens(row) > 0 && cacheRatio !== undefined && cacheRatio !== 1) values.push(`缓存读取价格：${billingPrice(inputPrice * cacheRatio)} / 1M tokens`)
    const cacheWriteRatio = numberValue(row, 'cache_creation_ratio')
    const cacheWrite5mRatio = numberValue(row, 'cache_creation_ratio_5m')
    const cacheWrite1hRatio = numberValue(row, 'cache_creation_ratio_1h')
    if (cacheWriteTokens(row) > 0 && cacheWriteRatio !== undefined && cacheWriteRatio !== 1) values.push(`缓存写入价格：${billingPrice(inputPrice * cacheWriteRatio)} / 1M tokens`)
    if (cacheWrite5mTokens(row) > 0 && cacheWrite5mRatio !== undefined && cacheWrite5mRatio !== 0) values.push(`缓存写入 5m 价格：${billingPrice(inputPrice * cacheWrite5mRatio)} / 1M tokens`)
    if (cacheWrite1hTokens(row) > 0 && cacheWrite1hRatio !== undefined && cacheWrite1hRatio !== 0) values.push(`缓存写入 1h 价格：${billingPrice(inputPrice * cacheWrite1hRatio)} / 1M tokens`)
  }
  if (groupRatio !== undefined) values.push(`分组倍率：${groupRatio}x`)
  values.push(`输入 Tokens：${formatNumber(row.prompt_tokens)}`)
  values.push(`输出 Tokens：${formatNumber(row.completion_tokens)}`)
  if (cacheReadTokens(row) > 0) values.push(`缓存读取：${formatNumber(cacheReadTokens(row))}`)
  if (cacheWriteTokens(row) > 0) values.push(`缓存写入：${formatNumber(cacheWriteTokens(row))}`)
  values.push(`最终花费：${money(row.quota)}`)
  return values
}
async function load() { try { await Promise.all([filters.loadInstances(), prefs.load()]) } catch { /* content request reports its own error */ } await state.reload() }
onMounted(() => { void load() })
watch(() => filters.site_id, (site, previous) => {
  if (site && site !== previous) {
    offset.value = 0
    void state.reload()
  }
})
</script>
<template><AppShell title="使用日志">
  <div class="filter-bar">
    <div class="filter-fields">
      <el-date-picker v-model="timeRange" type="datetimerange" range-separator="~" start-placeholder="开始时间" end-placeholder="结束时间" :clearable="false" class="time-range"/>
      <el-input v-model="modelName" clearable prefix-icon="Search" placeholder="模型名称"/>
      <el-select v-model="logType" clearable placeholder="全部类型"><el-option label="消费" :value="2"/><el-option label="错误" :value="5"/><el-option label="充值" :value="1"/><el-option label="管理" :value="3"/><el-option label="系统" :value="4"/><el-option label="退款" :value="6"/></el-select>
      <el-input v-model="tokenName" clearable prefix-icon="Search" placeholder="令牌名称"/>
      <el-input v-if="auth.user?.role==='admin'" v-model="username" clearable prefix-icon="Search" placeholder="用户名称"/>
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
        <template v-if="isConsume(s.row)"><div class="detail-row"><span>渠道信息</span><strong>{{channelInfo(s.row)}}</strong></div></template><div class="detail-row"><span>Request ID</span><strong>{{s.row.request_id||'—'}}</strong></div><template v-if="isConsume(s.row)"><div v-if="hasCacheTokens(s.row)" class="detail-row"><span>缓存 Tokens</span><strong><span v-if="cacheReadTokens(s.row)">读取 {{formatNumber(cacheReadTokens(s.row))}}</span><span v-if="cacheWriteTokens(s.row)">{{cacheReadTokens(s.row) ? '，' : ''}}写入 {{formatNumber(cacheWriteTokens(s.row))}}</span></strong></div><div class="detail-row"><span>日志详情</span><strong>{{s.row.content_summary||'—'}}</strong></div>
        <div class="detail-row detail-multiline"><span>计费过程</span><div><div v-for="line in billingLines(s.row)" :key="line">{{line}}</div></div></div></template><div class="detail-row"><span>请求路径</span><strong>{{requestPath(s.row)}}</strong></div><template v-if="isConsume(s.row)"><div class="detail-row"><span>流状态</span><strong>{{streamStatus(s.row)}}</strong></div><div class="detail-row"><span>参数覆盖</span><strong>{{overrideText(s.row)||'无'}}</strong></div></template><div class="detail-row"><span>请求转换</span><strong>{{conversion(s.row)}}</strong></div><div class="detail-row"><span>计费模式</span><strong>{{billingMode(s.row)}}</strong></div>
      </div></template></el-table-column>
      <el-table-column label="时间" width="172"><template #default="s">{{new Date(s.row.created_at).toLocaleString()}}</template></el-table-column><el-table-column label="用户" min-width="120"><template #default="s"><span class="user-cell"><i>{{initial(s.row.username)}}</i>{{s.row.username||`用户 ${s.row.user_id}`}}</span></template></el-table-column><el-table-column label="令牌" min-width="130" show-overflow-tooltip><template #default="s"><span class="pill pill-token">{{s.row.token_name||'—'}}</span></template></el-table-column><el-table-column label="类型" width="74"><template #default="s"><el-tag :type="typeMeta(s.row.type)[1]" round size="small">{{typeMeta(s.row.type)[0]}}</el-tag></template></el-table-column><el-table-column label="模型" min-width="155" show-overflow-tooltip><template #default="s"><span class="pill pill-model">{{s.row.model_name||'—'}}</span></template></el-table-column>
      <el-table-column label="用时/首字" width="132"><template #default="s"><span class="metric-pill">{{s.row.use_time}} s</span><span v-if="firstResponse(s.row)!==undefined" class="metric-pill warm">{{firstResponse(s.row)?.toFixed(1)}} s</span><span class="stream-pill">{{s.row.is_stream?'流':'非流'}}</span></template></el-table-column><el-table-column label="输入" width="128"><template #default="s"><div>{{formatNumber(s.row.prompt_tokens)}}</div><small v-if="cacheReadTokens(s.row)">缓存读 {{formatNumber(cacheReadTokens(s.row))}}</small><small v-if="cacheWriteTokens(s.row)">缓存写 {{formatNumber(cacheWriteTokens(s.row))}}</small></template></el-table-column><el-table-column label="输出" width="84"><template #default="s">{{formatNumber(s.row.completion_tokens)}}</template></el-table-column><el-table-column label="花费" width="112"><template #default="s">{{money(s.row.quota)}}</template></el-table-column><el-table-column label="详情" min-width="245"><template #default="s"><div class="billing-summary" v-for="part in billingSummary(s.row).split('；')" :key="part">{{part}}</div></template></el-table-column>
    </el-table>
  </div>
  <div class="pager"><el-button :disabled="offset===0" @click="page(offset-limit)">上一页</el-button><span>第 {{Math.floor(offset/limit)+1}} 页</span><el-button :disabled="(state.data.value?.items.length||0)<limit" @click="page(offset+limit)">下一页</el-button></div>
</AppShell></template>
<style scoped>.filter-bar{padding:12px 16px;margin-bottom:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.filter-fields{display:grid;grid-template-columns:minmax(360px,1.8fr) repeat(5,minmax(140px,1fr));align-items:center;gap:8px}.time-range{width:100%}.filter-actions{display:flex;justify-content:flex-end;align-items:center;gap:8px;margin-top:10px;padding-top:10px;border-top:1px solid var(--el-border-color-lighter)}.table-panel{height:calc(100vh - 245px);min-height:430px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden}.pager{display:flex;justify-content:flex-end;align-items:center;gap:12px;margin-top:12px}.expanded{margin:0 12px 0 36px;padding:10px 18px;background:#f5f7fa;border-radius:4px}.detail-row{display:grid;grid-template-columns:100px minmax(0,1fr);gap:16px;min-height:28px;align-items:start;color:#303133}.detail-row>span{color:#73767a;text-align:right}.detail-row strong{font-weight:400;word-break:break-all}.detail-multiline>div{line-height:1.7}.billing-summary{line-height:1.45}.pill{display:inline-block;max-width:100%;padding:3px 9px;border-radius:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.pill-token{background:#edf0f2;color:#303133}.pill-model{background:#f1e8fb;color:#7134a3}.user-cell{display:flex;align-items:center;gap:7px}.user-cell i{display:inline-flex;align-items:center;justify-content:center;width:24px;height:24px;border-radius:50%;background:#43c6b9;color:#fff;font-style:normal}.metric-pill,.stream-pill{display:inline-block;margin-right:4px;padding:2px 7px;border-radius:10px;background:#dff2dd;color:#237b34}.metric-pill.warm{background:#fff0d5;color:#a55d00}.stream-pill{background:#dfeaff;color:#2064b4}.table-panel small{display:block;color:#909399;white-space:nowrap}@media(max-width:1200px){.filter-fields{grid-template-columns:repeat(2,minmax(180px,1fr))}.time-range{grid-column:span 2}}</style>
