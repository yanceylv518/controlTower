<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import type { ReadonlyLog } from '@ct/shared'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { formatNumber, formatQuota } from '../utils/format'

type LogExtra = Record<string, unknown>
const auth = useAuthStore(), filters = useFiltersStore(), prefs = usePrefsStore()
const userIDs = ref(''), tokenName = ref(''), modelName = ref(''), requestID = ref('')
const channelID = ref<number>(), logType = ref<number>(), offset = ref(0), limit = 50
const timeRange = ref<[Date, Date]>([new Date(new Date().setHours(0, 0, 0, 0)), new Date()])
const params = computed(() => ({ site: filters.site_id, user_ids: auth.user?.role === 'admin' ? userIDs.value : undefined, start_time: timeRange.value[0].toISOString(), end_time: timeRange.value[1].toISOString(), token_name: tokenName.value, model_name: modelName.value, request_id: requestID.value, channel_id: channelID.value, log_type: logType.value, limit, offset: offset.value }))
const state = useAsyncData(() => passthrough.logs(params.value))
const extraCache = new WeakMap<object, LogExtra>()
function extra(row: ReadonlyLog): LogExtra { const cached = extraCache.get(row); if (cached) return cached; let value: LogExtra = {}; try { value = row.other ? JSON.parse(row.other) as LogExtra : {} } catch { value = {} }; extraCache.set(row, value); return value }
function first(row: ReadonlyLog, ...keys: string[]) { const data = extra(row); for (const key of keys) { const value = data[key]; if (value !== undefined && value !== null && value !== '') return value } return undefined }
function numberValue(row: ReadonlyLog, ...keys: string[]) { const value = Number(first(row, ...keys)); return Number.isFinite(value) ? value : undefined }
function textValue(row: ReadonlyLog, ...keys: string[]) { const value = first(row, ...keys); if (value === undefined) return ''; return typeof value === 'string' ? value : JSON.stringify(value) }
const search = () => { offset.value = 0; void state.reload() }
const page = (next: number) => { offset.value = Math.max(0, next); void state.reload() }
const reset = () => { userIDs.value = ''; tokenName.value = ''; modelName.value = ''; requestID.value = ''; channelID.value = undefined; logType.value = undefined; timeRange.value = [new Date(new Date().setHours(0, 0, 0, 0)), new Date()]; search() }
const typeMeta = (type: number) => ({ 1: ['充值', 'warning'], 2: ['消费', 'success'], 3: ['管理', 'info'], 4: ['系统', 'info'], 5: ['错误', 'danger'], 6: ['退款', 'warning'], 7: ['登录', 'info'] }[type] || [`类型 ${type}`, 'info']) as [string, 'success' | 'warning' | 'info' | 'danger']
const money = (quota: number) => formatQuota(quota, prefs.quotaPerUnit, prefs.currencySymbol)
const initial = (name: string) => name?.trim().slice(0, 1) || '用'
const requestPath = (row: ReadonlyLog) => textValue(row, 'request_path') || '—'
const cacheTokens = (row: ReadonlyLog) => numberValue(row, 'cache_tokens') || 0
const firstResponse = (row: ReadonlyLog) => numberValue(row, 'frt', 'first_response_time')
const streamStatus = (row: ReadonlyLog) => textValue(row, 'stream_status') || (row.is_stream ? '流式' : '非流式')
const conversion = (row: ReadonlyLog) => textValue(row, 'request_conversion_chain', 'final_request_format') || '原生格式'
const billingMode = (row: ReadonlyLog) => textValue(row, 'billing_mode', 'billing_source', 'billing_preference') || '上游返回'
const overrideText = (row: ReadonlyLog) => textValue(row, 'po', 'parameter_overrides')
const channelInfo = (row: ReadonlyLog) => `${row.channel_id}${textValue(row, 'channel_name') ? ` - ${textValue(row, 'channel_name')}` : ''}`
function billingLines(row: ReadonlyLog) { const values: string[] = []; for (const [label, key] of [['模型价格', 'model_price'], ['模型倍率', 'model_ratio'], ['输出倍率', 'completion_ratio'], ['缓存倍率', 'cache_ratio'], ['分组倍率', 'group_ratio']] as const) { const value = numberValue(row, key); if (value !== undefined) values.push(`${label}：${value}${key === 'model_price' ? '' : 'x'}`) }; values.push(`输入 ${formatNumber(row.prompt_tokens)} tokens，缓存 ${formatNumber(cacheTokens(row))} tokens，输出 ${formatNumber(row.completion_tokens)} tokens`); values.push(`最终花费：${money(row.quota)}`); return values }
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
    <el-date-picker v-model="timeRange" type="datetimerange" start-placeholder="开始时间" end-placeholder="结束时间" range-separator="至" class="filter-span-2"/>
    <el-input v-model="tokenName" clearable placeholder="令牌名称"/><el-input v-model="modelName" clearable placeholder="模型名称"/>
    <el-input v-model="userIDs" :disabled="auth.user?.role!=='admin'" clearable placeholder="用户 ID，多个用逗号分隔"/>
    <el-input v-model="requestID" clearable placeholder="Request ID"/><el-input v-model.number="channelID" clearable placeholder="渠道 ID"/>
    <el-select v-model="logType" clearable placeholder="全部类型"><el-option label="消费" :value="2"/><el-option label="充值" :value="1"/><el-option label="管理" :value="4"/><el-option label="系统" :value="5"/></el-select>
    <div class="filter-actions"><el-button type="primary" @click="search">查询</el-button><el-button @click="reset">重置</el-button><el-button @click="state.reload">刷新</el-button></div>
  </div>
  <el-alert title="最多查询 31 天，每页最多 100 条；内容摘要已脱敏。受限账号只能看到被授权用户。" type="info" :closable="false"/>
  <el-alert v-if="state.error.value" :title="state.error.value" type="error" show-icon :closable="false"><el-button link type="primary" @click="state.reload">重新加载</el-button></el-alert>
  <el-alert v-else-if="!state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。配置后可直接在此查询。" type="info" show-icon :closable="false"/>
  <div v-loading="state.loading.value" class="table-panel">
    <el-table :data="state.data.value?.items||[]" height="100%" empty-text="暂无日志数据"><el-table-column prop="created_at" label="时间" width="180"><template #default="s">{{new Date(s.row.created_at).toLocaleString()}}</template></el-table-column><el-table-column prop="channel_id" label="渠道" width="80"/><el-table-column prop="username" label="用户" min-width="110"/><el-table-column prop="token_name" label="令牌" min-width="130" show-overflow-tooltip/><el-table-column prop="type" label="类型" width="70"/><el-table-column prop="model_name" label="模型" min-width="160" show-overflow-tooltip/><el-table-column prop="use_time" label="用时" width="80"><template #default="s">{{s.row.use_time}} s</template></el-table-column><el-table-column prop="prompt_tokens" label="输入" width="90"/><el-table-column prop="completion_tokens" label="输出" width="90"/><el-table-column prop="quota" label="额度" width="100"/><el-table-column prop="request_id" label="Request ID" min-width="160" show-overflow-tooltip/><el-table-column prop="content_summary" label="详情" min-width="220" show-overflow-tooltip/></el-table>
  </div>
  <div class="pager"><el-button :disabled="offset===0" @click="page(offset-limit)">上一页</el-button><span>第 {{Math.floor(offset/limit)+1}} 页</span><el-button :disabled="(state.data.value?.items.length||0)<limit" @click="page(offset+limit)">下一页</el-button></div>
</AppShell></template>
<style scoped>.filter-bar{display:grid;grid-template-columns:repeat(4,minmax(160px,1fr));align-items:center;gap:8px;padding:12px 16px;margin-bottom:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.filter-span-2{width:100%}.separator{display:none}.filter-actions{display:flex;justify-content:flex-end;grid-column:4}.table-panel{height:calc(100vh - 350px);min-height:360px;margin-top:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden}.pager{display:flex;justify-content:flex-end;align-items:center;gap:12px;margin-top:16px}</style>
