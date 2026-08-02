<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { useAsyncData } from '../composables/useAsyncData'
const auth=useAuthStore(), filters=useFiltersStore(), userIDs=ref('')
const params=computed(()=>({site:filters.site_id,user_ids:auth.user?.role==='admin'?userIDs.value:undefined,limit:200}))
const state=useAsyncData(()=>passthrough.users(params.value))
async function load(){try{await filters.loadInstances()}catch{/* Keep the shell visible; the content request reports its own error. */}await state.reload()}
onMounted(() => { void load() })
watch(() => filters.site_id, (site, previous) => {
  if (site && site !== previous) void state.reload()
})
</script>
<template><AppShell title="用户管理">
  <div class="filter-bar">
    <el-input v-model="userIDs" :disabled="auth.user?.role!=='admin'" clearable placeholder="用户 ID，多个用逗号分隔" class="user-filter"/>
    <el-button type="primary" @click="state.reload">查询</el-button>
    <el-button @click="state.reload">刷新</el-button>
  </div>
  <el-alert title="数据直接读取所选站点的 new-api 只读库，仅显示账号授权范围，不提供修改操作。" type="info" :closable="false"/>
  <el-alert v-if="state.error.value" :title="state.error.value" type="error" show-icon :closable="false"><el-button link type="primary" @click="state.reload">重新加载</el-button></el-alert>
  <el-alert v-else-if="!state.loading.value && state.data.value && !state.data.value.configured" title="只读数据库尚未配置，当前暂无数据。配置后可直接在此查询。" type="info" show-icon :closable="false"/>
  <div v-loading="state.loading.value" class="table-panel">
    <el-table :data="state.data.value?.items||[]" height="100%" empty-text="暂无用户数据"><el-table-column prop="id" label="用户 ID"/><el-table-column prop="username" label="用户名"/><el-table-column prop="display_name" label="显示名称"/><el-table-column prop="quota" label="余额/额度"/><el-table-column prop="used_quota" label="已用额度"/><el-table-column label="状态"><template #default="s"><el-tag :type="s.row.status===1?'success':'info'">{{s.row.status===1?'正常':'停用'}}</el-tag></template></el-table-column></el-table>
  </div>
</AppShell></template>
<style scoped>.filter-bar{display:flex;align-items:center;gap:10px;padding:12px 16px;margin-bottom:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px}.user-filter{width:320px}.table-panel{height:calc(100vh - 290px);min-height:360px;margin-top:12px;background:#fff;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden}</style>
