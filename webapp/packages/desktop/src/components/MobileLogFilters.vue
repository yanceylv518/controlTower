<script lang="ts">
export interface MobileLogFilterValues {
  logType: number
  modelName: string
  group: string
  tokenName: string
  requestID: string
  upstreamRequestID: string
  channelID: string
  statusCode: string
  emptyOutput: boolean
  fallbackFinalOnly: boolean
}
</script>
<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { Filter, RefreshLeft, Hide, View } from '@element-plus/icons-vue'
import CompactDateTimeRangePicker from './CompactDateTimeRangePicker.vue'
import UserNamePicker, { type UserPickerOption } from './UserNamePicker.vue'

const props = defineProps<{
  username: string
  site?: string
  timeRange: [Date, Date]
  filters: MobileLogFilterValues
  busy: boolean
  admin: boolean
  sensitive: boolean
}>()
const emit = defineEmits<{
  'update:username': [value: string]
  'select-user': [value: UserPickerOption | null]
  'update:timeRange': [value: [Date, Date]]
  apply: [value: MobileLogFilterValues]
  search: []
  reset: []
  privacy: []
}>()
const sheetOpen = ref(false)
const emptyFilters = (): MobileLogFilterValues => ({ logType:0, modelName:'', group:'', tokenName:'', requestID:'', upstreamRequestID:'', channelID:'', statusCode:'', emptyOutput:false, fallbackFinalOnly:false })
const draft = reactive(emptyFilters())
const filterCount = computed(() => Object.entries(props.filters).filter(([key, value]) => (props.admin || (key !== 'channelID' && key !== 'emptyOutput')) && Boolean(typeof value === 'string' ? value.trim() : value)).length)
function openFilters() {
  Object.assign(draft, props.filters)
  sheetOpen.value = true
}
function applyFilters() {
  if (props.busy) return
  emit('apply', { ...draft, channelID: props.admin ? draft.channelID : '', emptyOutput: props.admin && draft.emptyOutput })
  sheetOpen.value = false
}
</script>

<template>
  <div class="mobile-log-filters">
    <form class="mobile-search" @submit.prevent="emit('search')">
      <UserNamePicker :model-value="username" :site="site" aria-label="用户名称" placeholder="输入用户名" @update:model-value="emit('update:username', $event)" @select="emit('select-user', $event)" @submit="emit('search')" />
      <el-button type="primary" native-type="submit" :loading="busy">查询</el-button>
    </form>
    <div class="mobile-filter-actions">
      <CompactDateTimeRangePicker :model-value="timeRange" compact @update:model-value="emit('update:timeRange', $event)" />
      <el-button :icon="Filter" :disabled="busy" @click="openFilters">更多筛选<span v-if="filterCount" class="filter-count">{{ filterCount }}</span></el-button>
      <el-button :icon="RefreshLeft" aria-label="重置全部筛选" :disabled="busy" @click="emit('reset')" />
      <el-button :icon="sensitive ? Hide : View" :aria-label="sensitive ? '隐藏敏感字段' : '显示敏感字段'" @click="emit('privacy')" />
    </div>
    <el-drawer v-model="sheetOpen" direction="btt" size="auto" title="更多筛选" class="mobile-log-filter-sheet" append-to-body destroy-on-close>
      <el-form label-position="top" @submit.prevent="applyFilters">
        <el-form-item label="日志类型">
          <el-select v-model="draft.logType" aria-label="日志类型">
            <el-option v-for="item in [[0,'全部'],[2,'消费'],[5,'错误'],[1,'充值'],[3,'管理'],[4,'系统'],[6,'退款'],[7,'登录']]" :key="String(item[0])" :label="String(item[1])" :value="item[0]" />
          </el-select>
        </el-form-item>
        <el-form-item label="模型名称"><el-input v-model="draft.modelName" aria-label="模型名称" placeholder="输入模型名称" clearable /></el-form-item>
        <el-form-item label="分组"><el-input v-model="draft.group" aria-label="分组" placeholder="输入分组" clearable /></el-form-item>
        <el-form-item label="令牌名称"><el-input v-model="draft.tokenName" aria-label="令牌名称" placeholder="输入令牌名称" clearable /></el-form-item>
        <el-form-item label="请求 ID"><el-input v-model="draft.requestID" aria-label="请求 ID" placeholder="输入请求 ID" clearable /></el-form-item>
        <el-form-item label="上游请求 ID"><el-input v-model="draft.upstreamRequestID" aria-label="上游请求 ID" placeholder="输入上游请求 ID" clearable /></el-form-item>
        <el-form-item label="错误码"><el-input v-model="draft.statusCode" aria-label="错误码" placeholder="例如 429、503" inputmode="numeric" clearable /></el-form-item>
        <el-form-item v-if="admin"><el-checkbox v-model="draft.emptyOutput">空输出</el-checkbox></el-form-item>
        <el-form-item v-if="admin"><el-checkbox v-model="draft.fallbackFinalOnly">Fallback 仅显示最后一条</el-checkbox></el-form-item>
        <el-form-item v-if="admin" label="渠道 ID"><el-input v-model="draft.channelID" aria-label="渠道 ID" placeholder="输入渠道 ID" inputmode="numeric" clearable /></el-form-item>
      </el-form>
      <template #footer>
        <div class="mobile-sheet-footer"><el-button @click="Object.assign(draft, emptyFilters())">重置</el-button><el-button type="primary" :loading="busy" @click="applyFilters">应用筛选</el-button></div>
      </template>
    </el-drawer>
  </div>
</template>

<style scoped>
.mobile-search { display:flex;gap:10px; }
.mobile-search .user-name-picker { flex:1;min-width:0; }
.mobile-log-filters :deep(.el-input__inner) { font-size:14px; }
.mobile-log-filters :deep(.el-input__wrapper),.mobile-log-filters .el-button { min-height:36px; }
.mobile-filter-actions { display:grid;grid-template-columns:minmax(0,1fr) 36px 36px;gap:6px 8px;margin-top:6px;align-items:center; }
.mobile-filter-actions .compact-date-range { grid-column:1 / -1;min-width:0; }
.mobile-filter-actions :deep(.compact-date-trigger) { height:36px;padding-right:8px;font-size:13px; }
.mobile-filter-actions .el-button { margin:0;padding:8px; }
.filter-count { display:inline-grid;place-items:center;min-width:18px;height:18px;margin-left:4px;border-radius:9px;background:#edf2ff;color:#315edb; }
.mobile-sheet-footer { display:grid;grid-template-columns:1fr 2fr;gap:12px; }
.mobile-sheet-footer .el-button { margin:0;min-height:36px; }
</style>
<style>
.mobile-log-filter-sheet { max-height:calc(100dvh - 24px);border-radius:18px 18px 0 0; }
.mobile-log-filter-sheet .el-drawer__header { margin:0;padding:20px;color:#202b3d;font-weight:600; }
.mobile-log-filter-sheet .el-drawer__close-btn { min-width:44px;min-height:44px; }
.mobile-log-filter-sheet .el-drawer__body { padding:0 20px;overflow-y:auto; }
.mobile-log-filter-sheet .el-form-item { margin-bottom:16px; }
.mobile-log-filter-sheet .el-input__wrapper,.mobile-log-filter-sheet .el-select__wrapper { min-height:36px; }
.mobile-log-filter-sheet .el-input__inner { font-size:14px; }
.mobile-log-filter-sheet .el-drawer__footer { border-top:1px solid #e5e9f0;padding:12px 20px calc(12px + env(safe-area-inset-bottom)); }
</style>
