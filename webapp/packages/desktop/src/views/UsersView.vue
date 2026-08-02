<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { siteOf, type ScopedUser } from '@ct/shared'
import { auth, dashboard } from '../api'
import AppShell from '../components/AppShell.vue'

const items = ref<ScopedUser[]>([])
const instances = ref<Awaited<ReturnType<typeof dashboard.instances>>['items']>([])
const customers = ref<Array<{ id: number; name: string }>>([])
const open = ref(false)
const saving = ref(false)
const loadingCustomers = ref(false)
const form = reactive({ username: '', password: '', scope_site: '', scope_user_ids: [] as number[] })
const sites = computed(() => [...new Set(instances.value.filter(item => item.enabled).map(siteOf))].sort())

async function load() {
  const [users, instanceResponse] = await Promise.all([auth.users(), dashboard.instances()])
  items.value = users.items
  instances.value = instanceResponse.items
}

function customerID(key: string) {
  const value = Number(key.split(':').pop())
  return Number.isInteger(value) && value > 0 ? value : 0
}

async function loadCustomers() {
  form.scope_user_ids = []
  customers.value = []
  if (!form.scope_site) return
  const instanceIDs = instances.value.filter(item => item.enabled && siteOf(item) === form.scope_site).map(item => item.instance_id)
  loadingCustomers.value = true
  try {
    const responses = await Promise.all(instanceIDs.map(id => dashboard.usage(720, id)))
    const byID = new Map<number, { id: number; name: string }>()
    for (const item of responses.flatMap(response => response.items)) {
      if (item.dimension_type !== 'instance_user') continue
      const id = customerID(item.dimension_key)
      if (!id) continue
      const name = item.display_name || item.display_key || `客户 ${id}`
      const current = byID.get(id)
      if (!current || current.name === `客户 ${id}`) byID.set(id, { id, name })
    }
    customers.value = [...byID.values()].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
  } finally { loadingCustomers.value = false }
}

async function showCreate() {
  if (!instances.value.length) await load()
  Object.assign(form, { username: '', password: '', scope_site: sites.value[0] || '', scope_user_ids: [] })
  open.value = true
  await loadCustomers()
}

async function create() {
  if (!form.username.trim() || form.password.length < 8 || !form.scope_site || !form.scope_user_ids.length) { ElMessage.warning('请填写账号、至少 8 位密码，并选择站点和客户'); return }
  saving.value = true
  try {
    await auth.createUser({ username: form.username.trim(), password: form.password, role: 'viewer', scope_site: form.scope_site, scope_user_ids: [...form.scope_user_ids] })
    open.value = false
    await load()
    ElMessage.success('客户查看账号已创建')
  } finally { saving.value = false }
}
async function toggle(row: ScopedUser, enabled: boolean) { await auth.updateUser(row.id, { role: row.role, scope_site: row.scope_site, scope_user_ids: row.scope_user_ids, enabled }); await load() }
void load()
</script>

<template>
  <AppShell title="访问账号">
    <template #tools><el-button type="primary" @click="showCreate">创建查看账号</el-button></template>
    <el-alert class="intro" type="info" :closable="false" title="查看账号只能访问客户监控；服务端会按指定站点和客户 ID 隔离数据，直接输入其他页面地址也无法越权。" />
    <el-table :data="items">
      <el-table-column prop="username" label="账号" />
      <el-table-column prop="role" label="角色"><template #default="s">{{ s.row.role === 'admin' ? '管理员' : '只读查看者' }}</template></el-table-column>
      <el-table-column prop="scope_site" label="可看站点"><template #default="s">{{ s.row.scope_site || '全部（管理员）' }}</template></el-table-column>
      <el-table-column label="可看客户 ID"><template #default="s">{{ s.row.scope_user_ids?.join('、') || '全部（管理员）' }}</template></el-table-column>
      <el-table-column label="状态"><template #default="s"><el-switch v-if="s.row.role !== 'admin'" :model-value="s.row.enabled" @change="toggle(s.row, Boolean($event))" /><el-tag v-else>启用</el-tag></template></el-table-column>
    </el-table>
    <el-dialog v-model="open" title="创建客户查看账号" width="560px">
      <el-form label-width="100px">
        <el-form-item label="登录账号"><el-input v-model="form.username" /></el-form-item>
        <el-form-item label="初始密码"><el-input v-model="form.password" type="password" show-password /><div class="tip">至少 8 位，创建后请安全地交给客户。</div></el-form-item>
        <el-form-item label="站点">
          <el-select v-model="form.scope_site" filterable placeholder="请选择站点" style="width:100%" @change="loadCustomers">
            <el-option v-for="site in sites" :key="site" :label="site" :value="site" />
          </el-select>
        </el-form-item>
        <el-form-item label="客户">
          <el-select v-model="form.scope_user_ids" multiple filterable collapse-tags collapse-tags-tooltip :max-collapse-tags="3" :loading="loadingCustomers" placeholder="请选择一个或多个客户" style="width:100%">
            <el-option v-for="customer in customers" :key="customer.id" :label="`${customer.name}（ID ${customer.id}）`" :value="customer.id" />
          </el-select>
          <div class="tip">显示该站点近 30 天有监控记录的客户，可按客户名称或 ID 搜索。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>
  </AppShell>
</template>
<style scoped>.intro{margin-bottom:16px}.tip{width:100%;margin-top:4px;font-size:12px;color:#8492a6}</style>
