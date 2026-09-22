<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { dashboard } from '../api';
import { normalizeChannelGroups, splitChannelGroups } from '../utils/channelGroup';

type Preset = { id: string; name: string; groups: string[] };
const props = defineProps<{ site: string; current: string; options: string[]; saving: boolean; manageOnly?: boolean }>();
const emit = defineEmits<{ save: [groups: string[]]; cancel: [] }>();
const tab = ref('adjust'), groups = ref<string[]>([]), selected = ref<string[]>([]), modified = ref(false);
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
  groups.value = [...current.value]; selected.value = []; modified.value = false;
  editorOpen.value = false; tab.value = props.manageOnly ? 'manage' : 'adjust'; void load();
}, { immediate: true });
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
  if (props.saving || !changed.value || !groups.value.length) return;
  try { emit('save', splitChannelGroups(normalizeChannelGroups(groups.value))); }
  catch (error) { ElMessage.warning((error as Error).message); }
}
</script>

<template>
  <div class="channel-group-editor">
    <div class="editor-scroll">
    <el-tabs v-model="tab">
      <el-tab-pane v-if="!manageOnly" label="调整分组" name="adjust">
        <div class="section-title"><span>选择分组组合 <small>可多选，自动去重</small></span><el-button link type="primary" @click="tab = 'manage'">管理组合</el-button></div>
        <el-alert v-if="loadError" class="preset-load-error" :title="loadError" type="warning" :closable="false"><el-button link type="primary" :loading="loading" @click="load">重新加载组合</el-button></el-alert>
        <div v-if="!loadError" v-loading="loading" class="presets-area">
          <el-checkbox-group v-if="presets.length" v-model="selected" class="preset-grid" :disabled="storing || saving" @change="usePresets">
            <el-checkbox v-for="p in presets" :key="p.id" :value="p.id" border class="preset-option"><div><div class="preset-title"><b>{{ p.name }}</b><span class="group-count">{{ p.groups.length }} 组</span></div><small>{{ p.groups.join(' · ') }}</small></div></el-checkbox>
          </el-checkbox-group>
          <div v-else-if="!loading && !loadError" class="empty-combinations">还没有保存组合 <el-button link type="primary" @click="tab = 'manage'; openEditor()">新建组合</el-button></div>
        </div>
        <div class="section-title"><span>目标分组 <small>已选 {{ groups.length }} 个</small></span><el-button link type="primary" :disabled="!groups.length || !writable" @click="openEditor(undefined, true)">存为新组合</el-button></div>
        <el-select v-model="groups" multiple filterable default-first-option :disabled="saving" placeholder="选择分组（可多选）" class="full-width" @change="modified = true"><el-option v-for="g in allOptions" :key="g" :value="g" :label="g" /></el-select>
        <p class="helper">{{ modified ? '已自行调整，原组合保持不变' : selected.length ? '已载入所选组合，可继续增删分组' : '可直接选择多个分组，或从组合库快速填充' }}</p>
        <div class="change-preview"><div class="preview-heading"><b>变更预览</b><small>{{ changed ? '核对后保存到渠道' : '与当前分组一致' }}</small></div><div class="preview-line"><span>新增</span><div class="preview-tags"><span v-for="g in added" :key="g" class="diff-tag added">＋ {{ g }}</span><span v-if="!added.length" class="no-change">无</span></div></div><div class="preview-line"><span>移除</span><div class="preview-tags"><span v-for="g in removed" :key="g" class="diff-tag removed">− {{ g }}</span><span v-if="!removed.length" class="no-change">无</span></div></div><div class="preview-line preview-result"><span>保存后</span><strong>{{ groups.join(' / ') || '尚未选择' }}</strong></div></div>
      </el-tab-pane>
      <el-tab-pane label="分组组合管理" name="manage">
        <div class="section-title"><span>已保存组合 <small>{{ presets.length }} 个</small></span><el-button type="primary" :disabled="!writable" @click="openEditor()">新建组合</el-button></div>
        <p class="helper">组合按站点共享保存；修改或删除组合不会自动修改已应用的渠道。</p>
        <el-alert v-if="loadError" class="preset-load-error" :title="loadError" type="warning" :closable="false"><el-button link type="primary" @click="load">重新加载</el-button></el-alert>
        <div v-loading="loading" class="saved-list"><div v-for="p in presets" :key="p.id" class="saved-row"><div class="saved-content"><b>{{ p.name }}</b><div class="group-tags"><el-tag v-for="g in p.groups" :key="g" size="small">{{ g }}</el-tag></div></div><div class="row-actions"><el-button v-if="!manageOnly" link type="primary" :disabled="storing" @click="applyPreset(p)">使用</el-button><el-button link type="primary" :disabled="!writable" @click="openEditor(p)">编辑</el-button><el-button link type="danger" :disabled="!writable" @click="removePreset(p)">删除</el-button></div></div><el-empty v-if="!presets.length && !loading && !loadError" description="暂无分组组合" :image-size="56" /></div>
      </el-tab-pane>
    </el-tabs>
    <el-form v-if="editorOpen" label-position="top" class="preset-form" @submit.prevent="savePreset"><b>{{ editorID ? '编辑组合' : '保存新组合' }}</b><el-form-item label="组合名称"><el-input v-model="editorName" maxlength="64" show-word-limit placeholder="例如：K3 主力客户" :disabled="storing" /></el-form-item><el-form-item label="包含分组"><el-select v-model="editorGroups" multiple filterable default-first-option class="full-width" placeholder="选择分组（可多选）" :disabled="storing"><el-option v-for="g in allOptions" :key="g" :value="g" :label="g" /></el-select></el-form-item><div class="editor-actions"><el-button type="primary" :loading="storing" :disabled="!writable" @click="savePreset">保存组合</el-button><el-button :disabled="storing" @click="editorOpen = false">取消</el-button></div></el-form>
    </div>
    <div class="dialog-actions"><el-button :disabled="saving || storing" @click="emit('cancel')">关闭</el-button><template v-if="tab === 'adjust'"><el-button :disabled="saving" @click="restore">恢复当前</el-button><el-button type="primary" :loading="saving" :disabled="!changed || !groups.length || storing" @click="saveChannel">保存渠道分组</el-button></template></div>
  </div>
</template>

<style scoped>
.channel-group-editor{color:var(--ct-ink);font-size:13px;display:flex;flex-direction:column;min-height:0;overflow:hidden}
.editor-scroll{min-height:0;overflow:auto;padding-right:4px;overscroll-behavior:contain}
.preset-load-error{margin-bottom:14px;flex-shrink:0}
.channel-group-editor :deep(.el-tabs__header){margin:0 0 14px}
.channel-group-editor :deep(.el-tabs__nav-wrap::after){height:1px;background:var(--ct-line)}
.channel-group-editor :deep(.el-tabs__item){height:36px;font-size:13px;font-weight:500}
.channel-group-editor :deep(.el-tabs__active-bar){height:2px;border-radius:2px}
.section-title{display:flex;justify-content:space-between;align-items:center;gap:12px;margin:0 0 8px;font-size:13px;font-weight:600}
.section-title small{margin-left:8px;font-size:12px;font-weight:400;color:var(--ct-ink-3)}
.helper{font-size:12px;line-height:1.7;color:var(--ct-ink-2);margin:6px 0 14px}
.full-width{width:100%}
.full-width :deep(.el-select__wrapper){min-height:36px;padding:5px 10px;border-radius:8px;box-shadow:0 0 0 1px var(--ct-line) inset}
.full-width :deep(.el-select__wrapper.is-focused){box-shadow:0 0 0 1px var(--ct-accent) inset}
.full-width :deep(.el-tag){border:0;background:var(--ct-accent-weak);color:var(--ct-accent);border-radius:4px;min-height:24px}
.presets-area{min-height:40px;margin-bottom:16px;max-height:240px;overflow:auto;padding:1px}
.preset-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}
.preset-option.el-checkbox{width:100%;height:auto;min-height:66px;margin:0;padding:12px;background:var(--ct-surface);border-color:var(--ct-line);border-radius:8px;align-items:flex-start;transition:border-color .15s,background-color .15s}
.preset-option.el-checkbox:hover{border-color:var(--ct-line-strong);background:var(--ct-surface-2)}
.preset-option.el-checkbox.is-checked{background:var(--ct-accent-weak);border-color:var(--ct-accent)}
.preset-option :deep(.el-checkbox__input){margin-top:3px}
.preset-option :deep(.el-checkbox__label){white-space:normal;overflow-wrap:anywhere;padding-left:10px;min-width:0;flex:1}
.preset-title{display:flex;align-items:flex-start;justify-content:space-between;gap:8px}
.preset-title b{font-size:13px;font-weight:600;color:var(--ct-ink);line-height:1.5}
.group-count{font-size:11px;color:var(--ct-ink-3);white-space:nowrap;font-weight:400}
.preset-option small{display:block;margin-top:4px;color:var(--ct-ink-2);font-size:12px;line-height:1.65}
.empty-combinations{display:flex;justify-content:center;align-items:center;gap:12px;min-height:56px;color:var(--ct-ink-3);padding:20px;border:1px dashed var(--ct-line-strong);border-radius:8px;background:var(--ct-surface-2)}
.change-preview{border:1px solid var(--ct-line);border-radius:8px;padding:12px 14px;margin:0 0 10px;background:var(--ct-surface-2)}
.preview-heading{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:8px}
.preview-heading b{font-weight:600;font-size:12px}.preview-heading small{font-size:11px;color:var(--ct-ink-3)}
.preview-line{display:grid;grid-template-columns:48px 1fr;gap:12px;align-items:start;margin-top:6px;overflow-wrap:anywhere}
.preview-line>span{color:var(--ct-ink-2);font-size:12px;line-height:24px}
.preview-tags{display:flex;flex-wrap:wrap;gap:6px}.diff-tag{display:inline-flex;padding:2px 8px;border-radius:4px;font-size:12px;line-height:20px}
.added{color:var(--ct-ok);background:var(--ct-ok-weak)}.removed{color:var(--ct-ink-2);background:var(--ct-line);text-decoration:line-through}
.no-change{color:var(--ct-ink-3);font-size:12px;line-height:24px}.preview-result{border-top:1px solid var(--ct-line);padding-top:8px;margin-top:8px}.preview-result strong{font-weight:400;font-size:12px;line-height:24px}
.saved-list{border-top:1px solid var(--ct-line)}.saved-row{padding:12px 0;border-bottom:1px solid var(--ct-line);display:flex;align-items:center;justify-content:space-between;gap:20px}.saved-content{min-width:0}.saved-content>b{font-size:14px;font-weight:500;overflow-wrap:anywhere}
.group-tags{display:flex;flex-wrap:wrap;gap:6px;margin-top:10px}.group-tags :deep(.el-tag){max-width:100%;height:auto;min-height:22px;white-space:normal;overflow-wrap:anywhere;background:var(--ct-surface-2);border:1px solid var(--ct-line);color:var(--ct-ink-2);border-radius:4px}
.row-actions{display:flex;flex-shrink:0;gap:16px}.row-actions .el-button+.el-button{margin-left:0}
.preset-form{padding:20px;background:var(--ct-surface-2);border:1px solid var(--ct-line);border-radius:8px;margin-top:14px}.preset-form>b{display:block;margin-bottom:20px;font-size:14px;font-weight:500}.preset-form :deep(.el-form-item){margin-bottom:20px}.preset-form :deep(.el-form-item__label){font-size:12px;color:var(--ct-ink-2);margin-bottom:8px}.preset-form :deep(.el-input__wrapper){min-height:38px;border-radius:6px}
.dialog-actions{flex-shrink:0;z-index:2;background:var(--ct-surface);display:flex;justify-content:flex-end;flex-wrap:wrap;gap:10px;border-top:1px solid var(--ct-line);padding:16px 0 0;margin-top:16px}.dialog-actions .el-button+.el-button{margin-left:0}.dialog-actions .el-button{min-height:34px;padding:8px 16px;border-radius:6px}.dialog-actions .el-button--primary{min-width:120px}.editor-actions{display:flex;justify-content:flex-end}
@media(max-width:600px){.preset-grid{grid-template-columns:1fr}.saved-row{align-items:flex-start;flex-direction:column}.row-actions{align-self:flex-end}.section-title small{display:block;margin:4px 0 0}.change-preview{padding:14px}.preview-heading{align-items:flex-start}.preview-heading small{max-width:50%;text-align:right}}
@media(prefers-reduced-motion:reduce){.preset-option.el-checkbox{transition:none}}
</style>
<style>
.el-dialog.tuning-group-dialog{padding:0;border:1px solid var(--ct-line);border-radius:12px;margin:4vh auto;max-height:92vh;max-height:92dvh;display:flex;flex-direction:column;overflow:hidden;box-shadow:0 16px 64px rgb(16 24 40 / 16%)}
.tuning-group-dialog .el-dialog__header{padding:16px 24px;margin:0;border-bottom:1px solid var(--ct-line);flex-shrink:0}
.tuning-group-dialog .el-dialog__title{font-size:16px;font-weight:600;line-height:24px;color:var(--ct-ink)}
.tuning-group-dialog .el-dialog__headerbtn{top:12px;right:12px;width:40px;height:40px}
.tuning-group-dialog .el-dialog__body{padding:16px 24px;overflow:hidden;min-height:0;display:flex;flex-direction:column}
.tuning-group-dialog .el-dialog__footer{display:none}
.tuning-group-dialog .group-editor-context{flex-shrink:0;max-height:120px;overflow:auto}
@media(max-width:600px){.tuning-group-dialog .el-dialog__header{padding:18px 20px}.tuning-group-dialog .el-dialog__body{padding:20px}}
</style>
