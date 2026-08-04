<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RefreshLeft, Search } from '@element-plus/icons-vue'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { formatQuota, formatTime } from '../utils/format'

const auth = useAuthStore()
const filters = useFiltersStore()
const prefs = usePrefsStore()
const keyword = ref('')
const status = ref<number | undefined>()
const page = ref(1)
const pageSize = ref(10)
const userIDs = ref('')
const params = computed(() => ({
  site: filters.site_id,
  user_ids: auth.user?.role === 'admin' ? userIDs.value : undefined,
  keyword: keyword.value,
  status: status.value,
  limit: pageSize.value,
  offset: (page.value - 1) * pageSize.value,
}))
const state = useAsyncData(() => passthrough.users(params.value))
const total = computed(() => state.data.value?.total ?? 0)
const pageStart = computed(() => total.value ? (page.value - 1) * pageSize.value + 1 : 0)
const pageEnd = computed(() => Math.min(page.value * pageSize.value, total.value))
const totalPages = computed(() => Math.ceil(total.value / pageSize.value))
const money = (quota: number) => formatQuota(quota, prefs.quotaPerUnit, prefs.currencySymbol)
const formatUnixTime = (value: number) => value > 0 ? formatTime(new Date(value * 1000).toISOString()) : '—'
const userInitial = (row: { display_name: string; username: string }) => (row.display_name || row.username || '?').trim().charAt(0).toUpperCase()
const totalQuota = (row: { quota: number; used_quota: number }) => row.quota + row.used_quota
const remainingPercent = (row: { quota: number; used_quota: number }) => {
  const totalValue = totalQuota(row)
  return totalValue > 0 ? Math.min(100, Math.round(row.quota / totalValue * 100)) : 0
}
const quotaColor = (row: { quota: number; used_quota: number }) => {
  const percent = remainingPercent(row)
  return percent <= 10 ? '#ce3b44' : percent <= 30 ? '#b96e0c' : '#2f5fe0'
}
async function load() {
  try { await Promise.all([filters.loadInstances(), prefs.load()]) } catch { /* Content request reports its own error. */ }
  await state.reload()
}
function search() { page.value = 1; void state.reload() }
function reset() { keyword.value = ''; status.value = undefined; userIDs.value = ''; search() }
function changePage(value: number) { page.value = value; void state.reload() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; void state.reload() }
onMounted(() => { void load() })
watch(() => filters.site_id, (site, previous) => {
  if (site && site !== previous) { page.value = 1; void state.reload() }
})
</script>
<template>
  <AppShell title="用户管理">
    <div class="filter-bar">
      <div class="filter-fields">
        <el-input v-model="keyword" clearable placeholder="搜索用户名、显示名称或用户 ID" class="keyword" @keyup.enter="search"/>
        <el-select v-model="status" clearable placeholder="全部状态" class="status-filter">
          <el-option label="正常" :value="1"/><el-option label="停用" :value="2"/>
        </el-select>
        <el-input v-if="auth.user?.role==='admin'" v-model="userIDs" clearable placeholder="限定用户 ID，逗号分隔" class="user-filter"/>
      </div>
      <div class="filter-actions">
        <el-button type="primary" :icon="Search" @click="search">查询</el-button>
        <el-button :icon="RefreshLeft" @click="reset">重置</el-button>
      </div>
    </div>
    <el-alert v-if="state.error.value" :title="state.error.value" type="error" show-icon :closable="false"><el-button link type="primary" @click="state.reload">重新加载</el-button></el-alert>
    <el-alert v-else-if="!state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。" type="info" show-icon :closable="false"/>
    <div v-loading="state.loading.value" class="users-card">
      <div class="table-panel">
        <el-table :data="state.data.value?.items||[]" height="100%" empty-text="暂无用户数据">
          <el-table-column prop="id" label="ID" width="72"><template #default="s"><span class="user-id">{{s.row.id}}</span></template></el-table-column>
          <el-table-column label="用户" min-width="250"><template #default="s"><div class="user-cell"><span class="user-avatar">{{userInitial(s.row)}}</span><div><div class="user-name">{{s.row.display_name||s.row.username}}</div><div class="subtle">@{{s.row.username}}</div></div></div></template></el-table-column>
          <el-table-column label="状态" width="100"><template #default="s"><span class="status-pill" :class="s.row.status===1?'is-active':'is-disabled'"><i/>{{s.row.status===1?'正常':'停用'}}</span></template></el-table-column>
          <el-table-column label="余额 / 总额度" min-width="330"><template #default="s"><div class="quota-cell"><div class="quota-line"><strong>{{money(s.row.quota)}}</strong><span>共 {{money(totalQuota(s.row))}}</span><em>{{remainingPercent(s.row)}}%</em></div><el-progress :percentage="remainingPercent(s.row)" :color="quotaColor(s.row)" :stroke-width="5" :show-text="false"/></div></template></el-table-column>
          <el-table-column label="已用额度" min-width="130"><template #default="s"><span class="used-quota">{{money(s.row.used_quota)}}</span></template></el-table-column>
          <el-table-column label="创建时间" width="180"><template #default="s"><time>{{formatUnixTime(s.row.created_at)}}</time></template></el-table-column>
          <el-table-column label="最后登录时间" width="180"><template #default="s"><time :class="{'is-empty':!s.row.last_login_at}">{{formatUnixTime(s.row.last_login_at)}}</time></template></el-table-column>
        </el-table>
      </div>
      <div class="pagination">
        <span>显示第 {{pageStart}} 条 - 第 {{pageEnd}} 条，共 {{total}} 条</span>
        <div class="pagination-controls">
          <span>总页数：{{totalPages}}</span>
          <el-pagination :current-page="page" :page-size="pageSize" layout="prev, pager, next" :total="total" @update:current-page="changePage"/>
          <el-select :model-value="pageSize" class="page-size-select" @change="changePageSize">
            <el-option v-for="size in [10,20,50,100]" :key="size" :label="`每页条数：${size}`" :value="size"/>
          </el-select>
        </div>
      </div>
    </div>
  </AppShell>
</template>
<style scoped>
.filter-bar,.filter-fields,.filter-actions,.pagination,.pagination-controls,.quota-line,.user-cell{display:flex;align-items:center}.filter-bar{justify-content:space-between;gap:16px;padding:12px 14px;margin-bottom:12px;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:10px;box-shadow:var(--ct-shadow)}.filter-fields{flex:1;gap:8px;min-width:0}.filter-actions{gap:8px;padding-left:14px;border-left:1px solid var(--ct-line)}.filter-actions :deep(.el-button+.el-button){margin-left:0}.filter-bar :deep(.el-input__wrapper),.filter-bar :deep(.el-select__wrapper){min-height:36px;border-radius:7px;box-shadow:0 0 0 1px var(--ct-line) inset;transition:box-shadow .18s ease,background .18s ease}.filter-bar :deep(.el-input__wrapper:hover),.filter-bar :deep(.el-select__wrapper:hover){box-shadow:0 0 0 1px var(--ct-line-strong) inset}.filter-bar :deep(.is-focus){box-shadow:0 0 0 1px var(--ct-accent) inset!important}.keyword{width:min(360px,32vw)}.status-filter{width:142px}.user-filter{width:250px}.users-card{background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:10px;box-shadow:0 1px 3px rgba(16,24,40,.06);overflow:hidden}.table-panel{height:calc(100vh - 220px);min-height:430px}.table-panel :deep(.el-table){--el-table-header-bg-color:#f8fafc;--el-table-header-text-color:#778397;--el-table-row-hover-bg-color:#f8faff;font-size:13px}.table-panel :deep(.el-table__header th.el-table__cell){height:42px;padding:0;border-bottom-color:var(--ct-line);font-size:12px;font-weight:600;letter-spacing:.02em}.table-panel :deep(.el-table__body td.el-table__cell){height:62px;padding:0;border-bottom-color:#edf0f4}.table-panel :deep(.el-table .cell){padding-left:13px;padding-right:13px}.table-panel :deep(.el-table__row){transition:background-color .15s ease}.user-id{color:var(--ct-ink-3);font-variant-numeric:tabular-nums}.user-cell{gap:11px}.user-avatar{display:inline-flex;align-items:center;justify-content:center;width:32px;height:32px;flex:0 0 32px;border-radius:9px;background:var(--ct-accent-weak);color:var(--ct-accent);font-size:12px;font-weight:700}.user-name{max-width:190px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--ct-ink);font-weight:600;line-height:1.35}.subtle{margin-top:3px;color:var(--ct-ink-3);font-size:11.5px;line-height:1.2}.status-pill{display:inline-flex;align-items:center;gap:6px;padding:3px 9px;border-radius:999px;font-size:12px;font-weight:500;line-height:20px}.status-pill i{width:6px;height:6px;border-radius:50%}.status-pill.is-active{background:var(--ct-ok-weak);color:var(--ct-ok)}.status-pill.is-active i{background:var(--ct-ok)}.status-pill.is-disabled{background:#f0f2f5;color:#778397}.status-pill.is-disabled i{background:#9aa4b2}.quota-cell{max-width:500px}.quota-line{gap:8px;margin-bottom:7px;line-height:1}.quota-line strong{color:var(--ct-ink);font-size:13px;font-weight:650;font-variant-numeric:tabular-nums}.quota-line span{color:var(--ct-ink-3);font-size:11.5px}.quota-line em{margin-left:auto;color:var(--ct-ink-3);font-size:11px;font-style:normal;font-variant-numeric:tabular-nums}.quota-cell :deep(.el-progress-bar__outer){background:#edf0f5}.used-quota,time{color:var(--ct-ink-2);font-variant-numeric:tabular-nums}.used-quota{font-weight:500}time{font-size:12px;white-space:nowrap}.is-empty{color:var(--ct-ink-3)}.pagination{justify-content:space-between;min-height:54px;padding:8px 14px;border-top:1px solid var(--ct-line);background:#fbfcfe;color:var(--ct-ink-3);font-size:12px}.pagination-controls{gap:12px;color:var(--ct-ink-2)}.pagination-controls :deep(.el-pagination){--el-pagination-button-bg-color:transparent;--el-pagination-hover-color:var(--ct-accent)}.pagination-controls :deep(.el-pager li),.pagination-controls :deep(.btn-prev),.pagination-controls :deep(.btn-next){border-radius:7px}.pagination-controls :deep(.el-pager li.is-active){background:var(--ct-accent-weak);color:var(--ct-accent);font-weight:600}.page-size-select{width:142px}.pagination-controls :deep(.page-size-select .el-select__wrapper){min-height:32px;border-radius:16px;background:#f0f3f7;box-shadow:none}@media(max-width:1280px){.keyword{width:280px}.user-filter{width:210px}.table-panel :deep(.el-table .cell){padding-left:10px;padding-right:10px}}
</style>
