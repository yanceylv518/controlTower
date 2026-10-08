<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { siteOf, type PermissionPreset, type ScopedUser } from '@ct/shared'
import { auth, dashboard, passthrough } from '../api'
import { useAuthStore } from '../stores/auth'
import { can, permissionChanges, permissionError, permissionKeys, samePermissions } from '../permissions'
import AppShell from '../components/AppShell.vue'
import PermissionTree from '../components/PermissionTree.vue'
import PermissionPresetManager from '../components/PermissionPresetManager.vue'

function showError(error: unknown) {
  ElMessage.error(permissionError(error))
}
const current = useAuthStore()
const tab = ref<'admin' | 'viewer' | 'presets'>('admin')
const manager = ref<InstanceType<typeof PermissionPresetManager> | null>(null)
const editorRole = ref<'admin' | 'viewer'>('admin')
const loading = ref(false), pageError = ref(''), editorError = ref(''), accountSearch = ref('')
const initialForm = ref('')
const presets = ref<PermissionPreset[]>([]), presetLoading = ref(false), presetError = ref('')
const selectedPreset = ref<number | null>(null), applyMode = ref<'merge' | 'replace'>('merge')
const savingPreset = ref(false), savePresetOpen = ref(false), savePresetError = ref('')
const presetForm = reactive({ name: '', description: '', permissions: [] as string[] })
const toggling = ref<number[]>([])
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
let usersController: AbortController | undefined, presetController: AbortController | undefined
let disposed = false
const changes = computed(() => permissionChanges(editing.value?.permissions || [], form.permissions, grantable.value))
const editorDirty = computed(() => formState() !== initialForm.value)
const draftPresetMatch = computed(() => presets.value.find(preset => samePermissions(preset.permissions, form.permissions)))
function formState() { return JSON.stringify({ ...form, permissions: [...form.permissions].sort(), scope_user_ids: [...form.scope_user_ids].sort((a,b) => a-b) }) }
function names(keys: string[]) { return keys.map(key => permissions.value.find(option => option.key === key)?.label || key).join('、') || '尚未分配' }
function presetMatch(row: ScopedUser) { return presets.value.find(preset => samePermissions(preset.permissions, row.permissions || []))?.name }
onBeforeUnmount(() => { disposed = true; usersController?.abort(); presetController?.abort(); customerRequest++ })

async function load(afterSave = false) {
  usersController?.abort()
  const request = new AbortController(); usersController = request
  const timeout = setTimeout(() => request.abort(), 15000)
  loading.value = true
  try {
    const users = await auth.users(request.signal)
    if (disposed || usersController !== request) return
    items.value = users.items; permissions.value = users.permissions; pageError.value = ''
  } catch (cause) {
    if (!disposed && usersController === request) pageError.value = `${afterSave ? '修改已保存，但列表刷新失败。' : ''}${permissionError(cause)}`
  } finally { clearTimeout(timeout); if (usersController === request) loading.value = false }
}
async function loadPresets() {
  presetController?.abort()
  const request = new AbortController(); presetController = request
  const timeout = setTimeout(() => request.abort(), 15000)
  presetLoading.value = true
  try {
    const result = await auth.permissionPresets(request.signal)
    if (disposed || presetController !== request) return
    presets.value = result.items; presetError.value = ''
    if (selectedPreset.value && !presets.value.some(p => p.id === selectedPreset.value)) selectedPreset.value = null
  } catch (cause) {
    if (!disposed && presetController === request) { presetError.value = permissionError(cause); presets.value = [] }
  } finally { clearTimeout(timeout); if (presetController === request) presetLoading.value = false }
}
async function leaveTab(next: string | number) {
  if (tab.value === 'presets' && manager.value && !await manager.value.confirmLeave()) return false
  accountSearch.value = ''; if (next !== 'presets') void loadPresets(); return true
}
async function confirmEditorLeave() {
  if (saving.value || savingPreset.value || resetSaving.value) return false
  if ((!open.value || !editorDirty.value) && !(savePresetOpen.value && (presetForm.name || presetForm.description))) return true
  try { await ElMessageBox.confirm('账号或预设草稿尚未保存，是否放弃？', '放弃修改', { confirmButtonText: '放弃修改', cancelButtonText: '继续编辑', type: 'warning' }); return true } catch { return false }
}
onBeforeRouteLeave(confirmEditorLeave)
async function closeEditor(done?: () => void) { if (await confirmEditorLeave()) { open.value = false; done?.() } }
function editorClosed() { form.password = ''; customerRequest++; loadingCustomers.value = false; editorError.value = '' }
function applyPreset() {
  const preset = presets.value.find(p => p.id === selectedPreset.value)
  if (!preset || saving.value) return
  form.permissions = applyMode.value === 'merge' ? [...new Set([...form.permissions, ...preset.permissions])] : [...preset.permissions]
  ElMessage.info(`已应用「${preset.name}」，保存账号后生效`)
}
function showSavePreset() {
  const keys = permissionKeys(form.permissions, grantable.value)
  if (!keys.length) { ElMessage.warning('请先选择至少一项权限'); return }
  Object.assign(presetForm, { name: '', description: '', permissions: keys }); savePresetError.value = ''; savePresetOpen.value = true
}
async function saveDraftPreset() {
  if (savingPreset.value) return
  if (!presetForm.name.trim()) { savePresetError.value = '请输入预设名称'; return }
  savingPreset.value = true; savePresetError.value = ''
  try {
    const result = await auth.createPermissionPreset({ ...presetForm, name: presetForm.name.trim(), description: presetForm.description.trim() })
    presets.value.push(result); savePresetOpen.value = false; presetError.value = ''
    ElMessage.success('预设已保存，账号草稿已保留')
  } catch (cause) { savePresetError.value = permissionError(cause) } finally { savingPreset.value = false }
}

async function loadCustomers(keyword = '') {
  const request = ++customerRequest, site = form.scope_site
  if (!site) { loadingCustomers.value = false; return }
  const query = keyword.trim()
  const cacheKey = `${site}:${query.toLowerCase()}`
  const cached = customerCache.get(cacheKey)
  if (cached && cached.expires > Date.now()) { customers.value = cached.items; loadingCustomers.value = false; return }
  loadingCustomers.value = true
  try {
    const response = await passthrough.users({ site, keyword: query, status: 1, limit: 50, offset: 0 })
    if (disposed || request !== customerRequest || !open.value) return
    const result = response.items.map(item => ({ id: item.id, name: item.display_name || item.username || `客户 ${item.id}` }))
    if (customerCache.size >= 30) customerCache.clear()
    customerCache.set(cacheKey, { expires: Date.now() + 5 * 60_000, items: result })
    if (request === customerRequest) customers.value = result
  } catch {
    if (request === customerRequest && !disposed && open.value) editorError.value = '客户列表加载失败，请检查站点只读连接后重新搜索'
  } finally { if (request === customerRequest) loadingCustomers.value = false }
}

async function loadSelectedCustomers(userIDs: number[]) {
  if (!form.scope_site || !userIDs.length) return
  const site = form.scope_site, request = customerRequest
  try {
    const response = await passthrough.users({ site, user_ids: userIDs.join(','), limit: Math.max(50, userIDs.length), offset: 0 })
    if (disposed || request !== customerRequest || !open.value) return
    const selected = response.items.map(item => ({ id: item.id, name: item.display_name || item.username || `客户 ${item.id}` }))
    const byID = new Map(customers.value.map(item => [item.id, item]))
    selected.forEach(item => byID.set(item.id, item))
    customers.value = [...byID.values()]
  } catch { if (!disposed && request === customerRequest && open.value) editorError.value = '已选客户名称加载失败，原客户 ID 保留' }
}

function changeSite() {
  form.scope_user_ids = []
  customers.value = []
  void loadCustomers()
}

async function showCreate() {
  const role = tab.value
  if (role === 'presets' || loading.value) return
  if (role === 'viewer') {
    try { instances.value = (await dashboard.instances()).items } catch (cause) { showError(cause); return }
  }
  if (disposed || tab.value !== role) return
  editorRole.value = role
  editing.value = null
  Object.assign(form, { username: '', password: '', scope_site: sites.value[0] || '', scope_user_ids: [], display_name: '', permissions: [] })
  initialForm.value = formState(); editorError.value = ''; selectedPreset.value = null; applyMode.value = 'replace'
  open.value = true
  if (editorRole.value === 'viewer') await loadCustomers()
  else void loadPresets()
}

async function showEdit(row: ScopedUser) {
  if (!manageable(row)) return
  editorRole.value = row.role === 'admin' ? 'admin' : 'viewer'
  editing.value = { ...row, permissions: [...(row.permissions || [])], scope_user_ids: [...(row.scope_user_ids || [])] }
  Object.assign(form, { username: row.username, password: '', scope_site: row.scope_site, scope_user_ids: [...(row.scope_user_ids || [])], display_name: row.display_name || '', permissions: [...(row.permissions || [])] })
  initialForm.value = formState(); editorError.value = ''; selectedPreset.value = null; applyMode.value = 'merge'
  customers.value = (row.scope_user_ids || []).map(id => ({ id, name: `客户 ${id}` }))
  open.value = true
  if (row.role === 'viewer') { await loadCustomers(); if (open.value && editing.value?.id === row.id) await loadSelectedCustomers(row.scope_user_ids) }
  else void loadPresets()
}

async function create() {
  if (editorRole.value === 'admin') { await saveAdmin(); return }
  if (!form.username.trim() || form.password.length < 8 || !form.scope_site || !form.scope_user_ids.length) { ElMessage.warning('请填写账号、至少 8 位密码，并选择站点和客户'); return }
  saving.value = true
  try {
    await auth.createUser({ username: form.username.trim(), password: form.password, role: 'viewer', scope_site: form.scope_site, scope_user_ids: [...form.scope_user_ids] })
    open.value = false
    form.password = ''; await load(true)
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
    await load(true)
    ElMessage.success('可查看客户已更新')
  } catch (error) { showError(error) } finally { saving.value = false }
}
async function submit() { if (saving.value || savingPreset.value) return; if (editing.value) await saveEdit(); else await create() }
async function toggle(row: ScopedUser, enabled: boolean) {
  if (!manageable(row) || toggling.value.includes(row.id)) return
  toggling.value.push(row.id)
  try { await auth.updateUser(row.id, { role: row.role, scope_site: row.scope_site, scope_user_ids: row.scope_user_ids, enabled, display_name: row.display_name, permissions: row.permissions }); row.enabled = enabled; await load(true) } catch (error) { showError(error) } finally { toggling.value = toggling.value.filter(id => id !== row.id) }
}
const visibleItems = computed(() => items.value.filter(item => item.role === tab.value && `${item.username} ${item.display_name || ''}`.toLowerCase().includes(accountSearch.value.trim().toLowerCase())))
const grantable = computed(() => permissions.value.filter(item => can(current.user, item.key)))
function manageable(row: ScopedUser) {
  return row.id !== current.user?.id && (row.role === 'viewer' || (row.permissions || []).every(p => can(current.user, p)))
}
async function saveAdmin() {
  if (!form.username.trim() || (!editing.value && (form.password.length < 8 || form.password.length > 1024))) { ElMessage.warning('请填写账号和至少 8 位的初始密码'); return }
  saving.value = true; editorError.value = ''
  try {
    if (!form.permissions.length) {
      try { await ElMessageBox.confirm('该管理员将没有可访问的功能，是否继续保存？', '未分配权限', { type: 'warning', confirmButtonText: '保存为空权限', cancelButtonText: '继续配置' }) } catch { return }
    }
    const body = { role: 'admin', scope_site: '', scope_user_ids: [], display_name: form.display_name.trim(), permissions: [...form.permissions] }
    if (editing.value) await auth.updateUser(editing.value.id, { ...body, enabled: editing.value.enabled })
    else await auth.createUser({ ...body, username: form.username.trim(), password: form.password })
    open.value = false
    form.password = ''; await load(true)
    ElMessage.success(editing.value ? '管理员权限已更新' : '管理员已创建')
  } catch (error) { editorError.value = permissionError(error) } finally { saving.value = false }
}
function showReset(row: ScopedUser) { resetPassword.value = ''; resetTarget.value = row }
async function submitReset() {
  if (resetSaving.value) return
  if (!resetTarget.value || resetPassword.value.length < 8) { ElMessage.warning('密码至少 8 位'); return }
  resetSaving.value = true
  try {
    await auth.resetPassword(resetTarget.value.id, resetPassword.value)
    resetTarget.value = null; resetPassword.value = ''
    ElMessage.success('密码已重置，该账号需要重新登录')
  } catch (error) { showError(error) } finally { resetSaving.value = false }
}
void load().catch(showError)
void loadPresets()
</script>

<template>
  <AppShell title="账号管理">
    <template #tools><el-button v-if="tab !== 'presets'" type="primary" :disabled="loading" @click="showCreate">{{ tab === 'admin' ? '创建管理员' : '创建查看账号' }}</el-button></template>
    <el-tabs v-model="tab" :before-leave="leaveTab"><el-tab-pane label="管理员" name="admin" /><el-tab-pane label="查看账号" name="viewer" /><el-tab-pane label="权限预设" name="presets" /></el-tabs>
    <PermissionPresetManager v-if="tab === 'presets'" ref="manager" @accounts-changed="load(true)" />
    <template v-else>
      <div class="account-toolbar"><span>{{ tab === 'admin' ? '按功能权限管理账号，权限变更会记录操作人。' : '按站点和客户 ID 隔离查看范围。' }}</span><el-input v-model="accountSearch" clearable placeholder="搜索登录账号或姓名" aria-label="搜索账号" /><el-button :loading="loading" @click="load()">刷新</el-button></div>
      <el-alert v-if="pageError" type="error" :closable="false" class="intro" :title="pageError" />
      <el-table v-loading="loading" v-mobile-cards :data="visibleItems" row-key="id" :empty-text="pageError ? '列表暂不可用，请重试' : '没有匹配的账号'">
        <el-table-column prop="username" label="登录账号" min-width="140" />
        <el-table-column v-if="tab === 'admin'" prop="display_name" label="姓名" min-width="110" />
        <el-table-column v-if="tab === 'admin'" label="权限" min-width="280"><template #default="{ row }">
          <el-tag v-if="row.permissions?.includes('*')">全部权限</el-tag>
          <span v-else-if="!row.permissions?.length" class="muted">尚未分配</span>
          <template v-else>
            <el-popover trigger="click" :width="320"><template #reference><el-button link class="permission-summary">{{ names(row.permissions.slice(0, 3)) }}{{ row.permissions.length > 3 ? '…' : '' }} · {{ row.permissions.length }} 项</el-button></template><div class="permission-tags"><el-tag v-for="key in row.permissions" :key="key" type="info">{{ names([key]) }}</el-tag></div></el-popover>
            <div v-if="presetMatch(row)" class="tip">与「{{ presetMatch(row) }}」权限一致</div>
          </template>
        </template></el-table-column>
        <el-table-column v-if="tab === 'viewer'" prop="scope_site" label="可看站点" />
        <el-table-column v-if="tab === 'viewer'" label="可看客户 ID"><template #default="{ row }">{{ row.scope_user_ids?.join('、') }}</template></el-table-column>
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-switch :disabled="!manageable(row)" :loading="toggling.includes(row.id)" :model-value="row.enabled" :aria-label="row.username + '的启用状态'" @change="toggle(row, Boolean($event))" /></template></el-table-column>
        <el-table-column label="操作" width="210"><template #default="{ row }"><template v-if="manageable(row)"><el-button link type="primary" @click="showEdit(row)">{{ tab === 'admin' ? '配置权限' : '修改客户' }}</el-button><el-button v-if="tab === 'admin'" link type="primary" @click="showReset(row)">重置密码</el-button></template><span v-else class="muted">{{ row.id === current.user?.id ? '当前账号' : '超出可管理权限' }}</span></template></el-table-column>
      </el-table>
    </template>
    <el-dialog v-model="open" :title="editing ? (editorRole === 'admin' ? '配置管理员权限 · ' + editing.username : '修改可查看客户') : (editorRole === 'admin' ? '创建管理员' : '创建查看账号')" :width="editorRole === 'admin' ? 'min(980px, calc(100vw - 24px))' : 'min(640px, calc(100vw - 24px))'" top="5vh" class="account-editor" :close-on-click-modal="false" :close-on-press-escape="!saving && !savingPreset" :show-close="!saving && !savingPreset" :before-close="closeEditor" destroy-on-close @closed="editorClosed">
      <el-alert v-if="editorError" type="error" :closable="false" class="intro" :title="editorError" />
      <el-form label-position="top" :disabled="saving" @submit.prevent="submit">
        <div :class="['account-basics', { creating: !editing, viewer: editorRole === 'viewer' }]">
          <el-form-item label="登录账号" :required="!editing"><span v-if="editing" class="account-identity">{{ form.username }}</span><el-input v-else v-model="form.username" maxlength="64" autocomplete="off" placeholder="请输入登录账号" /></el-form-item>
          <el-form-item v-if="editorRole === 'admin'" label="姓名"><el-input v-model="form.display_name" maxlength="64" placeholder="选填" /></el-form-item>
          <el-form-item v-if="!editing" label="初始密码" required><el-input v-model="form.password" type="password" maxlength="1024" autocomplete="new-password" show-password placeholder="至少 8 位" /></el-form-item>
        </div>
        <template v-if="editorRole === 'admin'">
          <section class="preset-picker">
            <div class="preset-title"><strong>从权限预设开始</strong><el-button link :disabled="saving || !form.permissions.length" @click="showSavePreset">当前权限另存为预设</el-button></div>
            <el-alert v-if="presetError" type="warning" :closable="false"><div>{{ presetError }}</div><el-button link :loading="presetLoading" @click="loadPresets">重新加载预设</el-button><span class="tip">仍可直接勾选下方权限。</span></el-alert>
            <div v-else class="preset-controls">
              <el-select v-model="selectedPreset" clearable filterable :loading="presetLoading" placeholder="选择权限预设" aria-label="权限预设"><el-option v-for="preset in presets" :key="preset.id" :value="preset.id" :label="preset.name + ' · ' + preset.permissions.length + ' 项'" /></el-select>
              <el-radio-group v-if="editing || form.permissions.length" v-model="applyMode" size="small"><el-radio-button value="merge">补充权限</el-radio-button><el-radio-button value="replace">替换现有权限</el-radio-button></el-radio-group>
              <el-button :disabled="!selectedPreset || presetLoading" @click="applyPreset">应用到草稿</el-button>
            </div>
            <div v-if="!presetError" class="tip">{{ applyMode === 'merge' ? '保留现有权限，补充预设包含的权限。' : '以预设重新选择，不在预设中的权限会被移除。' }} 保存账号后生效。</div>
            <div v-if="draftPresetMatch" class="tip">当前权限与「{{ draftPresetMatch.name }}」一致</div>
          </section>
          <PermissionTree v-model="form.permissions" :options="grantable" :allow-full="can(current.user, '*')" :baseline="editing?.permissions || []" :disabled="saving" :scrollable="false" />
        </template>
        <template v-else>
          <el-form-item label="站点" required><el-select v-model="form.scope_site" filterable :disabled="Boolean(editing)" placeholder="请选择站点" style="width:100%" @change="changeSite"><el-option v-if="editing" :label="form.scope_site" :value="form.scope_site" /><el-option v-for="site in editing ? [] : sites" :key="site" :label="site" :value="site" /></el-select></el-form-item>
          <el-form-item label="客户" required><el-select v-model="form.scope_user_ids" multiple filterable remote reserve-keyword collapse-tags collapse-tags-tooltip :max-collapse-tags="3" :loading="loadingCustomers" :remote-method="loadCustomers" placeholder="请选择或搜索客户" style="width:100%"><el-option v-for="customer in customers" :key="customer.id" :label="customer.name + '（ID ' + customer.id + '）'" :value="customer.id" /></el-select><div class="tip">默认显示前 50 个正常客户，可输入名称或 ID 继续搜索。</div></el-form-item>
        </template>
      </el-form>
      <template #footer><div class="editor-footer"><span v-if="editorRole === 'admin'" aria-live="polite">{{ form.permissions.includes('*') ? '全部权限（包含未来新增功能）' : '已选 ' + form.permissions.length + ' 项' }}<span v-if="editing"> · 新增 {{ changes.added.length }} / 移除 {{ changes.removed.length }}</span><span v-if="editorDirty"> · 尚未保存</span></span><div><el-button :disabled="saving || savingPreset" @click="closeEditor()">取消</el-button><el-button type="primary" :loading="saving" :disabled="savingPreset || (Boolean(editing) && !editorDirty)" @click="submit">{{ editing ? '保存修改' : (editorRole === 'admin' ? '创建管理员' : '创建查看账号') }}</el-button></div></div></template>
    </el-dialog>
    <el-dialog v-model="savePresetOpen" title="当前权限另存为预设" width="min(480px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!savingPreset" :show-close="!savingPreset" :before-close="done => { if (!savingPreset) done() }">
      <p class="tip">保存当前 {{ presetForm.permissions.length }} 项具体权限；账号信息和未保存修改会保留。预设不包含未来新增功能。</p>
      <el-alert v-if="savePresetError" type="error" :title="savePresetError" :closable="false" class="intro" />
      <el-form label-position="top" :disabled="savingPreset" @submit.prevent="saveDraftPreset"><el-form-item label="预设名称" required><el-input v-model="presetForm.name" maxlength="64" placeholder="例如：运维管理员" /></el-form-item><el-form-item label="备注"><el-input v-model="presetForm.description" maxlength="256" placeholder="选填" /></el-form-item></el-form>
      <template #footer><el-button :disabled="savingPreset" @click="savePresetOpen = false">取消</el-button><el-button type="primary" :loading="savingPreset" @click="saveDraftPreset">保存预设</el-button></template>
    </el-dialog>
    <el-dialog :model-value="Boolean(resetTarget)" title="重置管理员密码" width="min(460px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!resetSaving" :show-close="!resetSaving" @update:model-value="(value: boolean) => { if (!value && !resetSaving) { resetTarget = null; resetPassword = '' } }">
      <p>为 {{ resetTarget?.username }} 设置新密码。该账号的现有登录会话将失效。</p>
      <el-input v-model="resetPassword" :disabled="resetSaving" type="password" show-password maxlength="1024" autocomplete="new-password" placeholder="新密码，至少 8 位" @keyup.enter="submitReset" />
      <template #footer><el-button :disabled="resetSaving" @click="resetTarget = null; resetPassword = ''">取消</el-button><el-button type="primary" :loading="resetSaving" @click="submitReset">重置密码</el-button></template>
    </el-dialog>
  </AppShell>
</template>
<style scoped>
.intro{margin-bottom:16px}.tip,.muted{font-size:12px;color:var(--el-text-color-secondary);line-height:1.6}.tip{margin-top:6px}
.account-toolbar{display:flex;align-items:center;gap:12px;margin-bottom:16px}.account-toolbar>span{font-size:12px;color:var(--el-text-color-secondary);margin-right:auto}.account-toolbar>.el-input{max-width:260px}
.permission-tags{display:flex;gap:6px;flex-wrap:wrap}.permission-summary{height:auto;white-space:normal;text-align:left;line-height:1.6}
.account-basics{display:grid;grid-template-columns:1fr 1fr;gap:20px;padding-bottom:8px;border-bottom:1px solid var(--el-border-color-lighter);margin-bottom:18px}.account-basics.creating{grid-template-columns:1fr 1fr 1fr}.account-basics.viewer{grid-template-columns:1fr 1fr}.account-identity{font-weight:600;line-height:32px;overflow-wrap:anywhere}
.preset-picker{margin-bottom:18px}.preset-title{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px}.preset-title>strong{font-size:13px}.preset-controls{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.preset-controls>.el-select{flex:1;min-width:180px}
.editor-footer{display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px;border-top:1px solid var(--el-border-color-lighter);padding-top:12px}.editor-footer>span{font-size:12px;color:var(--el-text-color-secondary)}.editor-footer>div{margin-left:auto}
.account-editor :deep(.el-dialog__body){max-height:calc(88dvh - 130px);overflow:auto}.account-editor :deep(.el-form-item__label){font-size:13px;padding-bottom:5px}.account-editor :deep(.el-input__wrapper){min-height:36px}.account-editor :deep(.permission-groups){max-height:none;overflow:visible}
@media(max-width:700px){.account-toolbar{flex-wrap:wrap}.account-toolbar>span{flex-basis:100%}.account-toolbar>.el-input{flex:1;max-width:none}.account-basics.creating,.account-basics.viewer{grid-template-columns:1fr 1fr}.account-basics.creating>.el-form-item:last-child{grid-column:1/-1}.account-basics{gap:12px}.preset-title{flex-wrap:wrap}.preset-controls>.el-select{flex-basis:100%}.editor-footer{gap:8px}.editor-footer>span{flex-basis:100%}}
</style>
