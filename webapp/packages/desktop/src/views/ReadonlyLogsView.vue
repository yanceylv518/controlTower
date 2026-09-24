<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ArrowDown, ArrowUp, Hide, RefreshLeft, Search, View } from '@element-plus/icons-vue'
import { useRoute } from 'vue-router'
import type { ReadonlyLog } from '@ct/shared'
import { resolveModelProvider } from '../utils/modelProvider'
import ModelProviderIcon from '../components/ModelProviderIcon.vue'
import AppShell from '../components/AppShell.vue'
import FallbackRequestChain from '../components/FallbackRequestChain.vue'
import { attemptChannels, retryLookupUnknown, type ChainQuery } from '../utils/fallbackRequestChain'
import CompactDateTimeRangePicker from '../components/CompactDateTimeRangePicker.vue'
import MobileLogFilters, { type MobileLogFilterValues } from '../components/MobileLogFilters.vue'
import UserNamePicker, { type UserPickerOption } from '../components/UserNamePicker.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { useAppendPages } from '../composables/useAppendPages'
import ScrollLoadMore from '../components/ScrollLoadMore.vue'
import { decodeBillingExpression, dynamicBillingSummary, dynamicPriceFields, dynamicTierMatched, formatDynamicCondition, normalizeDynamicRequestRules, normalizeDynamicUsageFacts, parseDynamicTiers, type DynamicRequestRule, type DynamicTier, type DynamicUsageFact } from '../utils/billingDetails'
import { copyText as copyToClipboard } from '../utils/copyText'
import { logErrorCode } from '../utils/logErrorCode'
import { formatNumber } from '../utils/format'
import { getTokenColorClass, getUserAvatarFallback, getUserAvatarStyle, type UserAvatarStyle } from '../utils/identityColors'

type LogExtra = Record<string, unknown>
// New API 会把渠道亲和性命中快照写入管理员专属的 admin_info，字段缺失时保持兼容旧日志。
type ChannelAffinityInfo = {
  reason: string
  ruleName: string
  usingGroup: string
  selectedGroup: string
  keySource: string
  keyPath: string
  keyKey: string
  keyHint: string
  keyFingerprint: string
}
type LogColumnKey = 'channel' | 'user' | 'token' | 'model' | 'stream' | 'tokens' | 'quota' | 'timing' | 'details'
// new-api rc35 的耗时等级，neutral 仅用于缺失首字时间的流式记录。
type TimingVariant = 'success' | 'warning' | 'danger' | 'neutral'
type LogStatusTone = 'topup' | 'consume' | 'manage' | 'system' | 'error' | 'refund' | 'login' | 'unknown'
type RetryStepTone = 'plain' | 'failed' | 'success' | 'current'
type RetryChainStepView = { channel: string; position: number; current: boolean; tone: RetryStepTone }

// 列表只消费预计算的原始值和展示值，避免模板在每次响应式更新时重复解析同一条日志。
type LogRowView = {
  source: ReadonlyLog
  id: number
  // 由稳定的原始字段组成，避免刷新返回新对象时误使整行重新补丁。
  memoKey: string
  tone: string
  timeText: string
  timeShort: string
  timeFull: string
  statusLabel: string
  errorCode: string
  statusClass: string
  displayable: boolean
  timing: boolean
  channelID: number
  channelTone: string
  channelName: string
  hasChannel: boolean
  fallback: boolean
  retryUnknown: boolean
  fallbackChannels: string[]
  retryChain: string
  retrySteps: RetryChainStepView[]
  channelAffinity?: ChannelAffinityInfo
  username: string
  usernameCopy: string
  initial: string
  avatarStyle?: UserAvatarStyle
  tokenName: string
  hasToken: boolean
  group: string
  groupRatio?: number
  groupRatioText: string
  modelName: string
  modelTone: string
  modelProvider?: { name: string; src: string }
  groupTone: string
  modelMapping: string
  hasModel: boolean
  stream?: boolean
  streamLabel: string
  streamClass: string
  outputRateText: string
  hasTokens: boolean
  promptTokens: string
  completionTokens: string
  cacheReadTokens: number
  cacheWriteTokens: number
  cacheTotal: number
  cacheReadText: string
  cacheWriteText: string
  cacheTotalText: string
  quota: string
  firstResponseText: string
  firstResponseClass: string
  durationClass: string
  durationText: string
  summary: string
  summaryPreview: string
}

const logColumnOptions: readonly { key: LogColumnKey; label: string }[] = [
  { key: 'channel', label: '渠道' },
  { key: 'user', label: '用户' },
  { key: 'token', label: '令牌' },
  { key: 'model', label: '模型' },
  { key: 'stream', label: '流' },
  { key: 'tokens', label: 'Tokens' },
  { key: 'quota', label: '费用' },
  { key: 'timing', label: '耗时' },
  { key: 'details', label: '详情' },
]
const logColumnStorageKey = 'ct.readonly-logs.columns.v1'
const defaultLogColumnVisibility: Record<LogColumnKey, boolean> = {
  channel: true,
  user: true,
  token: true,
  model: true,
  stream: true,
  tokens: true,
  quota: true,
  timing: true,
  details: true,
}
const auth = useAuthStore()
const filters = useFiltersStore(), prefs = usePrefsStore()
const route = useRoute()
const username = ref(''), tokenName = ref(''), modelName = ref(''), group = ref(''), requestID = ref(''), upstreamRequestID = ref('')
// 选中下拉用户后按 ID 精确筛选；继续手动输入时由组件清除该精确条件。
const selectedUserID = ref<number | undefined>(undefined)
const channelID = ref('')
const statusCode = ref('')
const emptyOutput = ref(false)
const fallbackFinalOnly = ref(false)
const secondaryFiltersOpen = ref(false)
const secondaryFilterCount = computed(() => [group.value, tokenName.value, statusCode.value].filter(value => value.trim() !== '').length)
const requestScope = ref<{ request: string; user: string } | null>(null)
const scopedUserIDs = computed(() => requestScope.value?.request === requestID.value ? requestScope.value.user : undefined)
const logType = ref(0), offset = ref(0), limit = ref(100)
const sensitiveVisible = ref(true)
// 桌面和移动布局互斥挂载，避免隐藏的另一套 100 行节点继续参与更新和布局。
const mobileViewport = ref(typeof window !== 'undefined' && window.matchMedia('(max-width: 900px)').matches)
let viewportQuery: MediaQueryList | undefined
function syncViewport() {
  const next = viewportQuery?.matches ?? (typeof window !== 'undefined' && window.matchMedia('(max-width: 900px)').matches)
  if (next !== mobileViewport.value) {
    closeRequestChain()
    closePageSizeMenu()
  }
  mobileViewport.value = next
}
// rc35 的“查看”菜单只控制可选列，时间列始终保留以免失去日志排序上下文。
const columnVisibility = ref<Record<LogColumnKey, boolean>>({ ...defaultLogColumnVisibility })
function isColumnVisible(key: LogColumnKey) {
  return columnVisibility.value[key]
}
function restoreColumnVisibility() {
  try {
    const raw = localStorage.getItem(logColumnStorageKey)
    if (!raw) return
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
    const next = { ...defaultLogColumnVisibility }
    for (const option of logColumnOptions) {
      const value = (parsed as Record<string, unknown>)[option.key]
      if (typeof value === 'boolean') next[option.key] = value
    }
    columnVisibility.value = next
  } catch {
    // 偏好数据损坏时回到全量列，不影响日志查询。
  }
}
function toggleColumn(key: LogColumnKey, visible: boolean) {
  columnVisibility.value = { ...columnVisibility.value, [key]: visible }
  try {
    localStorage.setItem(logColumnStorageKey, JSON.stringify(columnVisibility.value))
  } catch {
    // 隐私模式或存储配额不足时仍保留本次会话的列状态。
  }
}
// 详情沿用 rc35 的独立弹窗，避免展开行改变表格高度和扫描节奏。
// 详情只读日志对象，不需要深度代理；避免打开弹窗时为整条日志建立额外响应式代理。
const detailRow = shallowRef<ReadonlyLog | null>(null)
const detailOpen = ref(false)
const detailCloseButton = ref<HTMLButtonElement | null>(null)
let detailReturnFocus: HTMLElement | null = null
// 亲和性弹窗只保存当前渠道的命中快照，避免打开标志时重新渲染整条日志详情。
type ChannelAffinityDialogTarget = { channelID: number; channelName: string; affinity: ChannelAffinityInfo }
const affinityTarget = shallowRef<ChannelAffinityDialogTarget | null>(null)
const affinityOpen = ref(false)
const affinityCloseButton = ref<HTMLButtonElement | null>(null)
const defaultTimeRange = (): [Date, Date] => {
  const now = new Date()
  // 默认从当前时刻往前两小时开始，截止时间保留当前时刻后一小时的余量。
  return [new Date(now.getTime() - 2 * 60 * 60 * 1000), new Date(now.getTime() + 60 * 60 * 1000)]
}
const timeRange = ref<[Date, Date]>(defaultTimeRange())
const parsedChannelID = computed(() => {
  const value = Number(channelID.value)
  return channelID.value.trim() !== '' && Number.isSafeInteger(value) && value >= 0 ? value : undefined
})
const parsedStatusCode = computed(() => {
  const value = statusCode.value.trim()
  return /^\d{3}$/.test(value) ? Number(value) : undefined
})
const params = computed(() => ({ site: filters.site_id, user_ids: selectedUserID.value !== undefined ? String(selectedUserID.value) : scopedUserIDs.value, username: selectedUserID.value !== undefined ? undefined : username.value, start_time: timeRange.value[0].toISOString(), end_time: timeRange.value[1].toISOString(), token_name: tokenName.value, model_name: modelName.value, group: group.value, request_id: requestID.value, upstream_request_id: upstreamRequestID.value, channel_id: parsedChannelID.value, status_code: parsedStatusCode.value, empty_output: auth.user?.role === 'admin' && emptyOutput.value ? 1 : undefined, fallback_final_only: auth.user?.role === 'admin' && fallbackFinalOnly.value ? 1 : undefined, log_type: logType.value || undefined, limit: limit.value, offset: offset.value }))
// 输入框为草稿；翻页与附属查询只消费点击查询时保存的快照。
const submitted = shallowRef({ ...params.value })
const queryRevision = ref(0)
const pageCursor = ref<string | undefined>(undefined)
const statParams = computed(() => {
  const { limit: _limit, offset: _offset, ...rest } = submitted.value
  return rest
})
const countParams = computed(() => {
  const { limit: _limit, offset: _offset, ...rest } = submitted.value
  return rest
})
// 响应携带查询批次与分页位置，失败时保留的数据不能冒充新条件的结果。
const state = useAsyncData(async (signal) => {
  const revision = queryRevision.value, pageOffset = offset.value, pageLimit = limit.value, requestCursor = pageCursor.value
  const feedQuery = { ...submitted.value, offset: pageOffset, limit: pageLimit }
  return { ...await passthrough.logs({ ...feedQuery, cursor: requestCursor }, signal), feedQuery, revision, pageOffset, pageLimit, requestCursor }
})
const statState = useAsyncData(async (signal) => {
  const revision = queryRevision.value
  return { ...await passthrough.logStat(statParams.value, signal), revision }
})
const countState = useAsyncData(async (signal) => {
  const revision = queryRevision.value
  return { ...await passthrough.logCount(countParams.value, signal), revision }
})
const listIsCurrent = computed(() => state.data.value?.revision === queryRevision.value)
const countIsCurrent = computed(() => listIsCurrent.value && countState.data.value?.revision === queryRevision.value && !countState.loading.value && !countState.error.value)
const tableScroll = ref<HTMLElement | null>(null)
const backgroundRefreshing = ref(false)
const mobileFeed = useAppendPages(() => state.data.value, () => state.loading.value || backgroundRefreshing.value || !listIsCurrent.value,
  (base, offset, signal) => passthrough.logs({ ...base.feedQuery, offset }, signal),
  (row: ReadonlyLog) => row.id, base => base.feedQuery.offset)
const mobileFilters = computed<MobileLogFilterValues>(() => ({ logType:logType.value, modelName:modelName.value, group:group.value, tokenName:tokenName.value, requestID:requestID.value, upstreamRequestID:upstreamRequestID.value, channelID:channelID.value, statusCode:statusCode.value, emptyOutput:auth.user?.role === 'admin' && emptyOutput.value, fallbackFinalOnly:fallbackFinalOnly.value }))
function applyMobileFilters(value: MobileLogFilterValues) {
  logType.value = value.logType
  modelName.value = value.modelName
  group.value = value.group
  tokenName.value = value.tokenName
  requestID.value = value.requestID
  upstreamRequestID.value = value.upstreamRequestID
  channelID.value = value.channelID
  statusCode.value = value.statusCode
  emptyOutput.value = value.emptyOutput
  fallbackFinalOnly.value = value.fallbackFinalOnly
  search()
}
function handleUserSelect(option: UserPickerOption | null) {
  selectedUserID.value = option?.id
}
// 列表、统计、总数共用一条浮动错误消息，不插入占位横幅或连续弹出多条。
let queryFailureNotified = false
function notifyQueryFailure(revision: number) {
  if (revision !== queryRevision.value || queryFailureNotified) return
  queryFailureNotified = true
  ElMessage.error('查询失败，请重试')
}
async function reloadSummary(target: typeof statState | typeof countState) {
  const revision = queryRevision.value
  await target.reload()
  if (target.error.value) notifyQueryFailure(revision)
}
function filterMobileUser(value: string, userID?: number) {
  if (!sensitiveVisible.value || backgroundRefreshing.value) return
  username.value = value
  selectedUserID.value = userID
  search()
}
// 首屏加载实例时会同步设置站点；在首次统一刷新完成前忽略 watcher，避免重复发起三组请求。
let initialLoadPending = true
const extraCache = new WeakMap<object, LogExtra>()

function extra(row: ReadonlyLog): LogExtra {
  const cached = extraCache.get(row)
  if (cached) return cached
  let value: LogExtra = {}
  try {
    const parsed: unknown = row.other ? JSON.parse(row.other) : {}
    value = parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as LogExtra : {}
  } catch {
    value = {}
  }
  extraCache.set(row, value)
  return value
}
function first(row: ReadonlyLog, ...keys: string[]) {
  const data = extra(row)
  for (const key of keys) {
    const value = data[key]
    if (value !== undefined && value !== null && value !== '') return value
  }
  return undefined
}
function numberValue(row: ReadonlyLog, ...keys: string[]) {
  const value = Number(first(row, ...keys))
  return Number.isFinite(value) ? value : undefined
}
function textValue(row: ReadonlyLog, ...keys: string[]) {
  const value = first(row, ...keys)
  if (value === undefined) return ''
  return typeof value === 'string' ? value : JSON.stringify(value)
}
// 首次进入页面和分页仍使用完整加载；已显示结果后的查询、重置统一走后台刷新。
function commitQuery() {
  if (channelID.value.trim() && (!/^\d+$/.test(channelID.value.trim()) || parsedChannelID.value === undefined)) {
    ElMessage.warning('渠道 ID 必须是非负整数')
    return false
  }
  if (statusCode.value.trim() && (parsedStatusCode.value === undefined || parsedStatusCode.value < 100 || parsedStatusCode.value > 599)) {
    ElMessage.warning('错误码必须是 100-599 的三位数字')
    return false
  }
  statState.cancel()
  countState.cancel()
  pageCursor.value = undefined
  submitted.value = { ...params.value }
  queryRevision.value += 1
  queryFailureNotified = false
  return true
}
const reloadAll = async () => {
  if (!commitQuery()) return
  const revision = queryRevision.value
  await state.reload()
  if (revision !== queryRevision.value) return
  if (state.lastRefreshError.value) notifyQueryFailure(revision)
  void reloadSummary(statState)
  void reloadSummary(countState)
}
// 查询和重置属于高频操作，保留现有列表并在后台更新，避免 v-loading 阻塞整张表。
const refreshSearch = async () => {
  if (backgroundRefreshing.value) return
  if (!commitQuery()) return
  const revision = queryRevision.value
  closeRequestChain()
  offset.value = 0
  backgroundRefreshing.value = true
  try {
    // 统计与总数独立加载；列表返回即可继续操作，后续查询由各自的取消机制接管。
    await state.refresh()
    if (revision !== queryRevision.value) return
    void reloadSummary(statState)
    void reloadSummary(countState)
    if (state.lastRefreshError.value) notifyQueryFailure(revision)
    else {
      state.error.value = ''
      if (tableScroll.value) tableScroll.value.scrollTop = 0
    }
  } finally {
    if (revision === queryRevision.value) backgroundRefreshing.value = false
  }
}
const search = () => {
  if (backgroundRefreshing.value) return
  void refreshSearch()
}
function handlePageSearchKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.defaultPrevented || event.isComposing || event.keyCode === 229 || event.repeat
    || event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return
  if (state.loading.value || backgroundRefreshing.value || detailOpen.value || affinityOpen.value || pageSizeOpen.value) return
  // 输入框已有查询绑定；按钮、分页及弹层继续使用各自的键盘操作。
  const target = event.target
  if (target instanceof Element && target.closest('input, textarea, select, button, a, [contenteditable]:not([contenteditable="false"]), [role="combobox"], [role="dialog"], [role="menu"], [role="listbox"]')) return
  const overlays = document.querySelectorAll('[role="dialog"], [role="listbox"], .el-popover, .compact-date-popover')
  if (Array.from(overlays).some(overlay => overlay.getClientRects().length > 0)) return
  event.preventDefault()
  search()
}
const fallbackTotal = computed(() => (state.data.value?.pageOffset || 0) + (state.data.value?.items.length || 0) + (state.data.value?.has_more ? 1 : 0))
// 总数不能低于当前已返回的列表行，防止聚合短暂失配时出现“有记录但总计为 0”。
const effectiveTotal = computed(() => {
  if (countIsCurrent.value && countState.data.value) {
    return Math.max(countState.data.value.total, fallbackTotal.value)
  }
  return fallbackTotal.value
})
// 新条件失败时保留的是旧页，页码必须与该页实际记录一致。
const currentPage = computed(() => !listIsCurrent.value && state.data.value
  ? Math.floor(state.data.value.pageOffset / state.data.value.pageLimit) + 1
  : Math.floor(offset.value / limit.value) + 1)
const totalPages = computed(() => Math.max(1, Math.ceil(effectiveTotal.value / limit.value)))
const pageSizeOptions = [10, 20, 30, 40, 50, 100] as const
type PageNumber = number | 'ellipsis'
// 页码按钮沿用 rc35 的紧凑省略规则，避免大数据量时把底部工具栏撑宽。
const pageNumbers = computed<PageNumber[]>(() => {
  const total = totalPages.value
  const current = currentPage.value
  if (total <= 4) return Array.from({ length: total }, (_, index) => index + 1)
  if (current <= 2) return [1, 2, 'ellipsis', total]
  if (current >= total - 1) return [1, 'ellipsis', total - 1, total]
  return [1, 'ellipsis', current, 'ellipsis', total]
})
const pageSizeOpen = ref(false)
const pageSizeTrigger = ref<HTMLButtonElement | null>(null)
const pageSizeMenu = ref<HTMLDivElement | null>(null)
const pageSizeMenuStyle = ref<Record<string, string>>({})
// 页码输入只作为一次性跳转参数，避免和当前页状态产生双向同步抖动。
const pageJump = ref('')
const changePage = (page: number) => {
  if (backgroundRefreshing.value || state.loading.value || !listIsCurrent.value) return
  const current = state.data.value
  const currentNumber = Math.floor(offset.value / limit.value) + 1
  pageCursor.value = page === currentNumber + 1 ? current?.next_cursor
    : page === currentNumber - 1 ? current?.previous_cursor : undefined
  offset.value = (page - 1) * limit.value
  void reloadPage()
}
// 翻页失败恢复已显示页码；成功只重置纵向位置，保留正在查看的横向列。
async function reloadPage() {
  const revision = queryRevision.value
  queryFailureNotified = false
  const resumeStat = statState.loading.value, resumeCount = countState.loading.value
  if (resumeStat) statState.cancel()
  if (resumeCount) countState.cancel()
  await state.reload()
  if (revision !== queryRevision.value) return
  if (state.lastRefreshError.value) notifyQueryFailure(revision)
  if (state.error.value && state.data.value?.revision === revision) {
    offset.value = state.data.value.pageOffset
    limit.value = state.data.value.pageLimit
    pageCursor.value = state.data.value.requestCursor
  } else if (tableScroll.value) tableScroll.value.scrollTop = 0
  if (resumeStat) void reloadSummary(statState)
  if (resumeCount) void reloadSummary(countState)
}
const changePageSize = (size: number) => {
  if (backgroundRefreshing.value || state.loading.value || !listIsCurrent.value) return
  if (!(pageSizeOptions as readonly number[]).includes(size)) return
  limit.value = size
  pageCursor.value = undefined
  offset.value = 0
  void reloadPage()
}
function closePageSizeMenu() {
  if (!pageSizeOpen.value && Object.keys(pageSizeMenuStyle.value).length === 0) return
  pageSizeOpen.value = false
  pageSizeMenuStyle.value = {}
}
// 浮层优先向上贴近底部工具栏，并限制在视口内，避免被表格容器裁剪。
function positionPageSizeMenu() {
  const trigger = pageSizeTrigger.value
  if (!pageSizeOpen.value || !trigger || !trigger.isConnected || typeof window === 'undefined') return
  const rect = trigger.getBoundingClientRect()
  const menu = pageSizeMenu.value
  const margin = 8
  const gap = 4
  const width = Math.max(rect.width, 144)
  // max-height 包含边框，额外预留两像素避免刚好贴合时出现无意义滚动条。
  const measuredHeight = (menu?.scrollHeight || pageSizeOptions.length * 28 + 8) + 2
  const height = Math.min(measuredHeight, Math.max(120, window.innerHeight - margin * 2))
  const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
  const above = rect.top - height - gap >= margin
  const top = above
    ? rect.top - height - gap
    : Math.min(Math.max(margin, rect.bottom + gap), Math.max(margin, window.innerHeight - height - margin))
  pageSizeMenuStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    width: `${width}px`,
    maxHeight: `${height}px`,
  }
}
function togglePageSizeMenu() {
  if (backgroundRefreshing.value) return
  pageSizeOpen.value = !pageSizeOpen.value
  if (pageSizeOpen.value) void nextTick(positionPageSizeMenu)
}
function selectPageSize(size: number) {
  changePageSize(size)
  closePageSizeMenu()
  pageSizeTrigger.value?.focus()
}
function focusPageSizeOption(index: number) {
  const options = pageSizeMenu.value?.querySelectorAll<HTMLButtonElement>('.page-size-option')
  if (!options?.length) return
  options[Math.max(0, Math.min(index, options.length - 1))]?.focus()
}
// 下拉项支持方向键、Home/End、Enter/Space 和 Esc，行为与 rc35 Select 保持一致。
function handlePageSizeOptionKeydown(event: KeyboardEvent, index: number) {
  if (event.key === 'ArrowDown') {
    event.preventDefault()
    focusPageSizeOption(index + 1)
  } else if (event.key === 'ArrowUp') {
    event.preventDefault()
    focusPageSizeOption(index - 1)
  } else if (event.key === 'Home') {
    event.preventDefault()
    focusPageSizeOption(0)
  } else if (event.key === 'End') {
    event.preventDefault()
    focusPageSizeOption(pageSizeOptions.length - 1)
  } else if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    selectPageSize(pageSizeOptions[index])
  } else if (event.key === 'Escape') {
    event.preventDefault()
    closePageSizeMenu()
    pageSizeTrigger.value?.focus()
  }
}
function handlePageSizeTriggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp' || event.key === 'Enter' || event.key === ' ') {
    event.preventDefault()
    if (!pageSizeOpen.value) {
      pageSizeOpen.value = true
      void nextTick(() => {
        positionPageSizeMenu()
        focusPageSizeOption(event.key === 'ArrowUp' ? pageSizeOptions.length - 1 : 0)
      })
    } else {
      focusPageSizeOption(event.key === 'ArrowUp' ? pageSizeOptions.length - 1 : 0)
    }
  } else if (event.key === 'Escape' && pageSizeOpen.value) {
    event.preventDefault()
    closePageSizeMenu()
  }
}
function handlePageSizePointerdown(event: PointerEvent) {
  if (!pageSizeOpen.value) return
  const target = event.target
  if (target instanceof Element && (target.closest('.page-size-control') || target.closest('.page-size-menu'))) return
  closePageSizeMenu()
}
function handlePageSizeKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && pageSizeOpen.value) {
    event.preventDefault()
    closePageSizeMenu()
    pageSizeTrigger.value?.focus()
  }
}
// 页码直达沿用当前筛选条件和每页条数；超出范围时收敛到最后一页。
const jumpToPage = () => {
  if (backgroundRefreshing.value) return
  // number 类型的 v-model 可能产出 number，统一转文本后再处理空值和格式。
  const raw = String(pageJump.value ?? '').trim()
  pageJump.value = ''
  const requested = Number(raw)
  if (!raw || !Number.isInteger(requested) || requested < 1) {
    if (raw) ElMessage.warning('请输入有效页码')
    return
  }
  const page = Math.min(requested, totalPages.value)
  changePage(page)
}
// 始终可恢复默认时间草稿，包括查询进行中；已提交的查询快照不受影响。
const resetTime = () => {
  timeRange.value = defaultTimeRange()
}

// 查询区的“重置”恢复整组筛选条件，和日期控件内的“重置时间”明确区分。
const reset = () => {
  if (backgroundRefreshing.value) return
  requestScope.value = null
  selectedUserID.value = undefined
  username.value = ''
  tokenName.value = ''
  modelName.value = ''
  group.value = ''
  requestID.value = ''
  upstreamRequestID.value = ''
  channelID.value = ''
  statusCode.value = ''
  emptyOutput.value = false
  fallbackFinalOnly.value = false
  logType.value = 0
  timeRange.value = defaultTimeRange()
  void refreshSearch()
}

function openDetail(row: ReadonlyLog) {
  detailReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  closeRequestChain()
  closePageSizeMenu()
  detailRow.value = row
  detailOpen.value = true
}
// 渠道亲和性标志沿用 New API 的独立 CacheStatsDialog 语义，不再复用整条日志详情弹窗。
function openChannelAffinity(view: LogRowView) {
  if (!view.channelAffinity) return
  closeRequestChain()
  closePageSizeMenu()
  closeDetail()
  affinityTarget.value = { channelID: view.channelID, channelName: view.channelName, affinity: view.channelAffinity }
  affinityOpen.value = true
}
function closeAffinity() {
  affinityOpen.value = false
}
function handleAffinityKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && affinityOpen.value) {
    event.preventDefault()
    closeAffinity()
  }
}
// 详情弹窗使用原生结构时仍需具备可访问的关闭和焦点生命周期。
function closeDetail() {
  detailOpen.value = false
}
function handleDetailKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && detailOpen.value) {
    event.preventDefault()
    closeDetail()
  }
  if (event.key === 'Tab' && detailOpen.value) {
    const controls = [...document.querySelectorAll<HTMLElement>('.detail-dialog button:not(:disabled), .detail-dialog summary, .detail-dialog a[href], .detail-dialog input:not(:disabled)')].filter(node => node.getClientRects().length)
    const first = controls[0], last = controls.at(-1)
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
const expandedRetryID = ref<number | null>(null)
const visibleColumnCount = computed(() => 1 + logColumnOptions.filter(column => isColumnVisible(column.key)).length)
// 记录渠道单元格与浮层位置，滚动表格时可重新定位而不会撑开列表布局。
type RetryHoverState = { id: number; chain: string; retryCount: number; firstAttempt: boolean; hasRetry: boolean; steps: RetryChainStepView[]; affinity?: ChannelAffinityInfo; left: number; top: number }
const retryHover = ref<RetryHoverState | null>(null)
const retryHoverTrigger = ref<HTMLElement | null>(null)
const retryHoverElement = ref<HTMLElement | null>(null)
const retryHoverStyle = computed(() => {
  const state = retryHover.value
  return state ? { left: `${state.left}px`, top: `${state.top}px` } : undefined
})
function closeRetryHover() {
  retryHover.value = null
  retryHoverTrigger.value = null
}
// 将重试提示限制在视口内，优先显示在渠道单元格上方，首行空间不足时自动转到下方。
function positionRetryHover() {
  const state = retryHover.value
  const trigger = retryHoverTrigger.value
  if (!state || !trigger || !trigger.isConnected || typeof window === 'undefined') {
    if (state && (!trigger || !trigger.isConnected)) closeRetryHover()
    return
  }
  const rect = trigger.getBoundingClientRect()
  const popover = retryHoverElement.value
  const width = popover?.offsetWidth || 260
  const height = popover?.offsetHeight || 52
  const margin = 8
  const left = Math.min(Math.max(margin, rect.left), Math.max(margin, window.innerWidth - width - margin))
  const preferredTop = rect.top - height - margin
  const top = preferredTop >= margin
    ? preferredTop
    : Math.min(Math.max(margin, rect.bottom + margin), Math.max(margin, window.innerHeight - height - margin))
  retryHover.value = { ...state, left, top }
}
function openRetryHover(view: LogRowView, event: MouseEvent | FocusEvent) {
  const hasRetry = Boolean(view.retrySteps.length || view.retryChain || view.fallback || view.retryUnknown)
  if (!isAdmin.value || !(view.hasChannel || view.retryUnknown) || (!hasRetry && !view.channelAffinity)) return
  const trigger = event.currentTarget
  if (!(trigger instanceof HTMLElement)) return
  retryHoverTrigger.value = trigger
  const channels = retryChannelsFor(view.source)
  const attempt = retryAttemptMeta(view.source, channels)
  retryHover.value = { id: view.id, chain: view.retryUnknown ? '重试状态未确认，点击查看请求记录' : view.retryChain || '未记录完整重试链路', retryCount: view.retryChain ? attempt.retryCount : 0, firstAttempt: Boolean(view.retryChain) && attempt.firstAttempt, hasRetry, steps: view.retrySteps, affinity: view.channelAffinity, left: 0, top: 0 }
  void nextTick(positionRetryHover)
}
function handleRetryHoverViewportChange() {
  if (retryHover.value) positionRetryHover()
}
function closeRequestChain() {
  expandedRetryID.value = null
  closeRetryHover()
}
function requestChainTitle(row: ReadonlyLog) {
  if (retryLookupUnknown(row)) return '重试状态未确认，点击查看请求记录'
  const sequence = retryChannelsFor(row)
  return sequence.length > 1
    ? retryAttemptLabel(row, sequence) + sequence.join(' → ')
    : 'Fallback 请求（未记录完整重试链路）'
}
function toggleRetryChain(view: LogRowView) {
  if (!isAdmin.value) return
  closeRetryHover()
  expandedRetryID.value = expandedRetryID.value === view.id ? null : view.id
}
async function filterRequestChain(query: ChainQuery) {
  if (query.site !== filters.site_id || backgroundRefreshing.value) return
  selectedUserID.value = undefined
  username.value = ''; tokenName.value = ''; modelName.value = ''; group.value = ''
  channelID.value = ''; statusCode.value = ''; emptyOutput.value = false; upstreamRequestID.value = ''; logType.value = 0
  requestID.value = query.request_id
  requestScope.value = { request: query.request_id, user: query.user_ids }
  timeRange.value = [new Date(query.start_time), new Date(query.end_time)]
  await refreshSearch()
}
function handleRequestChainKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && expandedRetryID.value !== null && !detailOpen.value && !affinityOpen.value) {
    event.preventDefault()
    closeRequestChain()
  }
}
function handlePageSizeViewportChange() {
  if (pageSizeOpen.value) positionPageSizeMenu()
}
watch(() => [filters.site_id, offset.value, limit.value], closeRequestChain)
watch(() => isColumnVisible('channel'), visible => { if (!visible) closeRequestChain() })
watch(detailOpen, async (open) => {
  if (typeof document === 'undefined') return
  document.body.classList.toggle('ct-detail-open', open)
  if (open) {
    window.addEventListener('keydown', handleDetailKeydown)
    await nextTick()
    detailCloseButton.value?.focus()
  } else {
    window.removeEventListener('keydown', handleDetailKeydown)
    if (detailReturnFocus?.isConnected) detailReturnFocus.focus()
  }
})
watch(affinityOpen, async (open) => {
  if (typeof document === 'undefined') return
  document.body.classList.toggle('ct-affinity-open', open)
  if (open) {
    window.addEventListener('keydown', handleAffinityKeydown)
    await nextTick()
    affinityCloseButton.value?.focus()
  } else {
    window.removeEventListener('keydown', handleAffinityKeydown)
  }
})
// 错误和退款行沿用 rc35 的轻底色提示，不改变记录内容或排序。
function rowTone(type: number) {
  return type === 5 ? 'row-error' : type === 6 ? 'row-refund' : ''
}
function statusClass(type: number) {
  return 'status-' + typeMeta(type)[1]
}

// 复制入口必须保留用户点击上下文，公共工具会在 HTTP 环境回退到选区复制并恢复焦点。
async function copyText(value: string | number | undefined | null) {
  const text = String(value ?? '').trim()
  if (!text || text === '—') return
  try {
    if (await copyToClipboard(text)) ElMessage.success('已复制：' + (text.length > 24 ? text.slice(0, 24) + '…' : text))
    else ElMessage.warning('复制失败，请手动选择文本')
  } catch {
    ElMessage.warning('复制失败，请手动选择文本')
  }
}
// 日志类型颜色沿用 new-api 的 LOG_TYPES，状态只用文字呈现，不再添加圆点。
const logTypeMeta: Record<number, [string, LogStatusTone]> = {
  1: ['充值', 'topup'],
  2: ['消费', 'consume'],
  3: ['管理', 'manage'],
  4: ['系统', 'system'],
  5: ['错误', 'error'],
  6: ['退款', 'refund'],
  7: ['登录', 'login'],
}
const typeMeta = (type: number): [string, LogStatusTone] => logTypeMeta[type] || ['类型 ' + type, 'unknown']
const outputRate = (row: ReadonlyLog) => row.use_time > 0 && row.completion_tokens > 0 ? Math.round(row.completion_tokens / row.use_time) : 0
const money = (quota: number) => {
  if (!Number.isFinite(prefs.quotaPerUnit) || prefs.quotaPerUnit <= 0) return '—'
  const amount = quota / prefs.quotaPerUnit
  // rc35 对零费用保留整数 0，避免错误日志被渲染成带六位小数的假精度。
  if (amount === 0) return prefs.currencySymbol + '0'
  return prefs.currencySymbol + (amount >= 1 ? amount.toFixed(2) : amount.toFixed(6))
}
const logSummary = computed(() => listIsCurrent.value && !statState.loading.value && !statState.error.value && statState.data.value?.revision === queryRevision.value ? statState.data.value.summary : undefined)
const billingPrice = (value: number) => Number.isFinite(prefs.priceMultiplier) ? prefs.currencySymbol + Number((value * prefs.priceMultiplier).toFixed(6)) : '—'
const two = (n: number) => String(n).padStart(2, '0')
const timeText = (iso: string) => {
  const d = new Date(iso)
  return d.getFullYear() + '-' + two(d.getMonth() + 1) + '-' + two(d.getDate()) + ' ' + two(d.getHours()) + ':' + two(d.getMinutes()) + ':' + two(d.getSeconds())
}
const timeShort = (iso: string) => {
  const d = new Date(iso)
  return two(d.getMonth() + 1) + '-' + two(d.getDate()) + ' ' + two(d.getHours()) + ':' + two(d.getMinutes()) + ':' + two(d.getSeconds())
}
const timeFull = (iso: string) => new Date(iso).toLocaleString()
const requestPath = (row: ReadonlyLog) => textValue(row, 'request_path') || '—'
const cacheReadTokens = (row: ReadonlyLog) => numberValue(row, 'cache_tokens') || 0
const cacheWrite5mTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_5m') || 0
const cacheWrite1hTokens = (row: ReadonlyLog) => numberValue(row, 'cache_creation_tokens_1h') || 0
const cacheWriteTokens = (row: ReadonlyLog) => {
  const split = cacheWrite5mTokens(row) + cacheWrite1hTokens(row)
  return split > 0 ? split : numberValue(row, 'cache_creation_tokens') || 0
}
// rc35 共同日志只把消费、错误、退款和汇总类型作为可展示的业务记录。
const displayableLogTypes = new Set([0, 2, 5, 6])
const timingLogTypes = new Set([2, 5])
const isDisplayableLog = (row: ReadonlyLog) => displayableLogTypes.has(row.type)
const isTimingLog = (row: ReadonlyLog) => timingLogTypes.has(row.type)
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
  return first(row, 'stream_status') !== undefined ? true : undefined
}
function firstResponse(row: ReadonlyLog): number | undefined {
  if (streamFlag(row) !== true) return undefined
  const milliseconds = numberValue(row, 'frt', 'first_response_time')
  return milliseconds !== undefined && milliseconds > 0 ? milliseconds / 1000 : undefined
}
// new-api rc35 对首字延迟使用固定阈值，短于 5 秒的请求应明确显示为绿色。
function firstResponseVariant(seconds: number | undefined): TimingVariant {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds <= 0) return 'neutral'
  if (seconds < 5) return 'success'
  if (seconds < 10) return 'warning'
  return 'danger'
}
// 总耗时优先按输出吞吐量着色，低输出量请求才回退到绝对耗时阈值。
function durationVariant(row: ReadonlyLog): Exclude<TimingVariant, 'neutral'> {
  const seconds = Number.isFinite(row.use_time) ? Math.max(0, row.use_time) : 0
  const completionTokens = Number.isFinite(row.completion_tokens) ? Math.max(0, row.completion_tokens) : 0
  if (completionTokens >= 100 && seconds > 0) {
    const tokensPerSecond = completionTokens / seconds
    if (tokensPerSecond >= 30) return 'success'
    if (tokensPerSecond >= 15) return 'warning'
    return 'danger'
  }
  if (seconds < 10) return 'success'
  if (seconds < 30) return 'warning'
  return 'danger'
}
const timingClass = (variant: TimingVariant) => 'timing-' + variant
const firstResponseText = (row: ReadonlyLog) => {
  const seconds = firstResponse(row)
  return seconds === undefined ? '—' : seconds.toFixed(1) + 's'
}
const streamLabel = (row: ReadonlyLog) => streamFlag(row) === true ? '流' : streamFlag(row) === false ? '非流' : '未知'
// 流式状态只区分蓝色“流”和灰色“非流”，未知值保持中性色。
const streamClass = (row: ReadonlyLog) => streamFlag(row) === true ? 'is-stream' : streamFlag(row) === false ? 'is-nonstream' : 'is-unknown'
function streamStatus(row: ReadonlyLog) {
  const value = first(row, 'stream_status')
  if (value && typeof value === 'object') {
    const status = String((value as LogExtra).status || '')
    const reason = String((value as LogExtra).end_reason || '')
    return status === 'ok' ? '✓ 正常' + (reason ? ' (' + reason + ')' : '') : [status || '异常', reason].filter(Boolean).join('：')
  }
  if (typeof value === 'string' && value) return value
  const flag = streamFlag(row)
  return flag === true ? '流式' : flag === false ? '非流式' : '旧版接口未返回流式标记'
}
// rc35 将转换链按步骤连接，兼容新旧日志中“数组”与“字符串”两种存储形式。
function conversion(row: ReadonlyLog) {
  const value = first(row, 'request_conversion_chain', 'request_conversion', 'final_request_format')
  if (Array.isArray(value)) {
    const steps = value.filter(Boolean).map(String)
    return steps.length > 1 ? steps.join(' → ') : steps[0] || '原生格式'
  }
  return typeof value === 'string' && value.trim() ? value : '原生格式'
}
const isAdmin = computed(() => auth.user?.role === 'admin')
// 根诊断信息只对拥有通配权限的管理员开放，避免普通管理员看到节点运行时数据。
const isRoot = computed(() => isAdmin.value && auth.user?.permissions?.includes('*') === true)
const visibleValue = (value: string | number | undefined | null) => sensitiveVisible.value ? String(value ?? '') : '••••'
const detailGroup = (row: ReadonlyLog) => sensitiveVisible.value ? (row.group || textValue(row, 'group')) : (row.group || textValue(row, 'group') ? '••••' : '')
const modelMapping = (row: ReadonlyLog) => isAdmin.value ? textValue(row, 'upstream_model_name') : ''
const contentSummary = (row: ReadonlyLog) => row.content_summary || row.content || '—'
// 只有确实有转换信息时才显示分组，避免把默认“原生格式”误当成审计数据。
const hasRequestConversion = (row: ReadonlyLog) => isAdmin.value && row.type !== 6 && Boolean(first(row, 'request_path', 'request_conversion_chain', 'final_request_format'))
function normalizeFallbackChannels(value: unknown): string[] {
  if (Array.isArray(value)) {
    const channels: string[] = []
    for (const item of value) {
      const text = String(item ?? '').trim()
      if (text && !channels.includes(text)) channels.push(text)
    }
    return channels
  }
  if (typeof value !== 'string') return []
  const parts = value.replaceAll('->', ' ').replaceAll('→', ' ').replaceAll(',', ' ').split(/\s+/)
  return parts.reduce<string[]>((channels, part) => {
    const text = part.trim()
    if (text && !channels.includes(text)) channels.push(text)
    return channels
  }, [])
}
// fallback 标志优先读取服务端归一化字段，兼容旧日志中的 use_channel 和显式布尔字段。
function fallbackChannelsFor(row: ReadonlyLog): string[] {
  if (row.fallback_channels?.length) return normalizeFallbackChannels(row.fallback_channels)
  const direct = normalizeFallbackChannels(first(row, 'fallback_channels', 'use_channel'))
  if (direct.length) return direct
  const adminInfo = first(row, 'admin_info')
  if (adminInfo && typeof adminInfo === 'object') {
    const info = adminInfo as LogExtra
    const nested = normalizeFallbackChannels(info.fallback_channels)
    return nested.length ? nested : normalizeFallbackChannels(info.use_channel)
  }
  return []
}
// 新服务端会为每条日志返回完整链路；只有确认总数匹配时才优先使用它，避免把局部旧字段误当成完整链路。
function completeFallbackChannelsFor(row: ReadonlyLog): string[] {
  const total = Number(row.fallback_total)
  if (!Number.isInteger(total) || total <= 1 || !Array.isArray(row.fallback_channels) || row.fallback_channels.length !== total) return []
  return row.fallback_channels.map(value => String(value).trim()).filter(value => /^\d+$/.test(value))
}
// 优先使用服务端完整路径，再保留原始 use_channel 的重复尝试顺序。
function retryChannelsFor(row: ReadonlyLog): string[] {
  const complete = completeFallbackChannelsFor(row)
  if (complete.length > 1) return complete
  const recorded = attemptChannels(row)
  if (recorded.length > 1) return recorded
  return fallbackChannelsFor(row)
}
function retryPositionFor(row: ReadonlyLog, channels: string[]): number {
  const recorded = Number(row.fallback_index)
  if (Number.isInteger(recorded) && recorded >= 1 && recorded <= channels.length) return recorded
  const attempt = attemptChannels(row)
  if (attempt.length > 0 && attempt.length <= channels.length && attempt.every((value, index) => value === channels[index])) return attempt.length
  const channel = String(row.channel_id || row.channel || '')
  const matches = channels.map((value, index) => value === channel ? index + 1 : 0).filter(Boolean)
  return matches.length === 1 ? matches[0] : 0
}
function retryChainStepsFor(row: ReadonlyLog, channels = retryChannelsFor(row)): RetryChainStepView[] {
  if (channels.length <= 1) return []
  const position = retryPositionFor(row, channels)
  const currentTone: RetryStepTone = row.type === 5 ? 'failed' : row.type === 2 && position === channels.length ? 'success' : position > 0 ? 'current' : 'plain'
  return channels.map((channel, index) => ({ channel, position: index + 1, current: index + 1 === position, tone: index + 1 === position ? currentTone : 'plain' }))
}
function retryAttemptMeta(row: ReadonlyLog, channels = retryChannelsFor(row)) {
  const position = retryPositionFor(row, channels)
  if (position === 1) return { firstAttempt: true, retryCount: 0 }
  if (position > 1) return { firstAttempt: false, retryCount: position - 1 }
  return { firstAttempt: false, retryCount: channels.length > 1 ? channels.length - 1 : 0 }
}
function retryAttemptLabel(row: ReadonlyLog, channels = retryChannelsFor(row)) {
  const attempt = retryAttemptMeta(row, channels)
  if (attempt.firstAttempt) return '首次尝试：'
  return attempt.retryCount > 0 ? `重试${attempt.retryCount}次：` : 'Fallback 请求（未记录完整重试链路）'
}
function isFallback(row: ReadonlyLog): boolean {
  if (!isAdmin.value) return false
  if (row.fallback === true) return true
  const explicit = first(row, 'fallback', 'is_fallback', 'fallback_flag')
  if (explicit === true || explicit === 1 || (typeof explicit === 'string' && ['1', 'true', 'yes'].includes(explicit.toLowerCase()))) return true
  return retryChannelsFor(row).length > 1
}
function retryChain(row: ReadonlyLog) {
  if (!isAdmin.value) return ''
  const channels = retryChannelsFor(row)
  return channels.length > 1 ? channels.join(' → ') : ''
}
function fallbackChain(row: ReadonlyLog) {
  if (!isAdmin.value) return ''
  const channels = retryChannelsFor(row)
  return channels.length > 1 ? channels.join(' → ') : ''
}
function detailInputPrice(row: ReadonlyLog) {
  const modelRatio = numberValue(row, 'model_ratio')
  return modelRatio === undefined ? '—' : formatModelPrice(row, modelRatio * 2) + ' / 1M tokens'
}
function detailOutputPrice(row: ReadonlyLog) {
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  return modelRatio === undefined ? '—' : formatModelPrice(row, modelRatio * 2 * (completionRatio ?? 1)) + ' / 1M tokens'
}
function detailCacheReadPrice(row: ReadonlyLog) {
  const modelRatio = numberValue(row, 'model_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  return modelRatio === undefined || cacheRatio === undefined ? '—' : formatModelPrice(row, modelRatio * 2 * cacheRatio) + ' / 1M tokens'
}
function effectiveGroupRatio(row: ReadonlyLog) {
  const userRatio = numberValue(row, 'user_group_ratio')
  return userRatio !== undefined && userRatio !== -1 ? userRatio : numberValue(row, 'group_ratio')
}
const isPerCallBilling = (modelPrice: number | undefined) => modelPrice !== undefined && modelPrice > 0
const ratioText = (value: number) => Number(value.toFixed(4)) + 'x'
function groupRatioLabel(row: ReadonlyLog) {
  return numberValue(row, 'user_group_ratio') !== undefined && numberValue(row, 'user_group_ratio') !== -1 ? '用户专属倍率' : '分组倍率'
}
const detailGroupRatioText = (row: ReadonlyLog) => {
  const ratio = effectiveGroupRatio(row)
  return ratio === undefined ? '—' : ratioText(ratio)
}
function billingSummary(row: ReadonlyLog) {
  if (!isConsume(row)) return row.content_summary || '—'
  // 表达式计费的普通倍率可能为零占位值，不能据此展示标准单价。
  if (isTieredBilling(row)) {
    return dynamicBillingSummary(
      dynamicExpression(row), textValue(row, 'matched_tier'),
      cacheReadTokens(row) > 0 || cacheWriteTokens(row) > 0, billingPrice,
    )
  }
  const modelPrice = numberValue(row, 'model_price')
  const modelRatio = numberValue(row, 'model_ratio')
  const completionRatio = numberValue(row, 'completion_ratio')
  const cacheRatio = numberValue(row, 'cache_ratio')
  const groupRatio = effectiveGroupRatio(row)
  const parts: string[] = []
  if (isPerCallBilling(modelPrice)) {
    parts.push('按次 · ' + billingPrice(modelPrice!))
  } else if (modelRatio !== undefined) {
    const inputPrice = modelRatio * 2
    const basePrices = [billingPrice(inputPrice)]
    if (completionRatio !== undefined) basePrices.push(billingPrice(inputPrice * completionRatio))
    parts.push('标准 · ' + basePrices.join(' / ') + '/1M')
    const cachePrices: string[] = []
    if (cacheReadTokens(row) > 0 && cacheRatio !== undefined && cacheRatio !== 1) cachePrices.push(billingPrice(inputPrice * cacheRatio))
    const cacheWriteRatio = numberValue(row, 'cache_creation_ratio')
    const cacheWrite1hRatio = numberValue(row, 'cache_creation_ratio_1h')
    if (cacheWriteTokens(row) > 0 && cacheWriteRatio !== undefined && cacheWriteRatio !== 1) cachePrices.push(billingPrice(inputPrice * cacheWriteRatio))
    if (cacheWrite1hTokens(row) > 0 && cacheWrite1hRatio !== undefined && cacheWrite1hRatio !== 0) cachePrices.push(billingPrice(inputPrice * cacheWrite1hRatio))
    if (cachePrices.length) parts.push('缓存 ' + cachePrices.join(' / '))
  } else if (groupRatio !== undefined) {
    parts.push(groupRatioLabel(row) + ' ' + ratioText(groupRatio))
  }
  return parts.join('；') || row.content_summary || '—'
}

// 一条响应只构造一次列表展示模型，集中承担 JSON 派生字段、格式化和权限脱敏。
// 这样统计接口或刷新状态变化时，Vue 不会在 100 行模板中重复执行同一组函数。
function logRowMemoKey(row: ReadonlyLog, visible: boolean, admin: boolean) {
  return [
    row.id, row.created_at, row.type, row.user_id, row.username, row.model_name,
    row.channel_id, row.channel, row.channel_name, row.token_id, row.token_name,
    row.prompt_tokens, row.completion_tokens, row.quota, row.use_time, row.request_id,
    row.upstream_request_id, row.content, row.content_summary, row.group, row.ip,
    row.is_stream, row.other, visible ? 1 : 0, admin ? 1 : 0,
    row.fallback ? 1 : 0, row.fallback_checked, (row.fallback_channels || []).join(','),
    row.fallback_index, row.fallback_total,
    // 列偏好也属于行模板的输入，签名变化可让 v-memo 正确重建可选单元格。
    logColumnOptions.map(option => columnVisibility.value[option.key] ? 1 : 0).join(''),
    prefs.quotaPerUnit, prefs.currencySymbol, prefs.priceMultiplier,
  ].join('\u001f')
}
// 查询会不断返回新对象，缓存上限控制在 500 条，避免长时间切换筛选条件造成内存增长。
const logRowCache = new Map<string, LogRowView>()
const logRows = computed<LogRowView[]>(() => {
  const items = mobileViewport.value ? mobileFeed.items.value : state.data.value?.items || []
  const visible = sensitiveVisible.value
  const admin = isAdmin.value
  return items.map((row): LogRowView => {
    const memoKey = logRowMemoKey(row, visible, admin)
    const cached = logRowCache.get(memoKey)
    if (cached) {
      // 列表动作需要指向本次响应的完整原始记录，展示字段仍复用旧结果。
      cached.source = row
      return cached
    }
    const meta = typeMeta(row.type)
    const displayable = isDisplayableLog(row)
    const timing = isTimingLog(row)
    const stream = streamFlag(row)
    const fallback = isFallback(row)
    const fallbackChannels = admin ? fallbackChannelsFor(row) : []
    const retryChannels = admin ? retryChannelsFor(row) : []
    const firstSeconds = timing && stream === true ? firstResponse(row) : undefined
    const firstVariant = firstResponseVariant(firstSeconds)
    const durationTone = durationVariant(row)
    const cacheRead = cacheReadTokens(row)
    const cacheWrite = cacheWriteTokens(row)
    const rate = timing ? outputRate(row) : 0
    const channelID = row.channel_id || row.channel || 0
    const userGroupRatio = numberValue(row, 'user_group_ratio')
    const recordedGroupRatio = numberValue(row, 'group_ratio')
    // rc35 省略默认分组的 1x，用户专属倍率仍按实际记录展示。
    const groupRatio = userGroupRatio !== undefined && userGroupRatio !== -1 ? userGroupRatio : recordedGroupRatio === 1 ? undefined : recordedGroupRatio
    const summary = billingSummary(row)
    const view: LogRowView = {
      source: row,
      id: row.id,
      memoKey,
      tone: rowTone(row.type),
      timeText: timeText(row.created_at),
      timeShort: timeShort(row.created_at),
      timeFull: timeFull(row.created_at),
      statusLabel: meta[0],
      errorCode: logErrorCode(row),
      statusClass: 'status-' + meta[1],
      displayable,
      timing,
      channelID,
      channelTone: channelID > 0 ? getTokenColorClass(String(channelID)) : 'token-tone-hidden',
      channelName: visible ? row.channel_name || '' : row.channel_name ? '••••' : '',
      hasChannel: displayable && channelID > 0,
      fallback,
      fallbackChannels,
      retryUnknown: admin && retryLookupUnknown(row),
      retryChain: admin && retryChannels.length > 1 ? retryChannels.join(' → ') : '',
      retrySteps: retryChainStepsFor(row, retryChannels),
      channelAffinity: channelAffinityFor(row),
      username: visible ? row.username || '用户 ' + row.user_id : '••••',
      usernameCopy: visible ? String(row.username || row.user_id || '') : '',
      initial: visible ? getUserAvatarFallback(row.username) : '•',
      avatarStyle: visible && row.username ? getUserAvatarStyle(row.username) : undefined,
      tokenName: visible ? row.token_name : row.token_name ? '••••' : '',
      hasToken: displayable && Boolean(row.token_name),
      group: visible ? row.group : row.group ? '••••' : '',
      groupRatio,
      groupRatioText: groupRatio === undefined ? '' : ratioText(groupRatio),
      modelName: row.model_name,
      modelProvider: resolveModelProvider(row.model_name || ''),
      groupTone: visible && row.group && row.group !== 'auto' ? getTokenColorClass(row.group) : 'token-tone-hidden',
      modelTone: row.model_name ? getTokenColorClass(row.model_name) : 'token-tone-hidden',
      modelMapping: admin ? textValue(row, 'upstream_model_name') : '',
      hasModel: displayable && Boolean(row.model_name),
      stream,
      streamLabel: stream === true ? '流' : stream === false ? '非流' : '未知',
      streamClass: stream === true ? 'is-stream' : stream === false ? 'is-nonstream' : 'is-unknown',
      outputRateText: rate > 0 ? formatNumber(rate) + ' t/s' : '',
      hasTokens: displayable && Boolean(row.prompt_tokens || row.completion_tokens),
      promptTokens: formatNumber(row.prompt_tokens),
      completionTokens: formatNumber(row.completion_tokens),
      cacheReadTokens: cacheRead,
      cacheWriteTokens: cacheWrite,
      cacheTotal: cacheRead + cacheWrite,
      cacheReadText: formatNumber(cacheRead),
      cacheWriteText: formatNumber(cacheWrite),
      cacheTotalText: formatNumber(cacheRead + cacheWrite),
      quota: displayable ? money(row.quota) : '—',
      firstResponseText: firstSeconds === undefined ? '—' : firstSeconds.toFixed(1) + 's',
      firstResponseClass: timingClass(firstVariant),
      durationClass: timingClass(durationTone),
      durationText: (Number.isFinite(row.use_time) ? row.use_time : 0).toFixed(1) + 's',
      summary,
      summaryPreview: summary.replaceAll('；', ' · '),
    }
    logRowCache.set(memoKey, view)
    if (logRowCache.size > 500) {
      const oldest = logRowCache.keys().next().value
      if (oldest !== undefined) logRowCache.delete(oldest)
    }
    return view
  })
})

// 结果变化时分帧追加行，避免一次性创建 100 行 DOM 阻塞点击后的首帧。
// 以展示签名判断重复结果，刷新接口返回新对象但内容未变时保持现有节点不动。
const renderedRows = ref<LogRowView[]>([])
const logRowChunkSize = 10
let renderedRowsSignature = ''
let rowRenderGeneration = 0
let rowRenderFrame: number | undefined
function cancelRowRender() {
  if (rowRenderFrame !== undefined) {
    window.cancelAnimationFrame(rowRenderFrame)
    rowRenderFrame = undefined
  }
  rowRenderGeneration += 1
}
function updateRenderedRows(rows: LogRowView[]) {
  const signature = rows.map(view => view.memoKey).join('\u001e')
  if (signature === renderedRowsSignature) return
  closeRequestChain()
  cancelRowRender()
  renderedRowsSignature = signature
  const generation = rowRenderGeneration
  const previousRows = renderedRows.value.slice()
  // 非空结果直接复用当前页的索引行，避免先删后加导致整张表重复布局。
  if (previousRows.length > 0 && rows.length > 0) {
    renderedRows.value = rows
    return
  }
  let remaining = previousRows.length
  let cursor = 0
  const appendChunk = () => {
    if (generation !== rowRenderGeneration) return
    cursor = Math.min(cursor + logRowChunkSize, rows.length)
    renderedRows.value = rows.slice(0, cursor)
    if (cursor < rows.length) {
      rowRenderFrame = window.requestAnimationFrame(appendChunk)
    } else {
      rowRenderFrame = undefined
    }
  }
  // 先按同样的批量大小移除旧行，避免 100 行切换到另一批数据时一次性触发布局。
  const removeChunk = () => {
    if (generation !== rowRenderGeneration) return
    if (remaining > 0) {
      remaining = Math.max(0, remaining - logRowChunkSize)
      renderedRows.value = previousRows.slice(0, remaining)
      rowRenderFrame = window.requestAnimationFrame(removeChunk)
      return
    }
    if (rows.length > 0) {
      cursor = Math.min(logRowChunkSize, rows.length)
      renderedRows.value = rows.slice(0, cursor)
      if (cursor < rows.length) rowRenderFrame = window.requestAnimationFrame(appendChunk)
      else rowRenderFrame = undefined
    } else {
      renderedRows.value = []
      rowRenderFrame = undefined
    }
  }
  if (remaining > 0) rowRenderFrame = window.requestAnimationFrame(removeChunk)
  else if (rows.length > 0) {
    cursor = Math.min(logRowChunkSize, rows.length)
    renderedRows.value = rows.slice(0, cursor)
    if (cursor < rows.length) rowRenderFrame = window.requestAnimationFrame(appendChunk)
  }
}
watch(logRows, updateRenderedRows, { immediate: true })

type DetailToolSurcharge = { name: string; count: number; price: number }
type DetailDiscountSnapshot = { before: number; after: number; savings: number }
type DetailSubscriptionField = { label: string; value: string }
type DetailParameterOverride = { action: string; content: string }

// 详情模板会在一次渲染中多次读取相同的解析结果；日志对象不可变，按对象缓存纯派生值，避免重复解码和 JSON 规范化阻塞点击反馈。
const detailDerivedCache = new WeakMap<object, Map<string, unknown>>()
function detailCached<T>(row: ReadonlyLog, key: string, factory: () => T): T {
  let cache = detailDerivedCache.get(row)
  if (!cache) {
    cache = new Map<string, unknown>()
    detailDerivedCache.set(row, cache)
  }
  if (cache.has(key)) return cache.get(key) as T
  const value = factory()
  cache.set(key, value)
  return value
}

// 详情字段来自 New API 的 JSON 快照，所有嵌套对象先经过类型收窄再交给模板。
function recordValue(value: unknown): LogExtra | undefined {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as LogExtra
    : undefined
}

function adminInfoFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(first(row, 'admin_info'))
}

function rootInfoFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(first(row, 'root_info'))
}

function auditInfoFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(first(row, 'audit_info'))
}

function quotaSaturationFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(adminInfoFor(row)?.quota_saturation)
}

function taskPluginFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(adminInfoFor(row)?.task_plugin)
}

function rootTaskPluginFor(row: ReadonlyLog): LogExtra | undefined {
  return recordValue(rootInfoFor(row)?.task_plugin)
}

function stringField(value: unknown): string {
  if (typeof value === 'string') return value.trim()
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return ''
}

// 亲和性只从管理员快照读取；viewer 的后端投影会移除 admin_info，避免前端误显示敏感缓存键。
function channelAffinityFor(row: ReadonlyLog): ChannelAffinityInfo | undefined {
  if (!isAdmin.value) return undefined
  const value = recordValue(adminInfoFor(row)?.channel_affinity)
  if (!value) return undefined
  const affinity: ChannelAffinityInfo = {
    reason: stringField(value.reason),
    ruleName: stringField(value.rule_name),
    usingGroup: stringField(value.using_group),
    selectedGroup: stringField(value.selected_group),
    keySource: stringField(value.key_source),
    keyPath: stringField(value.key_path),
    keyKey: stringField(value.key_key),
    keyHint: stringField(value.key_hint),
    keyFingerprint: stringField(value.key_fp),
  }
  return Object.values(affinity).some(Boolean) ? affinity : undefined
}

function channelAffinityGroup(affinity: ChannelAffinityInfo | undefined): string {
  return affinity?.usingGroup || affinity?.selectedGroup || ''
}

// 亲和性键摘要属于敏感字段，关闭敏感字段显示时只保留命中事实和规则名称。
function channelAffinitySensitive(value: string): string {
  return value ? (sensitiveVisible.value ? value : '••••') : ''
}

function channelAffinityTitle(affinity: ChannelAffinityInfo): string {
  const rule = affinity.ruleName || '未命名规则'
  const group = channelAffinityGroup(affinity)
  return `渠道亲和性命中：${rule}${group ? ` · 分组 ${sensitiveVisible.value ? group : '••••'}` : ''}`
}

function booleanField(value: unknown): boolean {
  if (value === true || value === 1) return true
  return typeof value === 'string' && ['1', 'true', 'yes'].includes(value.trim().toLowerCase())
}

function isViolation(row: ReadonlyLog): boolean {
  return booleanField(first(row, 'violation_fee')) || Boolean(textValue(row, 'violation_fee_code')) || Boolean(textValue(row, 'violation_fee_marker'))
}

function topupAuditFieldsFor(row: ReadonlyLog): DetailSubscriptionField[] {
  return detailCached(row, 'topupAuditFields', () => {
    const info = adminInfoFor(row)
    if (!info || row.type !== 1 || !isAdmin.value) return []
    const fields: DetailSubscriptionField[] = []
    const values: [string, string][] = [
      ['支付方式', stringField(info.payment_method)],
      ['回调支付方式', stringField(info.callback_payment_method)],
      ['回调调用方 IP', stringField(info.caller_ip)],
      ['服务器 IP', stringField(info.server_ip)],
      ['节点名称', stringField(info.node_name)],
      ['系统版本', stringField(info.version)],
    ]
    for (const [label, value] of values) if (value) fields.push({ label, value })
    return fields
  })
}

function manageOperatorFor(row: ReadonlyLog): string {
  if (row.type !== 3 || !isAdmin.value) return ''
  const info = adminInfoFor(row)
  if (!info) return ''
  const username = stringField(info.admin_username)
  const id = stringField(info.admin_id)
  if (username && id) return `${username}（ID: ${id}）`
  return username || (id ? `ID: ${id}` : '')
}

function operationTextFor(row: ReadonlyLog): string {
  const operation = recordValue(first(row, 'op'))
  if (operation) {
    const action = stringField(operation.action)
    const params = recordValue(operation.params)
    const target = params
      ? [stringField(params.username), stringField(params.name), stringField(params.id)]
          .filter(Boolean)
          .join(' / ')
      : ''
    if (action && target) return `${action}（${target}）`
    if (action) return action
  }
  return textValue(row, 'operation') || (row.type === 3 ? row.content_summary || '' : '')
}

function operationAuthMethodFor(row: ReadonlyLog): string {
  const value = stringField(adminInfoFor(row)?.auth_method)
  if (value === 'access_token') return '访问令牌'
  if (value === 'session') return '会话'
  return value
}

function changedFieldsFor(row: ReadonlyLog): string {
  const info = recordValue(first(row, 'op'))
  const params = recordValue(info?.params)
  const values = params?.changed_fields
  if (!Array.isArray(values)) return ''
  const labels: Record<string, string> = {
    status: '状态', models: '模型', group: '分组', type: '类型', base_url: 'Base URL', key: '密钥',
  }
  return values.map((value) => labels[String(value)] || String(value)).join('，')
}

function loginFieldsFor(row: ReadonlyLog): DetailSubscriptionField[] {
  return detailCached(row, 'loginFields', () => {
    if (row.type !== 7) return []
    const fields: DetailSubscriptionField[] = []
    const method = textValue(row, 'login_method')
    const userAgent = textValue(row, 'user_agent')
    if (method) fields.push({ label: '登录方式', value: method })
    if (row.ip) fields.push({ label: 'IP 地址', value: row.ip })
    if (userAgent) fields.push({ label: 'User Agent', value: userAgent })
    return fields
  })
}

function auditRequestFor(row: ReadonlyLog): string {
  const info = auditInfoFor(row)
  const method = stringField(info?.method)
  const route = stringField(info?.route || info?.path)
  return [method, route].filter(Boolean).join(' ')
}

function auditResultFor(row: ReadonlyLog): string {
  const info = auditInfoFor(row)
  if (!info || info.status === undefined || info.status === null) return ''
  return `${booleanField(info.success) ? '成功' : '失败'}（${String(info.status)}）`
}

function billingDiscountFactor(row: ReadonlyLog): number {
  const value = numberValue(row, 'user_model_discount', 'user_model_discount_ratio')
  return value !== undefined && value > 0 && Number.isFinite(value) ? Math.min(value, 1) : 1
}

function billingDiscountLabel(row: ReadonlyLog): string {
  const factor = billingDiscountFactor(row)
  if (factor >= 1) return ''
  return '折扣 ' + (factor * 100).toFixed(2).replace(/\.?0+$/, '') + '%'
}

// 价格快照同时展示原价和折后价，避免管理员把历史折扣误认为当前配置。
function formatModelPrice(row: ReadonlyLog, value: number): string {
  const original = billingPrice(value)
  if (original === '—') return original
  const factor = billingDiscountFactor(row)
  if (factor < 1) return original + ' → ' + billingPrice(value * factor)
  return original
}

function dynamicExpression(row: ReadonlyLog): string {
  const encoded = first(row, 'expr_b64')
  const decoded = decodeBillingExpression(encoded)
  if (decoded) return decoded
  const raw = first(row, 'expr_string', 'billing_expr', 'expr')
  return typeof raw === 'string' ? raw : ''
}

function dynamicTiersFor(row: ReadonlyLog): DynamicTier[] {
  return detailCached(row, 'dynamicTiers', () => parseDynamicTiers(dynamicExpression(row)))
}

function dynamicRulesFor(row: ReadonlyLog): DynamicRequestRule[] {
  return detailCached(row, 'dynamicRules', () => normalizeDynamicRequestRules(first(row, 'request_rules')))
}

function dynamicUsageFactsFor(row: ReadonlyLog): DynamicUsageFact[] {
  return detailCached(row, 'dynamicUsageFacts', () => normalizeDynamicUsageFacts(first(row, 'usage_facts')))
}

// 动态阶梯只显示表达式中实际出现的价格列，避免空列把紧凑弹窗撑宽。
function dynamicPriceFieldsFor(row: ReadonlyLog) {
  return detailCached(row, 'dynamicPriceFields', () => {
    const tiers = dynamicTiersFor(row)
    return dynamicPriceFields.filter((field) => tiers.some((tier) => {
      const value = tier.prices[field.key]
      return value !== undefined && Number.isFinite(value) && value > 0
    }))
  })
}

function dynamicTierPriceText(row: ReadonlyLog, tier: DynamicTier, key: keyof DynamicTier['prices']): string {
  const value = tier.prices[key]
  return value !== undefined && Number.isFinite(value) && value > 0
    ? formatModelPrice(row, value) + ' / 1M tokens'
    : '—'
}

function isTieredBilling(row: ReadonlyLog): boolean {
  return isConsume(row) && textValue(row, 'billing_mode') === 'tiered_expr'
}

function discountSnapshot(row: ReadonlyLog): DetailDiscountSnapshot | undefined {
  return detailCached(row, 'discountSnapshot', () => {
    const before = numberValue(row, 'quota_before_discount')
    const after = numberValue(row, 'quota_after_discount')
    const savings = numberValue(row, 'discount_quota')
    if (before === undefined || after === undefined || savings === undefined) return undefined
    if (!Number.isSafeInteger(before) || !Number.isSafeInteger(after) || !Number.isSafeInteger(savings) || before < 0 || after < 0 || savings < 0 || before - after !== savings) return undefined
    return { before, after, savings }
  })
}

function toolSurchargesFor(row: ReadonlyLog): DetailToolSurcharge[] {
  return detailCached(row, 'toolSurcharges', () => {
    let value: unknown = first(row, 'tool_surcharges')
    if (typeof value === 'string') {
      try { value = JSON.parse(value) } catch { return [] }
    }
    if (!Array.isArray(value)) return []
    return value.flatMap((item) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) return []
      const record = item as LogExtra
      const name = typeof record.name === 'string' ? record.name.trim() : ''
      const count = Number(record.count)
      const price = Number(record.price)
      if (!name || !Number.isFinite(count) || count <= 0 || !Number.isFinite(price) || price <= 0) return []
      return [{ name, count, price }]
    }).slice(0, 50)
  })
}

function legacyToolSurchargesFor(row: ReadonlyLog): DetailToolSurcharge[] {
  return detailCached(row, 'legacyToolSurcharges', () => {
    const entries: DetailToolSurcharge[] = []
    const add = (enabled: unknown, count: unknown, price: unknown, name: string) => {
      const amount = Number(price)
      const calls = Number(count)
      if (booleanField(enabled) && Number.isFinite(calls) && calls > 0 && Number.isFinite(amount) && amount > 0) {
        entries.push({ name, count: calls, price: amount })
      }
    }
    add(first(row, 'web_search'), first(row, 'web_search_call_count'), first(row, 'web_search_price'), '网页搜索')
    add(first(row, 'file_search'), first(row, 'file_search_call_count'), first(row, 'file_search_price'), '文件搜索')
    add(first(row, 'image_generation_call'), first(row, 'image_generation_call_count') || 1, first(row, 'image_generation_call_price'), '图片生成')
    return entries
  })
}

function allToolSurchargesFor(row: ReadonlyLog): DetailToolSurcharge[] {
  return detailCached(row, 'allToolSurcharges', () => {
    const structured = toolSurchargesFor(row)
    return structured.length > 0 ? structured : legacyToolSurchargesFor(row)
  })
}

function streamStatusDetails(row: ReadonlyLog): LogExtra | undefined {
  return detailCached(row, 'streamStatusDetails', () => recordValue(first(row, 'stream_status')))
}

function streamStatusValue(row: ReadonlyLog): string {
  const value = streamStatusDetails(row)
  return value ? stringField(value.status) || '错误' : streamStatus(row)
}

function streamStatusHasError(row: ReadonlyLog): boolean {
  const value = streamStatusDetails(row)
  return Boolean(value) && stringField(value?.status).toLowerCase() !== 'ok'
}

function streamEndReasonFor(row: ReadonlyLog): string {
  return stringField(streamStatusDetails(row)?.end_reason)
}

function streamEndErrorFor(row: ReadonlyLog): string {
  return stringField(streamStatusDetails(row)?.end_error)
}

function streamErrorCountFor(row: ReadonlyLog): number {
  const value = Number(streamStatusDetails(row)?.error_count)
  return Number.isFinite(value) && value > 0 ? value : 0
}

function streamErrorsFor(row: ReadonlyLog): string[] {
  return detailCached(row, 'streamErrors', () => {
    const errors = streamStatusDetails(row)?.errors
    return Array.isArray(errors) ? errors.map(String).filter(Boolean).slice(0, 100) : []
  })
}

function parameterOverrideLines(row: ReadonlyLog): string[] {
  return detailCached(row, 'parameterOverrideLines', () => {
    let value: unknown = first(row, 'po', 'parameter_overrides')
    if (typeof value === 'string') {
      const raw = value
      try { value = JSON.parse(raw) } catch { return raw.trim() ? [raw.trim()] : [] }
    }
    if (Array.isArray(value)) return value.filter((item): item is string => typeof item === 'string' && item.trim() !== '').slice(0, 50)
    return value === undefined || value === null || value === '' ? [] : [String(value)]
  })
}

function parameterOverridesFor(row: ReadonlyLog): DetailParameterOverride[] {
  return detailCached(row, 'parameterOverrides', () => parameterOverrideLines(row).map((line) => {
    const separator = line.indexOf(' ')
    if (separator <= 0) return { action: line, content: line }
    return { action: line.slice(0, separator), content: line.slice(separator + 1) }
  }))
}

function parameterActionLabel(action: string): string {
  const labels: Record<string, string> = {
    set: '设置', delete: '删除', copy: '复制', move: '移动', append: '追加', prepend: '前置',
    trim_prefix: '移除前缀', trim_suffix: '移除后缀', ensure_prefix: '确保前缀', ensure_suffix: '确保后缀',
    trim_space: '清理空格', to_lower: '转小写', to_upper: '转大写', replace: '替换', regex_replace: '正则替换',
    set_header: '设置请求头', delete_header: '删除请求头', copy_header: '复制请求头', move_header: '移动请求头',
    pass_headers: '透传请求头', sync_fields: '同步字段', return_error: '返回错误',
  }
  return labels[action.toLowerCase()] || action
}

// rc35 的内容区只在日志确实有正文时出现；消费日志保留原始内容而不是重复列表摘要。
function detailContentFor(row: ReadonlyLog): string {
  const raw = row.content || (row.type === 2 ? '' : row.content_summary || '')
  return String(raw).trim()
}

function subscriptionFieldsFor(row: ReadonlyLog): DetailSubscriptionField[] {
  if (first(row, 'billing_source') !== 'subscription') return []
  const fields: DetailSubscriptionField[] = []
  const planID = first(row, 'subscription_plan_id')
  const planTitle = first(row, 'subscription_plan_title')
  if (planID !== undefined || planTitle !== undefined) fields.push({ label: '套餐', value: ('#' + String(planID ?? '') + ' ' + String(planTitle ?? '')).trim() })
  const subscriptionID = first(row, 'subscription_id')
  if (subscriptionID !== undefined) fields.push({ label: '订阅实例', value: '#' + String(subscriptionID) })
  const quotaFields: [string, string][] = [
    ['subscription_pre_consumed', '预扣额度'],
    ['subscription_post_delta', '结算差额'],
    ['subscription_consumed', '最终消耗'],
    ['subscription_remain', '剩余额度'],
  ]
  for (const [key, label] of quotaFields) {
    const value = numberValue(row, key)
    if (value !== undefined && (key !== 'subscription_post_delta' || value !== 0)) fields.push({ label, value: money(value) })
  }
  const total = numberValue(row, 'subscription_total')
  if (total !== undefined && fields.some((field) => field.label === '剩余额度')) {
    const remain = fields.find((field) => field.label === '剩余额度')!
    remain.value += ' / ' + money(total)
  }
  return fields
}

function billingMode(row: ReadonlyLog) {
  const mode = textValue(row, 'billing_mode')
  if (mode === 'tiered_expr') return '动态计费'
  if (isPerCallBilling(numberValue(row, 'model_price'))) return '每次调用'
  if (mode && mode !== 'token') return mode
  return '按 Token'
}
function billingPath(row: ReadonlyLog) {
  const info = adminInfoFor(row)
  const path = typeof info?.usage_billing_path === 'string' ? info.usage_billing_path : textValue(row, 'usage_billing_path')
  const labels: Record<string, string> = {
    local: '本地计费',
    upstream: '上游返回',
    'billing-usage-openai': '上游返回（OpenAI 用量）',
    'billing-usage-openai-estimated': '上游返回（OpenAI 估算）',
    'billing-usage-anthropic': '上游返回（Anthropic 用量）',
    'billing-usage-anthropic-estimated': '上游返回（Anthropic 估算）',
    'billing-usage-gemini': '上游返回（Gemini 用量）',
    'billing-usage-gemini-estimated': '上游返回（Gemini 估算）',
  }
  if (path && labels[path]) return labels[path]
  return info?.local_count_tokens === true ? '本地计费' : '上游返回'
}
async function load() {
  try {
    await Promise.all([filters.loadInstances(), prefs.load()])
  } catch {
    // content request reports its own error
  }
  await reloadAll()
  initialLoadPending = false
}
onMounted(() => {
  restoreColumnVisibility()
  document.body.classList.add('ct-rc35-logs-theme')
  document.addEventListener('pointerdown', handlePageSizePointerdown)
  window.addEventListener('keydown', handleRequestChainKeydown)
  window.addEventListener('keydown', handlePageSearchKeydown)
  window.addEventListener('keydown', handlePageSizeKeydown)
  window.addEventListener('resize', handlePageSizeViewportChange)
  window.addEventListener('scroll', handlePageSizeViewportChange, true)
  window.addEventListener('resize', handleRetryHoverViewportChange)
  window.addEventListener('scroll', handleRetryHoverViewportChange, true)
  viewportQuery = window.matchMedia('(max-width: 900px)')
  viewportQuery.addEventListener('change', syncViewport)
  syncViewport()
  // 账单抽屉可通过用户和月份或明确时间范围定位到日志。
  const qUser = typeof route.query.username === 'string' ? route.query.username : ''
  const qMonth = typeof route.query.month === 'string' ? route.query.month : ''
  const qFrom = typeof route.query.from === 'string' ? route.query.from : ''
  const qTo = typeof route.query.to === 'string' ? route.query.to : ''
  if (qUser) username.value = qUser
  const cap = new Date(Date.now() + 60 * 60 * 1000)
  if (qFrom && qTo && !Number.isNaN(Date.parse(qFrom)) && !Number.isNaN(Date.parse(qTo))) {
    const start = new Date(qFrom), end = new Date(qTo)
    if (end > start) timeRange.value = [start, end < cap ? end : cap]
  } else if (/^\d{4}-\d{2}$/.test(qMonth)) {
    const start = new Date(qMonth + '-01T00:00:00')
    const nextMonth = new Date(start)
    nextMonth.setMonth(start.getMonth() + 1)
    timeRange.value = [start, nextMonth < cap ? nextMonth : cap]
  }
  void load()
})
onUnmounted(() => {
  queryRevision.value += 1
  cancelRowRender()
  state.cancel()
  statState.cancel()
  countState.cancel()
  closeRequestChain()
  closePageSizeMenu()
  document.removeEventListener('pointerdown', handlePageSizePointerdown)
  window.removeEventListener('keydown', handleRequestChainKeydown)
  window.removeEventListener('keydown', handlePageSearchKeydown)
  window.removeEventListener('keydown', handlePageSizeKeydown)
  window.removeEventListener('resize', handlePageSizeViewportChange)
  window.removeEventListener('scroll', handlePageSizeViewportChange, true)
  window.removeEventListener('resize', handleRetryHoverViewportChange)
  window.removeEventListener('scroll', handleRetryHoverViewportChange, true)
  window.removeEventListener('keydown', handleDetailKeydown)
  window.removeEventListener('keydown', handleAffinityKeydown)
  document.body.classList.remove('ct-detail-open')
  document.body.classList.remove('ct-affinity-open')
  document.body.classList.remove('ct-rc35-logs-theme')
  viewportQuery?.removeEventListener('change', syncViewport)
  viewportQuery = undefined
})
watch(() => filters.site_id, (site, previous) => {
  if (!initialLoadPending && site && site !== previous) {
    // 站点隔离：加载新站点时立即移除上一站点结果，取消其仍在执行的请求。
    state.cancel(); statState.cancel(); countState.cancel()
    backgroundRefreshing.value = false
    state.data.value = undefined; statState.data.value = undefined; countState.data.value = undefined
    requestScope.value = null
    selectedUserID.value = undefined
    offset.value = 0
    detailRow.value = null
    detailOpen.value = false
    void reloadAll()
  }
})
</script>

<template>
  <AppShell title="使用日志">
    <div class="logs-page">

      <!-- rc35 的工具栏将低频筛选条件收进可展开的第二行，首行始终填满可用宽度。 -->
      <section class="logs-toolbar">
        <MobileLogFilters v-if="mobileViewport" v-model:username="username" v-model:time-range="timeRange" :site="filters.site_id" :filters="mobileFilters" :busy="state.loading.value || backgroundRefreshing" :admin="isAdmin" :sensitive="sensitiveVisible" @select-user="handleUserSelect" @apply="applyMobileFilters" @search="search" @reset="reset" @privacy="sensitiveVisible = !sensitiveVisible" />
        <div v-else class="toolbar-primary">
          <div class="primary-filters">
            <div class="filter-row filter-row-primary">
              <CompactDateTimeRangePicker v-model="timeRange" reset-enabled class="filter-time" @reset="resetTime" />
              <UserNamePicker v-model="username" :site="filters.site_id" class="filter-username" placeholder="用户名称" aria-label="用户名称" @select="handleUserSelect" @submit="search" />
              <el-input v-model="channelID" clearable placeholder="渠道 ID" @keyup.enter="search" class="filter-channel" />
              <el-input v-model="requestID" clearable placeholder="请求ID" @keyup.enter="search" class="filter-request" />
              <el-input v-model="modelName" clearable placeholder="模型名称" @keyup.enter="search" class="filter-model" />
              <el-input v-model="upstreamRequestID" clearable placeholder="上游请求ID" @keyup.enter="search" class="filter-upstream" />
              <button type="button" class="filter-expand-action" :aria-expanded="secondaryFiltersOpen" aria-controls="readonly-log-secondary-filters" :title="secondaryFiltersOpen ? '收起更多筛选' : '展开更多筛选'" @click="secondaryFiltersOpen = !secondaryFiltersOpen">
                <el-icon aria-hidden="true"><ArrowUp v-if="secondaryFiltersOpen" /><ArrowDown v-else /></el-icon>
                <span>{{ secondaryFiltersOpen ? '收起' : '展开' }}</span>
                <span v-if="secondaryFilterCount" class="filter-count">{{ secondaryFilterCount }}</span>
              </button>
            </div>
            <div v-show="secondaryFiltersOpen" id="readonly-log-secondary-filters" class="filter-row filter-row-secondary">
              <el-input v-model="group" clearable placeholder="分组" @keyup.enter="search" class="filter-group" />
              <el-input v-model="tokenName" clearable placeholder="令牌名称" @keyup.enter="search" class="filter-token" />
              <el-input v-model="statusCode" clearable placeholder="错误码" aria-label="错误码" inputmode="numeric" @keyup.enter="search" class="filter-status-code" />
            </div>
          </div>
        </div>
        <div class="toolbar-meta">
          <div class="stat-badges">
            <span class="stat-badge"><i class="accent-sky" /><span>用量</span><strong>{{ sensitiveVisible ? (logSummary ? money(logSummary.quota) : '—') : '••••' }}</strong></span>
            <span class="stat-badge"><i class="accent-rose" /><span>RPM</span><strong>{{ logSummary ? formatNumber(logSummary.rpm) : '—' }}</strong></span>
            <span class="stat-badge"><i class="accent-slate" /><span>TPM</span><strong>{{ logSummary ? formatNumber(logSummary.tpm) : '—' }}</strong></span>
          </div>
          <div v-if="!mobileViewport" class="toolbar-actions">
            <el-select v-model="logType" placeholder="全部类型" class="filter-type action-type"><el-option label="全部类型" :value="0" /><el-option label="消费" :value="2" /><el-option label="错误" :value="5" /><el-option label="充值" :value="1" /><el-option label="管理" :value="3" /><el-option label="系统" :value="4" /><el-option label="退款" :value="6" /><el-option label="登录" :value="7" /></el-select>
            <label v-if="isAdmin" class="empty-output-toggle"><input v-model="emptyOutput" type="checkbox" :disabled="backgroundRefreshing" /><span>空输出</span></label>
            <label v-if="isAdmin" class="empty-output-toggle fallback-final-toggle" title="同一请求只保留 fallback 链路最后一次尝试"><input v-model="fallbackFinalOnly" type="checkbox" :disabled="backgroundRefreshing" /><span>Fallback 仅最后一条</span></label>
            <button type="button" class="icon-action" :title="sensitiveVisible ? '隐藏敏感字段' : '显示敏感字段'" :aria-label="sensitiveVisible ? '隐藏敏感字段' : '显示敏感字段'" @click="sensitiveVisible = !sensitiveVisible"><el-icon><component :is="sensitiveVisible ? View : Hide" /></el-icon></button>
            <button type="button" class="secondary-action" :disabled="backgroundRefreshing" @click="reset"><el-icon><RefreshLeft /></el-icon><span>{{ backgroundRefreshing ? '更新中' : '重置' }}</span></button>
            <button type="button" class="primary-action" :disabled="state.loading.value || backgroundRefreshing" @click="search"><el-icon><Search /></el-icon><span>{{ state.loading.value ? '查询中' : backgroundRefreshing ? '更新中' : '查询' }}</span></button>
            <!-- 列配置与 rc35 的 DataTableViewOptions 对齐，并记住每个浏览器的选择。 -->
            <el-popover placement="bottom-end" trigger="click" :width="190" popper-class="logs-column-popover">
              <template #reference><button type="button" class="secondary-action view-action" title="查看列" aria-label="查看列"><el-icon><View /></el-icon><span>查看</span></button></template>
              <div class="column-menu">
                <strong>显示列</strong>
                <el-checkbox v-for="column in logColumnOptions" :key="column.key" :model-value="isColumnVisible(column.key)" @change="toggleColumn(column.key, Boolean($event))">{{ column.label }}</el-checkbox>
              </div>
            </el-popover>
          </div>
        </div>
      </section>

      <div v-if="scopedUserIDs" class="request-scope-banner"><span>请求关联筛选 · 同一用户 ID {{ sensitiveVisible ? scopedUserIDs : '••••' }}</span><button type="button" class="secondary-action" :disabled="backgroundRefreshing" @click="reset">清除关联筛选</button></div>
      <el-alert v-if="!state.error.value && !state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。配置后可直接在此查询。" type="info" show-icon :closable="false" />

      <div v-if="mobileViewport" class="mobile-result-count" aria-live="polite">{{ state.loading.value ? '正在加载日志…' : backgroundRefreshing ? '正在更新…' : `共 ${formatNumber(effectiveTotal)} 条记录` }}</div>
      <section class="logs-table-shell" :class="{ 'is-background-refreshing': backgroundRefreshing }" :aria-busy="backgroundRefreshing">
        <div v-if="!mobileViewport" v-loading="state.loading.value" class="desktop-table" ref="tableScroll">
          <table class="logs-table">
            <thead><tr><th class="col-time">时间</th><th v-if="isColumnVisible('channel')" class="col-channel">渠道</th><th v-if="isColumnVisible('user')" class="col-user">用户</th><th v-if="isColumnVisible('token')" class="col-token">令牌</th><th v-if="isColumnVisible('model')" class="col-model">模型</th><th v-if="isColumnVisible('stream')" class="col-stream">流</th><th v-if="isColumnVisible('tokens')" class="col-tokens">Tokens</th><th v-if="isColumnVisible('quota')" class="col-quota">费用</th><th v-if="isColumnVisible('timing')" class="col-timing">耗时</th><th v-if="isColumnVisible('details')" class="col-details">详情</th></tr></thead>
            <tbody>
              <!-- 所有记录共用一个行组，减少分页查询时反复计算 table section 的布局。 -->
              <template v-for="view in renderedRows" :key="view.id">
              <!-- 日志记录不可变时复用整行 DOM，只有字段或显示偏好变化才重新补丁。 -->
              <tr v-memo="[view.memoKey, expandedRetryID === view.id]" class="log-row" :class="view.tone">
                  <!-- 表格时间列复制当前展示值，移动端卡片和详情时间保持原有交互。 -->
                  <td class="col-time"><div class="time-cell" :title="view.timeFull"><button type="button" class="time-copy-button copyable" title="点击复制时间" aria-label="复制时间" @click.stop="copyText(view.timeText)"><span class="time-text">{{ view.timeText }}</span></button><span class="status-badge" :class="view.statusClass">{{ view.statusLabel }}</span></div></td>
                  <td v-if="isColumnVisible('channel')" class="col-channel" @mouseenter="openRetryHover(view, $event)" @mouseleave="closeRetryHover" @focusin="openRetryHover(view, $event)" @focusout="closeRetryHover"><div v-if="view.hasChannel || view.retryUnknown" class="channel-cell" :aria-label="isAdmin && view.retryChain ? requestChainTitle(view.source) : undefined"><div class="channel-line"><span v-if="view.hasChannel" class="channel-affinity-anchor"><button type="button" :class="['channel-badge', 'copyable', view.channelTone]" title="点击复制渠道 ID" @click.stop="copyText(view.channelID)">#{{ view.channelID }}</button><button v-if="view.channelAffinity" type="button" class="channel-affinity-trigger" :title="channelAffinityTitle(view.channelAffinity)" :aria-label="channelAffinityTitle(view.channelAffinity)" @click.stop="openChannelAffinity(view)"><svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11.017 2.814a1 1 0 0 1 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 1-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 0-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 0 1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/></svg></button></span><button v-if="isAdmin && (view.fallback || view.retryChain || view.retryUnknown)" type="button" class="retry-chain-trigger" :class="{ 'retry-chain-unknown': view.retryUnknown }" :aria-label="requestChainTitle(view.source)" :aria-expanded="expandedRetryID === view.id" @click.stop="toggleRetryChain(view)"><svg aria-hidden="true" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3v12M18 9a9 9 0 0 1-9 9"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="6" r="3"/></svg><span v-if="view.retryUnknown">待确认</span></button></div><span v-if="view.channelName" class="cell-secondary">{{ view.channelName }}</span></div><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('user')" class="col-user"><button v-if="view.source.username" type="button" class="user-cell copyable" :title="sensitiveVisible ? '点击复制用户名' : undefined" @click.stop="copyText(view.usernameCopy)"><i class="user-avatar" :class="{ 'is-hidden': !sensitiveVisible }" :style="view.avatarStyle">{{ view.initial }}</i><span class="truncate">{{ view.username }}</span></button><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('token')" class="col-token"><div v-if="view.hasToken" class="token-cell"><button type="button" class="token-badge copyable" :title="sensitiveVisible ? '点击复制令牌名称' : undefined" @click.stop="copyText(sensitiveVisible ? view.source.token_name : '')"><svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3v-3h3v-3h2.172a2 2 0 0 0 1.414-.586l1.814-1.814a6.5 6.5 0 1 0-4-4z"/><circle cx="16.5" cy="7.5" r=".5" fill="currentColor"/></svg><span>{{ view.tokenName }}</span></button><span v-if="view.source.group || view.groupRatio !== undefined" class="group-meta"><span v-if="view.source.group" :class="view.groupTone">{{ view.group }}</span><span v-if="view.source.group && view.groupRatio !== undefined"> </span><span v-if="view.groupRatio !== undefined" class="ratio">{{ view.groupRatioText }}</span></span></div><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('model')" class="col-model"><div v-if="view.hasModel" class="model-cell"><button type="button" class="model-badge copyable" :class="view.modelProvider ? undefined : view.modelTone" @click.stop="copyText(view.source.model_name)"><ModelProviderIcon v-if="view.modelProvider" :name="view.modelProvider.name" />{{ view.modelName }}</button><span v-if="view.modelMapping" class="cell-secondary truncate">{{ view.modelMapping }}</span></div><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('stream')" class="col-stream"><div v-if="view.timing" class="stream-cell"><span class="stream-label" :class="view.streamClass">{{ view.streamLabel }}</span><small v-if="view.outputRateText">{{ view.outputRateText }}</small></div><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('tokens')" class="col-tokens"><div v-if="view.hasTokens" class="tokens-cell"><span class="token-pair">{{ view.promptTokens }} <b>/</b> {{ view.completionTokens }}</span><small v-if="view.cacheTotal"><span v-if="view.cacheReadTokens">缓存↓ {{ view.cacheReadText }}</span><span v-if="view.cacheWriteTokens"> ↑ {{ view.cacheWriteText }}</span></small></div><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('quota')" class="col-quota"><span v-if="view.displayable" class="cost-text">{{ view.quota }}</span><span v-else class="muted">—</span></td>
                  <td v-if="isColumnVisible('timing')" class="col-timing">
                    <div v-if="view.timing" class="timing-cell">
                      <span class="timing-bar" aria-hidden="true">
                        <i v-if="view.stream === true" class="timing-segment" :class="view.firstResponseClass" />
                        <i class="timing-segment" :class="view.durationClass" />
                      </span>
                      <div class="timing-values">
                        <span v-if="view.stream === true" class="timing-value" :class="view.firstResponseClass">首字 <b>{{ view.firstResponseText }}</b></span>
                        <span class="timing-value" :class="view.durationClass">耗时 <b>{{ view.durationText }}</b></span>
                      </div>
                    </div>
                    <span v-else class="muted">—</span>
                  </td>
                  <td v-if="isColumnVisible('details')" class="col-details"><button type="button" class="details-button" :title="'查看完整详情：' + view.summary" :aria-label="'查看完整详情：' + view.summary" @click.stop="openDetail(view.source)">{{ view.summaryPreview }}</button></td>
              </tr>
              <tr v-if="expandedRetryID === view.id && isAdmin" class="request-chain-row"><td :colspan="visibleColumnCount"><FallbackRequestChain :row="view.source" :site="filters.site_id" :sensitive="sensitiveVisible" :money="money" @close="closeRequestChain" @filter="filterRequestChain" @detail="openDetail" /></td></tr>
              </template>
            </tbody>
          </table>
          <div v-if="!state.loading.value && !renderedRows.length && !(state.data.value?.items.length)" class="empty-state"><el-icon><Search /></el-icon><strong>暂无日志</strong><span>调整时间范围或筛选条件后重试</span></div>
        </div>

        <!-- rc35 的移动端摘要布局：首屏保留模型、费用、状态和关键维度。 -->
        <div v-else v-loading="state.loading.value" class="mobile-log-list">
          <article v-for="view in renderedRows" v-memo="[view.memoKey, expandedRetryID === view.id]" :key="view.id" class="mobile-log-card" :class="view.tone">
            <div class="mobile-card-time"><time :title="view.timeFull">{{ view.timeText }}</time><span class="status-badge" :class="view.statusClass">{{ view.statusLabel }}<template v-if="view.errorCode"> · {{ view.errorCode }}</template></span></div>
            <div class="mobile-card-identity"><button type="button" :disabled="!sensitiveVisible || backgroundRefreshing" :aria-label="'按用户名筛选：' + view.username" @click="filterMobileUser(view.source.username, view.source.user_id)">{{ view.username }}</button><span aria-hidden="true">·</span><strong>{{ view.modelName || '未记录模型' }}</strong></div>
            <div class="mobile-card-token">令牌：{{ view.tokenName || '—' }}</div>
            <dl class="mobile-card-metrics">
              <div><dt>费用</dt><dd>{{ view.quota }}</dd></div>
              <div><dt>输入 / 输出</dt><dd>{{ view.hasTokens ? view.promptTokens + ' / ' + view.completionTokens : '—' }}</dd></div>
              <div><dt>首字</dt><dd>{{ view.firstResponseText }}</dd></div>
              <div><dt>总耗时</dt><dd>{{ view.timing ? view.durationText : '—' }}</dd></div>
            </dl>
            <div v-if="view.hasChannel || view.retryUnknown" class="mobile-card-channel" @mouseenter="openRetryHover(view, $event)" @mouseleave="closeRetryHover" @focusin="openRetryHover(view, $event)" @focusout="closeRetryHover">
              <span v-if="view.hasChannel" class="channel-affinity-anchor"><button type="button" :class="['channel-badge', 'copyable', view.channelTone]" title="点击复制渠道 ID" @click.stop="copyText(view.channelID)">#{{ view.channelID }}</button><button v-if="view.channelAffinity" type="button" class="channel-affinity-trigger" :title="channelAffinityTitle(view.channelAffinity)" :aria-label="channelAffinityTitle(view.channelAffinity)" @click.stop="openChannelAffinity(view)"><svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11.017 2.814a1 1 0 0 0 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a2 2 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 1-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051 5.558a2 2 0 0 1-1.594 1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 1-1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/></svg></button></span><span v-else>渠道 #{{ view.channelID }} {{ view.channelName }}</span><span v-if="view.hasChannel && view.channelName" class="cell-secondary">{{ view.channelName }}</span><button v-if="isAdmin && (view.fallback || view.retryChain || view.retryUnknown)" type="button" class="retry-chain-trigger" :aria-expanded="expandedRetryID === view.id" aria-label="查看请求重试链路" @click="toggleRetryChain(view)"><svg aria-hidden="true" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M6 3v12M18 9a9 9 0 0 1-9 9"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="6" r="3"/></svg><span v-if="view.retryUnknown">待确认</span></button></div>
            <button type="button" class="mobile-card-detail" @click="openDetail(view.source)">{{ view.source.type === 5 ? '查看错误详情' : '查看详情' }}<span aria-hidden="true">›</span></button>
            <div v-if="expandedRetryID === view.id && isAdmin" class="mobile-request-chain"><FallbackRequestChain :row="view.source" :site="filters.site_id" :sensitive="sensitiveVisible" :money="money" @close="closeRequestChain" @filter="filterRequestChain" @detail="openDetail" /></div>
          </article>
          <div v-if="!state.loading.value && !renderedRows.length && !(state.data.value?.items.length)" class="empty-state"><el-icon><Search /></el-icon><strong>暂无日志</strong><span>调整时间范围或筛选条件后重试</span></div>
        </div>
      </section>

      <ScrollLoadMore v-if="mobileViewport" :loading="mobileFeed.loading.value || state.loading.value" :disabled="backgroundRefreshing || !!state.error.value" :has-more="mobileFeed.hasMore.value" :error="mobileFeed.error.value" @load="mobileFeed.loadMore" />
      <footer v-else class="pagination-bar">
        <div class="pager-summary"><span>{{ countIsCurrent ? '总计：' : '已加载至：' }}</span><strong>{{ formatNumber(countIsCurrent ? effectiveTotal : (state.data.value?.pageOffset || 0) + (state.data.value?.items.length || 0)) }}</strong></div>
        <div class="pager-controls">
          <div class="page-size-control">
            <span class="page-size-label">每页行数</span>
            <button ref="pageSizeTrigger" type="button" class="page-size page-size-trigger" role="combobox" aria-haspopup="listbox" aria-controls="readonly-log-page-size-menu" :aria-expanded="pageSizeOpen" aria-label="每页行数" :disabled="backgroundRefreshing || state.loading.value || !listIsCurrent" @click="togglePageSizeMenu" @keydown="handlePageSizeTriggerKeydown">
              <span>{{ limit }}</span>
              <i class="page-size-chevron" aria-hidden="true" />
            </button>
          </div>
          <nav class="pager-navigation" aria-label="日志分页">
            <button type="button" class="page-button page-edge" :disabled="currentPage <= 1 || backgroundRefreshing || state.loading.value || !listIsCurrent" aria-label="跳转到第一页" title="第一页" @click="changePage(1)"><span aria-hidden="true">&laquo;</span></button>
            <button type="button" class="page-button" :disabled="currentPage <= 1 || backgroundRefreshing || state.loading.value || !listIsCurrent" aria-label="上一页" title="上一页" @click="changePage(currentPage - 1)"><span aria-hidden="true">&lsaquo;</span></button>
            <template v-for="(page, index) in pageNumbers" :key="page + '-' + index">
              <span v-if="page === 'ellipsis'" class="page-ellipsis" aria-hidden="true">&hellip;</span>
              <button v-else type="button" class="page-button page-number" :class="{ active: page === currentPage }" :disabled="page === currentPage || backgroundRefreshing || state.loading.value || !listIsCurrent" :aria-current="page === currentPage ? 'page' : undefined" :aria-label="'跳转到第 ' + page + ' 页'" @click="changePage(page)">{{ page }}</button>
            </template>
            <button type="button" class="page-button" :disabled="currentPage >= totalPages || backgroundRefreshing || state.loading.value || !listIsCurrent" aria-label="下一页" title="下一页" @click="changePage(currentPage + 1)"><span aria-hidden="true">&rsaquo;</span></button>
            <button type="button" class="page-button page-edge" :disabled="currentPage >= totalPages || backgroundRefreshing || state.loading.value || !listIsCurrent" aria-label="跳转到最后一页" title="最后一页" @click="changePage(totalPages)"><span aria-hidden="true">&raquo;</span></button>
          </nav>
        </div>
        <form class="page-jump" novalidate @submit.prevent="jumpToPage">
          <label for="readonly-log-page-jump">跳至</label>
          <input id="readonly-log-page-jump" v-model="pageJump" type="number" min="1" :max="totalPages" step="1" inputmode="numeric" placeholder="页码" aria-label="跳转到页码" />
          <button type="submit" :disabled="backgroundRefreshing || state.loading.value || !listIsCurrent" aria-label="确认跳转页码">跳转</button>
        </form>
      </footer>

      <!-- 分页选择菜单脱离底部 overflow 容器，按 rc35 Select 的上方浮层展示。 -->
      <Teleport to="body">
        <div v-if="pageSizeOpen" id="readonly-log-page-size-menu" ref="pageSizeMenu" class="page-size-menu" role="listbox" aria-label="每页行数" :style="pageSizeMenuStyle" @pointerdown.stop>
          <button v-for="(size, index) in pageSizeOptions" :key="size" type="button" class="page-size-option" role="option" :aria-selected="size === limit" @click="selectPageSize(size)" @keydown="handlePageSizeOptionKeydown($event, index)">
            <span>{{ size }}</span>
            <span class="page-size-check" aria-hidden="true">✓</span>
          </button>
        </div>
      </Teleport>


      <!-- 详情按 rc35 的信息流重构：标题、单列概览、分组卡片和内部滚动彼此独立。 -->
      <Teleport to="body">
        <!-- 首次打开后保留详情 DOM，仅切换显示状态，关闭时不再同步销毁整棵信息流。 -->
        <div v-if="detailRow" v-show="detailOpen" class="detail-dialog-backdrop" @mousedown.self="closeDetail">
          <section class="detail-dialog" :class="{ 'is-wide': isConsume(detailRow) && first(detailRow, 'billing_mode') === 'tiered_expr' }" role="dialog" aria-modal="true" aria-labelledby="readonly-log-detail-title" aria-describedby="readonly-log-detail-description" @mousedown.stop>
            <header class="detail-dialog-header">
              <div class="detail-dialog-title">
                <h2 id="readonly-log-detail-title">日志详情</h2>
                <span class="detail-dialog-status" :class="statusClass(detailRow.type)">{{ typeMeta(detailRow.type)[0] }}</span><span v-if="isFallback(detailRow)" class="detail-dialog-fallback">Fallback</span>
              </div>
              <button ref="detailCloseButton" type="button" class="detail-dialog-close" aria-label="关闭日志详情" title="关闭" @click="closeDetail"><span class="detail-close-glyph" aria-hidden="true" /></button>
            </header>
            <p id="readonly-log-detail-description" class="sr-only">查看当前日志的请求、计费、流状态和审计信息</p>

            <div class="detail-dialog-body">
              <div class="detail-dialog-body-inner">
                <div class="detail-dialog-content">
                <div v-if="mobileViewport" class="mobile-detail-summary">
                  <section class="mobile-detail-card mobile-detail-intro">
                    <div><strong>{{ visibleValue(detailRow.username) }}</strong><p>{{ detailRow.model_name || '未记录模型' }}</p><time>{{ timeFull(detailRow.created_at) }}</time></div>
                    <div class="mobile-detail-amount"><span>本次费用</span><b>{{ isDisplayableLog(detailRow) ? money(detailRow.quota) : '—' }}</b></div>
                  </section>
                  <section v-if="isTimingLog(detailRow)" class="mobile-detail-card">
                    <h3>请求表现</h3>
                    <dl class="mobile-performance">
                      <div><dt>首字时间</dt><dd>{{ streamFlag(detailRow) === true ? firstResponseText(detailRow) : '—' }}</dd></div>
                      <div><dt>总耗时</dt><dd>{{ detailRow.use_time }}s</dd></div>
                      <div><dt>输出速度</dt><dd>{{ outputRate(detailRow) > 0 ? outputRate(detailRow) + ' Tokens/s' : '—' }}</dd></div>
                      <div><dt>流式响应</dt><dd>{{ streamFlag(detailRow) === true ? '是' : streamFlag(detailRow) === false ? '否' : '未知' }}</dd></div>
                    </dl>
                  </section>
                  <section v-if="isDisplayableLog(detailRow)" class="mobile-detail-card">
                    <h3>用量明细</h3>
                    <dl class="mobile-detail-rows"><div><dt>输入 Tokens</dt><dd>{{ formatNumber(detailRow.prompt_tokens) }}</dd></div><div><dt>输出 Tokens</dt><dd>{{ formatNumber(detailRow.completion_tokens) }}</dd></div><div v-if="cacheReadTokens(detailRow)"><dt>缓存读取</dt><dd>{{ formatNumber(cacheReadTokens(detailRow)) }}</dd></div><div v-if="cacheWriteTokens(detailRow)"><dt>缓存写入</dt><dd>{{ formatNumber(cacheWriteTokens(detailRow)) }}</dd></div></dl>
                  </section>
                  <section class="mobile-detail-card">
                    <h3>请求信息</h3>
                    <dl class="mobile-detail-rows"><div><dt>令牌</dt><dd>{{ visibleValue(detailRow.token_name) }}</dd></div><div><dt>分组</dt><dd>{{ detailGroup(detailRow) }}</dd></div><div><dt>请求 ID</dt><dd>{{ visibleValue(detailRow.request_id) }}</dd></div></dl>
                  </section>
                  <section v-if="detailRow.type === 5 && detailContentFor(detailRow)" class="mobile-detail-card mobile-error-content"><h3>错误详情</h3><p>{{ detailContentFor(detailRow) }}</p></section>
                </div>
                <details class="detail-full-information" :open="!mobileViewport">
                <summary v-if="mobileViewport">计费与完整信息</summary>
                <!-- 详情顺序与 rc35 的 DetailsDialog 一致，基础信息之后按审计、用量、计费和内容分组。 -->
                <div class="detail-overview">
                  <div v-if="detailRow.request_id" class="detail-row"><span class="detail-label">请求ID</span><span class="detail-value detail-mono copyable-value"><button type="button" class="detail-copy-value" @click="copyText(detailRow.request_id)">{{ detailRow.request_id }}<i class="copy-glyph" aria-hidden="true" /></button></span></div>
                  <div v-if="detailRow.upstream_request_id" class="detail-row"><span class="detail-label">上游请求ID</span><span class="detail-value detail-mono copyable-value"><button type="button" class="detail-copy-value" @click="copyText(detailRow.upstream_request_id)">{{ detailRow.upstream_request_id }}<i class="copy-glyph" aria-hidden="true" /></button></span></div>
                  <div v-if="detailRow.channel_id || detailRow.channel" class="detail-row"><span class="detail-label">渠道</span><span class="detail-value detail-mono">{{ (detailRow.channel_id || detailRow.channel) + (detailRow.channel_name && sensitiveVisible ? '（' + detailRow.channel_name + '）' : '') }}</span></div>
                  <div v-if="retryChain(detailRow)" class="detail-row"><span class="detail-label">重试链路</span><span class="detail-value detail-mono">{{ retryChain(detailRow) }}</span></div>
                  <div v-if="isFallback(detailRow)" class="detail-row"><span class="detail-label">Fallback</span><span class="detail-value detail-warning">是<span v-if="fallbackChain(detailRow)" class="detail-fallback-chain">（{{ fallbackChain(detailRow) }}）</span></span></div>
                  <div v-if="detailRow.token_id" class="detail-row"><span class="detail-label">令牌 ID</span><span class="detail-value detail-mono">{{ sensitiveVisible ? detailRow.token_id : '••••' }}</span></div>
                  <div v-if="detailRow.token_name" class="detail-row"><span class="detail-label">令牌</span><span class="detail-value detail-mono">{{ visibleValue(detailRow.token_name) }}</span></div>
                  <div v-if="detailRow.group || textValue(detailRow, 'group')" class="detail-row"><span class="detail-label">分组</span><span class="detail-value detail-mono">{{ detailGroup(detailRow) }}</span></div>
                  <div v-if="detailRow.ip && (isAdmin || isTimingLog(detailRow))" class="detail-row"><span class="detail-label">IP 地址</span><span class="detail-value detail-mono">{{ sensitiveVisible ? detailRow.ip : '••••' }}</span></div>
                   <div v-if="isTimingLog(detailRow) && detailRow.use_time > 0" class="detail-row"><span class="detail-label">响应时间</span><span class="detail-value detail-mono detail-timing"><strong :class="timingClass(durationVariant(detailRow))">{{ detailRow.use_time.toFixed(1) }}s</strong><span v-if="streamFlag(detailRow) === true && firstResponse(detailRow) !== undefined" :class="timingClass(firstResponseVariant(firstResponse(detailRow)))">（首字 {{ firstResponseText(detailRow) }}）</span></span></div>
                 </div>

                 <section v-if="channelAffinityFor(detailRow)" class="detail-section">
                   <h3>渠道亲和性</h3>
                   <div class="detail-section-card channel-affinity-card">
                     <div class="detail-row"><span class="detail-label">状态</span><span class="detail-value detail-success"><span class="channel-affinity-status"><svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M9.937 15.5A2 2 0 0 0 8.5 14.063L2 12l6.5-2.063A2 2 0 0 0 9.937 8.5L12 2l2.063 6.5A2 2 0 0 0 15.5 9.937L22 12l-6.5 2.063a2 2 0 0 0-1.437 1.437L12 22z"/><path d="M20 3v4M22 5h-4"/></svg>已命中</span></span></div>
                     <div v-if="channelAffinityFor(detailRow)?.ruleName" class="detail-row"><span class="detail-label">规则</span><span class="detail-value detail-mono">{{ channelAffinityFor(detailRow)?.ruleName }}</span></div>
                     <div v-if="channelAffinityGroup(channelAffinityFor(detailRow))" class="detail-row"><span class="detail-label">分组</span><span class="detail-value detail-mono">{{ channelAffinitySensitive(channelAffinityGroup(channelAffinityFor(detailRow))) }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.reason" class="detail-row"><span class="detail-label">命中原因</span><span class="detail-value">{{ channelAffinityFor(detailRow)?.reason }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.keySource" class="detail-row"><span class="detail-label">Key 来源</span><span class="detail-value detail-mono">{{ channelAffinityFor(detailRow)?.keySource }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.keyPath" class="detail-row"><span class="detail-label">Key 路径</span><span class="detail-value detail-mono">{{ channelAffinityFor(detailRow)?.keyPath }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.keyKey" class="detail-row"><span class="detail-label">Key 字段</span><span class="detail-value detail-mono">{{ channelAffinityFor(detailRow)?.keyKey }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.keyHint" class="detail-row"><span class="detail-label">Key 摘要</span><span class="detail-value detail-mono">{{ channelAffinitySensitive(channelAffinityFor(detailRow)?.keyHint || '') }}</span></div>
                     <div v-if="channelAffinityFor(detailRow)?.keyFingerprint" class="detail-row"><span class="detail-label">Key 指纹</span><span class="detail-value detail-mono">{{ channelAffinitySensitive(channelAffinityFor(detailRow)?.keyFingerprint || '') }}</span></div>
                   </div>
                 </section>

                 <!-- 金额拆分沿用 rc35 的快照字段，历史日志缺字段时不反推金额。 -->
                <section v-if="(isConsume(detailRow) || detailRow.type === 6) && discountSnapshot(detailRow)" class="detail-section">
                  <h3>{{ textValue(detailRow, 'discount_cost_scope') === 'task_total' ? '任务总费用' : '费用明细' }}</h3>
                  <div class="detail-section-card">
                    <div class="detail-row"><span class="detail-label">折扣前额度</span><span class="detail-value detail-mono">{{ money(discountSnapshot(detailRow)!.before) }}</span></div>
                    <div class="detail-row"><span class="detail-label">折扣后额度</span><span class="detail-value detail-mono">{{ money(discountSnapshot(detailRow)!.after) }}</span></div>
                    <div class="detail-row"><span class="detail-label">节省额度</span><span class="detail-value detail-mono detail-success">{{ money(discountSnapshot(detailRow)!.savings) }}</span></div>
                  </div>
                </section>

                <section v-if="hasRequestConversion(detailRow)" class="detail-section">
                  <h3>请求转换</h3>
                  <div class="detail-section-card">
                    <div v-if="requestPath(detailRow) !== '—'" class="detail-row"><span class="detail-label">路径</span><span class="detail-value detail-mono">{{ requestPath(detailRow) }}</span></div>
                    <div class="detail-row detail-conversion-row"><span class="detail-label">格式</span><span class="detail-value">{{ conversion(detailRow) }}</span><button type="button" class="detail-copy-button" aria-label="复制请求转换" title="复制请求转换" @click="copyText(conversion(detailRow))"><i class="copy-glyph" aria-hidden="true" /></button></div>
                  </div>
                </section>

                <section v-if="isAdmin && quotaSaturationFor(detailRow)" class="detail-section detail-section-danger">
                  <h3>额度已钳制</h3>
                  <div class="detail-section-card">
                    <p class="detail-inline-warning">额度换算触发了边界保护。</p>
                    <div class="detail-row"><span class="detail-label">类型</span><span class="detail-value detail-danger">{{ stringField(quotaSaturationFor(detailRow)?.kind) }}</span></div>
                    <div class="detail-row"><span class="detail-label">原始值</span><span class="detail-value detail-mono">{{ stringField(quotaSaturationFor(detailRow)?.original) }}</span></div>
                    <div class="detail-row"><span class="detail-label">限制值</span><span class="detail-value detail-mono">{{ stringField(quotaSaturationFor(detailRow)?.clamped) }}</span></div>
                    <div class="detail-row"><span class="detail-label">运算</span><span class="detail-value detail-mono">{{ stringField(quotaSaturationFor(detailRow)?.op) }}</span></div>
                  </div>
                </section>

                <section v-if="isAdmin && stringField(adminInfoFor(detailRow)?.reject_reason)" class="detail-section detail-section-danger">
                  <h3>拒绝原因</h3>
                  <div class="detail-section-card"><p class="detail-value multiline detail-danger">{{ stringField(adminInfoFor(detailRow)?.reject_reason) }}</p></div>
                </section>

                <section v-if="isViolation(detailRow)" class="detail-section detail-section-danger">
                  <h3>违规扣费</h3>
                  <div class="detail-section-card">
                    <div v-if="textValue(detailRow, 'violation_fee_code')" class="detail-row"><span class="detail-label">违规代码</span><span class="detail-value detail-mono">{{ textValue(detailRow, 'violation_fee_code') }}</span></div>
                    <div v-if="textValue(detailRow, 'violation_fee_marker')" class="detail-row"><span class="detail-label">违规标记</span><span class="detail-value">{{ textValue(detailRow, 'violation_fee_marker') }}</span></div>
                    <div class="detail-row"><span class="detail-label">费用额度</span><span class="detail-value detail-mono detail-danger">{{ money(numberValue(detailRow, 'fee_quota') ?? detailRow.quota) }}</span></div>
                  </div>
                </section>

                <section v-if="detailRow.type === 6 && (textValue(detailRow, 'task_id') || textValue(detailRow, 'reason'))" class="detail-section">
                  <h3>退款详情</h3>
                  <div class="detail-section-card">
                    <div v-if="textValue(detailRow, 'task_id')" class="detail-row"><span class="detail-label">任务 ID</span><span class="detail-value detail-mono">{{ textValue(detailRow, 'task_id') }}</span></div>
                    <div v-if="textValue(detailRow, 'reason')" class="detail-row"><span class="detail-label">原因</span><span class="detail-value multiline">{{ textValue(detailRow, 'reason') }}</span></div>
                  </div>
                </section>

                <section v-if="isAdmin && taskPluginFor(detailRow)" class="detail-section">
                  <h3>任务插件</h3>
                  <div class="detail-section-card">
                    <div v-if="stringField(taskPluginFor(detailRow)?.key)" class="detail-row"><span class="detail-label">插件键</span><span class="detail-value detail-mono">{{ stringField(taskPluginFor(detailRow)?.key) }}</span></div>
                    <div v-if="stringField(taskPluginFor(detailRow)?.name)" class="detail-row"><span class="detail-label">名称</span><span class="detail-value">{{ stringField(taskPluginFor(detailRow)?.name) }}</span></div>
                    <div v-if="stringField(taskPluginFor(detailRow)?.version)" class="detail-row"><span class="detail-label">版本</span><span class="detail-value detail-mono">{{ stringField(taskPluginFor(detailRow)?.version) }}</span></div>
                    <div v-if="stringField(taskPluginFor(detailRow)?.author)" class="detail-row"><span class="detail-label">作者</span><span class="detail-value">{{ stringField(taskPluginFor(detailRow)?.author) }}</span></div>
                  </div>
                </section>

                <section v-if="isRoot && rootInfoFor(detailRow)" class="detail-section">
                  <h3>Root 诊断</h3>
                  <div class="detail-section-card">
                    <div v-if="rootTaskPluginFor(detailRow) && stringField(rootTaskPluginFor(detailRow)?.api_version)" class="detail-row"><span class="detail-label">API 版本</span><span class="detail-value detail-mono">{{ stringField(rootTaskPluginFor(detailRow)?.api_version) }}</span></div>
                    <div v-if="rootTaskPluginFor(detailRow) && stringField(rootTaskPluginFor(detailRow)?.generation)" class="detail-row"><span class="detail-label">插件运行代次</span><span class="detail-value detail-mono">{{ stringField(rootTaskPluginFor(detailRow)?.generation) }}</span></div>
                    <div v-if="stringField(rootInfoFor(detailRow)?.upstream_task_id)" class="detail-row"><span class="detail-label">上游任务 ID</span><span class="detail-value detail-mono">{{ stringField(rootInfoFor(detailRow)?.upstream_task_id) }}</span></div>
                    <div v-if="stringField(rootInfoFor(detailRow)?.node_name)" class="detail-row"><span class="detail-label">节点名称</span><span class="detail-value detail-mono">{{ stringField(rootInfoFor(detailRow)?.node_name) }}</span></div>
                  </div>
                </section>

                <section v-if="isAdmin && detailRow.type === 1 && (topupAuditFieldsFor(detailRow).length > 0 || !adminInfoFor(detailRow))" class="detail-section">
                  <h3>充值审计信息</h3>
                  <div class="detail-section-card">
                    <div v-for="field in topupAuditFieldsFor(detailRow)" :key="field.label" class="detail-row"><span class="detail-label">{{ field.label }}</span><span class="detail-value detail-mono">{{ field.value }}</span></div>
                    <p v-if="!adminInfoFor(detailRow)" class="detail-inline-warning">历史记录没有审计快照，无法补写；新充值记录会记录服务器、回调和版本信息。</p>
                  </div>
                </section>

                <section v-if="detailRow.type === 1 && operationTextFor(detailRow)" class="detail-section">
                  <h3>额度调整详情</h3>
                  <div class="detail-section-card"><div class="detail-row"><span class="detail-label">操作</span><span class="detail-value multiline">{{ operationTextFor(detailRow) }}</span></div></div>
                </section>

                <div v-if="manageOperatorFor(detailRow)" class="detail-row"><span class="detail-label">操作管理员</span><span class="detail-value detail-mono">{{ manageOperatorFor(detailRow) }}</span></div>

                <section v-if="isAdmin && detailRow.type === 3 && (operationTextFor(detailRow) || auditRequestFor(detailRow) || auditResultFor(detailRow))" class="detail-section">
                  <h3>操作审计信息</h3>
                  <div class="detail-section-card">
                    <div v-if="operationTextFor(detailRow)" class="detail-row"><span class="detail-label">操作</span><span class="detail-value multiline">{{ operationTextFor(detailRow) }}</span></div>
                    <div v-if="operationAuthMethodFor(detailRow)" class="detail-row"><span class="detail-label">认证方式</span><span class="detail-value">{{ operationAuthMethodFor(detailRow) }}</span></div>
                    <div v-if="changedFieldsFor(detailRow)" class="detail-row"><span class="detail-label">变更字段</span><span class="detail-value">{{ changedFieldsFor(detailRow) }}</span></div>
                    <div v-if="auditRequestFor(detailRow)" class="detail-row"><span class="detail-label">请求</span><span class="detail-value detail-mono">{{ auditRequestFor(detailRow) }}</span></div>
                    <div v-if="auditResultFor(detailRow)" class="detail-row"><span class="detail-label">结果</span><span class="detail-value detail-mono">{{ auditResultFor(detailRow) }}</span></div>
                  </div>
                </section>

                <section v-if="loginFieldsFor(detailRow).length > 0" class="detail-section">
                  <h3>登录信息</h3>
                  <div class="detail-section-card">
                    <div v-if="operationTextFor(detailRow)" class="detail-row"><span class="detail-label">操作</span><span class="detail-value">{{ operationTextFor(detailRow) }}</span></div>
                    <div v-for="field in loginFieldsFor(detailRow)" :key="field.label" class="detail-row"><span class="detail-label">{{ field.label }}</span><span class="detail-value detail-mono">{{ field.value }}</span></div>
                  </div>
                </section>

                <section v-if="booleanField(first(detailRow, 'ws')) || booleanField(first(detailRow, 'audio'))" class="detail-section">
                  <h3>语音 Token</h3>
                  <div class="detail-section-card">
                    <div v-if="numberValue(detailRow, 'audio_input') !== undefined && numberValue(detailRow, 'audio_input')! > 0" class="detail-row"><span class="detail-label">音频输入</span><span class="detail-value detail-mono">{{ formatNumber(numberValue(detailRow, 'audio_input')) }}</span></div>
                    <div v-if="numberValue(detailRow, 'audio_output') !== undefined && numberValue(detailRow, 'audio_output')! > 0" class="detail-row"><span class="detail-label">音频输出</span><span class="detail-value detail-mono">{{ formatNumber(numberValue(detailRow, 'audio_output')) }}</span></div>
                    <div v-if="numberValue(detailRow, 'text_input') !== undefined && numberValue(detailRow, 'text_input')! > 0" class="detail-row"><span class="detail-label">文本输入</span><span class="detail-value detail-mono">{{ formatNumber(numberValue(detailRow, 'text_input')) }}</span></div>
                    <div v-if="numberValue(detailRow, 'text_output') !== undefined && numberValue(detailRow, 'text_output')! > 0" class="detail-row"><span class="detail-label">文本输出</span><span class="detail-value detail-mono">{{ formatNumber(numberValue(detailRow, 'text_output')) }}</span></div>
                  </div>
                </section>

                <div v-if="textValue(detailRow, 'reasoning_effort')" class="detail-row"><span class="detail-label">推理强度</span><span class="detail-value"><span class="detail-tag" :class="'detail-tag-' + textValue(detailRow, 'reasoning_effort').toLowerCase()">{{ textValue(detailRow, 'reasoning_effort') }}</span></span></div>
                <div v-if="booleanField(first(detailRow, 'is_system_prompt_overwritten'))" class="detail-row"><span class="detail-label">系统提示词</span><span class="detail-value"><span class="detail-tag detail-tag-warning">已覆盖</span></span></div>

                <section v-if="modelMapping(detailRow) && booleanField(first(detailRow, 'is_model_mapped'))" class="detail-section">
                  <h3>模型映射</h3>
                  <div class="detail-section-card">
                    <div class="detail-row"><span class="detail-label">请求模型</span><span class="detail-value detail-mono">{{ detailRow.model_name }}</span></div>
                    <div class="detail-row"><span class="detail-label">实际模型</span><span class="detail-value detail-mono">{{ modelMapping(detailRow) }}</span></div>
                  </div>
                </section>

                <section v-if="isDisplayableLog(detailRow) && (detailRow.prompt_tokens > 0 || detailRow.completion_tokens > 0 || cacheReadTokens(detailRow) > 0 || cacheWriteTokens(detailRow) > 0 || cacheWrite5mTokens(detailRow) > 0 || cacheWrite1hTokens(detailRow) > 0 || (numberValue(detailRow, 'image_output') || 0) > 0)" class="detail-section">
                  <h3>Token 明细</h3>
                  <div class="detail-section-card">
                    <div v-if="detailRow.prompt_tokens > 0" class="detail-row"><span class="detail-label">输入 Token</span><span class="detail-value detail-mono">{{ formatNumber(detailRow.prompt_tokens) }}</span></div>
                    <div v-if="detailRow.completion_tokens > 0" class="detail-row"><span class="detail-label">输出 Token</span><span class="detail-value detail-mono">{{ formatNumber(detailRow.completion_tokens) }}</span></div>
                    <div v-if="cacheReadTokens(detailRow) > 0" class="detail-row"><span class="detail-label">缓存读取</span><span class="detail-value detail-mono">{{ formatNumber(cacheReadTokens(detailRow)) }}</span></div>
                    <div v-if="cacheWriteTokens(detailRow) > 0 && cacheWrite5mTokens(detailRow) === 0 && cacheWrite1hTokens(detailRow) === 0" class="detail-row"><span class="detail-label">缓存写入</span><span class="detail-value detail-mono">{{ formatNumber(cacheWriteTokens(detailRow)) }}</span></div>
                    <div v-if="cacheWrite5mTokens(detailRow) > 0" class="detail-row"><span class="detail-label">缓存写入（5 分钟）</span><span class="detail-value detail-mono">{{ formatNumber(cacheWrite5mTokens(detailRow)) }}</span></div>
                    <div v-if="cacheWrite1hTokens(detailRow) > 0" class="detail-row"><span class="detail-label">缓存写入（1 小时）</span><span class="detail-value detail-mono">{{ formatNumber(cacheWrite1hTokens(detailRow)) }}</span></div>
                    <div v-if="numberValue(detailRow, 'image_output') !== undefined && numberValue(detailRow, 'image_output')! > 0" class="detail-row"><span class="detail-label">图像 Token</span><span class="detail-value detail-mono">{{ formatNumber(numberValue(detailRow, 'image_output')) }}</span></div>
                  </div>
                </section>

                <section v-if="isConsume(detailRow) && !isViolation(detailRow)" class="detail-section">
                  <h3>计费详情</h3>
                  <div class="detail-section-card">
                    <div class="detail-row"><span class="detail-label">计费模式</span><span class="detail-value">{{ billingMode(detailRow) }}</span></div>
                    <div v-if="isPerCallBilling(numberValue(detailRow, 'model_price'))" class="detail-row"><span class="detail-label">模型单价</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_price')!) }}</span></div>
                    <div v-else-if="!isTieredBilling(detailRow) && numberValue(detailRow, 'model_ratio') !== undefined" class="detail-row"><span class="detail-label">输入</span><span class="detail-value detail-mono">{{ detailInputPrice(detailRow) }}</span></div>
                    <div v-if="!isTieredBilling(detailRow) && !isPerCallBilling(numberValue(detailRow, 'model_price')) && numberValue(detailRow, 'model_ratio') !== undefined && numberValue(detailRow, 'completion_ratio') !== undefined" class="detail-row"><span class="detail-label">输出</span><span class="detail-value detail-mono">{{ detailOutputPrice(detailRow) }}</span></div>
                    <div v-if="billingDiscountLabel(detailRow)" class="detail-row"><span class="detail-label">模型折扣</span><span class="detail-value detail-success">{{ billingDiscountLabel(detailRow) }}</span></div>
                    <div v-if="effectiveGroupRatio(detailRow) !== undefined" class="detail-row"><span class="detail-label">{{ groupRatioLabel(detailRow) }}</span><span class="detail-value detail-mono">{{ detailGroupRatioText(detailRow) }}</span></div>
                    <div v-if="!isTieredBilling(detailRow) && booleanField(first(detailRow, 'claude')) && cacheReadTokens(detailRow) > 0 && numberValue(detailRow, 'cache_ratio') !== undefined" class="detail-row"><span class="detail-label">缓存读取</span><span class="detail-value detail-mono">{{ detailCacheReadPrice(detailRow) }}</span></div>
                    <div v-if="!isTieredBilling(detailRow) && booleanField(first(detailRow, 'claude')) && cacheWriteTokens(detailRow) > 0 && numberValue(detailRow, 'model_ratio') !== undefined && numberValue(detailRow, 'cache_creation_ratio') !== undefined" class="detail-row"><span class="detail-label">缓存创建</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'cache_creation_ratio')!) }} / 1M tokens</span></div>
                    <div v-if="!isTieredBilling(detailRow) && booleanField(first(detailRow, 'claude')) && cacheWrite5mTokens(detailRow) > 0 && numberValue(detailRow, 'model_ratio') !== undefined && numberValue(detailRow, 'cache_creation_ratio_5m') !== undefined" class="detail-row"><span class="detail-label">缓存创建 (5m)</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'cache_creation_ratio_5m')!) }} / 1M tokens</span></div>
                    <div v-if="!isTieredBilling(detailRow) && booleanField(first(detailRow, 'claude')) && cacheWrite1hTokens(detailRow) > 0 && numberValue(detailRow, 'model_ratio') !== undefined && numberValue(detailRow, 'cache_creation_ratio_1h') !== undefined" class="detail-row"><span class="detail-label">缓存创建 (1h)</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'cache_creation_ratio_1h')!) }} / 1M tokens</span></div>
                    <div v-if="!isTieredBilling(detailRow) && numberValue(detailRow, 'audio_ratio') !== undefined && numberValue(detailRow, 'audio_ratio') !== 1 && numberValue(detailRow, 'model_ratio') !== undefined" class="detail-row"><span class="detail-label">音频输入</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'audio_ratio')!) }} / 1M tokens</span></div>
                    <div v-if="!isTieredBilling(detailRow) && numberValue(detailRow, 'audio_completion_ratio') !== undefined && numberValue(detailRow, 'audio_completion_ratio') !== 1 && numberValue(detailRow, 'model_ratio') !== undefined" class="detail-row"><span class="detail-label">音频输出</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'audio_completion_ratio')!) }} / 1M tokens</span></div>
                    <div v-if="!isTieredBilling(detailRow) && numberValue(detailRow, 'image_ratio') !== undefined && numberValue(detailRow, 'image_ratio') !== 1 && numberValue(detailRow, 'model_ratio') !== undefined" class="detail-row"><span class="detail-label">图片输入</span><span class="detail-value detail-mono">{{ formatModelPrice(detailRow, numberValue(detailRow, 'model_ratio')! * 2 * numberValue(detailRow, 'image_ratio')!) }} / 1M tokens</span></div>
                    <div v-for="fee in allToolSurchargesFor(detailRow)" :key="fee.name + fee.count + fee.price" class="detail-row"><span class="detail-label">{{ fee.name }}</span><span class="detail-value detail-mono">{{ fee.count }} × {{ billingPrice(fee.price) }}</span></div>
                    <div v-if="booleanField(first(detailRow, 'audio_input_seperate_price')) && numberValue(detailRow, 'audio_input_price') !== undefined" class="detail-row"><span class="detail-label">音频输入价格</span><span class="detail-value detail-mono">{{ billingPrice(numberValue(detailRow, 'audio_input_price')!) }}</span></div>
                    <div v-if="isAdmin" class="detail-row"><span class="detail-label">计费路径</span><span class="detail-value">{{ billingPath(detailRow) }}</span></div>
                    <div class="detail-row"><span class="detail-label">总费用</span><span class="detail-value detail-mono detail-total-cost">{{ money(detailRow.quota) }}</span></div>
                  </div>
                </section>

                <section v-if="isTieredBilling(detailRow)" class="detail-section">
                  <h3>动态计费</h3>
                  <div class="detail-section-card">
                    <div class="detail-row"><span class="detail-label">计费模式</span><span class="detail-value">动态计费</span></div>
                    <div v-if="dynamicExpression(detailRow)" class="detail-row"><span class="detail-label">计费表达式</span><code class="detail-value detail-mono dynamic-expression">{{ dynamicExpression(detailRow) }}</code></div>
                    <div class="detail-row"><span class="detail-label">命中阶梯</span><span class="detail-value detail-mono">{{ textValue(detailRow, 'matched_tier') || '未匹配到阶梯' }}</span></div>
                    <div v-if="dynamicTiersFor(detailRow).length && dynamicPriceFieldsFor(detailRow).length" class="dynamic-table-wrap">
                      <div class="detail-subheading">分档价格表</div>
                      <table class="dynamic-tier-table">
                        <thead><tr><th>阶梯</th><th v-for="field in dynamicPriceFieldsFor(detailRow)" :key="field.key">{{ field.label }}</th></tr></thead>
                        <tbody>
                          <tr v-for="tier in dynamicTiersFor(detailRow)" :key="tier.label" :class="{ 'is-matched': dynamicTierMatched(tier, textValue(detailRow, 'matched_tier')) }">
                            <td><span class="detail-tier-label">{{ tier.label || '默认' }}</span><span v-if="dynamicTierMatched(tier, textValue(detailRow, 'matched_tier'))" class="detail-tier-hit">命中</span><div v-if="tier.conditions.length" class="detail-condition">{{ tier.conditions.map(formatDynamicCondition).join(' && ') }}</div></td>
                            <td v-for="field in dynamicPriceFieldsFor(detailRow)" :key="field.key" class="detail-mono">{{ dynamicTierPriceText(detailRow, tier, field.key) }}</td>
                          </tr>
                        </tbody>
                      </table>
                    </div>
                    <div v-else-if="dynamicExpression(detailRow)" class="detail-raw-expression">无法解析为结构化价格，保留原始表达式。</div>
                    <div v-if="dynamicRulesFor(detailRow).length" class="detail-subheading">条件乘数</div>
                    <div v-for="rule in dynamicRulesFor(detailRow)" :key="rule.condition + rule.multiplier" class="detail-rule-row" :class="{ 'is-matched': rule.matched }"><span>{{ rule.condition }}</span><strong>{{ rule.multiplier }}x<span v-if="rule.matched"> · 已命中</span></strong></div>
                    <div v-if="dynamicUsageFactsFor(detailRow).length" class="detail-subheading">用量参数</div>
                    <div v-for="fact in dynamicUsageFactsFor(detailRow)" :key="fact.key" class="detail-row"><span class="detail-label">{{ fact.key }}</span><span class="detail-value detail-mono">{{ fact.value }}</span></div>
                  </div>
                </section>

                <div v-if="isAdmin && !isConsume(detailRow) && detailRow.type !== 6 && adminInfoFor(detailRow)" class="detail-row"><span class="detail-label">计费路径</span><span class="detail-value">{{ billingPath(detailRow) }}</span></div>

                <section v-if="streamStatusHasError(detailRow)" class="detail-section">
                  <h3>流状态</h3>
                  <div class="detail-section-card">
                    <div class="detail-row"><span class="detail-label">状态</span><span class="detail-value detail-danger">{{ streamStatusValue(detailRow) }}</span></div>
                    <div v-if="streamEndReasonFor(detailRow)" class="detail-row"><span class="detail-label">结束原因</span><span class="detail-value">{{ streamEndReasonFor(detailRow) }}</span></div>
                    <div v-if="streamErrorCountFor(detailRow) > 0" class="detail-row"><span class="detail-label">软错误</span><span class="detail-value detail-mono">{{ streamErrorCountFor(detailRow) }}</span></div>
                    <div v-if="streamEndErrorFor(detailRow)" class="detail-row"><span class="detail-label">结束错误</span><span class="detail-value detail-danger multiline">{{ streamEndErrorFor(detailRow) }}</span></div>
                    <pre v-if="streamErrorsFor(detailRow).length" class="detail-error-list">{{ streamErrorsFor(detailRow).join('\n') }}</pre>
                  </div>
                </section>

                <section v-if="subscriptionFieldsFor(detailRow).length" class="detail-section">
                  <h3>订阅计费</h3>
                  <div class="detail-section-card"><div v-for="field in subscriptionFieldsFor(detailRow)" :key="field.label" class="detail-row"><span class="detail-label">{{ field.label }}</span><span class="detail-value detail-mono">{{ field.value }}</span></div></div>
                </section>

                <section v-if="parameterOverridesFor(detailRow).length" class="detail-section">
                  <h3>参数覆盖</h3>
                  <div class="detail-section-card"><div v-for="override in parameterOverridesFor(detailRow)" :key="override.action + override.content" class="detail-override-line"><span class="detail-tag">{{ parameterActionLabel(override.action) }}</span><span class="detail-mono multiline">{{ override.content }}</span></div></div>
                </section>

                <section v-if="detailContentFor(detailRow)" class="detail-section">
                  <h3>{{ detailRow.type === 5 ? '错误详情' : '内容' }}</h3>
                  <div class="detail-section-card detail-content-card"><button type="button" class="detail-copy-button" aria-label="复制内容" title="复制内容" @click="copyText(detailContentFor(detailRow))"><i class="copy-glyph" aria-hidden="true" /></button><div class="detail-value multiline detail-content-value">{{ detailContentFor(detailRow) }}</div></div>
                </section>
                </details>
                </div>
              </div>
            </div>
            <footer v-if="mobileViewport" class="mobile-detail-footer"><button type="button" :disabled="!detailRow.request_id || !sensitiveVisible" @click="copyText(detailRow.request_id)">复制请求 ID</button></footer>
          </section>
        </div>
      </Teleport>
    </div>
  </AppShell>
      <!-- 渠道亲和性沿用 New API 的独立信息弹窗，避免点击角标时打开展开内容过多的日志详情。 -->
      <Teleport to="body">
        <div v-if="affinityTarget" v-show="affinityOpen" class="affinity-dialog-backdrop" @mousedown.self="closeAffinity">
          <section class="affinity-dialog" role="dialog" aria-modal="true" aria-labelledby="readonly-log-affinity-title" aria-describedby="readonly-log-affinity-description" @mousedown.stop>
            <header class="detail-dialog-header affinity-dialog-header">
              <div class="detail-dialog-title">
                <span class="affinity-dialog-icon" aria-hidden="true"><svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11.017 2.814a1 1 0 0 0 1.966 0l1.051 5.558a2 2 0 0 0 1.594 1.594l5.558 1.051a1 1 0 0 1 0 1.966l-5.558 1.051a2 2 0 0 1-1.594 1.594l-1.051 5.558a1 1 0 0 1-1.966 0l-1.051-5.558a2 2 0 0 1-1.594-1.594l-5.558-1.051a1 1 0 0 1 0-1.966l5.558-1.051a2 2 0 0 1 1.594-1.594z"/><path d="M20 2v4"/><path d="M22 4h-4"/><circle cx="4" cy="20" r="2"/></svg></span>
                <h2 id="readonly-log-affinity-title">渠道亲和性</h2>
                <span class="affinity-dialog-status">已命中</span>
              </div>
              <button ref="affinityCloseButton" type="button" class="detail-dialog-close" aria-label="关闭渠道亲和性详情" title="关闭" @click="closeAffinity"><span class="detail-close-glyph" aria-hidden="true" /></button>
            </header>
            <p id="readonly-log-affinity-description" class="affinity-dialog-hint">该请求命中了渠道亲和性规则。</p>
            <div class="affinity-dialog-body">
              <div class="affinity-dialog-channel"><span>渠道</span><strong>#{{ affinityTarget.channelID }}</strong><span v-if="affinityTarget.channelName">{{ affinityTarget.channelName }}</span></div>
              <div class="affinity-dialog-card">
                <div v-if="affinityTarget.affinity.ruleName" class="affinity-dialog-row"><span>规则</span><code>{{ affinityTarget.affinity.ruleName }}</code></div>
                <div v-if="channelAffinityGroup(affinityTarget.affinity)" class="affinity-dialog-row"><span>分组</span><code>{{ channelAffinitySensitive(channelAffinityGroup(affinityTarget.affinity)) }}</code></div>
                <div v-if="affinityTarget.affinity.reason" class="affinity-dialog-row"><span>命中原因</span><code>{{ affinityTarget.affinity.reason }}</code></div>
                <div v-if="affinityTarget.affinity.keySource" class="affinity-dialog-row"><span>Key 来源</span><code>{{ affinityTarget.affinity.keySource }}</code></div>
                <div v-if="affinityTarget.affinity.keyPath" class="affinity-dialog-row"><span>Key 路径</span><code>{{ affinityTarget.affinity.keyPath }}</code></div>
                <div v-if="affinityTarget.affinity.keyKey" class="affinity-dialog-row"><span>Key 字段</span><code>{{ affinityTarget.affinity.keyKey }}</code></div>
                <div v-if="affinityTarget.affinity.keyHint" class="affinity-dialog-row"><span>Key 摘要</span><code>{{ channelAffinitySensitive(affinityTarget.affinity.keyHint) }}</code></div>
                <div v-if="affinityTarget.affinity.keyFingerprint" class="affinity-dialog-row"><span>Key 指纹</span><code>{{ channelAffinitySensitive(affinityTarget.affinity.keyFingerprint) }}</code></div>
              </div>
            </div>
          </section>
        </div>
      </Teleport>
      <!-- 重试提示独立于表格滚动层，鼠标进入整个渠道单元格即可查看。 -->
      <Teleport to="body">
        <div v-if="retryHover" ref="retryHoverElement" class="retry-hover-card" :class="{ 'has-affinity': retryHover.affinity }" role="tooltip" :style="retryHoverStyle">
          <div v-if="retryHover.hasRetry" class="retry-hover-line"><strong v-if="retryHover.firstAttempt">首次尝试：</strong><strong v-else-if="retryHover.retryCount">重试{{ retryHover.retryCount }}次：</strong><strong v-else>Fallback：</strong><span v-if="retryHover.steps.length" class="retry-hover-chain"><template v-for="step in retryHover.steps" :key="step.position"><i v-if="step.position > 1" class="retry-hover-separator" aria-hidden="true">→</i><b class="retry-hover-step" :class="step.current ? `is-${step.tone}` : ''">{{ step.channel }}</b></template></span><span v-else>{{ retryHover.chain }}</span></div>
          <div v-if="retryHover.affinity" class="retry-hover-affinity">
            <strong>渠道亲和性命中</strong>
            <span v-if="retryHover.affinity.ruleName">规则：{{ retryHover.affinity.ruleName }}</span>
            <span v-if="channelAffinityGroup(retryHover.affinity)">分组：{{ channelAffinitySensitive(channelAffinityGroup(retryHover.affinity)) }}</span>
          </div>
        </div>
      </Teleport>

</template>

<style scoped>
/* rc35 使用日志布局基线（色彩随全局主题）：低对比边框、紧凑控件和可扫描的数字列。 */
:global(body.ct-rc35-logs-theme) {
  min-width: 0 !important;
  overflow-x: hidden;
  background: var(--ct-bg);
  color: var(--ct-ink);
}
:global(body.ct-rc35-logs-theme .shell .workspace),
:global(body.ct-rc35-logs-theme .shell .content) { background: var(--ct-bg); }
:global(body.ct-rc35-logs-theme .shell .topbar) {
  background: var(--ct-surface);
  border-bottom-color: var(--ct-line);
  color: var(--ct-ink);
}
:global(body.ct-rc35-logs-theme .shell .topbar h1),
:global(body.ct-rc35-logs-theme .shell .topbar .user) { color: var(--ct-ink); }
:global(body.ct-rc35-logs-theme .shell .topbar .el-select__wrapper),
:global(body.ct-rc35-logs-theme .shell .topbar .fixed-site) {
  background: var(--ct-surface);
  border-color: var(--ct-line-strong);
  color: var(--ct-ink-2);
}

/* rc35 的三种耗时状态直接挂在根节点，主题切换时不会被 scoped 容器变量覆盖。 */
:global(:root) {
  --rc35-timing-success: oklch(0.596 0.145 163.225);
  --rc35-timing-warning: oklch(0.681 0.162 75.834);
  --rc35-timing-danger: oklch(0.577 0.245 27.325);
}
:global(:root[data-theme="dark"]) {
  /* 暗色模式同步 NewAPI rc35 的明度和色相，保证三种状态可直接对照。 */
  --rc35-timing-success: oklch(0.696 0.17 162.48);
  --rc35-timing-warning: oklch(0.769 0.188 70.08);
  --rc35-timing-danger: oklch(0.704 0.191 22.216);
}

.logs-page {
  --rc35-bg: var(--ct-bg);
  --rc35-surface: var(--ct-surface);
  --rc35-surface-2: var(--ct-surface-2);
  --rc35-line: var(--ct-line);
  --rc35-line-strong: var(--ct-line-strong);
  --rc35-ink: var(--ct-ink);
  --rc35-ink-2: var(--ct-ink-2);
  --rc35-ink-3: var(--ct-ink-3);
  --rc35-accent-weak: var(--ct-accent-weak);
  --rc35-blue: var(--ct-accent);
  --rc35-green: var(--ct-ok);
  --rc35-amber: var(--ct-warn);
  display: flex;
  min-height: calc(100vh - 88px);
  flex-direction: column;
  gap: 12px;
  margin: -12px -16px -24px;
  padding: 12px 8px 18px;
  background: var(--rc35-bg);
  color: var(--rc35-ink);
}
.logs-page > :deep(.el-alert) { flex: none; }
.logs-page > :deep(.el-alert),
.logs-page > :deep(.el-alert .el-alert__title),
.logs-page > :deep(.el-alert .el-alert__description) { color: var(--rc35-ink-2); }
.logs-page > :deep(.el-alert) {
  border-color: var(--rc35-line);
  background: var(--rc35-surface);
}

/* 工具栏按 rc35 的“主筛选、统计与操作”两层组织；低频筛选器可展开查看。 */
.logs-toolbar {
  flex: none;
  padding: 12px 14px 10px;
  border: 1px solid var(--rc35-line);
  border-radius: 8px;
  background: var(--rc35-surface);
  box-shadow: 0 1px 2px rgba(16, 24, 40, .05);
}
.toolbar-primary { display: block; }
.primary-filters {
  display: grid;
  min-width: 0;
  gap: 8px;
}
.filter-row {
  display: flex;
  min-width: 0;
  /* 行间距由 primary-filters 的 gap 统一控制，避免叠加全局下边距。 */
  margin: 0;
  flex-wrap: nowrap;
  align-items: center;
  gap: 8px;
  overflow-x: auto;
  overflow-y: hidden;
  padding-bottom: 2px;
  scrollbar-width: thin;
}
.filter-time {
  width: 294px;
  min-width: 280px;
  max-width: 294px;
  flex: 0 0 294px;
}
/* 日期文本只需要紧凑固定宽度，避免 flex 空白吞掉主筛选行空间。 */
.logs-toolbar :deep(.filter-time.compact-date-range) {
  width: 294px !important;
  min-width: 280px !important;
  max-width: 294px !important;
  flex: 0 0 294px !important;
}
.filter-username { width: 145px; min-width: 128px; flex: 0 1 145px; }
.filter-channel { width: 125px; min-width: 112px; flex: 0 1 125px; }
.filter-request { width: 165px; min-width: 145px; flex: 0 1 165px; }
.filter-model, .filter-group { width: 150px; min-width: 132px; flex: 0 1 150px; }
.filter-token, .filter-upstream { width: 184px; min-width: 156px; flex: 0 1 184px; }
.filter-status-code { width: 112px; min-width: 100px; flex: 0 1 112px; }
.filter-row-primary .filter-username { width: auto; flex: 1 1 145px; }
.filter-row-primary .filter-channel { width: auto; flex: 1 1 125px; }
.filter-row-primary .filter-request { width: auto; flex: 1 1 165px; }
.filter-row-primary .filter-model { width: auto; flex: 1 1 150px; }
.filter-row-primary .filter-upstream { width: auto; flex: 1 1 184px; }
.filter-expand-action {
  display: inline-flex;
  height: 34px;
  flex: 0 0 auto;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 7px;
  background: transparent;
  color: var(--rc35-ink-2);
  cursor: pointer;
  font-size: 12px;
  white-space: nowrap;
}
.filter-expand-action:hover,
.filter-expand-action:focus-visible {
  border-color: var(--rc35-blue);
  background: var(--rc35-surface-2);
  color: var(--rc35-ink);
  outline: none;
}
.filter-expand-action .el-icon { font-size: 14px; }
.filter-count {
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: var(--rc35-accent-weak);
  color: var(--rc35-blue);
  font-size: 11px;
  line-height: 16px;
  text-align: center;
}
.logs-toolbar :deep(.el-input__wrapper),
.logs-toolbar :deep(.el-select__wrapper) {
  min-height: 34px;
  border-radius: 8px;
  background: var(--rc35-surface-2);
  box-shadow: 0 0 0 1px var(--rc35-line) inset;
  color: var(--rc35-ink);
  transition: box-shadow .16s ease, background .16s ease;
}
.logs-toolbar :deep(.el-input__inner) { color: var(--rc35-ink); }
.logs-toolbar :deep(.el-input__inner::placeholder) { color: var(--rc35-ink-3); }
.logs-toolbar :deep(.el-input__prefix),
.logs-toolbar :deep(.el-input__suffix) { color: var(--rc35-ink-3); }
.logs-toolbar :deep(.el-input__wrapper:hover),
.logs-toolbar :deep(.el-select__wrapper:hover) {
  box-shadow: 0 0 0 1px var(--rc35-line-strong) inset;
}
.logs-toolbar :deep(.is-focus) { box-shadow: 0 0 0 1px var(--rc35-blue) inset !important; }

/* 统计块和操作块共用一行，类型选择按需求放在敏感字段按钮之前。 */
.toolbar-meta {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-top: 10px;
  padding-top: 10px;
  border-top: 1px solid var(--rc35-line);
}
.stat-badges { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.stat-badge {
  display: inline-flex;
  height: 28px;
  align-items: center;
  gap: 7px;
  padding: 0 10px;
  border: 1px solid var(--rc35-line);
  border-radius: 6px;
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-2);
  font-size: 12px;
  white-space: nowrap;
}
.stat-badge i { width: 3px; height: 15px; border-radius: 2px; }
.stat-badge strong {
  color: var(--rc35-ink);
  font: 600 12px ui-monospace, SFMono-Regular, Consolas, monospace;
  font-variant-numeric: tabular-nums;
}
.accent-sky { background: var(--ct-primary-solid); }
.accent-rose { background: var(--ct-danger-solid); }
.accent-slate { background: var(--ct-line-strong); }
.toolbar-actions {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  margin-left: auto;
}
.action-type { width: 122px; flex: 0 0 122px; }
.empty-output-toggle {
  display: inline-flex;
  height: 32px;
  align-items: center;
  gap: 5px;
  padding: 0 7px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 6px;
  color: var(--rc35-ink-2);
  cursor: pointer;
  font-size: 12px;
  white-space: nowrap;
}
.empty-output-toggle:hover { background: var(--rc35-surface-2); color: var(--rc35-ink); }
.empty-output-toggle input { width: 14px; height: 14px; margin: 0; accent-color: var(--rc35-blue); }
.empty-output-toggle:has(input:disabled) { cursor: not-allowed; opacity: .55; }
.icon-action, .secondary-action, .primary-action {
  display: inline-flex;
  height: 32px;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border-radius: 6px;
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  white-space: nowrap;
}
.icon-action {
  width: 32px;
  flex: 0 0 32px;
  border: 0;
  background: transparent;
  color: var(--rc35-ink-3);
}
.icon-action:hover, .secondary-action:hover {
  background: var(--rc35-surface-2);
  color: var(--rc35-ink);
}
.secondary-action {
  padding: 0 10px;
  border: 1px solid var(--rc35-line-strong);
  background: var(--ct-surface);
  color: var(--rc35-ink-2);
}
.secondary-action:disabled {
  cursor: not-allowed;
  opacity: .45;
}
.primary-action {
  padding: 0 12px;
  border: 1px solid var(--rc35-blue);
  background: var(--ct-primary-solid);
  color: var(--ct-on-solid);
  box-shadow: none;
}
.primary-action:hover { background: var(--ct-primary-solid); }
.primary-action:disabled { cursor: wait; opacity: .62; }

/* 列按内容确定宽度，可选列隐藏后仍保持各自语义样式。 */
.logs-table-shell {
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--rc35-line);
  border-radius: 8px;
  background: var(--rc35-surface);
  box-shadow: 0 1px 2px rgba(16, 24, 40, .05);
}
/* 后台重置期间保留旧结果，仅用轻微透明度提示数据正在更新。 */
.logs-table-shell.is-background-refreshing .desktop-table,
.logs-table-shell.is-background-refreshing .mobile-log-list {
  opacity: .78;
  transition: opacity .16s ease;
}
/* 表格使用内容驱动的最小宽度，视口不足时由这一层提供底部横向滚动条。 */
.desktop-table {
  height: 100%;
  min-width: 0;
  min-height: 430px;
  overflow: auto;
  scrollbar-width: thin;
}

/* 桌面端把日志页锁定在壳层视口内，长列表只在表格区域滚动。 */
@media (min-width: 761px) {
  .logs-page {
    height: calc(100vh - 52px);
    min-height: 0;
    overflow: hidden;
  }
  .logs-table-shell { flex: 1 1 0; }
  .desktop-table { min-height: 0; }
}
.logs-table {
  width: max-content;
  min-width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  table-layout: auto;
  font-size: 13px;
}
.logs-table th {
  position: sticky;
  top: 0;
  z-index: 1;
  height: 40px;
  padding: 0 10px;
  border-bottom: 1px solid var(--rc35-line);
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-2);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0;
  text-align: left;
  white-space: nowrap;
}
.logs-table td {
  height: 62px;
  padding: 8px 10px;
  border-bottom: 1px solid var(--rc35-line);
  color: var(--rc35-ink);
  vertical-align: middle;
}
.logs-table th.col-time, .logs-table td.col-time { min-width: 132px; }
.logs-table th.col-channel, .logs-table td.col-channel { min-width: 112px; }
.logs-table th.col-user, .logs-table td.col-user { min-width: 124px; }
.logs-table th.col-token, .logs-table td.col-token { min-width: 146px; }
.logs-table th.col-model, .logs-table td.col-model { min-width: 150px; }
.logs-table th.col-stream, .logs-table td.col-stream { min-width: 62px; }
.logs-table th.col-tokens, .logs-table td.col-tokens { min-width: 110px; }
.logs-table th.col-quota, .logs-table td.col-quota { min-width: 86px; }
.logs-table th.col-timing, .logs-table td.col-timing { min-width: 96px; }
.logs-table th.col-details, .logs-table td.col-details { width: 160px; min-width: 160px; max-width: 160px; box-sizing: border-box; }
/* 保留内容自适应与横向滚动，但限制异常长名称和错误摘要对整表宽度的影响。 */
.logs-table .channel-cell, .logs-table .user-cell { max-width: 260px; }
.logs-table .channel-cell .cell-secondary { max-width: 100%; }
.logs-table .token-cell { max-width: 280px; }
.logs-table .token-badge > span { overflow: hidden; text-overflow: ellipsis; }
.logs-table .model-cell { max-width: 320px; }
.logs-table .details-button { width: 140px; max-width: 140px; }
.log-row { cursor: default; transition: background-color .14s ease; }
.log-row:hover td { background: var(--ct-accent-weak); }
.log-row.row-error td { background: color-mix(in srgb, var(--ct-crit-weak) 55%, var(--ct-surface)); }
.log-row.row-error:hover td { background: color-mix(in srgb, var(--ct-crit-weak) 80%, var(--ct-surface)); }
.log-row.row-refund td { background: color-mix(in srgb, var(--ct-accent-weak) 55%, var(--ct-surface)); }
.log-row.row-refund:hover td { background: color-mix(in srgb, var(--ct-accent-weak) 80%, var(--ct-surface)); }

/* 各配色共用同一阅读层级：主数据 13px，辅助信息至少 12px。 */
.time-cell, .token-cell, .model-cell, .stream-cell, .tokens-cell {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 3px;
}
.time-text {
  color: var(--rc35-ink);
  font: 500 12px/20px ui-monospace, SFMono-Regular, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.time-copy-button {
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  align-items: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  cursor: pointer;
  font: inherit;
  text-align: left;
}
.time-copy-button:focus-visible { outline: 2px solid var(--rc35-blue); outline-offset: 2px; }
.status-badge {
  display: inline;
  width: fit-content;
  color: var(--rc35-ink-2);
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
  white-space: nowrap;
}
.status-topup { color: var(--ct-accent); }
.status-consume { color: var(--rc35-green); }
.status-manage { color: var(--rc35-amber); }
.status-system { color: var(--ct-ink-3); }
.status-error { color: var(--ct-crit); }
.status-refund { color: var(--ct-accent); }
.status-login { color: var(--ct-accent); }
.status-unknown { color: var(--rc35-ink-3); }
.channel-cell { display: flex; min-width: 0; flex-direction: column; align-items: flex-start; gap: 4px; }
.channel-line { display: inline-flex; min-width: 0; align-items: center; gap: 3px; }
.channel-affinity-anchor {
  position: relative;
  display: inline-flex;
  min-width: 0;
  align-items: center;
  overflow: visible;
}
.channel-affinity-trigger {
  position: absolute;
  top: -4px;
  right: -4px;
  z-index: 1;
  display: inline-flex;
  width: 12px;
  height: 12px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  background: transparent;
  color: #f59e0b;
  cursor: pointer;
  line-height: 1;
}
.channel-affinity-trigger:hover,
.channel-affinity-trigger:focus-visible { color: #d97706; }
.channel-affinity-trigger:focus-visible { outline: 2px solid var(--ct-warn); outline-offset: 1px; border-radius: 3px; }
.channel-affinity-trigger svg { display: block; width: 12px; height: 12px; fill: currentColor; stroke: currentColor; stroke-width: 2; stroke-linecap: round; stroke-linejoin: round; }
.retry-chain-trigger {
  position: relative;
  display: inline-flex;
  width: 22px;
  height: 22px;
  flex: 0 0 22px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--rc35-amber);
  cursor: pointer;
}
.retry-chain-unknown { width: auto; flex: 0 0 auto; gap: 3px; padding: 0 4px; border-radius: 4px; white-space: nowrap; font-size: 11px; }
.retry-chain-unknown svg { flex: 0 0 15px; }
.retry-chain-trigger:hover,
.retry-chain-trigger:focus-visible { background: var(--ct-warn-weak); color: var(--ct-warn); }
.retry-chain-trigger:focus-visible { outline: 2px solid var(--ct-warn); outline-offset: 1px; }
.retry-chain-trigger > .el-icon { font-size: 15px; }
.retry-hover-card {
  position: fixed;
  z-index: 4100;
  display: flex;
  --rc35-ink-2: #525252;
  --rc35-amber: var(--ct-warn);
  max-width: min(320px, calc(100vw - 16px));
  align-items: baseline;
  gap: 4px;
  padding: 8px 10px;
  border: 1px solid #d7dee8;
  border-radius: 6px;
  background: #fff;
  box-shadow: 0 8px 22px rgba(28, 43, 68, .16);
  color: var(--rc35-ink-2);
  font-size: 12px;
  line-height: 18px;
  pointer-events: none;
  white-space: normal;
}
.retry-hover-card.has-affinity { flex-direction: column; align-items: stretch; gap: 6px; }
.retry-hover-line { display: flex; min-width: 0; align-items: baseline; gap: 4px; }
.retry-hover-card strong {
  flex: none;
  color: var(--rc35-amber);
  font-weight: 600;
}
.retry-hover-card span {
  min-width: 0;
  overflow-wrap: anywhere;
  color: var(--rc35-ink-2);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
.retry-hover-chain { display: inline-flex; min-width: 0; align-items: baseline; flex-wrap: wrap; gap: 0; }
.retry-hover-step { color: var(--rc35-ink-2); font: inherit; font-weight: 400; }
.retry-hover-step.is-current { color: var(--ct-warn); font-weight: 700; }
.retry-hover-step.is-failed { color: var(--ct-crit); font-weight: 700; }
.retry-hover-step.is-success { color: var(--ct-ok); font-weight: 700; }
.retry-hover-separator { margin: 0 4px; color: var(--rc35-ink-3); font-style: normal; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.retry-hover-affinity { display: flex; min-width: 0; flex-direction: column; gap: 2px; padding-top: 6px; border-top: 1px solid #e5e7eb; }
.retry-hover-affinity strong { color: #f59e0b; }
.retry-hover-affinity span { color: var(--rc35-ink-2); font-family: inherit; }

.retry-chain-text { color: var(--rc35-amber); font-size: 12px; line-height: 18px; white-space: normal; }
.channel-badge, .token-badge, .model-badge {
  display: inline-flex;
  max-width: 100%;
  min-width: 0;
  align-items: center;
  gap: 5px;
  overflow: hidden;
  border: 1px solid transparent;
  border-radius: 6px;
  font: inherit;
  font-size: 13px;
  line-height: 22px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.channel-badge { height: 20px; padding: 0 6px; border: 0; border-radius: 0; background: transparent; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-weight: 500; }
/* 对应 rc35 StatusBadge 的 textColorMap 和浅色主题变量。 */
.token-tone-amber, .token-tone-orange, .token-tone-yellow { --identity-ink: var(--ct-warn); }
.token-tone-blue, .token-tone-indigo { --identity-ink: var(--ct-accent); }
.token-tone-cyan, .token-tone-teal { --identity-ink: var(--ct-accent); }
.token-tone-green { --identity-ink: var(--ct-ok); }
.token-tone-light-green { --identity-ink: var(--ct-ok); }
.token-tone-grey, .token-tone-hidden { --identity-ink: var(--ct-ink-3); }
.token-tone-light-blue { --identity-ink: var(--ct-accent); }
.token-tone-lime { --identity-ink: var(--ct-purple); }
.token-tone-pink { --identity-ink: var(--ct-ok); }
.token-tone-purple, .token-tone-violet { --identity-ink: var(--ct-purple); }
.token-tone-red { --identity-ink: var(--ct-crit); }
.channel-badge, .group-meta [class*="token-tone-"], .mobile-token small, .model-badge[class*="token-tone-"] { color: var(--identity-ink); }
.copyable { cursor: pointer; }
.copyable:hover {
  filter: brightness(.97);
}
.copyable:focus-visible,
.user-cell:focus-visible,
.details-button:focus-visible,
.mobile-user:focus-visible {
  outline: 2px solid var(--rc35-blue);
  outline-offset: 2px;
}
.cell-secondary, .group-meta, .token-cell small, .stream-cell small, .tokens-cell small {
  min-width: 0;
  overflow: hidden;
  color: var(--rc35-ink-3);
  font-size: 12px;
  line-height: 18px;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.user-cell, .mobile-user {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 7px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--rc35-ink);
  font-size: 13px;
  font-weight: 500;
  text-align: left;
}
.user-avatar {
  display: inline-grid;
  width: 25px;
  height: 25px;
  flex: 0 0 25px;
  place-items: center;
  border: 1px solid var(--ct-line);
  border-radius: 50%;
  background: var(--ct-accent-weak);
  color: var(--rc35-green);
  font-size: 11px;
  font-style: normal;
  font-weight: 700;
}
.user-avatar.is-hidden {
  border-color: var(--rc35-line-strong);
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-3);
}
.truncate { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
/* 标签使用轻背景和弱边框，模型名称比辅助维度更醒目。 */
.token-badge, .model-badge { width: fit-content; height: 24px; min-height: 24px; padding: 0 8px; gap: 6px; border: 1px solid color-mix(in srgb, var(--ct-line) 65%, transparent); border-radius: 6px; background: color-mix(in srgb, var(--ct-surface-2) 55%, transparent); color: var(--ct-ink); font-weight: 500; text-align: left; }
.model-badge { font-weight: 600; }
.token-badge svg, .model-icon { flex: none; }
.model-icon { width: 18px; height: 18px; object-fit: contain; }
.group-meta { color: var(--ct-ink-3); }
.group-meta .ratio { color: var(--ct-ink-3); font-variant-numeric: tabular-nums; }
.stream-cell { gap: 3px; }
.stream-label {
  display: inline-flex;
  width: fit-content;
  min-height: 18px;
  align-items: center;
  padding: 0;
  color: var(--rc35-ink-3);
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  white-space: nowrap;
}
.stream-label.is-stream { color: var(--ct-accent); font-weight: 600; }
.stream-label.is-nonstream, .stream-label.is-unknown { color: var(--rc35-ink-3); }
.tokens-cell { align-items: flex-start; }
.token-pair, .cost-text {
  color: var(--rc35-ink);
  font: 500 13px/20px ui-monospace, SFMono-Regular, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.token-pair b { color: var(--rc35-ink-3); font-weight: 400; }
.tokens-cell small { max-width: 150px; }
.cost-text { display: inline-block; padding: 1px 7px; border: 1px solid color-mix(in srgb, var(--ct-line) 65%, transparent); border-radius: 6px; background: color-mix(in srgb, var(--ct-surface-2) 55%, transparent); font-weight: 600; }

/* 首字和总耗时分别取色，色条仅表达同一行的两个独立指标。 */
.timing-cell {
  display: flex;
  min-height: 40px;
  flex-direction: row;
  align-items: stretch;
  gap: 7px;
  white-space: nowrap;
}
.timing-bar {
  display: flex;
  width: 4px;
  flex: 0 0 4px;
  flex-direction: column;
  overflow: hidden;
  border-radius: 2px;
}
.timing-segment { display: block; min-height: 0; flex: 1 1 0; }
.timing-segment.timing-success { background: color-mix(in srgb, var(--rc35-timing-success) 90%, transparent); }
/* 色条透明度与 rc35 的 bg-success/90、bg-warning/80、bg-destructive/80 保持一致。 */
.timing-segment.timing-warning { background: color-mix(in srgb, var(--rc35-timing-warning) 80%, transparent); }
.timing-segment.timing-danger { background: color-mix(in srgb, var(--rc35-timing-danger) 80%, transparent); }
.timing-segment.timing-neutral { background: var(--rc35-ink-3); }
.timing-values {
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 1px;
  white-space: nowrap;
}
.timing-value { color: var(--rc35-ink-3); font-size: 12px; line-height: 18px; }
.timing-value b { font-size: 12px; font-weight: 500; font-variant-numeric: tabular-nums; }
.timing-value.timing-success b { color: var(--ct-ok); }
.timing-value.timing-warning b { color: var(--ct-warn); }
.timing-value.timing-danger b { color: var(--ct-crit); }
.timing-value.timing-neutral b { color: var(--rc35-ink-3); }
.details-button {
  display: block;
  max-width: 100%;
  overflow: hidden;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--rc35-ink-2);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  line-height: 20px;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.details-button:hover { color: var(--rc35-blue); text-decoration: underline; text-underline-offset: 3px; }

.muted, .empty-state span { color: var(--rc35-ink-3); }
.empty-state {
  display: flex;
  min-height: 240px;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  color: var(--rc35-ink-3);
}
.empty-state .el-icon { color: var(--rc35-line-strong); font-size: 24px; }
.empty-state strong { color: var(--rc35-ink-2); font-size: 13px; }
.empty-state span { font-size: 11.5px; }

/* 移动端改为 rc35 的卡片列表，同时压缩 AppShell 的固定侧栏。 */
.mobile-log-list { display: none; }
.pagination-bar {
  display: flex;
  min-height: 48px;
  flex: none;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-start;
  gap: 16px;
  padding: 7px 12px;
  border: 1px solid var(--rc35-line);
  border-radius: 8px;
  background: var(--rc35-surface);
  color: var(--rc35-ink-3);
  font-size: 11.5px;
}
.pager-summary {
  display: flex;
  flex: 0 0 auto;
  align-items: baseline;
  gap: 6px;
  color: var(--rc35-ink-3);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}
.pager-summary strong {
  color: var(--rc35-ink);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}
.pager-controls {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  margin-left: auto;
  gap: 12px;
  justify-content: flex-end;
  color: var(--rc35-ink-2);
}
.page-size-control {
  position: relative;
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 6px;
}
.page-size-label {
  color: var(--rc35-ink-3);
  font-size: 12px;
  font-weight: 500;
  white-space: nowrap;
}
.page-size {
  width: 70px;
  height: 32px;
  padding: 0 24px 0 10px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 8px;
  outline: 0;
  background: var(--rc35-surface);
  color: var(--rc35-ink);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 500;
  line-height: 30px;
  font-variant-numeric: tabular-nums;
}
.page-size-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  text-align: left;
}
.page-size-trigger:disabled { cursor: not-allowed; opacity: .5; }
.page-size-chevron {
  display: block;
  width: 6px;
  height: 6px;
  flex: 0 0 6px;
  margin: -3px 2px 0 8px;
  border-right: 1.5px solid var(--rc35-ink-3);
  border-bottom: 1.5px solid var(--rc35-ink-3);
  transform: rotate(45deg);
}
.page-size-trigger[aria-expanded='true'] .page-size-chevron {
  margin-top: 3px;
  transform: rotate(225deg);
}
.page-size:hover:not(:disabled) { border-color: var(--rc35-line-strong); }
.page-size:focus-visible {
  border-color: var(--rc35-blue);
  box-shadow: 0 0 0 2px rgba(47, 95, 224, .14);
}
.page-size-menu {
  /* Teleport 后菜单位于 body 下，显式携带 rc35 变量，避免背景和选中色失效。 */
  --rc35-surface: var(--ct-surface);
  --rc35-surface-2: var(--ct-surface-2);
  --rc35-line: var(--ct-line);
  --rc35-ink: var(--ct-ink);
  --rc35-ink-2: var(--ct-ink-2);
  --rc35-ink-3: var(--ct-ink-3);
  --rc35-accent-weak: var(--ct-accent-weak);
  --rc35-blue: var(--ct-accent);
  position: fixed;
  z-index: 4000;
  display: flex;
  min-width: 144px;
  flex-direction: column;
  padding: 4px;
  border: 1px solid var(--rc35-line);
  border-radius: 8px;
  background: var(--rc35-surface);
  box-shadow: 0 8px 24px rgba(28, 43, 68, .16);
  color: var(--rc35-ink);
  overflow-x: hidden;
  overflow-y: auto;
  animation: page-size-menu-in .12s ease-out;
}
.page-size-option {
  position: relative;
  display: flex;
  width: 100%;
  min-height: 28px;
  align-items: center;
  gap: 6px;
  padding: 4px 32px 4px 6px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--rc35-ink-2);
  cursor: pointer;
  font: inherit;
  font-size: 14px;
  line-height: 20px;
  text-align: left;
  white-space: nowrap;
}
.page-size-option:hover,
.page-size-option:focus-visible {
  outline: 0;
  background: var(--rc35-surface-2);
  color: var(--rc35-ink);
}
.page-size-option[aria-selected='true'] {
  background: var(--rc35-accent-weak);
  color: var(--rc35-blue);
  font-weight: 500;
}
.page-size-check {
  position: absolute;
  right: 8px;
  color: currentColor;
  font-size: 14px;
  line-height: 20px;
  opacity: 0;
}
.page-size-option[aria-selected='true'] .page-size-check { opacity: 1; }
@keyframes page-size-menu-in {
  from { opacity: 0; transform: translateY(3px); }
  to { opacity: 1; transform: translateY(0); }
}
.page-size-menu[style*='top:'] {
  transform-origin: bottom center;
}
.page-size-menu .page-size-option:focus-visible {
  box-shadow: 0 0 0 2px rgba(47, 95, 224, .18) inset;
}
.page-size-trigger:focus-visible {
  border-color: var(--rc35-blue);
  box-shadow: 0 0 0 2px rgba(47, 95, 224, .14);
}
.pager-navigation {
  display: flex;
  min-width: 0;
  flex: 0 0 auto;
  align-items: center;
  gap: 4px;
}
.page-button {
  display: inline-flex;
  width: 32px;
  height: 32px;
  flex: 0 0 32px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 6px;
  background: var(--rc35-surface);
  color: var(--rc35-ink-2);
  cursor: pointer;
  font: inherit;
  font-size: 13px;
  font-weight: 500;
  line-height: 30px;
  font-variant-numeric: tabular-nums;
}
/* 图标按钮固定尺寸，数字页码按内容增长，避免五位以上页码溢出边框。 */
.page-button.page-number {
  width: auto;
  min-width: 32px;
  flex-basis: auto;
  padding-inline: 8px;
}
.page-button:hover:not(:disabled) { border-color: var(--rc35-blue); color: var(--rc35-blue); }
.page-button.active { border-color: var(--rc35-blue); background: var(--ct-primary-solid); color: var(--ct-on-solid); font-weight: 600; }
.page-button:disabled { cursor: not-allowed; opacity: .45; }
.page-ellipsis {
  display: inline-flex;
  width: 20px;
  height: 32px;
  align-items: center;
  justify-content: center;
  color: var(--rc35-ink-3);
  font-size: 13px;
}
/* 页码直达输入与分页按钮保持同高，窄屏时独占一行避免控件互相挤压。 */
.page-jump {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 4px;
  color: var(--rc35-ink-2);
  white-space: nowrap;
}
.page-jump label { color: var(--rc35-ink-3); }
.page-jump input {
  width: 52px;
  height: 30px;
  padding: 0 5px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 6px;
  outline: 0;
  background: var(--rc35-surface-2);
  color: var(--rc35-ink);
  font: 12px ui-monospace, SFMono-Regular, Consolas, monospace;
  text-align: center;
  transition: border-color .16s ease, box-shadow .16s ease;
}
.page-jump input::placeholder { color: var(--rc35-ink-3); }
.page-jump input:focus { border-color: var(--rc35-blue); box-shadow: 0 0 0 2px rgba(47, 95, 224, .14); }
.page-jump input::-webkit-inner-spin-button,
.page-jump input::-webkit-outer-spin-button { margin: 0; appearance: none; }
.page-jump button {
  height: 30px;
  padding: 0 8px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 6px;
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-2);
  cursor: pointer;
  font-size: 11.5px;
  white-space: nowrap;
}
.page-jump button:hover { border-color: var(--rc35-blue); color: var(--rc35-blue); }
.page-jump button:disabled { cursor: not-allowed; opacity: .45; }

/* 列选择弹层会 teleport 到 body，因此不能只依赖页面内的 CSS 变量。 */
:global(.logs-column-popover.el-popover.el-popper) {
  z-index: 3000 !important;
  width: 190px !important;
  max-width: calc(100vw - 24px);
  box-sizing: border-box;
  padding: 12px !important;
  border: 1px solid var(--ct-line) !important;
  background: var(--ct-surface) !important;
  background-image: none !important;
  --el-popover-bg-color: var(--ct-surface);
  color: var(--ct-ink-2);
  box-shadow: 0 8px 24px rgba(16, 24, 40, .14);
  isolation: isolate;
  opacity: 1 !important;
}
:global(.logs-column-popover .el-popper__arrow::before) {
  border-color: var(--ct-line) !important;
  background: var(--ct-surface) !important;
}
.column-menu {
  display: flex;
  width: 100%;
  min-width: 0;
  max-height: min(60vh, 360px);
  flex-direction: column;
  gap: 4px;
  overflow-y: auto;
  color: var(--rc35-ink-2);
}
.column-menu > strong {
  display: block;
  padding-bottom: 5px;
  border-bottom: 1px solid var(--rc35-line);
  color: var(--rc35-ink);
  font-size: 12px;
  line-height: 18px;
  white-space: nowrap;
}
.column-menu :deep(.el-checkbox) {
  width: 100%;
  min-width: 0;
  height: 28px;
  margin: 0;
  border-radius: 4px;
  color: var(--rc35-ink-2);
}
.column-menu :deep(.el-checkbox:hover) { background: var(--rc35-surface-2); color: var(--rc35-ink); }
.column-menu :deep(.el-checkbox__input) { flex: none; }
.column-menu :deep(.el-checkbox__label) {
  min-width: 0;
  overflow: hidden;
  color: inherit;
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 详情弹窗按 rc35 Dialog 的固定规格映射，正文只在弹窗内部滚动。 */
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
:global(body.ct-detail-open) { overflow: hidden; }
.affinity-dialog-backdrop {
  --rc35-surface: var(--ct-surface);
  --rc35-surface-2: var(--ct-surface-2);
  --rc35-line: var(--ct-line);
  --rc35-line-strong: var(--ct-line-strong);
  --rc35-ink: var(--ct-ink);
  --rc35-ink-2: var(--ct-ink-2);
  --rc35-ink-3: var(--ct-ink-3);
  position: fixed;
  z-index: 3100;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--ct-overlay);
  backdrop-filter: blur(2px);
}
:global(body.ct-affinity-open) { overflow: hidden; }
.affinity-dialog {
  position: relative;
  display: flex;
  width: min(512px, calc(100% - 2rem));
  max-height: calc(100vh - 2rem);
  min-height: 0;
  flex-direction: column;
  gap: 12px;
  overflow: hidden;
  padding: 16px;
  border: 0;
  border-radius: 12px;
  background: var(--rc35-surface);
  color: var(--rc35-ink);
  box-shadow: 0 0 0 1px rgba(28, 37, 52, .1);
}
.affinity-dialog-header { min-height: 28px; }
.affinity-dialog-icon { display: inline-flex; width: 16px; height: 16px; align-items: center; justify-content: center; color: #f59e0b; }
.affinity-dialog-icon svg { display: block; width: 14px; height: 14px; }
.affinity-dialog-status { color: #f59e0b; font-size: 12px; font-weight: 600; line-height: 20px; }
.affinity-dialog-hint { margin: 0; color: var(--rc35-ink-3); font-size: 12px; line-height: 18px; }
.affinity-dialog-body { min-height: 0; overflow-y: auto; scrollbar-color: var(--rc35-line-strong) transparent; scrollbar-width: thin; }
.affinity-dialog-channel { display: flex; min-width: 0; align-items: baseline; gap: 8px; margin-bottom: 10px; color: var(--rc35-ink-2); font-size: 13px; }
.affinity-dialog-channel strong { color: #f59e0b; font: 600 13px ui-monospace, SFMono-Regular, Consolas, monospace; }
.affinity-dialog-channel span:last-child { min-width: 0; overflow: hidden; color: var(--rc35-ink-3); text-overflow: ellipsis; white-space: nowrap; }
.affinity-dialog-card { display: flex; min-width: 0; flex-direction: column; gap: 4px; padding: 10px; border: 1px solid var(--rc35-line); border-radius: 6px; background: var(--rc35-surface-2); }
.affinity-dialog-row { display: grid; min-width: 0; grid-template-columns: 5.25rem minmax(0, 1fr); gap: 8px; align-items: baseline; min-height: 20px; font-size: 12px; line-height: 16px; }
.affinity-dialog-row > span { color: var(--rc35-ink-3); white-space: nowrap; }
.affinity-dialog-row > code { min-width: 0; overflow-wrap: anywhere; color: var(--rc35-ink-2); font: 12px/16px ui-monospace, SFMono-Regular, Consolas, monospace; }
.detail-dialog-backdrop {
  --rc35-surface: var(--ct-surface);
  --rc35-surface-2: var(--ct-surface-2);
  --rc35-line: var(--ct-line);
  --rc35-line-strong: var(--ct-line-strong);
  --rc35-ink: var(--ct-ink);
  --rc35-ink-2: var(--ct-ink-2);
  --rc35-ink-3: var(--ct-ink-3);
  --rc35-blue: var(--ct-accent);
  --rc35-green: var(--ct-ok);
  --rc35-amber: var(--ct-warn);
  position: fixed;
  z-index: 3000;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--ct-overlay);
  backdrop-filter: blur(2px);
}
.detail-dialog {
  position: relative;
  display: flex;
  width: 100%;
  max-width: calc(100% - 2rem);
  max-height: calc(100vh - 2rem);
  min-height: 0;
  flex-direction: column;
  gap: 16px;
  overflow: hidden;
  border: 0;
  border-radius: 12px;
  background: var(--rc35-surface);
  color: var(--rc35-ink);
  font-size: 14px;
  line-height: 20px;
  box-shadow: 0 0 0 1px rgba(28, 37, 52, .1);
  padding: 16px;
}
.detail-dialog:not(.is-wide) { max-width: 512px; }
.detail-dialog.is-wide { max-width: 1024px; }
.detail-dialog-header {
  display: flex;
  min-width: 0;
  flex: none;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  padding: 0;
  text-align: left;
}
.detail-dialog-title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
}
.detail-dialog-title h2 {
  margin: 0;
  color: var(--rc35-ink);
  font-size: 16px;
  font-weight: 500;
  line-height: 1;
}
.detail-dialog-status {
  display: inline-flex;
  width: fit-content;
  max-width: 100%;
  min-width: 0;
  height: 20px;
  flex: none;
  align-items: center;
  gap: 4px;
  padding: 0 6px;
  border-radius: 9999px;
  font-size: 14px;
  font-weight: 500;
  line-height: 1;
  letter-spacing: normal;
  white-space: nowrap;
}
.detail-dialog-fallback {
  color: var(--rc35-amber);
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  white-space: nowrap;
}
.detail-dialog-close {
  position: absolute;
  top: 8px;
  right: 8px;
  display: inline-flex;
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--rc35-ink-3);
  cursor: pointer;
  font: inherit;
}
.detail-dialog-close:hover { background: var(--rc35-surface-2); color: var(--rc35-ink); }
/* 用两条细线复刻 rc35 关闭图标，避免详情弹窗重新依赖 Element Plus 图标组件。 */
.detail-close-glyph {
  position: relative;
  display: block;
  width: 14px;
  height: 14px;
}
.detail-close-glyph::before,
.detail-close-glyph::after {
  position: absolute;
  top: 6px;
  left: 0;
  width: 14px;
  height: 1.5px;
  border-radius: 1px;
  background: currentColor;
  content: '';
}
.detail-close-glyph::before { transform: rotate(45deg); }
.detail-close-glyph::after { transform: rotate(-45deg); }
.detail-dialog-close:focus-visible,
.detail-copy-button:focus-visible,
.detail-copy-value:focus-visible {
  outline: 0;
  outline-offset: 2px;
  box-shadow: 0 0 0 2px rgba(47, 95, 224, .28);
}
.detail-dialog-body {
  min-height: 0;
  flex: 0 1 auto;
  height: min(72dvh, 720px);
  max-height: calc(100vh - 14rem);
  margin-inline: -4px;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-color: var(--rc35-line-strong) transparent;
  scrollbar-width: thin;
}
/* 对齐 rc35 Dialog body 的 px-1、py-1 和 pr-2/pr-4 内层留白。 */
.detail-dialog-body-inner {
  min-width: 0;
  padding: 4px 8px 4px 4px;
}
.detail-dialog-content {
  display: flex;
  width: 100%;
  max-width: 100%;
  min-width: 0;
  margin: 0;
  padding: 4px 0;
  flex-direction: column;
  gap: 10px;
  overflow-x: hidden;
  color: var(--rc35-ink-2);
}
.detail-overview {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.detail-row {
  display: grid;
  min-width: 0;
  grid-template-columns: 5.25rem minmax(0, 1fr);
  gap: 8px;
  align-items: start;
  color: var(--rc35-ink-2);
  font-size: 14px;
  line-height: 20px;
}
.detail-label {
  min-width: 0;
  color: var(--rc35-ink-3);
  font-size: 12px;
  line-height: 16px;
  text-align: left;
  white-space: nowrap;
}
.detail-value {
  min-width: 0;
  color: var(--rc35-ink-2);
  font-size: 12px;
  line-height: 16px;
  overflow-wrap: anywhere;
}
.detail-mono { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; }
.detail-copy-value {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 8px;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--rc35-blue);
  cursor: pointer;
  font: 12px/16px ui-monospace, SFMono-Regular, Consolas, monospace;
  text-align: left;
  overflow-wrap: anywhere;
}
.detail-copy-value:hover { text-decoration: underline; text-underline-offset: 3px; }
.detail-copy-value .copy-glyph { opacity: 0; }
.detail-copy-value:hover .copy-glyph,
.detail-copy-value:focus-visible .copy-glyph { opacity: .75; }
.detail-timing strong { font-weight: 650; }
.detail-timing > span { margin-left: 8px; }
/* 详情弹窗中的首字和总耗时与列表使用同一套 rc35 状态颜色。 */
.detail-timing .timing-success { color: var(--rc35-timing-success); }
.detail-timing .timing-warning { color: var(--rc35-timing-warning); }
.detail-timing .timing-danger { color: var(--rc35-timing-danger); }
.detail-success { color: var(--rc35-green); }
.detail-danger { color: var(--ct-crit); }
.detail-warning { color: var(--rc35-amber); }
.channel-affinity-status { display: inline-flex; align-items: center; gap: 4px; color: var(--rc35-amber); font-weight: 600; }
.channel-affinity-status svg { display: block; color: currentColor; fill: currentColor; }
.detail-fallback-chain { color: var(--rc35-ink-2); font-weight: 400; }
.detail-total-cost { color: var(--rc35-ink); font-weight: 650; }
.detail-section {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}
.detail-section h3 {
  margin: 0;
  color: var(--rc35-ink);
  font-size: 12px;
  font-weight: 600;
  line-height: 16px;
}
.detail-section-card {
  position: relative;
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
  border: 1px solid var(--rc35-line);
  border-radius: 6px;
  background: var(--rc35-surface-2);
}
.detail-section-card .detail-row { grid-template-columns: 5.25rem minmax(0, 1fr); }
.detail-conversion-row { padding-right: 24px; }
.detail-copy-button {
  position: absolute;
  top: 10px;
  right: 10px;
  display: inline-flex;
  width: 20px;
  height: 20px;
  align-items: center;
  justify-content: center;
  padding: 0;
  border: 0;
  border-radius: 4px;
  background: transparent;
  color: var(--rc35-ink-3);
  cursor: pointer;
}
.detail-copy-button:hover { background: var(--rc35-surface); color: var(--rc35-blue); }
.copy-glyph {
  position: relative;
  display: inline-block;
  width: 12px;
  height: 12px;
  border: 1.5px solid currentColor;
  border-radius: 2px;
  box-sizing: border-box;
}
.copy-glyph::before {
  position: absolute;
  top: -4px;
  left: 3px;
  width: 8px;
  height: 8px;
  border: 1.5px solid currentColor;
  border-radius: 2px;
  background: var(--rc35-surface-2);
  content: '';
}
.multiline { white-space: pre-line; }
.detail-content-value { line-height: 1.6; }
/* 动态计费表沿用 rc35 的紧凑表格，窄屏只让表格本身横向滚动。 */
.dynamic-expression {
  display: block;
  max-height: 120px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.dynamic-table-wrap {
  min-width: 0;
  overflow-x: auto;
  border: 1px solid var(--rc35-line);
  border-radius: 4px;
  background: var(--rc35-surface);
}
.detail-subheading {
  margin: 8px 8px 4px;
  color: var(--rc35-ink);
  font-size: 11px;
  font-weight: 600;
  line-height: 16px;
}
.dynamic-table-wrap > .detail-subheading { margin-bottom: 2px; }
.dynamic-tier-table {
  width: 100%;
  min-width: 480px;
  border-collapse: collapse;
  color: var(--rc35-ink-2);
  font-size: 11px;
  line-height: 16px;
}
.dynamic-tier-table th,
.dynamic-tier-table td {
  padding: 6px 8px;
  border-bottom: 1px solid var(--rc35-line);
  text-align: left;
  vertical-align: top;
  white-space: nowrap;
}
.dynamic-tier-table th {
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-3);
  font-size: 10px;
  font-weight: 600;
}
.dynamic-tier-table tbody tr:last-child td { border-bottom: 0; }
.dynamic-tier-table tbody tr.is-matched td { background: var(--ct-ok-weak); color: var(--rc35-ink); }
.dynamic-tier-table td.detail-condition {
  max-width: 230px;
  overflow: hidden;
  color: var(--rc35-ink-3);
  text-overflow: ellipsis;
}
.detail-tier-label { color: var(--rc35-blue); font-weight: 600; }
.detail-tier-hit {
  display: inline-block;
  margin-left: 5px;
  color: var(--rc35-green);
  font-size: 10px;
  font-weight: 600;
}
.detail-rule-row {
  display: flex;
  min-width: 0;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 6px 8px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: var(--rc35-surface);
  color: var(--rc35-ink-2);
  font-size: 11px;
  line-height: 16px;
}
.detail-rule-row span { min-width: 0; overflow-wrap: anywhere; }
.detail-rule-row strong { flex: none; color: var(--rc35-amber); font-weight: 600; }
.detail-rule-row.is-matched { border-color: var(--ct-line-strong); background: var(--ct-ok-weak); }
.detail-rule-row.is-matched strong { color: var(--rc35-green); }
.detail-raw-expression,
.detail-inline-warning {
  margin: 0;
  color: var(--rc35-amber);
  font-size: 11px;
  line-height: 16px;
}
.detail-section-danger .detail-section-card { border-color: var(--ct-line); background: var(--ct-crit-weak); }
.detail-content-card { position: relative; }
.detail-error-list {
  max-height: 128px;
  margin: 2px 0 0;
  padding: 8px;
  overflow: auto;
  border: 1px solid var(--rc35-line);
  border-radius: 4px;
  background: var(--rc35-surface);
  color: var(--ct-crit);
  font: 11px/1.55 ui-monospace, SFMono-Regular, Consolas, monospace;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.detail-tag {
  display: inline-flex;
  min-height: 20px;
  align-items: center;
  padding: 0 6px;
  border: 1px solid var(--rc35-line-strong);
  border-radius: 9999px;
  background: var(--rc35-surface-2);
  color: var(--rc35-ink-2);
  font-size: 11px;
  line-height: 18px;
  white-space: nowrap;
}
.detail-tag-low,
.detail-tag-minimal { border-color: var(--ct-line-strong); background: var(--ct-ok-weak); color: var(--rc35-green); }
.detail-tag-medium { border-color: var(--ct-line-strong); background: var(--ct-warn-weak); color: var(--rc35-amber); }
.detail-tag-high,
.detail-tag-xhigh,
.detail-tag-max,
.detail-tag-warning { border-color: var(--ct-line-strong); background: var(--ct-warn-weak); color: var(--rc35-amber); }
.detail-override-line {
  display: flex;
  min-width: 0;
  align-items: flex-start;
  gap: 8px;
  padding: 7px 8px;
  border: 1px solid var(--rc35-line);
  border-radius: 4px;
  background: var(--rc35-surface);
  color: var(--rc35-ink-2);
  font-size: 11px;
  line-height: 16px;
}
.detail-override-line > span:last-child { min-width: 0; overflow-wrap: anywhere; }

/* rc35 的 sm 断点同时放大弹窗和行的标签列，保持桌面与移动规格一致。 */
@media (min-width: 640px) {
  .detail-dialog {
    max-width: 512px;
    padding: 24px;
  }
  .detail-dialog.is-wide { max-width: 1024px; }
  .detail-dialog-body-inner { padding-right: 16px; }
  .detail-dialog-content { gap: 12px; }
  .detail-row,
  .detail-section-card .detail-row {
    grid-template-columns: 7rem minmax(0, 1fr);
    gap: 12px;
  }
}

/* 窄桌面只调整最小宽度，完整内容仍可横向滚动查看。 */
@media (min-width: 761px) and (max-width: 1320px) {
  .filter-time { min-width: 280px; flex-basis: 300px; }
  .filter-username { width: 132px; flex-basis: 132px; }
  .filter-channel { width: 112px; flex-basis: 112px; }
  .filter-request { width: 145px; flex-basis: 145px; }
  .filter-model, .filter-group { width: 138px; flex-basis: 138px; }
  .logs-table th.col-time, .logs-table td.col-time { min-width: 130px; }
  .logs-table th.col-channel, .logs-table td.col-channel { min-width: 102px; }
  .logs-table th.col-user, .logs-table td.col-user { min-width: 110px; }
  .logs-table th.col-token, .logs-table td.col-token { min-width: 126px; }
  .logs-table th.col-model, .logs-table td.col-model { min-width: 134px; }
  .logs-table th.col-stream, .logs-table td.col-stream { min-width: 54px; }
  .logs-table th.col-tokens, .logs-table td.col-tokens { min-width: 98px; }
  .logs-table th.col-quota, .logs-table td.col-quota { min-width: 70px; }
  .logs-table th.col-timing, .logs-table td.col-timing { min-width: 86px; }
  .logs-table th.col-details, .logs-table td.col-details { width: 128px; min-width: 128px; max-width: 128px; }
  .logs-table .details-button { width: 108px; max-width: 108px; }
}

/* 移动端保持控件可触达，主筛选使用两列，日期和操作横跨整行。 */
@media (max-width: 900px) {
  :global(body.ct-rc35-logs-theme) { min-width: 0 !important; overflow-x: hidden; }
  /* 外壳尺寸、侧栏隐藏和底部导航留白由共享手机布局统一管理。 */
  .logs-page { height: auto; min-height: 0; margin: -10px -8px -18px; overflow: visible; padding: 14px 8px 18px; }
  .logs-toolbar { padding: 12px; }
  .primary-filters { display: grid; grid-template-columns: minmax(0, 1fr); }
  .filter-row { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); overflow: visible; }
  .filter-expand-action { grid-column: 1 / -1; justify-content: center; }
  .filter-time {
    width: 100% !important;
    max-width: none;
    min-width: 0;
    grid-column: 1 / -1;
    flex: none;
  }
  /* 覆盖桌面端的高优先级固定宽度，避免日期控件把移动页面撑出横向滚动。 */
  .logs-toolbar :deep(.filter-time.compact-date-range) {
    width: 100% !important;
    min-width: 0 !important;
    max-width: none !important;
    flex: none !important;
  }
  .filter-username, .filter-channel, .filter-request,
  .filter-model, .filter-group {
    width: 100% !important;
    min-width: 0;
    flex: none;
  }
  .filter-token, .filter-upstream { width: 100% !important; min-width: 0; flex: none; }
  .toolbar-meta { align-items: flex-start; flex-direction: column; }
  .toolbar-actions {
    width: 100%;
    justify-content: flex-end;
    padding-top: 8px;
    border-top: 1px solid var(--rc35-line);
  }
  .action-type { width: 100%; flex: 1 1 100%; }
  .logs-table-shell { flex: 0 0 auto; overflow: visible; border: 0; background: transparent; box-shadow: none; }
  .desktop-table { display: none; }
  .mobile-log-list { display: grid; min-height: 220px; gap: 8px; }
  .mobile-log-card {
    padding: 12px;
    border: 1px solid var(--rc35-line);
    border-radius: 8px;
    background: var(--rc35-surface);
    box-shadow: 0 1px 2px rgba(16, 24, 40, .05);
  }
  .mobile-log-card.row-error { border-color: var(--ct-line); background: var(--ct-crit-weak); }
  .mobile-log-card.row-refund { border-color: var(--ct-line); background: var(--ct-accent-weak); }
  .mobile-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 10px; }
  .mobile-model { min-width: 0; }
  .mobile-model .model-badge { max-width: min(68vw, 270px); }
  .mobile-time {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 6px;
    color: var(--rc35-ink-3);
    font: 10.5px ui-monospace, SFMono-Regular, Consolas, monospace;
  }
  .mobile-cost {
    flex: none;
    color: var(--rc35-ink);
    font: 600 12px ui-monospace, SFMono-Regular, Consolas, monospace;
  }
  .mobile-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 6px;
    margin-top: 10px;
  }
  .mobile-field {
    min-width: 0;
    padding: 7px 8px;
    border-radius: 6px;
    background: var(--rc35-surface-2);
  }
  .mobile-field > span {
    display: block;
    margin-bottom: 4px;
    color: var(--rc35-ink-3);
    font-size: 10.5px;
  }
  .mobile-field > strong { color: var(--rc35-ink-2); font-size: 11.5px; font-weight: 400; }
  .mobile-field .channel-badge { max-width: 100%; }
  .mobile-field .channel-badge small { max-width: 115px; overflow: hidden; text-overflow: ellipsis; }
  .mobile-field .retry-chain-text { margin-top: 4px; }
  .mobile-user { gap: 6px; font: inherit; font-size: 11.5px; }
  .mobile-token { display: flex; min-width: 0; flex-direction: column; align-items: flex-start; gap: 3px; }
  .mobile-token .token-badge { max-width: 100%; }
  .mobile-timing { display: flex; min-width: 0; align-items: center; gap: 6px; }
  .mobile-timing-values {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 1px;
    color: var(--rc35-ink-2);
    line-height: 1.25;
  }
  .mobile-timing-values .timing-value { font-size: 10.5px; }
  .mobile-details-preview { grid-column: 1 / -1; }
  .mobile-details-preview .details-button { width: 100%; }
  .pagination-bar { width: 100%; max-width: 100%; align-items: center; gap: 8px; overflow: hidden; }
  .pager-summary { width: auto; }
  .pager-controls { width: auto; min-width: 0; flex: 1 1 auto; flex-wrap: nowrap; justify-content: flex-end; gap: 8px; margin-left: 0; overflow: hidden; }
  .page-edge { display: none; }
  .pager-navigation { max-width: 100%; overflow: hidden; }
  .page-jump { width: 100%; flex: 1 0 100%; justify-content: flex-end; }
}

/* rc35 的移动弹窗使用视口两侧各 12px 留白，规格仍保留 p-4 和 rounded-xl。 */
@media (max-width: 639px) {
  .detail-dialog-header { gap: 4px; }
  .detail-dialog {
    width: calc(100vw - 1.5rem);
    max-width: calc(100vw - 1.5rem);
    max-height: calc(100dvh - 1.5rem);
  }
}
</style>

<style scoped>
.logs-table .request-chain-row>td{padding:0;white-space:normal;text-align:left}
.mobile-request-chain{margin-top:12px}
.request-scope-banner{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;color:var(--rc35-ink-2);font-size:12px}
.retry-chain-trigger[aria-expanded="true"]{box-shadow:0 0 0 2px var(--rc35-accent-weak)}
.detail-full-information { display:contents; }
@media(max-width:900px) {
  .logs-page { margin:0;padding:0;gap:8px; }
  .logs-toolbar { padding:0;background:transparent;border:0;box-shadow:none; }
  .toolbar-meta { margin-top:8px;padding-top:0;border-top:0;gap:8px; }
  .stat-badges { display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0;width:100%;padding:8px 0;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:8px; }
  .stat-badge { display:flex;flex-direction:column;gap:4px;min-width:0;height:auto;min-height:36px;padding:0 6px;border:0;border-radius:0;background:none;font-size:12px; }
  .stat-badge+.stat-badge { border-left:1px solid var(--ct-line); }
  .stat-badge i { display:none; }
  .stat-badge strong { font-size:14px;color:var(--ct-ink);overflow-wrap:anywhere; }
  .mobile-result-count { font-size:12px;color:var(--ct-ink-3); }
  .mobile-log-list { gap:8px;align-content:start; }
  .mobile-log-card { padding:10px 12px 0;border-radius:9px;box-shadow:none; }
  .mobile-card-time { display:flex;justify-content:space-between;align-items:center;gap:8px;color:var(--ct-ink-3);font-size:12px; }
  .mobile-card-time time { flex-shrink:0; }
  .mobile-card-time .status-badge { min-width:0;text-align:right;overflow-wrap:anywhere; }
  .mobile-card-identity { display:flex;align-items:center;gap:6px;min-width:0;flex-wrap:wrap;color:var(--ct-ink); }
  .mobile-card-identity button { padding:4px 0;min-height:32px;max-width:100%;white-space:normal;border:0;background:transparent;color:inherit;font-size:14px;font-weight:600;overflow-wrap:anywhere;text-align:left; }
  .mobile-card-identity strong { min-width:0;max-width:100%;font-size:14px;overflow-wrap:anywhere; }
  .mobile-card-identity>span { color:var(--ct-ink-3); }
  .mobile-card-token { margin:0 0 6px;color:var(--ct-ink-3);font-size:12px;overflow-wrap:anywhere; }
  .mobile-card-metrics { display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:6px 12px;margin:0 0 6px;font-size:12px; }
  .mobile-card-metrics>div { display:flex;flex-wrap:wrap;gap:4px 8px;min-width:0; }
  .mobile-card-metrics dt { color:var(--ct-ink-3); }
  .mobile-card-metrics dd { margin:0;color:var(--ct-ink);font-size:13px;font-weight:600;overflow-wrap:anywhere;font-variant-numeric:tabular-nums; }
  .mobile-card-channel { display:flex;align-items:center;gap:6px;flex-wrap:wrap;margin-bottom:4px;font-size:12px;color:var(--ct-ink-3);overflow-wrap:anywhere; }
  .mobile-card-channel>span { min-width:0;max-width:100%; }
  .mobile-card-detail { display:flex;align-items:center;justify-content:space-between;min-height:36px;width:100%;padding:4px 0;border:0;border-top:1px solid var(--ct-line);background:transparent;color:var(--ct-accent);font:inherit;font-size:13px;text-align:left; }
  .mobile-card-detail span { font-size:22px; }
  .mobile-pagination { display:flex;justify-content:space-between;align-items:center;gap:12px;color:var(--ct-ink-3);font-size:13px; }
  .mobile-pagination button,.mobile-detail-footer button { min-height:44px;padding:8px 16px;border:1px solid var(--ct-line-strong);border-radius:7px;background:var(--ct-surface);color:var(--ct-accent);font:inherit; }
  .mobile-pagination button:disabled,.mobile-detail-footer button:disabled { color:var(--ct-ink-3);background:var(--ct-surface-2); }
  .detail-dialog,.detail-dialog:not(.is-wide),.detail-dialog.is-wide { width:100%;max-width:100%;height:100dvh;max-height:100dvh;border-radius:0;padding:0;gap:0; }
  .detail-dialog-header { padding:16px 54px 16px 16px;min-height:60px;border-bottom:1px solid var(--ct-line); }
  .detail-dialog-title { flex-wrap:wrap; }
  .detail-dialog-close { top:8px;right:8px;width:44px;height:44px; }
  .detail-dialog-body { flex:1;min-height:0;height:auto;max-height:none;margin:0;background:var(--ct-bg); }
  .detail-dialog-body-inner { padding:12px; }
  .detail-dialog-content { gap:12px; }
  .mobile-detail-summary { display:grid;gap:12px; }
  .mobile-detail-card { padding:16px;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:9px;min-width:0; }
  .mobile-detail-card h3 { margin:0 0 14px;font-size:14px;font-weight:600;color:var(--ct-ink); }
  .mobile-detail-intro { display:flex;flex-wrap:wrap;justify-content:space-between;gap:12px; }
  .mobile-detail-intro>div { min-width:0;overflow-wrap:anywhere; }
  .mobile-detail-intro strong { font-size:18px;color:var(--ct-ink); }
  .mobile-detail-intro p { margin:6px 0;color:var(--ct-ink); }
  .mobile-detail-intro time { color:var(--ct-ink-3);font-size:12px; }
  .mobile-detail-amount { display:flex;flex-direction:column;gap:8px; }
  .mobile-detail-amount span { color:var(--ct-ink-3);font-size:12px; }
  .mobile-detail-amount b { color:var(--ct-ink);font-size:24px;line-height:1.2; }
  .mobile-performance { display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px 12px;margin:0; }
  .mobile-performance dt,.mobile-detail-rows dt { color:var(--ct-ink-3);font-size:13px; }
  .mobile-performance dd { margin:6px 0 0;color:var(--ct-ink);font-weight:600;overflow-wrap:anywhere; }
  .mobile-detail-rows { margin:0; }
  .mobile-detail-rows>div { display:flex;justify-content:space-between;gap:16px;padding:12px 0;border-bottom:1px solid var(--ct-line); }
  .mobile-detail-rows>div:last-child { border-bottom:0;padding-bottom:0; }
  .mobile-detail-rows dt { flex-shrink:0; }
  .mobile-detail-rows dd { margin:0;min-width:0;text-align:right;color:var(--ct-ink);overflow-wrap:anywhere; }
  .mobile-error-content p { margin:0;white-space:pre-wrap;overflow-wrap:anywhere;color:var(--ct-crit); }
  .detail-full-information { display:block;padding:16px;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:9px; }
  .detail-full-information summary { min-height:28px;cursor:pointer;font-weight:600; }
  .detail-full-information[open]>summary { margin-bottom:16px; }
  .detail-full-information .detail-section { margin-top:16px; }
  .mobile-detail-footer { flex:none;padding:12px 16px calc(12px + env(safe-area-inset-bottom));border-top:1px solid var(--ct-line);background:var(--ct-surface); }
  .mobile-detail-footer button { width:100%; }
}
</style>
