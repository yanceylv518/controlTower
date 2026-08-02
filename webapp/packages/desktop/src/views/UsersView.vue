<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { siteOf, type ScopedUser } from '@ct/shared'
import { auth, dashboard, passthrough } from '../api'
import AppShell from '../components/AppShell.vue'

const items = ref<ScopedUser[]>([])
const instances = ref<Awaited<ReturnType<typeof dashboard.instances>>['items']>([])
const customers = ref<Array<{ id: number; name: string }>>([])
const open = ref(false)
const saving = ref(false)
const loadingCustomers = ref(false)
const form = reactive({ username: '', password: '', scope_site: '', scope_user_ids: [] as number[] })
const sites = computed(() => [...new Set(instances.value.filter(item => item.enabled).map(siteOf))].sort())
const customerCache = new Map<string, { expires: number; items: Array<{ id: number; name: string }> }>()
let customerRequest = 0

async function load() {
  const [users, instanceResponse] = await Promise.all([auth.users(), dashboard.instances()])
  items.value = users.items
  instances.value = instanceResponse.items
}

async function loadCustomers(keyword = '') {
  if (!form.scope_site) return
  const query = keyword.trim()
  const cacheKey = `${form.scope_site}:${query.toLowerCase()}`
  const cached = customerCache.get(cacheKey)
  if (cached && cached.expires > Date.now()) { customers.value = cached.items; return }
  const request = ++customerRequest
  loadingCustomers.value = true
  try {
    const response = await passthrough.users({ site: form.scope_site, keyword: query, status: 1, limit: 50, offset: 0 })
    const result = response.items.map(item => ({ id: item.id, name: item.display_name || item.username || `客户 ${item.id}` }))
    customerCache.set(cacheKey, { expires: Date.now() + 5 * 60_000, items: result })
    if (request === customerRequest) customers.value = result
  } catch {
    if (request === customerRequest) customers.value = []
    ElMessage.error('客户列表加载失败，请检查站点只读连接')
  } finally { if (request === customerRequest) loadingCustomers.value = false }
}

function changeSite() {
  form.scope_user_ids = []
  customers.value = []
  void loadCustomers()
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
          <el-select v-model="form.scope_site" filterable placeholder="请选择站点" style="width:100%" @change="changeSite">
            <el-option v-for="site in sites" :key="site" :label="site" :value="site" />
          </el-select>
        </el-form-item>
        <el-form-item label="客户">
          <el-select v-model="form.scope_user_ids" multiple filterable remote reserve-keyword collapse-tags collapse-tags-tooltip :max-collapse-tags="3" :loading="loadingCustomers" :remote-method="loadCustomers" placeholder="请选择或搜索客户" style="width:100%">
            <el-option v-for="customer in customers" :key="customer.id" :label="`${customer.name}（ID ${customer.id}）`" :value="customer.id" />
          </el-select>
          <div class="tip">默认显示前 50 个正常客户，可输入客户名称或 ID 继续搜索。</div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="open = false">取消</el-button><el-button type="primary" :loading="saving" @click="create">创建</el-button></template>
    </el-dialog>
  </AppShell>
</template>
<style scoped>.intro{margin-bottom:16px}.tip{width:100%;margin-top:4px;font-size:12px;color:#8492a6}</style>
