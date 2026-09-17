<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { dashboard } from '../api';
import { normalizeChannelGroups, splitChannelGroups } from '../utils/channelGroup';

type Preset = { id: string; name: string; groups: string[] };
const props = defineProps<{ site: string; current: string; options: string[]; saving: boolean; manageOnly?: boolean }>();
const emit = defineEmits<{ save: [groups: string[]]; cancel: [] }>();
const tab = ref('adjust'), groups = ref<string[]>([]), selected = ref<string[]>([]), modified = ref(false), confirmed = ref(false);
const presets = ref<Preset[]>([]), revision = ref(0), loading = ref(false), storing = ref(false), loadError = ref('');
const editorOpen = ref(false), editorID = ref(''), editorName = ref(''), editorGroups = ref<string[]>([]);
let generation = 0;
const current = computed(() => splitChannelGroups(props.current));
const allOptions = computed(() => [...new Set([...props.options, ...current.value, ...presets.value.flatMap(p => p.groups), ...groups.value])].sort((a,b) => a.localeCompare(b)));
const added = computed(() => groups.value.filter(g => !current.value.includes(g)));
const removed = computed(() => current.value.filter(g => !groups.value.includes(g)));
const changed = computed(() => added.value.length > 0 || removed.value.length > 0);
const writable = computed(() => !loading.value && !storing.value && !loadError.value);

async function load() {
  const ticket = ++generation, site = props.site;
  loading.value = true; loadError.value = ''; presets.value = []; revision.value = 0;
  try {
    const result = await dashboard.tuningGroupPresets(site);
    if (ticket !== generation) return;
    if (!Array.isArray(result.items) || !Number.isSafeInteger(result.revision)) throw new Error('组合接口不可用，请升级 Server 后重试');
    presets.value = result.items; revision.value = result.revision;
  } catch (error) {
    if (ticket === generation) loadError.value = (error as { status?: number }).status === 404
      ? '当前 Server 尚未提供组合库接口，请升级 Server 后重试'
      : error instanceof Error ? error.message : '组合加载失败';
  } finally { if (ticket === generation) loading.value = false; }
}
watch(() => props.site, () => {
  groups.value = [...current.value]; selected.value = []; confirmed.value = false; modified.value = false;
  editorOpen.value = false; tab.value = props.manageOnly ? 'manage' : 'adjust'; void load();
}, { immediate: true });
watch(groups, () => { confirmed.value = false; }, { deep: true });
function usePresets() {
  groups.value = [...new Set(presets.value.filter(p => selected.value.includes(p.id)).flatMap(p => p.groups))];
  modified.value = false;
}
function restore() { groups.value = [...current.value]; selected.value = []; modified.value = false; }
function openEditor(preset?: Preset, fromCurrent = false) {
  editorID.value = preset?.id || ''; editorName.value = preset?.name || '';
  editorGroups.value = [...(preset?.groups || (fromCurrent ? groups.value : []))]; editorOpen.value = true;
}
async function store(items: Preset[]) {
  if (!writable.value) return false;
  const site = props.site, ticket = generation;
  storing.value = true;
  try {
    const result = await dashboard.saveTuningGroupPresets(site, { items, revision: revision.value });
    if (ticket !== generation) return false;
    presets.value = result.items; revision.value = result.revision;
    return true;
  } catch (error) {
    if (ticket !== generation) return false;
    const code = (error as { code?: string }).code;
    if (code === 'presets_changed') {
      ElMessage.warning('组合已被其他人更新，已重新加载；你的编辑内容仍保留，请核对后再保存');
      await load();
    } else ElMessage.error(error instanceof Error ? error.message : '组合保存失败');
    return false;
  } finally { storing.value = false; }
}
async function savePreset() {
  const name = editorName.value.trim();
  if (!name || Array.from(name).length > 64 || /[\u0000-\u001f\u007f]/u.test(name)) { ElMessage.warning('请输入 1–64 个字符的组合名称'); return; }
  if (presets.value.some(p => p.id !== editorID.value && p.name.toLowerCase() === name.toLowerCase())) { ElMessage.warning('组合名称已存在'); return; }
  let normalized: string;
  try { normalized = normalizeChannelGroups(editorGroups.value); }
  catch (error) { ElMessage.warning((error as Error).message); return; }
  if (!normalized) { ElMessage.warning('请至少选择一个分组'); return; }
  if (editorID.value && !presets.value.some(p => p.id === editorID.value)) { ElMessage.warning('原组合已被删除，请新建组合'); return; }
  if (!editorID.value && presets.value.length >= 100) { ElMessage.warning('每个站点最多保存 100 个组合'); return; }
  const id = editorID.value || `preset-${Date.now()}-${Math.random().toString(36).slice(2,10)}`;
  const item = { id, name, groups: splitChannelGroups(normalized) };
  const items = editorID.value ? presets.value.map(p => p.id === id ? item : p) : [...presets.value, item];
  if (await store(items)) { editorOpen.value = false; if (selected.value.includes(id)) modified.value = true; ElMessage.success('组合已保存，渠道分组未改变'); }
}
async function removePreset(preset: Preset) {
  const site = props.site;
  try { await ElMessageBox.confirm(`删除组合“${preset.name}”？已应用的渠道不受影响。`, '删除组合', { type: 'warning' }); }
  catch { return; }
  if (site !== props.site) return;
  if (await store(presets.value.filter(p => p.id !== preset.id))) {
    selected.value = selected.value.filter(id => id !== preset.id); modified.value = true; ElMessage.success('组合已删除');
  }
}
function applyPreset(preset: Preset) { selected.value = [preset.id]; usePresets(); tab.value = 'adjust'; }
function saveChannel() {
  if (!confirmed.value || props.saving || !changed.value || !groups.value.length) return;
  try { emit('save', splitChannelGroups(normalizeChannelGroups(groups.value))); }
  catch (error) { ElMessage.warning((error as Error).message); }
}
</script>

<template>
  <div class="channel-group-editor">
    <el-tabs v-model="tab">
      <el-tab-pane v-if="!manageOnly" label="调整分组" name="adjust">
        <div class="section-title"><span>选择分组组合 <small>可多选，自动去重</small></span><el-button link type="primary" @click="tab = 'manage'">管理组合</el-button></div>
        <el-alert v-if="loadError" :title="loadError" type="error" :closable="false"><el-button link type="primary" :loading="loading" @click="load">重新加载组合</el-button></el-alert>
        <div v-loading="loading" class="presets-area">
          <el-checkbox-group v-if="presets.length" v-model="selected" class="preset-grid" :disabled="storing || saving" @change="usePresets">
            <el-checkbox v-for="p in presets" :key="p.id" :value="p.id" border class="preset-option"><div><b>{{ p.name }}</b><small>{{ p.groups.join('、') }}</small></div></el-checkbox>
          </el-checkbox-group>
          <div v-else-if="!loading && !loadError" class="empty-combinations">还没有保存组合 <el-button link type="primary" @click="tab = 'manage'; openEditor()">新建组合</el-button></div>
        </div>
        <div class="section-title"><span>目标分组 <small>已选 {{ groups.length }} 个</small></span><el-button link type="primary" :disabled="!groups.length || !writable" @click="openEditor(undefined, true)">存为新组合</el-button></div>
        <el-select v-model="groups" multiple filterable allow-create default-first-option :disabled="saving" placeholder="多选分组，或输入新名称后按 Enter" class="full-width" @change="modified = true"><el-option v-for="g in allOptions" :key="g" :value="g" :label="g" /></el-select>
        <p class="helper">{{ modified ? '已自行调整，原组合保持不变' : selected.length ? '已载入所选组合，可继续增删分组' : '可直接选择多个分组，或从组合库快速填充' }}</p>
        <div class="change-preview"><b>变更预览</b><div><span>新增</span><strong class="added">{{ added.join(' / ') || '无' }}</strong></div><div><span>移除</span><strong class="removed">{{ removed.join(' / ') || '无' }}</strong></div><div><span>保存后</span><strong>{{ groups.join(' / ') || '尚未选择' }}</strong></div></div>
        <el-checkbox v-model="confirmed" :disabled="saving || !changed || !groups.length" class="confirm-change">确认将渠道分组替换为上述目标分组</el-checkbox>
      </el-tab-pane>
      <el-tab-pane label="分组组合管理" name="manage">
        <div class="section-title"><span>已保存组合 <small>{{ presets.length }} 个</small></span><el-button type="primary" :disabled="!writable" @click="openEditor()">新建组合</el-button></div>
        <p class="helper">组合按站点共享保存；修改或删除组合不会自动修改已应用的渠道。</p>
        <el-alert v-if="loadError" :title="loadError" type="error" :closable="false"><el-button link type="primary" @click="load">重新加载</el-button></el-alert>
        <div v-loading="loading" class="saved-list"><div v-for="p in presets" :key="p.id" class="saved-row"><div class="saved-content"><b>{{ p.name }}</b><div class="group-tags"><el-tag v-for="g in p.groups" :key="g" size="small">{{ g }}</el-tag></div></div><div class="row-actions"><el-button v-if="!manageOnly" link type="primary" :disabled="storing" @click="applyPreset(p)">使用</el-button><el-button link type="primary" :disabled="!writable" @click="openEditor(p)">编辑</el-button><el-button link type="danger" :disabled="!writable" @click="removePreset(p)">删除</el-button></div></div><el-empty v-if="!presets.length && !loading && !loadError" description="暂无分组组合" :image-size="56" /></div>
      </el-tab-pane>
    </el-tabs>
    <el-form v-if="editorOpen" label-position="top" class="preset-form" @submit.prevent="savePreset"><b>{{ editorID ? '编辑组合' : '保存新组合' }}</b><el-form-item label="组合名称"><el-input v-model="editorName" maxlength="64" show-word-limit placeholder="例如：K3 主力客户" :disabled="storing" /></el-form-item><el-form-item label="包含分组"><el-select v-model="editorGroups" multiple filterable allow-create default-first-option class="full-width" placeholder="多选分组，或输入新名称后按 Enter" :disabled="storing"><el-option v-for="g in allOptions" :key="g" :value="g" :label="g" /></el-select></el-form-item><div class="editor-actions"><el-button type="primary" :loading="storing" :disabled="!writable" @click="savePreset">保存组合</el-button><el-button :disabled="storing" @click="editorOpen = false">取消</el-button></div></el-form>
    <div class="dialog-actions"><el-button :disabled="saving || storing" @click="emit('cancel')">关闭</el-button><template v-if="tab === 'adjust'"><el-button :disabled="saving" @click="restore">恢复当前</el-button><el-button type="primary" :loading="saving" :disabled="!confirmed || !changed || !groups.length || storing" @click="saveChannel">保存渠道分组</el-button></template></div>
  </div>
</template>

<style scoped>
.channel-group-editor{color:var(--ct-ink);font-size:13px}.section-title{display:flex;justify-content:space-between;align-items:center;gap:12px;margin:8px 0 12px;font-weight:500}.section-title small,.helper{color:var(--ct-ink-2);font-size:12px;font-weight:400}.helper{margin:8px 0 16px}.full-width{width:100%}.presets-area{min-height:40px;margin-bottom:20px}.preset-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.preset-option.el-checkbox{width:100%;height:auto;min-height:72px;margin:0;padding:12px;background:var(--ct-surface);border-color:var(--ct-line);border-radius:var(--ct-r-ctl);align-items:flex-start}.preset-option.is-checked{background:var(--ct-accent-weak);border-color:var(--ct-accent)}.preset-option :deep(.el-checkbox__input){margin-top:3px}.preset-option :deep(.el-checkbox__label){white-space:normal;overflow-wrap:anywhere;padding-left:8px}.preset-option b{font-size:13px;font-weight:500}.preset-option small{display:block;margin-top:4px;color:var(--ct-ink-2);font-size:12px}.empty-combinations{color:var(--ct-ink-2);padding:12px;background:var(--ct-surface-2);border-radius:var(--ct-r-ctl)}.change-preview{background:var(--ct-surface-2);border-radius:var(--ct-r-ctl);padding:16px;margin:16px 0}.change-preview>b{display:block;margin-bottom:12px;font-weight:500}.change-preview>div{display:grid;grid-template-columns:56px 1fr;gap:12px;margin:8px 0;overflow-wrap:anywhere}.change-preview span{color:var(--ct-ink-2)}.change-preview strong{font-weight:400}.added{color:var(--ct-ok)}.removed{color:var(--ct-ink-3);text-decoration:line-through}.confirm-change{height:auto;white-space:normal}.confirm-change :deep(.el-checkbox__label){white-space:normal}.saved-row{padding:16px 0;border-bottom:1px solid var(--ct-line);display:flex;align-items:center;justify-content:space-between;gap:16px}.saved-content{min-width:0}.saved-content>b{font-weight:500;overflow-wrap:anywhere}.group-tags{display:flex;flex-wrap:wrap;gap:6px;margin-top:8px}.group-tags :deep(.el-tag){max-width:100%;height:auto;white-space:normal;overflow-wrap:anywhere}.row-actions{display:flex;flex-shrink:0;gap:12px}.row-actions .el-button+.el-button{margin-left:0}.preset-form{padding:16px;background:var(--ct-surface-2);border-radius:var(--ct-r-ctl);margin-top:20px}.preset-form>b{display:block;margin-bottom:16px;font-weight:500}.dialog-actions{display:flex;justify-content:flex-end;flex-wrap:wrap;gap:8px;border-top:1px solid var(--ct-line);padding-top:16px;margin-top:20px}.dialog-actions .el-button+.el-button{margin-left:0}.editor-actions{display:flex;justify-content:flex-end}@media(max-width:600px){.preset-grid{grid-template-columns:1fr}.saved-row{align-items:flex-start;flex-direction:column}.row-actions{align-self:flex-end}}
</style>
