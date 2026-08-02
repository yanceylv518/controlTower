<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AppShell from '../components/AppShell.vue'
import AsyncPanel from '../components/AsyncPanel.vue'
import { passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { useFiltersStore } from '../stores/filters'
import { useAsyncData } from '../composables/useAsyncData'
const auth=useAuthStore(), filters=useFiltersStore(), userIDs=ref('')
const params=computed(()=>({site:filters.site_id,user_ids:auth.user?.role==='admin'?userIDs.value:undefined,limit:200}))
const state=useAsyncData(()=>passthrough.users(params.value))
onMounted(async () => { await filters.loadInstances(); void state.reload() })
watch(() => filters.site_id, () => void state.reload())
</script>
<template><AppShell title="用户资料（只读）">
  <template #tools><el-input v-if="auth.user?.role==='admin'" v-model="userIDs" placeholder="用户 ID，多个用逗号分隔" style="width:260px"/><el-button @click="state.reload">查询</el-button></template>
  <el-alert title="数据直接读取所选站点的 new-api 只读库，仅显示账号授权范围，不提供修改操作。" type="info" :closable="false"/>
  <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="Boolean(state.data.value?.configured)&&!state.data.value?.items.length" @retry="state.reload">
    <el-empty v-if="state.data.value && !state.data.value.configured" description="该站点未开通明细查询，请联系管理员配置只读连接"/>
    <el-table v-else :data="state.data.value?.items||[]"><el-table-column prop="id" label="用户 ID"/><el-table-column prop="username" label="用户名"/><el-table-column prop="display_name" label="显示名称"/><el-table-column prop="quota" label="余额/额度"/><el-table-column prop="used_quota" label="已用额度"/><el-table-column label="状态"><template #default="s">{{s.row.status===1?'正常':'停用'}}</template></el-table-column></el-table>
  </AsyncPanel>
</AppShell></template>
