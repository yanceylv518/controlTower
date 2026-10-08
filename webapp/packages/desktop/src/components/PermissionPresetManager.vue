<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { PermissionOption, PermissionPreset, ScopedUser } from '@ct/shared'
import { auth } from '../api'
import { useAuthStore } from '../stores/auth'
import { can, permissionChanges, permissionError, samePermissions } from '../permissions'
import PermissionTree from './PermissionTree.vue'

const emit = defineEmits<{ 'accounts-changed': [] }>()
const current = useAuthStore()
const presets = ref<PermissionPreset[]>([])
const users = ref<ScopedUser[]>([])
const catalog = ref<PermissionOption[]>([])
const maxPresets = ref(100), maxAccounts = ref(50)
const loading = ref(false), saving = ref(false), error = ref('')
const keyword = ref(''), selected = ref<PermissionPreset | null>(null)
const form = reactive({ name: '', description: '', permissions: [] as string[] })
const options = computed(() => catalog.value.filter(option => can(current.user, option.key)))
const filtered = computed(() => presets.value.filter(preset => `${preset.name} ${preset.description}`.toLowerCase().includes(keyword.value.trim().toLowerCase())))
const dirty = computed(() => form.name !== (selected.value?.name || '') || form.description !== (selected.value?.description || '') || !samePermissions(form.permissions, selected.value?.permissions || []))
const changes = computed(() => permissionChanges(selected.value?.permissions || [], form.permissions, options.value))
let controller: AbortController | undefined
let disposed = false

function edit(preset: PermissionPreset | null) {
  selected.value = preset ? { ...preset, permissions: [...preset.permissions] } : null
  Object.assign(form, { name: preset?.name || '', description: preset?.description || '', permissions: [...(preset?.permissions || [])] })
  error.value = ''
}
async function confirmLeave() {
  if (saving.value || batchSaving.value) return false
  if (!dirty.value) return true
  try { await ElMessageBox.confirm('预设修改尚未保存，是否放弃？', '离开编辑', { confirmButtonText: '放弃修改', cancelButtonText: '继续编辑', type: 'warning' }); return true } catch { return false }
}
defineExpose({ confirmLeave })
onBeforeRouteLeave(confirmLeave)
onBeforeUnmount(() => { disposed = true; controller?.abort() })
async function load() {
  controller?.abort()
  const request = new AbortController(); controller = request
  const timeout = setTimeout(() => request.abort(), 15000)
  loading.value = true
  try {
    const [list, accounts] = await Promise.all([auth.permissionPresets(request.signal), auth.users(request.signal)])
    if (disposed || controller !== request) return false
    presets.value = list.items; maxPresets.value = list.max_presets; maxAccounts.value = list.max_apply_accounts
    users.value = accounts.items; catalog.value = accounts.permissions; error.value = ''
    return true
  } catch (cause) {
    if (!disposed && controller === request) error.value = permissionError(cause)
    return false
  } finally { clearTimeout(timeout); if (controller === request) loading.value = false }
}
async function select(preset: PermissionPreset | null) { if (await confirmLeave()) edit(preset) }
async function refresh() {
  if (!await confirmLeave()) return
  const id = selected.value?.id
  if (await load()) edit(presets.value.find(p => p.id === id) || presets.value[0] || null)
}
function copyPreset() {
  if (!selected.value || dirty.value || saving.value) return
  const source = selected.value
  edit(null); form.name = `${source.name.slice(0,58)}（副本）`; form.description = source.description; form.permissions = [...source.permissions]
}
async function save() {
  if (saving.value || !dirty.value) return
  if (!form.name.trim() || !form.permissions.length) { error.value = '请填写预设名称并选择至少一项权限'; return }
  if (!selected.value && presets.value.length >= maxPresets.value) { error.value = '预设数量已达到上限'; return }
  saving.value = true; error.value = ''
  try {
    const body = { name: form.name.trim(), description: form.description.trim(), permissions: [...form.permissions], version: selected.value?.version }
    const result = selected.value ? await auth.updatePermissionPreset(selected.value.id, body) : await auth.createPermissionPreset(body)
    presets.value = [...presets.value.filter(p => p.id !== result.id), result].sort((a,b) => a.id-b.id)
    edit(result); ElMessage.success('预设已保存，已有账号权限未改变')
  } catch (cause) { error.value = permissionError(cause) } finally { saving.value = false }
}
async function removePreset() {
  if (!selected.value || saving.value) return
  const target = selected.value
  try { await ElMessageBox.confirm(`删除「${target.name}」？已赋权账号保留现有权限。`, '删除权限预设', { type: 'warning', confirmButtonText: '删除预设', cancelButtonText: '取消' }) } catch { return }
  saving.value = true
  try {
    await auth.deletePermissionPreset(target.id, target.version)
    presets.value = presets.value.filter(p => p.id !== target.id); edit(presets.value[0] || null)
    ElMessage.success('预设已删除，账号权限未改变')
  } catch (cause) { error.value = permissionError(cause) } finally { saving.value = false }
}
function names(keys: string[]) { return keys.map(key => catalog.value.find(option => option.key === key)?.label || key).join('、') || '无' }

const batchOpen = ref(false), batchSaving = ref(false), batchReview = ref(false), batchError = ref('')
const batchPreset = ref<PermissionPreset | null>(null)
const batchUsers = ref<ScopedUser[]>([]), batchIDs = ref<number[]>([])
const batchMode = ref<'merge' | 'replace'>('merge'), batchSearch = ref(''), batchPage = ref(1)
function disabledReason(user: ScopedUser) {
  if (user.id === current.user?.id) return '当前账号'
  if (user.role !== 'admin') return '查看账号'
  if (user.permissions?.includes('*')) return '受保护管理员'
  if (!(user.permissions || []).every(key => can(current.user, key))) return '超出可管理权限'
  return ''
}
const batchMatches = computed(() => batchUsers.value.filter(user => user.role === 'admin' && `${user.username} ${user.display_name || ''}`.toLowerCase().includes(batchSearch.value.trim().toLowerCase())))
const batchVisible = computed(() => batchMatches.value.slice((batchPage.value-1)*20, batchPage.value*20))
watch(batchSearch, () => { batchPage.value = 1 })
const preview = computed(() => batchUsers.value.filter(user => batchIDs.value.includes(user.id)).map(user => {
  const before = user.permissions || []
  const after = batchMode.value === 'merge' ? [...new Set([...before, ...(batchPreset.value?.permissions || [])])] : [...(batchPreset.value?.permissions || [])]
  return { user, before, after, ...permissionChanges(before, after, catalog.value) }
}))
const changedCount = computed(() => preview.value.filter(row => row.added.length || row.removed.length).length)
async function openBatch() {
  if (!selected.value || dirty.value || saving.value) return
  const id = selected.value.id
  if (!await load()) return
  const latest = presets.value.find(p => p.id === id)
  if (!latest) { edit(null); error.value = '预设已被删除'; return }
  edit(latest); batchPreset.value = { ...latest, permissions: [...latest.permissions] }
  batchUsers.value = users.value.map(user => ({ ...user, permissions: [...(user.permissions || [])] }))
  batchIDs.value = []; batchMode.value = 'merge'; batchSearch.value = ''; batchPage.value = 1; batchReview.value = false; batchError.value = ''; batchOpen.value = true
}
function selectPage() {
  batchIDs.value = [...new Set([...batchIDs.value, ...batchVisible.value.filter(user => !disabledReason(user)).map(user => user.id)])].slice(0,maxAccounts.value)
}
async function refreshBatch() {
  const id = batchPreset.value?.id
  if (!await load()) { batchError.value = error.value; return }
  const latest = presets.value.find(p => p.id === id)
  if (!latest) { batchError.value = '预设已删除，请关闭并选择其他预设'; return }
  batchPreset.value = { ...latest, permissions: [...latest.permissions] }; edit(latest)
  batchUsers.value = users.value.map(user => ({ ...user, permissions: [...(user.permissions || [])] }))
  batchIDs.value = batchIDs.value.filter(id => batchUsers.value.some(user => user.id === id && !disabledReason(user)))
  batchError.value = ''; batchReview.value = false
}
async function applyBatch() {
  if (batchSaving.value || !batchPreset.value || !batchIDs.value.length || !batchReview.value) return
  batchSaving.value = true; batchError.value = ''
  try {
    const result = await auth.applyPermissionPreset(batchPreset.value.id, { version: batchPreset.value.version, user_ids: [...batchIDs.value], mode: batchMode.value, expected_permissions: Object.fromEntries(preview.value.map(row => [String(row.user.id), row.before])) })
    batchOpen.value = false
    ElMessage.success(`已修改 ${result.changed} 个账号，${result.selected-result.changed} 个账号权限未变化`)
    emit('accounts-changed')
  } catch (cause) { batchError.value = permissionError(cause) } finally { batchSaving.value = false }
}
void load().then(ok => { if (ok && !disposed) edit(presets.value[0] || null) })
</script>

<template>
  <div class="preset-intro">预设用于复制一组权限。保存或删除预设不会自动修改账号。</div>
  <el-alert v-if="error" type="error" :closable="false" class="preset-error"><div role="alert">{{ error }}</div><el-button link :disabled="saving || loading" @click="refresh">载入最新数据</el-button></el-alert>
  <div v-loading="loading" class="preset-workspace">
    <aside class="preset-sidebar">
      <div class="preset-tools"><strong>权限预设</strong><el-button size="small" :disabled="saving || loading || presets.length >= maxPresets" @click="select(null)">新建预设</el-button></div>
      <el-input v-model="keyword" clearable placeholder="搜索预设名称或备注" aria-label="搜索权限预设" />
      <div class="preset-list">
        <button v-for="preset in filtered" :key="preset.id" type="button" :class="['preset-item', { active: selected?.id === preset.id }]" :aria-pressed="selected?.id === preset.id" :disabled="saving" @click="select(preset)"><strong>{{ preset.name }}</strong><span>{{ preset.permissions.length }} 项权限 · {{ preset.description || '暂无备注' }}</span></button>
        <el-empty v-if="!filtered.length && !loading" :description="keyword ? '没有匹配的预设' : '还没有权限预设'" :image-size="48" />
      </div>
    </aside>
    <section class="preset-editor">
      <div class="preset-tools"><strong>{{ selected ? '编辑权限预设' : '新建权限预设' }}</strong><div><el-button v-if="selected" link :disabled="saving || dirty" @click="copyPreset">复制</el-button><el-button v-if="selected" link type="danger" :disabled="saving" @click="removePreset">删除</el-button></div></div>
      <el-form label-position="top" :disabled="saving" @submit.prevent="save">
        <div class="preset-fields"><el-form-item label="预设名称" required><el-input v-model="form.name" maxlength="64" placeholder="例如：运维管理员" /></el-form-item><el-form-item label="备注"><el-input v-model="form.description" maxlength="256" placeholder="说明适用职责（选填）" /></el-form-item></div>
        <PermissionTree :key="selected?.id || 'new'" v-model="form.permissions" :options="options" :allow-full="false" :baseline="selected?.permissions || []" :disabled="saving" />
      </el-form>
      <div v-if="selected" class="preset-meta">最近更新：{{ new Date(selected.updated_at).toLocaleString('zh-CN') }} · {{ selected.updated_by }}</div>
      <div class="preset-footer"><span>已选 {{ form.permissions.length }} 项 · 新增 {{ changes.added.length }} / 移除 {{ changes.removed.length }}</span><div><el-button v-if="selected" :disabled="dirty || saving || loading" @click="openBatch">应用到账号</el-button><el-button type="primary" :loading="saving" :disabled="!dirty || loading" @click="save">{{ selected ? '保存预设' : '创建预设' }}</el-button></div></div>
      <div v-if="dirty && selected" class="preset-meta">保存预设后可应用到账号。</div>
    </section>
  </div>
  <el-dialog v-model="batchOpen" title="按预设批量赋权" width="min(800px, calc(100vw - 24px))" top="5vh" :close-on-click-modal="false" :close-on-press-escape="!batchSaving" :show-close="!batchSaving" :before-close="(done: () => void) => { if (!batchSaving) done() }" class="preset-batch">
    <div class="batch-heading"><strong>{{ batchPreset?.name }}</strong><span>{{ batchPreset?.permissions.length }} 项权限</span></div>
    <el-alert v-if="batchError" type="error" :closable="false" class="preset-error"><div role="alert">{{ batchError }}</div><el-button link :disabled="batchSaving || loading" @click="refreshBatch">刷新预设与账号差异</el-button></el-alert>
    <template v-if="!batchReview">
      <el-radio-group v-model="batchMode"><el-radio-button value="merge">补充权限</el-radio-button><el-radio-button value="replace">替换现有权限</el-radio-button></el-radio-group>
      <p class="preset-meta">{{ batchMode === 'merge' ? '保留已有权限，补充预设包含的权限。' : '以此预设替换权限，不在预设中的权限将被移除。' }} 最多选择 {{ maxAccounts }} 个账号。</p>
      <div class="batch-tools"><el-input v-model="batchSearch" clearable placeholder="搜索账号或姓名" aria-label="搜索赋权账号" /><el-button @click="selectPage">选择本页可管理账号</el-button></div>
      <el-checkbox-group v-model="batchIDs" :max="maxAccounts" class="batch-accounts">
        <el-checkbox v-for="user in batchVisible" :key="user.id" :value="user.id" :disabled="Boolean(disabledReason(user))"><span class="batch-name">{{ user.display_name || user.username }}<small>{{ user.username }}</small></span><span class="preset-meta">{{ disabledReason(user) || (user.enabled ? '已启用' : '已停用，赋权不会启用账号') }}</span></el-checkbox>
      </el-checkbox-group>
      <el-empty v-if="!batchMatches.length" description="没有匹配的管理员" :image-size="48" />
      <el-pagination v-model:current-page="batchPage" :page-size="20" :total="batchMatches.length" layout="prev, pager, next" small />
    </template>
    <template v-else>
      <p>{{ batchMode === 'merge' ? '补充权限' : '替换现有权限' }} · {{ changedCount }} 个账号会变化，{{ preview.length-changedCount }} 个保持不变</p>
      <div class="batch-review">
        <details v-for="row in preview" :key="row.user.id" class="batch-diff"><summary>{{ row.user.display_name || row.user.username }}（{{ row.user.username }}）<span>新增 {{ row.added.length }} / 移除 {{ row.removed.length }}</span></summary><p>新增：{{ names(row.added) }}</p><p class="removed">移除：{{ names(row.removed) }}</p></details>
      </div>
    </template>
    <template #footer><div class="preset-footer"><span>已选 {{ batchIDs.length }} / {{ maxAccounts }} 个账号</span><div><el-button :disabled="batchSaving" @click="batchReview ? batchReview = false : batchOpen = false">{{ batchReview ? '返回选择' : '取消' }}</el-button><el-button v-if="!batchReview" type="primary" :disabled="!batchIDs.length || loading || Boolean(batchError)" @click="batchReview = true">查看变更</el-button><el-button v-else type="primary" :loading="batchSaving" :disabled="Boolean(batchError) || loading" @click="applyBatch">确认赋权 {{ batchIDs.length }} 个账号</el-button></div></div></template>
  </el-dialog>
</template>

<style scoped>
.preset-workspace{height:clamp(460px,calc(100dvh - 220px),900px);min-height:0}.preset-sidebar,.preset-editor{display:flex;flex-direction:column;min-height:0}.preset-sidebar>.el-input,.preset-tools,.preset-fields,.preset-meta,.preset-footer{flex-shrink:0}.preset-sidebar .preset-list{flex:1;min-height:0;overflow:auto;overscroll-behavior:contain;scrollbar-width:thin}.preset-editor>.el-form{display:flex;flex-direction:column;flex:1;min-height:0}.preset-editor :deep(.permission-picker){flex:1;min-height:120px}
.preset-intro,.preset-meta{font-size:12px;color:var(--el-text-color-secondary);line-height:1.6}.preset-intro{margin:0 0 16px}.preset-error{margin-bottom:14px}.preset-workspace{display:grid;grid-template-columns:240px minmax(0,1fr);gap:24px}.preset-sidebar{border-right:1px solid var(--el-border-color-lighter);padding-right:20px}.preset-tools{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:14px}.preset-list{margin-top:12px}.preset-item{display:block;width:100%;text-align:left;border:0;border-bottom:1px solid var(--el-border-color-lighter);padding:12px;background:transparent;color:var(--el-text-color-primary);cursor:pointer}.preset-item.active{background:var(--el-color-primary-light-9);color:var(--el-color-primary)}.preset-item strong,.preset-item span{display:block;overflow-wrap:anywhere}.preset-item span{font-size:12px;color:var(--el-text-color-secondary);margin-top:4px}.preset-item:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:-2px}.preset-editor{min-width:0}.preset-fields{display:grid;grid-template-columns:1fr 1fr;gap:16px}.preset-footer{display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:12px;margin-top:18px}.preset-footer>span{font-size:12px;color:var(--el-text-color-secondary)}.preset-footer>div{display:flex;gap:8px;flex-wrap:wrap}.preset-footer :deep(.el-button+.el-button){margin-left:0}.batch-heading{display:flex;align-items:center;gap:12px;margin-bottom:16px}.batch-heading>span{font-size:12px;color:var(--el-text-color-secondary)}.batch-tools{display:flex;gap:10px;margin:16px 0}.batch-accounts{display:grid;gap:4px;max-height:380px;overflow:auto;margin-bottom:12px}.batch-accounts :deep(.el-checkbox){margin-right:0;height:auto;min-height:52px;padding:8px}.batch-accounts :deep(.el-checkbox__label){display:flex;flex:1;justify-content:space-between;gap:12px;white-space:normal}.batch-name small{display:block;font-size:12px;color:var(--el-text-color-secondary)}.batch-review{max-height:440px;overflow:auto}.batch-diff{border-bottom:1px solid var(--el-border-color-lighter);padding:12px 0}.batch-diff summary{cursor:pointer;overflow-wrap:anywhere}.batch-diff summary>span{margin-left:12px;font-size:12px;color:var(--el-text-color-secondary)}.batch-diff p{font-size:12px;overflow-wrap:anywhere}.removed{color:var(--el-color-danger)}
@media(max-width:760px){.preset-workspace{grid-template-columns:1fr;gap:18px}.preset-sidebar{border-right:0;border-bottom:1px solid var(--el-border-color-lighter);padding:0 0 16px}.preset-list{max-height:190px;overflow:auto}.preset-fields{grid-template-columns:1fr;gap:0}.batch-tools{flex-wrap:wrap}.batch-accounts :deep(.el-checkbox__label){flex-wrap:wrap;gap:4px}.preset-footer>div{margin-left:auto}}
@media(max-width:760px){.preset-workspace{height:auto}.preset-editor{height:min(720px,calc(100dvh - 140px));min-height:440px}.preset-sidebar .preset-list{flex:none}}
</style>
