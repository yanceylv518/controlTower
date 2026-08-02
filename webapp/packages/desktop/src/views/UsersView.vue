<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import type { ScopedUser } from '@ct/shared'
import { auth } from '../api'
import AppShell from '../components/AppShell.vue'

const items = ref<ScopedUser[]>([])
const open = ref(false)
const saving = ref(false)
const form = reactive({ username: '', password: '', scope_site: '', scope_user_ids: '' })
async function load() { items.value = (await auth.users()).items }
function ids(value: string) { return [...new Set(value.split(/[,，\s]+/).map(Number).filter(x => Number.isInteger(x) && x > 0))] }
async function create() {
  const scope = ids(form.scope_user_ids)
  if (!form.username.trim() || form.password.length < 8 || !form.scope_site.trim() || !scope.length) { ElMessage.warning('请填写账号、至少 8 位密码、站点和客户 ID'); return }
  saving.value = true
  try {
    await auth.createUser({ username: form.username.trim(), password: form.password, role: 'viewer', scope_site: form.scope_site.trim(), scope_user_ids: scope })
    open.value = false
    Object.assign(form, { username: '', password: '', scope_site: '', scope_user_ids: '' })
    await load()
    ElMessage.success('访问账号已创建')
  } finally { saving.value = false }
}
async function toggle(row: ScopedUser, enabled: boolean) { await auth.updateUser(row.id, { role: row.role, scope_site: row.scope_site, scope_user_ids: row.scope_user_ids, enabled }); await load() }
void load()
</script>

<template>
  <AppShell title="访问账号">
    <template #tools><el-button type="primary" @click="open = true">创建查看账号</el-button></template>
    <el-alert class="intro" type="info" :closable="false" title="查看账号只能访问客户监控；服务端会按指定站点和客户 ID 隔离数据，直接输入其他页面地址也无法越权。" />
    <el-table :data="items">
      <el-table-column prop="username" label="账号" />
      <el-table-column prop="role" label="角色"><template #default="s">{{ s.row.role === 'admin' ? '管理员' : '只读查看者' }}</template></el-table-column>
      <el-table-column prop="scope_site" label="可看站点"><template #default="s">{{ s.row.scope_site || '全部（管理员）' }}</template></el-table-column>
      <el-table-column label="可看客户 ID"><template #default="s">{{ s.row.scope_user_ids?.join('、') || '全部（管理员）' }}</template></el-table-column>
      <el-table-column label="状态"><template #default="s"><el-switch v-if="s.row.role !== 'admin'" :model-value="s.row.enabled" @change="toggle(s.row, Boolean($event))" /><el-tag v-else>启用</el-tag></template></el-table-column>
    </el-table>
    <el-dialog v-model="open" title="创建客户查看账号" width="520px">
      <el-form label-width="100px">
        <el-form-item label="登录账号"><el-input v-model="form.username" /></el-form-item>
        <el-form-item label="初始密码"><el-input v-model="form.password" type="password" show-password /><div class="tip">至少 8 位，创建后请安全地交给客户。</div></el-form-item>
        <el-form-item label="站点 ID"><el-input v-model="form.scope_site" placeholder="例如 pinducloud-cn" /></el-form-item>
        <el-form-item label="客户 ID"><el-input v-model="form.scope_user_ids" placeholder="多个 ID 用逗号分隔，例如 9, 26" /><div class="tip">只允许查看这些 new-api 用户对应的监控数据。</div></el-form-item>
      </el-form>
      <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>
  </AppShell>
</template>
<style scoped>.intro{margin-bottom:16px}.tip{font-size:12px;color:#8492a6}</style>
