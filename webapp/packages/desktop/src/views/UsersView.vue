<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, siteOf, type ScopedUser } from '@ct/shared'
import { auth, dashboard, passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { can } from '../permissions'
import AppShell from '../components/AppShell.vue'
import PermissionTree from '../components/PermissionTree.vue'

function showError(error: unknown) {
  const messages: Record<string, string> = { forbidden: '权限不足，不能分配或管理超出自身权限的账号', invalid_user: '请检查账号是否重复、密码长度和必填信息', last_full_admin: '必须保留至少一个启用且拥有全部权限的管理员' }
  ElMessage.error(error instanceof ApiError ? (messages[error.code] || '操作失败，请稍后重试') : '请求失败，请检查连接')
}
const current = useAuthStore()
const tab = ref<'admin' | 'viewer'>('admin')
const permissions = ref<Array<{ key: string; label: string; description: string }>>([])
const resetTarget = ref<ScopedUser | null>(null)
const resetPassword = ref('')
const resetSaving = ref(false)
const items = ref<ScopedUser[]>([])
const instances = ref<Awaited<ReturnType<typeof dashboard.instances>>['items']>([])
const customers = ref<Array<{ id: number; name: string }>>([])
const open = ref(false)
const editing = ref<ScopedUser | null>(null)
const saving = ref(false)
const loadingCustomers = ref(false)
const form = reactive({ username: '', password: '', scope_site: '', scope_user_ids: [] as number[], display_name: '', permissions: [] as string[] })
const sites = computed(() => [...new Set(instances.value.filter(item => item.enabled).map(siteOf))].sort())
const customerCache = new Map<string, { expires: number; items: Array<{ id: number; name: string }> }>()
let customerRequest = 0

async function load() {
  const [users, instanceResponse] = await Promise.all([auth.users(), dashboard.instances()])
  items.value = users.items
  permissions.value = users.permissions
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

async function loadSelectedCustomers(userIDs: number[]) {
  if (!form.scope_site || !userIDs.length) return
  const response = await passthrough.users({ site: form.scope_site, user_ids: userIDs.join(','), limit: Math.max(50, userIDs.length), offset: 0 })
  const selected = response.items.map(item => ({ id: item.id, name: item.display_name || item.username || `客户 ${item.id}` }))
  const byID = new Map(customers.value.map(item => [item.id, item]))
  selected.forEach(item => byID.set(item.id, item))
  customers.value = [...byID.values()]
}

function changeSite() {
  form.scope_user_ids = []
  customers.value = []
  void loadCustomers()
}

async function showCreate() {
  if (!instances.value.length) await load()
  editing.value = null
  Object.assign(form, { username: '', password: '', scope_site: sites.value[0] || '', scope_user_ids: [], display_name: '', permissions: [] })
  open.value = true
  if (tab.value === 'viewer') await loadCustomers()
}

async function showEdit(row: ScopedUser) {
  editing.value = row
  Object.assign(form, { username: row.username, password: '', scope_site: row.scope_site, scope_user_ids: [...(row.scope_user_ids || [])], display_name: row.display_name || '', permissions: [...(row.permissions || [])] })
  customers.value = []
  open.value = true
  if (row.role === 'viewer') { await loadCustomers(); await loadSelectedCustomers(row.scope_user_ids) }
}

async function create() {
  if (tab.value === 'admin') { await saveAdmin(); return }
  if (!form.username.trim() || form.password.length < 8 || !form.scope_site || !form.scope_user_ids.length) { ElMessage.warning('请填写账号、至少 8 位密码，并选择站点和客户'); return }
  saving.value = true
  try {
    await auth.createUser({ username: form.username.trim(), password: form.password, role: 'viewer', scope_site: form.scope_site, scope_user_ids: [...form.scope_user_ids] })
    open.value = false
    await load()
    ElMessage.success('客户查看账号已创建')
  } catch (error) { showError(error) } finally { saving.value = false }
}
async function saveEdit() {
  if (editing.value?.role === 'admin') { await saveAdmin(); return }
  if (!editing.value || !form.scope_user_ids.length) { ElMessage.warning('请至少选择一个客户'); return }
  saving.value = true
  try {
    await auth.updateUser(editing.value.id, {
      role: editing.value.role,
      scope_site: editing.value.scope_site,
      scope_user_ids: [...form.scope_user_ids],
      enabled: editing.value.enabled,
    })
    open.value = false
    await load()
    ElMessage.success('可查看客户已更新')
  } catch (error) { showError(error) } finally { saving.value = false }
}
async function submit() { if (editing.value) await saveEdit(); else await create() }
async function toggle(row: ScopedUser, enabled: boolean) { try { await auth.updateUser(row.id, { role: row.role, scope_site: row.scope_site, scope_user_ids: row.scope_user_ids, enabled, display_name: row.display_name, permissions: row.permissions }); await load() } catch (error) { showError(error) } }
const visibleItems = computed(() => items.value.filter(item => item.role === tab.value))
const grantable = computed(() => permissions.value.filter(item => can(current.user, item.key)))
function manageable(row: ScopedUser) {
  return row.id !== current.user?.id && (row.role === 'viewer' || (row.permissions || []).every(p => can(current.user, p)))
}
async function saveAdmin() {
  if (!form.username.trim() || (!editing.value && (form.password.length < 8 || form.password.length > 1024))) { ElMessage.warning('请填写账号和至少 8 位的初始密码'); return }
  saving.value = true
  try {
    const body = { role: 'admin', scope_site: '', scope_user_ids: [], display_name: form.display_name.trim(), permissions: [...form.permissions] }
    if (editing.value) await auth.updateUser(editing.value.id, { ...body, enabled: editing.value.enabled })
    else await auth.createUser({ ...body, username: form.username.trim(), password: form.password })
    open.value = false
    await load()
    ElMessage.success(editing.value ? '管理员权限已更新' : '管理员已创建')
  } catch (error) { showError(error) } finally { saving.value = false }
}
function showReset(row: ScopedUser) { resetPassword.value = ''; resetTarget.value = row }
async function submitReset() {
  if (!resetTarget.value || resetPassword.value.length < 8) { ElMessage.warning('密码至少 8 位'); return }
  resetSaving.value = true
  try {
    await auth.resetPassword(resetTarget.value.id, resetPassword.value)
    resetTarget.value = null; resetPassword.value = ''
    ElMessage.success('密码已重置，该账号需要重新登录')
  } catch (error) { showError(error) } finally { resetSaving.value = false }
}
void load().catch(showError)
</script>

<template>
  <AppShell title="账号管理">
    <template #tools><el-button type="primary" @click="showCreate">{{ tab === 'admin' ? '创建管理员' : '创建查看账号' }}</el-button></template>
    <el-tabs v-model="tab"><el-tab-pane label="管理员" name="admin" /><el-tab-pane label="查看账号" name="viewer" /></el-tabs>
    <el-alert class="intro" type="info" :closable="false" :title="tab === 'admin' ? '管理员按勾选的功能授权；未分配权限的账号无法访问业务功能。账号创建和权限变更会记录操作人。' : '查看账号按指定站点和客户 ID 隔离数据，保留原有查看权限。'" />
    <el-table :data="visibleItems" row-key="id">
      <el-table-column prop="username" label="账号" />
      <el-table-column v-if="tab === 'admin'" prop="display_name" label="姓名" />
      <el-table-column v-if="tab === 'admin'" label="权限" min-width="240"><template #default="s">
        <el-tag v-if="s.row.permissions?.includes('*')">全部权限</el-tag>
        <span v-else-if="!s.row.permissions?.length">尚未分配</span>
        <div v-else class="permission-tags"><el-tag v-for="key in s.row.permissions" :key="key" type="info">{{ permissions.find(p => p.key === key)?.label || key }}</el-tag></div>
      </template></el-table-column>
      <el-table-column v-if="tab === 'viewer'" prop="scope_site" label="可看站点" />
      <el-table-column v-if="tab === 'viewer'" label="可看客户 ID"><template #default="s">{{ s.row.scope_user_ids?.join('、') }}</template></el-table-column>
      <el-table-column label="状态" width="100"><template #default="s"><el-switch :disabled="!manageable(s.row)" :model-value="s.row.enabled" @change="toggle(s.row, Boolean($event))" /></template></el-table-column>
      <el-table-column label="操作" width="220"><template #default="s">
        <template v-if="manageable(s.row)">
          <el-button link type="primary" @click="showEdit(s.row)">{{ tab === 'admin' ? '配置权限' : '修改客户' }}</el-button>
          <el-button v-if="tab === 'admin'" link type="primary" @click="showReset(s.row)">重置密码</el-button>
        </template>
        <span v-else>{{ s.row.id === current.user?.id ? '当前账号' : '超出可管理权限' }}</span>
      </template></el-table-column>
    </el-table>
    <el-dialog v-model="open" :title="editing ? (tab === 'admin' ? '配置管理员权限' : '修改可查看客户') : (tab === 'admin' ? '创建管理员' : '创建客户查看账号')" :width="tab === 'admin' ? 'min(920px, calc(100vw - 32px))' : 'min(640px, calc(100vw - 32px))'" top="6vh" class="account-editor" @closed="form.password = ''">
      <div v-if="tab === 'admin'" class="editor-subtitle">设置登录信息，并选择该管理员可以使用的功能。</div>
      <el-form :label-position="tab === 'admin' ? 'top' : 'right'" :label-width="tab === 'admin' ? undefined : '100px'" :class="{ 'admin-form': tab === 'admin' }" @submit.prevent="submit">
        <section :class="{ 'account-basics': tab === 'admin' }">
        <h3 v-if="tab === 'admin'" class="editor-section-title">基本信息</h3>
        <el-form-item label="登录账号"><el-input v-model="form.username" maxlength="64" :disabled="Boolean(editing)" autocomplete="off" placeholder="请输入登录账号" /></el-form-item>
        <el-form-item v-if="tab === 'admin'" label="姓名"><el-input v-model="form.display_name" maxlength="64" placeholder="请输入姓名（选填）" /></el-form-item>
        <el-form-item v-if="!editing" label="初始密码"><el-input v-model="form.password" type="password" maxlength="1024" autocomplete="new-password" show-password placeholder="设置至少 8 位密码" /><div class="tip">至少 8 位，用于首次登录。</div></el-form-item>
        </section>
        <section v-if="tab === 'admin'" class="account-access">
          <h3 class="editor-section-title">功能权限<span>按菜单分组授权</span></h3>
          <el-form-item class="access-form-item">
            <PermissionTree v-model="form.permissions" :options="grantable" :allow-full="can(current.user, '*')" />
            <div class="tip access-hint">勾选分组可整组授权；未勾选的功能将不可访问。</div>
          </el-form-item>
        </section>
        <template v-else>
          <el-form-item label="站点">
            <el-select v-model="form.scope_site" filterable :disabled="Boolean(editing)" placeholder="请选择站点" style="width:100%" @change="changeSite">
              <el-option v-for="site in sites" :key="site" :label="site" :value="site" />
            </el-select>
          </el-form-item>
          <el-form-item label="客户">
            <el-select v-model="form.scope_user_ids" multiple filterable remote reserve-keyword collapse-tags collapse-tags-tooltip :max-collapse-tags="3" :loading="loadingCustomers" :remote-method="loadCustomers" placeholder="请选择或搜索客户" style="width:100%">
              <el-option v-for="customer in customers" :key="customer.id" :label="`${customer.name}（ID ${customer.id}）`" :value="customer.id" />
            </el-select>
            <div class="tip">默认显示前 50 个正常客户，可输入客户名称或 ID 继续搜索。</div>
          </el-form-item>
        </template>
      </el-form>
      <template #footer><div class="editor-footer"><span v-if="tab === 'admin'">{{ form.permissions.includes('*') ? '已选择全部权限' : `已选择 ${form.permissions.length} 项权限` }}</span><div><el-button @click="open = false">取消</el-button><el-button type="primary" :loading="saving" @click="submit">{{ editing ? '保存修改' : (tab === 'admin' ? '创建管理员' : '创建') }}</el-button></div></div></template>
    </el-dialog>
    <el-dialog :model-value="Boolean(resetTarget)" title="重置管理员密码" width="min(460px, calc(100vw - 32px))" @update:model-value="(value: boolean) => { if (!value) { resetTarget = null; resetPassword = '' } }">
      <p>为 {{ resetTarget?.username }} 设置新密码。该账号的现有登录会话将失效。</p>
      <el-input v-model="resetPassword" type="password" show-password maxlength="1024" autocomplete="new-password" placeholder="新密码，至少 8 位" @keyup.enter="submitReset" />
      <template #footer><el-button @click="resetTarget = null; resetPassword = ''">取消</el-button><el-button type="primary" :loading="resetSaving" @click="submitReset">重置密码</el-button></template>
    </el-dialog>
  </AppShell>
</template>
<style scoped>
.intro{margin-bottom:16px}.tip{width:100%;margin-top:4px;font-size:12px;color:var(--el-text-color-secondary)}
.permission-tags{display:flex;gap:6px;flex-wrap:wrap}
.editor-subtitle{color:var(--el-text-color-secondary);font-size:13px;margin:-4px 0 24px}
.admin-form{display:grid;grid-template-columns:240px minmax(0,1fr);gap:28px}
.account-basics{padding-right:24px;border-right:1px solid var(--el-border-color-lighter)}
.editor-section-title{display:flex;align-items:center;justify-content:space-between;margin:0 0 18px;font-size:14px;font-weight:600;color:var(--el-text-color-primary)}
.editor-section-title span{font-size:12px;font-weight:400;color:var(--el-text-color-secondary)}
.admin-form :deep(.el-form-item__label){padding-bottom:6px;line-height:20px;font-size:13px}
.admin-form :deep(.el-input__wrapper){min-height:36px;box-sizing:border-box}
.access-form-item{margin-bottom:0}.access-hint{margin-top:10px;line-height:1.6}
.editor-footer{display:flex;justify-content:space-between;align-items:center;gap:12px;padding-top:16px;border-top:1px solid var(--el-border-color-lighter)}
.editor-footer>span{font-size:12px;color:var(--el-text-color-secondary)}.editor-footer>div{margin-left:auto}
@media(max-width:700px){.admin-form{grid-template-columns:1fr;gap:20px}.account-basics{padding-right:0;border-right:0}.editor-subtitle{margin-bottom:16px}}
</style>
