<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppShell from '../components/AppShell.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { useAsyncData } from '../composables/useAsyncData'
const auth=useAuthStore(),filters=useFiltersStore(),userIDs=ref(''),tokenName=ref(''),modelName=ref(''),requestID=ref(''),channelID=ref<number>(),logType=ref<number>(),offset=ref(0),limit=50
const end=ref(new Date()),start=ref(new Date(new Date().setHours(0,0,0,0)))
const params=computed(()=>({site:filters.site_id,user_ids:auth.user?.role==='admin'?userIDs.value:undefined,start_time:start.value.toISOString(),end_time:end.value.toISOString(),token_name:tokenName.value,model_name:modelName.value,request_id:requestID.value,channel_id:channelID.value,log_type:logType.value,limit,offset:offset.value}))
const state=useAsyncData(()=>passthrough.logs(params.value))
const search=()=>{offset.value=0;void state.reload()};const page=(next:number)=>{offset.value=Math.max(0,next);void state.reload()}
const reset=()=>{userIDs.value='';tokenName.value='';modelName.value='';requestID.value='';channelID.value=undefined;logType.value=undefined;start.value=new Date(new Date().setHours(0,0,0,0));end.value=new Date();search()}
async function load(){try{await filters.loadInstances()}catch{/* Keep the shell visible; the content request reports its own error. */}await state.reload()}
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
    <el-date-picker v-model="start" type="datetime" placeholder="开始时间" class="filter-span-2"/><span class="separator">至</span><el-date-picker v-model="end" type="datetime" placeholder="结束时间" class="filter-span-2"/>
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
