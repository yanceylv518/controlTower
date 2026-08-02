<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { usePrefsStore } from '../stores/prefs'
import { useAsyncData } from '../composables/useAsyncData'
import { formatQuota } from '../utils/format'

const auth = useAuthStore()
const filters = useFiltersStore()
const prefs = usePrefsStore()
const keyword = ref('')
const status = ref<number | undefined>()
const page = ref(1)
const pageSize = ref(20)
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
const money = (quota: number) => formatQuota(quota, prefs.quotaPerUnit, prefs.currencySymbol)
const totalQuota = (row: { quota: number; used_quota: number }) => row.quota + row.used_quota
const remainingPercent = (row: { quota: number; used_quota: number }) => {
  const totalValue = totalQuota(row)
  return totalValue > 0 ? Math.min(100, Math.round(row.quota / totalValue * 100)) : 0
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
    <div class="page-head">
      <div><strong>用户列表</strong></div>
      <el-button @click="state.reload">刷新</el-button>
    </div>
    <div class="filter-bar">
      <el-input v-model="keyword" clearable placeholder="搜索用户名、显示名称或用户 ID" class="keyword" @keyup.enter="search"/>
      <el-select v-model="status" clearable placeholder="全部状态" class="status-filter">
        <el-option label="正常" :value="1"/><el-option label="停用" :value="2"/>
      </el-select>
      <el-input v-if="auth.user?.role==='admin'" v-model="userIDs" clearable placeholder="限定用户 ID，逗号分隔" class="user-filter"/>
      <el-button type="primary" @click="search">查询</el-button><el-button @click="reset">重置</el-button>
    </div>
    <el-alert v-if="state.error.value" :title="state.error.value" type="error" show-icon :closable="false"><el-button link type="primary" @click="state.reload">重新加载</el-button></el-alert>
    <el-alert v-else-if="!state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。" type="info" show-icon :closable="false"/>
    <div v-loading="state.loading.value" class="table-panel">
      <el-table :data="state.data.value?.items||[]" height="100%" empty-text="暂无用户数据">
        <el-table-column prop="id" label="ID" width="80"/>
        <el-table-column label="用户" min-width="220"><template #default="s"><div class="user-name">{{s.row.display_name||s.row.username}}</div><div class="subtle">@{{s.row.username}}</div></template></el-table-column>
        <el-table-column label="状态" width="100"><template #default="s"><el-tag :type="s.row.status===1?'success':'info'" effect="light">{{s.row.status===1?'正常':'停用'}}</el-tag></template></el-table-column>
        <el-table-column label="余额 / 总额度" min-width="300"><template #default="s"><div class="quota-line"><strong>{{money(s.row.quota)}}</strong><span>/ {{money(totalQuota(s.row))}}</span></div><el-progress :percentage="remainingPercent(s.row)" :stroke-width="6" :show-text="false"/></template></el-table-column>
        <el-table-column label="已用额度" min-width="150"><template #default="s">{{money(s.row.used_quota)}}</template></el-table-column>
      </el-table>
    </div>
    <div class="pagination"><span>共 {{total}} 位用户</span><el-pagination :current-page="page" :page-size="pageSize" :page-sizes="[20,50,100]" layout="sizes, prev, pager, next" :total="total" @update:current-page="changePage" @update:page-size="changePageSize"/></div>
  </AppShell>
</template>
<style scoped>
.page-head,.page-head>div,.filter-bar,.pagination,.quota-line{display:flex;align-items:center}.page-head{justify-content:space-between;margin-bottom:12px}.page-head>div{gap:12px}.page-head span,.subtle,.pagination{color:var(--el-text-color-secondary);font-size:12px}.filter-bar{gap:10px;padding:12px 16px;margin-bottom:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.keyword{width:330px}.status-filter{width:140px}.user-filter{width:240px}.table-panel{height:calc(100vh - 355px);min-height:360px;margin-top:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden}.user-name{font-weight:600}.quota-line{gap:6px;margin-bottom:7px}.quota-line span{color:var(--el-text-color-secondary)}.pagination{justify-content:flex-end;gap:18px;padding:14px 4px}
</style>
