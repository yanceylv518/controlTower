import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(
  new URL('../src/views/ReadonlyLogsView.vue', import.meta.url),
  'utf8'
).replace(/\r\n/g, '\n')
const asyncDataSource = readFileSync(
  new URL('../src/composables/useAsyncData.ts', import.meta.url),
  'utf8'
)
const passthroughApiSource = readFileSync(
  new URL('../../shared/src/api/passthrough.ts', import.meta.url),
  'utf8'
)

// 回归保护：rc35 的列菜单必须保留持久化入口和全部可选列。
test('readonly logs exposes a persistent rc35 column menu', () => {
  assert.match(source, /ct\.readonly-logs\.columns\.v1/)
  assert.match(source, /restoreColumnVisibility\(\)/)
  assert.match(source, /toggleColumn\(column\.key, Boolean\(\$event\)\)/)
  assert.match(source, /background: var\(--ct-surface\) !important/)
  assert.match(source, /z-index: 3000 !important/)
  assert.doesNotMatch(source, /<header class=\"logs-heading\">/)
  for (const label of ['渠道', '用户', '令牌', '模型', '流', 'Tokens', '费用', '耗时', '详情']) {
    assert.match(source, new RegExp(`label: '${label}'`))
  }
})

// 回归保护：默认范围应覆盖当天记录，避免稀疏日志在最近一小时之外时显示为空。
test('readonly logs defaults to the current day', () => {
  assert.match(source, /start\.setHours\(0, 0, 0, 0\)/)
  assert.match(source, /new Date\(now\.getTime\(\) \+ 60 \* 60 \* 1000\)/)
  assert.match(source, /timeRangeChanged = computed\(/)
  assert.match(source, /:reset-enabled="timeRangeChanged && !backgroundRefreshing"/)
})

// 回归保护：首行字段顺序必须与 rc35 参考布局一致，模型和分组不能重新占满整行。
test('readonly logs keeps identity filters before compact model filters', () => {
  const primary = source.match(/<div class="primary-filters"[^>]*>([\s\S]*?)<\/div>/)?.[1] || ''
  for (const className of ['filter-username', 'filter-channel', 'filter-request', 'filter-model', 'filter-group']) {
    assert.ok(primary.includes(className), `missing ${className} in primary filters`)
  }
  const order = ['filter-username', 'filter-channel', 'filter-request', 'filter-model', 'filter-group']
    .map((className) => primary.indexOf(className))
  assert.deepEqual(order, [...order].sort((a, b) => a - b))
  assert.match(source, /\.filter-model, \.filter-group \{ width: 150px; min-width: 132px; flex: 0 1 150px; \}/)
})

// 回归保护：所有筛选项始终位于同一组主筛选结构中，不再存在折叠状态。
test('readonly logs keeps all filters visible in the primary row', () => {
  const primary = source.match(/<div class="primary-filters"[^>]*>([\s\S]*?)<\/div>/)?.[1] || ''
  for (const className of ['filter-token', 'filter-upstream']) {
    assert.ok(primary.includes(className))
  }
  const order = ['filter-group', 'filter-token', 'filter-upstream']
    .map((className) => primary.indexOf(className))
  assert.deepEqual(order, [...order].sort((a, b) => a - b))
  assert.doesNotMatch(source, /advancedOpen|advancedFilterCount|filter-toggle|has-advanced|advanced-filters/)
  assert.match(source, /\.filter-token, \.filter-upstream \{ width: 184px; min-width: 156px; flex: 0 1 184px; \}/)
  assert.match(source, /\.primary-filters \{[\s\S]*flex-wrap: nowrap;[\s\S]*overflow-x: auto;/)
})

// 回归保护：日志类型位于敏感字段切换之前，避免把操作控件挤回主筛选行。
test('readonly logs places type selector before sensitive toggle', () => {
  const actions = source.match(/<div class="toolbar-actions">([\s\S]*?)<\/div>/)?.[1] || ''
  assert.ok(actions.indexOf('action-type') >= 0)
  assert.ok(actions.indexOf('action-type') < actions.indexOf('sensitiveVisible'))
  assert.doesNotMatch(source.match(/<div class="primary-filters"[^>]*>([\s\S]*?)<\/div>/)?.[1] || '', /filter-type/)
})

// 回归保护：日期控件的独立重置只修改时间，不清空其他筛选条件。
test('readonly logs keeps an isolated time reset action', () => {
  const reset = source.match(/const resetTime = \(\) => \{([\s\S]*?)\n\}/)?.[1] || ''
  assert.match(source, /const resetTime = \(\) =>/)
  assert.match(reset, /timeRange\.value = defaultTimeRange\(\)/)
  assert.match(reset, /resetRange\.value = \[/)
  assert.match(reset, /refreshSearch\(\)/)
  for (const field of ['username', 'tokenName', 'modelName', 'group', 'requestID', 'upstreamRequestID', 'channelID', 'logType']) {
    assert.doesNotMatch(reset, new RegExp(`${field}\\.value\\s*=`))
  }
})

// 查询区的重置仍然恢复整组条件，避免把原有行为误改成只重置日期。
test('readonly logs keeps the full reset beside search', () => {
  const reset = source.match(/const reset = \(\) => \{([\s\S]*?)\n\}/)?.[1] || ''
  for (const field of ['username', 'tokenName', 'modelName', 'group', 'requestID', 'upstreamRequestID', 'channelID', 'logType', 'timeRange']) {
    assert.match(reset, new RegExp(`${field}\\.value\\s*=`))
  }
  assert.match(source, /class="secondary-action" :disabled="backgroundRefreshing" @click="reset"/)
  assert.match(source, /backgroundRefreshing \? '更新中' : '重置'/)
  assert.match(source, /'查询中' : backgroundRefreshing \? '更新中' : '查询'/)
})

// 回归保护：查询与两个重置入口必须复用后台刷新，避免旧列表被 v-loading 覆盖。
test('readonly logs query and resets use a non-blocking refresh', () => {
  assert.match(source, /const backgroundRefreshing = ref\(false\)/)
  assert.match(source, /const refreshSearch = async \(\) =>/)
  assert.match(source, /state\.refresh\(\)/)
  assert.match(source, /void statState\.reload\(\)/)
  assert.match(source, /void countState\.reload\(\)/)
  assert.match(source, /日志查询失败，当前保留上一次列表，请重新查询/)
  assert.match(source, /:reset-enabled="timeRangeChanged && !backgroundRefreshing"/)
  assert.match(source, /:class="\{ 'is-background-refreshing': backgroundRefreshing \}"/)
  assert.match(source, /:aria-busy="backgroundRefreshing"/)
  assert.match(source, /if \(backgroundRefreshing\.value\) return/)
  assert.match(source, /const search = \(\) => \{\n  if \(backgroundRefreshing\.value\) return\n  void refreshSearch\(\)/)
})

// 回归保护：桌面和移动列表只能挂载一套，避免隐藏节点继续消耗渲染预算。
test('readonly logs mounts only the active responsive layout', () => {
  assert.match(source, /mobileViewport = ref\(/)
  assert.match(source, /matchMedia\('\(max-width: 900px\)'\)/)
  assert.match(source, /<div v-if="!mobileViewport" v-loading="state\.loading\.value" class="desktop-table" ref="tableScroll">/)
  assert.match(source, /<div v-else v-loading="state\.loading\.value" class="mobile-log-list">/)
  assert.match(source, /<tbody v-for="view in renderedRows" :key="view.id" v-memo="\[view.memoKey, expandedRetryID === view.id\]"/)
  assert.match(source, /<article v-for="view in renderedRows" v-memo=/)
  assert.match(source, /const logRowChunkSize = 10/)
  assert.match(source, /window\.requestAnimationFrame\(appendChunk\)/)
  assert.match(source, /const logRowCache = new Map<string, LogRowView>\(\)/)
  assert.match(source, /if \(previousRows\.length > 0 && rows\.length > 0\)/)
  assert.match(source, /:key="view.id"/)
})

// 回归保护：实例列表设置首个站点时不能让 watcher 与首屏加载重复刷新。
test('readonly logs suppresses the initial site watcher refresh', () => {
  assert.match(source, /initialLoadPending = true/)
  assert.match(source, /await reloadAll\(\)\n  initialLoadPending = false/)
  assert.match(source, /if \(!initialLoadPending && site && site !== previous\)/)
})

// 回归保护：快速切换筛选条件时，旧请求不能覆盖最后一次查询结果。
test('async data ignores stale responses', () => {
  assert.match(asyncDataSource, /let requestSequence = 0/)
  assert.match(asyncDataSource, /const requestId = \+\+requestSequence/)
  assert.match(asyncDataSource, /let activeController: AbortController \| undefined/)
  assert.match(asyncDataSource, /const controller = new AbortController\(\)/)
  assert.match(asyncDataSource, /loader\(controller\.signal\)/)
  assert.match(asyncDataSource, /function cancel\(\)/)
  assert.match(asyncDataSource, /if \(requestId !== requestSequence\) return/)
  assert.match(asyncDataSource, /if \(requestId === requestSequence\)/)
  assert.match(asyncDataSource, /if \(!background\) loading\.value = false/)
  assert.match(source, /state\.cancel\(\)/)
  assert.match(source, /statState\.cancel\(\)/)
  assert.match(source, /countState\.cancel\(\)/)
})

// 回归保护：日期范围必须使用 rc35 独立组件，不能回退到 Element Plus 双月面板。
test('readonly logs keeps rc35 geometry and theme-aware poppers', () => {
  assert.doesNotMatch(source, /logs-table th:nth-child|logs-table td:nth-child/)
  assert.match(source, /--rc35-surface: var\(--ct-surface\)/)
  assert.match(source, /--rc35-ink: var\(--ct-ink\)/)
  assert.match(source, /<CompactDateTimeRangePicker v-model="timeRange" :reset-enabled="timeRangeChanged && !backgroundRefreshing" class="filter-time" @reset="resetTime" \/>/)
  assert.doesNotMatch(source, /<el-date-picker[^>]+v-model="timeRange"/)
  assert.match(source, /CompactDateTimeRangePicker from '\.\.\/components\/CompactDateTimeRangePicker\.vue'/)
  assert.match(source, /\.logs-toolbar :deep\(\.filter-time\.compact-date-range\)/)
  assert.match(source, /width: 294px !important/)
})

const datePickerSource = readFileSync(
  new URL('../src/components/CompactDateTimeRangePicker.vue', import.meta.url),
  'utf8'
)
const identityColorsSource = readFileSync(
  new URL('../src/utils/identityColors.ts', import.meta.url),
  'utf8'
)

// rc35 日期组件的快捷项、确认提交和边界行为必须独立于 Element Plus。
test('compact date picker follows the rc35 interaction contract', () => {
  assert.doesNotMatch(datePickerSource, /<el-/)
  // rc35 每端只使用一个 datetime-local 输入，避免回退到双月面板或四个拆分控件。
  assert.equal((datePickerSource.match(/type="datetime-local"/g) || []).length, 2)
  assert.doesNotMatch(datePickerSource, /type="date"|type="time"/)
  for (const label of ['今天', '近 7 天', '本周', '近 30 天', '本月']) {
    assert.match(datePickerSource, new RegExp(`label: '${label}'`))
  }
  assert.match(datePickerSource, /emit\('update:modelValue', range\)/)
  assert.match(datePickerSource, /reset:\s*\[\]/)
  assert.match(datePickerSource, /class="compact-date-reset"/)
  assert.match(source, /:reset-enabled="timeRangeChanged && !backgroundRefreshing" class="filter-time" @reset="resetTime"/)
  assert.match(datePickerSource, /结束时间必须晚于开始时间/)
  assert.match(datePickerSource, /document\.addEventListener\('pointerdown'/)
})

// 回归保护：用户头像与分组沿用 New API 的身份色，令牌保持中性样式。
test('readonly logs keeps stable user and group identity colors', () => {
  assert.match(identityColorsSource, /hash = \(hash \* 31 \+ value\.charCodeAt\(index\)\) >>> 0/)
  assert.match(identityColorsSource, /const hue = hash % 360/)
  assert.match(identityColorsSource, /const saturation = 54 \+ \(hash % 8\)/)
  assert.match(identityColorsSource, /const lightness = 52 \+ \(\(hash >> 4\) % 8\)/)
  assert.match(identityColorsSource, /sum \+= name\.charCodeAt\(index\)/)
  assert.match(source, /avatarStyle: visible && row\.username \? getUserAvatarStyle\(row\.username\) : undefined/)
  assert.match(source, /groupTone: visible && row\.group && row\.group !== 'auto' \? getTokenColorClass\(row\.group\) : 'token-tone-hidden'/)
  assert.match(source, /:class="\{ 'is-hidden': !sensitiveVisible \}" :style="view\.avatarStyle"/)
  assert.match(source, /:class="view\.groupTone"/)
  for (const tone of [
    'amber', 'blue', 'cyan', 'green', 'grey', 'indigo', 'light-blue', 'lime',
    'orange', 'pink', 'purple', 'red', 'teal', 'violet', 'yellow', 'hidden',
  ]) {
    assert.match(source, new RegExp(`token-tone-${tone}`))
  }
})

// 回归保护：fallback 事实必须从接口一路传到列表和详情；管理员可见链路，viewer 只见布尔标志。
test('readonly logs exposes fallback requests and channel chains', () => {
  assert.match(passthroughApiSource, /fallback\?: boolean/)
  assert.match(passthroughApiSource, /fallback_channels\?: string\[\]/)
  assert.match(source, /function fallbackChannelsFor\(row: ReadonlyLog\)/)
  assert.match(source, /function retryChannelsFor\(row: ReadonlyLog\)/)
  assert.match(source, /function isFallback\(row: ReadonlyLog\)/)
  assert.match(source, /const fallback = isFallback\(row\)/)
  assert.match(source, /fallback,\n\s+fallbackChannels,/)
  assert.match(source, /const retryChannels = retryChannelsFor\(row\)/)
  assert.match(source, /retryChain: admin && retryChannels\.length > 1 \? retryChannels\.join\(' → '\) : ''/)
  assert.match(source, /class="retry-chain-trigger"/)
  assert.match(source, /v-if="isAdmin && \(view\.fallback \|\| view\.retryChain\)"/)
  assert.match(source, /function openRetryHover\(view: LogRowView, event: MouseEvent \| FocusEvent\)/)
  assert.match(source, /const retryCount = Math\.max\(1, retryChannelsFor\(view\.source\)\.length - 1\)/)
  assert.match(source, /class="retry-hover-card"/)
  assert.match(source, /class="channel-cell" :aria-label="isAdmin && view\.retryChain \? requestChainTitle\(view\.source\) : undefined"/)
  assert.doesNotMatch(source, /class="channel-cell"[^>]*:title=/)
  assert.match(source, /@mouseenter="openRetryHover\(view, \$event\)" @mouseleave="closeRetryHover"/)
  assert.match(source, /重试\{\{ retryHover\.retryCount \}\}次：/)
  assert.doesNotMatch(source, /fallback-label/)
  assert.match(source, /const expandedRetryID = ref<number \| null>\(null\)/)
  assert.match(source, /<FallbackRequestChain/)
  assert.match(source, /:colspan="visibleColumnCount"/)
  assert.match(source, /@click.stop="toggleRetryChain\(view\)"/)
  assert.doesNotMatch(source, /class="retry-chain-popover/)
  assert.match(source, /class="detail-dialog-fallback"/)
  assert.match(source, /<span class="detail-label">Fallback<\/span>/)
  assert.match(source, /admin_info/)
  assert.match(source, /row\.fallback_channels\?\.length/)
})

// 渠道按 ID 取文字色，品牌图标使用原始 SVG，不能再次退回通用连接图标。
test('readonly logs keeps rc35 channel and model semantic colors', () => {
  assert.match(source, /channelTone: channelID > 0 \? getTokenColorClass\(String\(channelID\)\)/)
  assert.match(source, /modelTone: row\.model_name \? getTokenColorClass\(row\.model_name\)/)
  assert.match(source, /view\.channelTone/)
  assert.match(source, /<ModelProviderIcon v-if="view\.modelProvider" :name="view\.modelProvider\.name"/)
  assert.match(source, /\.channel-badge \{[^}]*border: 0;[^}]*background: transparent;/)
  assert.doesNotMatch(source, /<Connection\s*\/>|\.token-badge\[class/)
})

// 回归保护：详情必须采用 rc35 的单列信息流和分组卡片，不得退回旧双列 Element Plus 弹窗。
test('readonly logs keeps the rc35 native detail dialog layout', () => {
  assert.match(source, /<Teleport to="body">/)
  assert.match(source, /class="detail-dialog-backdrop"[\s\S]*@mousedown\.self="closeDetail"/)
  assert.match(source, /class="detail-dialog" :class="[^"]+" role="dialog" aria-modal="true"/)
  assert.match(source, /class="detail-dialog-title"[\s\S]*日志详情[\s\S]*detail-dialog-status/)
  assert.match(source, /class="detail-overview"/)
  assert.match(source, /class="detail-section"[\s\S]*<h3>请求转换<\/h3>/)
  assert.match(source, /<h3>模型映射<\/h3>/)
  assert.match(source, /<h3>Token 明细<\/h3>/)
  assert.match(source, /<h3>计费详情<\/h3>/)
  assert.match(source, /\.detail-dialog \{[\s\S]*max-width: calc\(100% - 2rem\);/)
  assert.match(source, /\.detail-dialog:not\(\.is-wide\) \{ max-width: 512px; \}/)
  assert.match(source, /\.detail-dialog\.is-wide \{ max-width: 1024px; \}/)
  assert.match(source, /\.detail-dialog-status \{[\s\S]*height: 20px;[\s\S]*gap: 4px;[\s\S]*padding: 0 6px;/)
  assert.match(source, /\.detail-row \{[\s\S]*grid-template-columns: 5\.25rem minmax\(0, 1fr\);/)
  assert.match(source, /\.detail-section-card \{[\s\S]*gap: 4px;[\s\S]*padding: 10px;/)
  assert.match(source, /\.detail-dialog-content \{[\s\S]*gap: 10px;/)
  assert.match(source, /\.detail-label \{[\s\S]*text-align: left;/)
  assert.match(source, /\.detail-dialog-body \{[\s\S]*flex: 0 1 auto;[\s\S]*overflow-y: auto;/)
  assert.match(source, /\.detail-dialog-body-inner \{[\s\S]*padding: 4px 8px 4px 4px;/)
  assert.match(source, /backdrop-filter: blur\(2px\)/)
  assert.match(source, /class="detail-close-glyph"/)
  assert.match(source, /detailCloseButton[\s\S]*handleDetailKeydown/)
  assert.doesNotMatch(source, /<el-dialog/)
  assert.doesNotMatch(source, /detail-grid|log-detail-dialog|1170px|el-dialog__footer/)
  assert.match(source, /\.logs-toolbar :deep\(\.filter-time\.compact-date-range\) \{[\s\S]*width: 100% !important;/)
})

// 隐藏列后表头和数据单元格必须使用同一稳定 class，避免 nth-child 偏移错位。
test('readonly logs binds optional table columns to stable classes', () => {
  for (const key of ['channel', 'user', 'token', 'model', 'stream', 'tokens', 'quota', 'timing', 'details']) {
    assert.match(source, new RegExp(`class=\\"col-${key}`))
    assert.match(source, new RegExp(`isColumnVisible\\('${key}'\\)`))
  }
})

// 回归保护：流状态和日志类型必须使用 rc35 的纯文字语义色，不能重新引入前置圆点或旧胶囊。
test('readonly logs renders stream and log types as text-only status labels', () => {
  assert.match(source, /class="stream-label"/)
  assert.match(source, /\.stream-label\.is-stream \{ color: var\(--ct-accent\)/)
  assert.match(source, /\.stream-label\.is-nonstream, \.stream-label\.is-unknown \{ color: var\(--rc35-ink-3\)/)
  assert.match(source, /\.status-consume \{ color: var\(--rc35-green\)/)
  assert.match(source, /\.status-error \{ color: var\(--ct-crit\)/)
  assert.doesNotMatch(source, /stream-pill/)
  assert.doesNotMatch(source, /status-badge i/)
  assert.doesNotMatch(source, /<span class="status-badge"[^>]*>\s*<i/)
})

// 回归保护：筛选输入不应挂载无效的字符串前缀图标，避免文字前出现空占位。
test('readonly logs filters do not render empty prefix icon slots', () => {
  assert.doesNotMatch(source, /prefix-icon=/)
})

// 回归保护：所有日志列统一左对齐，数字列不应再制造左侧大片留白。
test('readonly logs keeps table columns left aligned', () => {
  assert.doesNotMatch(source, /align-right/)
  assert.match(source, /\.tokens-cell \{ align-items: flex-start;/)
})

// 回归保护：页码可以直接提交跳转，每页条数位于其它分页控件之前。
test('readonly logs supports direct page navigation and compact pagination order', () => {
  assert.match(source, /const pageJump = ref\(''\)/)
  assert.match(source, /const pageSizeOptions = \[10, 20, 30, 40, 50, 100\] as const/)
  assert.match(source, /function togglePageSizeMenu\(\)/)
  assert.match(source, /function selectPageSize\(size: number\)/)
  assert.match(source, /const jumpToPage = \(\) => \{[\s\S]*Number\.isInteger\(requested\)[\s\S]*Math\.min\(requested, totalPages\.value\)[\s\S]*changePage\(page\)/)
  assert.match(source, /<form class="page-jump"[^>]*novalidate[^>]*@submit\.prevent="jumpToPage">/)
  assert.match(source, /id="readonly-log-page-jump"[^>]*type="number"/)
  const controlsStart = source.indexOf('<div class="pager-controls">')
  const pageSizeIndex = source.indexOf('class="page-size page-size-trigger"', controlsStart)
  const navigationIndex = source.indexOf('class="pager-navigation"', controlsStart)
  const jumpIndex = source.indexOf('class="page-jump"', controlsStart)
  assert.ok(pageSizeIndex >= 0 && pageSizeIndex < navigationIndex)
  assert.ok(navigationIndex < jumpIndex)
  assert.match(source, /countIsCurrent \? '总计：' : '已加载至：'/)
  assert.match(source, /<span class="page-size-label">每页行数<\/span>/)
  assert.match(source, /class="page-size page-size-trigger"[^>]*role="combobox"/)
  assert.match(source, /id="readonly-log-page-size-menu"[^>]*class="page-size-menu"[^>]*role="listbox"/)
  assert.match(source, /class="page-size-option"[^>]*role="option"[^>]*:aria-selected="size === limit"/)
  assert.match(source, /<Teleport to="body">[\s\S]*readonly-log-page-size-menu/)
  assert.match(source, /\.page-size-menu \{[\s\S]*--rc35-surface: var\(--ct-surface\);[\s\S]*--rc35-line: var\(--ct-line\);[\s\S]*position: fixed;[\s\S]*border-radius: 8px;[\s\S]*background: var\(--rc35-surface\);[\s\S]*box-shadow:/)
  assert.match(source, /\.page-size \{[\s\S]*border-radius: 8px;/)
  assert.match(source, /const measuredHeight = \(menu\?\.scrollHeight \|\| pageSizeOptions\.length \* 28 \+ 8\) \+ 2/)
  assert.match(source, /\.page-size-option \{[\s\S]*min-height: 28px;[\s\S]*padding: 4px 32px 4px 6px;/)
  assert.match(source, /\.page-size-option\[aria-selected='true'\] \{[\s\S]*background: var\(--rc35-accent-weak\);/)
  assert.match(source, /\.page-size-option\[aria-selected='true'\] \.page-size-check \{ opacity: 1; \}/)
  assert.doesNotMatch(source, /<select[^>]*class="page-size"/)
  assert.doesNotMatch(source, /<el-pagination/)
  assert.doesNotMatch(source, /每页条数：/)
  assert.match(source, /aria-label="跳转到页码"/)
})

// 回归保护：聚合短暂失配时，总计不能低于当前列表已有行数。
test('readonly logs keeps a visible list floor for the total count', () => {
  assert.match(source, /Math\.max\(countState\.data\.value\.total, fallbackTotal\.value\)/)
})

// 回归保护：桌面日志页固定在视口内，移动端仍由卡片列表自然增长。
test('readonly logs keeps desktop table scrolling inside the page shell', () => {
  assert.match(source, /@media \(min-width: 761px\) \{[\s\S]*\.logs-page \{[\s\S]*height: calc\(100vh - 52px\);[\s\S]*overflow: hidden;[\s\S]*\.logs-table-shell \{ flex: 1 1 0; \}[\s\S]*\.desktop-table \{ min-height: 0; \}/)
  assert.match(source, /\.desktop-table \{[\s\S]*height: 100%;[\s\S]*min-width: 0;[\s\S]*min-height: 430px;[\s\S]*overflow: auto;/)
  assert.match(source, /\.logs-table \{[\s\S]*width: max-content;[\s\S]*min-width: 100%;[\s\S]*table-layout: auto;/)
  assert.match(source, /\.logs-table th\.col-channel, \.logs-table td\.col-channel \{ min-width: 112px; \}/)
  assert.match(source, /\.logs-table-shell \{ flex: 0 0 auto; overflow: visible; border: 0; background: transparent; box-shadow: none; \}/)
  assert.match(source, /@media \(max-width: 900px\) \{[\s\S]*\.logs-page \{ height: auto;[\s\S]*overflow: visible;/)
  assert.match(source, /@media \(max-width: 900px\) \{[\s\S]*\.desktop-table \{ display: none; \}[\s\S]*\.mobile-log-list \{ display: grid;/)
})

// 回归保护：rc35 详情中的两个请求标识统一使用中文字段名，筛选和详情保持一致。
test('readonly logs uses the compact Chinese request id labels', () => {
  assert.match(source, /placeholder="请求ID"/)
  assert.match(source, /placeholder="上游请求ID"/)
  assert.match(source, /class="detail-label">请求ID<\/span>/)
  assert.match(source, /class="detail-label">上游请求ID<\/span>/)
  assert.doesNotMatch(source, /class="detail-label">Request ID<\/span>/)
  assert.doesNotMatch(source, /class="detail-label">上游 Request ID<\/span>/)
})

// 回归保护：首字和总耗时必须独立着色，并跟随 new-api rc35 的阈值。
test('readonly logs follows rc35 timing severity rules', () => {
  assert.match(source, /seconds < 5\) return 'success'/)
  assert.match(source, /seconds < 10\) return 'warning'/)
  assert.match(source, /seconds < 30\) return 'warning'/)
  assert.match(source, /completionTokens >= 100 && seconds > 0/)
  assert.match(source, /tokensPerSecond >= 30/)
  assert.match(source, /class=\"timing-segment\"/)
  assert.match(source, /const firstVariant = firstResponseVariant\(firstSeconds\)/)
  assert.match(source, /const durationTone = durationVariant\(row\)/)
  assert.doesNotMatch(source, /timingTone\(row\)/)
})

// 回归保护：错误日志仍属于 rc35 的业务记录，零费用和零耗时不能被旧版短路成空白。
test('readonly logs keeps error rows in the rc35 display and timing contract', () => {
  assert.match(source, /const displayableLogTypes = new Set\(\[0, 2, 5, 6\]\)/)
  assert.match(source, /const timingLogTypes = new Set\(\[2, 5\]\)/)
  assert.match(source, /const displayable = isDisplayableLog\(row\)/)
  assert.match(source, /<div v-if="view\.timing" class="timing-cell">/)
  assert.match(source, /quota: displayable \? money\(row\.quota\) : '—'/)
  assert.match(source, /if \(amount === 0\) return prefs\.currencySymbol \+ '0'/)
})

// 回归保护：详情必须覆盖 rc35 的计费、审计、流状态和内容字段，而不是只显示旧摘要。
test('readonly log details expose rc35 snapshot sections', () => {
  for (const heading of ['费用明细', '请求转换', '动态计费', '流状态', '订阅计费', '参数覆盖']) {
    assert.match(source, new RegExp(heading))
  }
  for (const field of ['折扣前额度', '折扣后额度', '节省额度', '计费表达式', '命中阶梯', '缓存写入（5 分钟）', '缓存写入（1 小时）', '结束原因', '软错误', '结束错误']) {
    assert.match(source, new RegExp(field))
  }
  assert.match(source, /dynamicTierMatched\(tier, textValue\(detailRow, 'matched_tier'\)\)/)
  assert.match(source, /dynamicRulesFor\(detailRow\)/)
  assert.match(source, /dynamicUsageFactsFor\(detailRow\)/)
  assert.match(source, /function detailContentFor\(row: ReadonlyLog\)/)
})
