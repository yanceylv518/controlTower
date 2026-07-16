<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ApiError, type SystemSettingItem } from '@ct/shared'
import { ElMessage } from 'element-plus'
import { auth, dashboard } from '../api'
import AppShell from '../components/AppShell.vue'
import { useAuthStore } from '../stores/auth'

const store = useAuthStore()
const loading = ref(false)
const saving = ref(false)
const items = ref<Record<string, SystemSettingItem>>({})
const values = reactive<Record<string, string | number>>({})
const password = reactive({ old_password: '', new_password: '', confirm_password: '' })

const sections = [
  { title: '数据保留', fields: [
    ['CT_RETENTION_DETAIL_DAYS', '明细数据保留天数', 1, 365], ['CT_RETENTION_METRIC5M_DAYS', '5 分钟指标保留天数', 1, 365], ['CT_RETENTION_RUNTIME_DAYS', '运行状态保留天数', 1, 365],
  ] },
  { title: '告警阈值', fields: [
    ['CT_OFFLINE_ALERT_SECONDS', '实例离线秒数', 1, 86400], ['CT_CPU_WARN_PERCENT', 'CPU 警告 (%)', 1, 100], ['CT_CPU_CRIT_PERCENT', 'CPU 严重 (%)', 1, 100],
    ['CT_MEMORY_WARN_PERCENT', '内存警告 (%)', 1, 100], ['CT_MEMORY_CRIT_PERCENT', '内存严重 (%)', 1, 100], ['CT_DISK_WARN_PERCENT', '磁盘警告 (%)', 1, 100], ['CT_DISK_CRIT_PERCENT', '磁盘严重 (%)', 1, 100],
    ['CT_ERROR_RATE_WARN_PERCENT', '错误率警告 (%)', 1, 100], ['CT_ERROR_RATE_CRIT_PERCENT', '错误率严重 (%)', 1, 100], ['CT_P95_WARN_SECONDS', 'P95 警告 (秒)', .5, 600], ['CT_P95_CRIT_SECONDS', 'P95 严重 (秒)', .5, 600],
  ] },
] as const

function sourceText(source: string) { return ({ db: '数据库覆盖', env: '环境变量', default: '内置默认' } as Record<string,string>)[source] || source }
async function load() { loading.value=true; try { const response=await dashboard.settings(); items.value=response.items; Object.entries(response.items).forEach(([key,item])=>values[key]=key==='CT_NOTIFICATIONS_ENABLED'?item.value:Number(item.value)) } finally { loading.value=false } }
async function save() { saving.value=true; try { const payload=Object.fromEntries(Object.entries(values).map(([key,value])=>[key,String(value)])); const response=await dashboard.saveSettings(payload); items.value=response.items; ElMessage.success('设置已保存，将在下一轮任务中生效') } catch (e) { ElMessage.error(e instanceof ApiError && e.status===400 ? '设置校验失败，请检查阈值范围' : '保存失败') } finally { saving.value=false } }
async function changePassword() { if(password.new_password.length<8||password.new_password!==password.confirm_password){ElMessage.error('请确认新密码至少 8 位且两次输入一致');return} await auth.changePassword(password.old_password,password.new_password);ElMessage.success('密码修改成功') }
onMounted(load)
</script>

<template>
  <AppShell title="设置">
    <section class="panel settings-card"><h2>账户信息</h2><el-descriptions :column="2" border><el-descriptions-item label="用户名">{{store.user?.username}}</el-descriptions-item><el-descriptions-item label="角色">{{store.user?.role}}</el-descriptions-item></el-descriptions></section>
    <div v-loading="loading">
      <section v-for="section in sections" :key="section.title" class="panel settings-card"><h2>{{section.title}}</h2><el-form label-width="210px"><el-form-item v-for="field in section.fields" :key="field[0]" :label="field[1]"><el-input-number v-model="values[field[0]]" :min="field[2]" :max="field[3]" :step="field[2]===.5?.5:1"/><el-tag class="setting-source" :type="items[field[0]]?.source==='db'?'warning':'info'">{{sourceText(items[field[0]]?.source)}}</el-tag><span class="setting-default">默认 {{items[field[0]]?.default}}</span></el-form-item></el-form></section>
      <section class="panel settings-card"><h2>通知</h2><el-form label-width="210px"><el-form-item label="告警通知总开关"><el-switch v-model="values.CT_NOTIFICATIONS_ENABLED" active-value="true" inactive-value="false"/><el-tag class="setting-source" :type="items.CT_NOTIFICATIONS_ENABLED?.source==='db'?'warning':'info'">{{sourceText(items.CT_NOTIFICATIONS_ENABLED?.source)}}</el-tag></el-form-item><el-button type="primary" :loading="saving" @click="save">保存系统设置</el-button></el-form></section>
    </div>
    <section class="panel settings-card"><h2>修改密码</h2><el-form :model="password" label-width="110px"><el-form-item label="旧密码"><el-input v-model="password.old_password" type="password" show-password/></el-form-item><el-form-item label="新密码"><el-input v-model="password.new_password" type="password" show-password/></el-form-item><el-form-item label="确认新密码"><el-input v-model="password.confirm_password" type="password" show-password/></el-form-item><el-button type="primary" @click="changePassword">修改密码</el-button></el-form></section>
  </AppShell>
</template>

<style scoped>.settings-card{min-height:0;margin-bottom:18px}.settings-card h2{margin:0 0 18px}.setting-source{margin-left:12px}.setting-default{margin-left:10px;color:#8993a4;font-size:12px}</style>
