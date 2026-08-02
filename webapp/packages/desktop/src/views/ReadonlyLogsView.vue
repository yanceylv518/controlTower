<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppShell from '../components/AppShell.vue'
import AsyncPanel from '../components/AsyncPanel.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { useAsyncData } from '../composables/useAsyncData'
const auth=useAuthStore(),filters=useFiltersStore(),userIDs=ref(''),offset=ref(0),limit=50
const end=ref(new Date()),start=ref(new Date(Date.now()-24*3600_000))
const params=computed(()=>({site:filters.site_id,user_ids:auth.user?.role==='admin'?userIDs.value:undefined,start_time:start.value.toISOString(),end_time:end.value.toISOString(),limit,offset:offset.value}))
const state=useAsyncData(()=>passthrough.logs(params.value))
const search=()=>{offset.value=0;void state.reload()};const page=(next:number)=>{offset.value=Math.max(0,next);void state.reload()}
// No top-level await: an async setup component needs a <Suspense> boundary
// this app does not have, and would silently render a blank page.
onMounted(async () => { await filters.loadInstances(); void state.reload() })
watch(() => filters.site_id, () => { offset.value = 0; void state.reload() })
</script>
<template><AppShell title="使用日志（只读）">
  <template #tools><el-input v-if="auth.user?.role==='admin'" v-model="userIDs" placeholder="用户 ID，多个用逗号分隔" style="width:240px"/><el-date-picker v-model="start" type="datetime" placeholder="开始时间"/><el-date-picker v-model="end" type="datetime" placeholder="结束时间"/><el-button type="primary" @click="search">查询</el-button></template>
  <el-alert title="最多查询 31 天，每页最多 100 条；内容摘要已脱敏。受限账号只能看到被授权用户。" type="info" :closable="false"/>
  <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="Boolean(state.data.value?.configured)&&!state.data.value?.items.length" @retry="state.reload">
    <el-empty v-if="state.data.value && !state.data.value.configured" description="该站点未开通明细查询，请联系管理员配置只读连接"/>
    <template v-else><el-table :data="state.data.value?.items||[]"><el-table-column prop="created_at" label="时间" width="180"><template #default="s">{{new Date(s.row.created_at).toLocaleString()}}</template></el-table-column><el-table-column prop="user_id" label="用户 ID" width="90"/><el-table-column prop="username" label="用户"/><el-table-column prop="model_name" label="模型"/><el-table-column prop="channel_id" label="渠道" width="80"/><el-table-column label="Token"><template #default="s">{{s.row.prompt_tokens}} / {{s.row.completion_tokens}}</template></el-table-column><el-table-column prop="use_time" label="耗时(s)" width="90"/><el-table-column prop="content_summary" label="内容摘要" min-width="220" show-overflow-tooltip/></el-table><div class="pager"><el-button :disabled="offset===0" @click="page(offset-limit)">上一页</el-button><span>第 {{Math.floor(offset/limit)+1}} 页</span><el-button :disabled="(state.data.value?.items.length||0)<limit" @click="page(offset+limit)">下一页</el-button></div></template>
  </AsyncPanel>
</AppShell></template>
<style scoped>.pager{display:flex;justify-content:flex-end;align-items:center;gap:12px;margin-top:16px}</style>
