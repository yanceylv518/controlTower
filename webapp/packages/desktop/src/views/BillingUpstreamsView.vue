<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { Search, Refresh, Plus, Edit, ArrowRight, Connection, InfoFilled } from '@element-plus/icons-vue';
import type { BillingReadonlyChannel, BillingUpstream } from '@ct/shared';
import { dashboard } from '../api';
import AppShell from '../components/AppShell.vue';
import AsyncPanel from '../components/AsyncPanel.vue';
import { useAsyncData } from '../composables/useAsyncData';
import { useFiltersStore } from '../stores/filters';

const filters = useFiltersStore();
const search = ref(''), channelSearch = ref(''), pickerSearch = ref('');
const statusFilter = ref('all'), selected = ref<number | 'unassigned' | null>(null);
const drawerOpen = ref(false), saving = ref(false), deleting = ref(false), selectedOnly = ref(false);
const form = reactive<BillingUpstream>({ id: 0, instance_id: '', name: '', enabled: true, remark: '', channels: [] });
const state = useAsyncData(async () => {
  const site = filters.site_id;
  if (!site) return { site, items: [] as BillingUpstream[], channels: [] as BillingReadonlyChannel[] };
  const result = await dashboard.billingUpstreams(site);
  return { site, items: result.items || [], channels: result.channels || [] };
});
const current = computed(() => state.data.value?.site === filters.site_id ? state.data.value : undefined);
const allItems = computed(() => current.value?.items || []);
const channels = computed(() => [...new Map((current.value?.channels || []).map(c => [c.channel_id, c])).values()]);
const channelMap = computed(() => new Map(channels.value.map(c => [c.channel_id, c])));
const owners = computed(() => new Map(allItems.value.flatMap(u => u.channels.map(c => [c.channel_id, u] as const))));
const unassigned = computed(() => channels.value.filter(c => !owners.value.has(c.channel_id)));
const enabledCount = computed(() => allItems.value.filter(u => u.enabled).length);
const items = computed(() => {
  const q = search.value.trim().toLowerCase();
  return allItems.value.filter(u => (statusFilter.value === 'all' || u.enabled === (statusFilter.value === 'enabled')) &&
    (!q || `${u.name} ${u.remark} ${u.channels.map(c => `${c.channel_id} ${c.channel_name}`).join(' ')}`.toLowerCase().includes(q)))
    .sort((a, b) => b.channels.length - a.channels.length);
});
const active = computed(() => allItems.value.find(u => u.id === selected.value));
const isUnassigned = computed(() => selected.value === 'unassigned');
const detailChannels = computed(() => isUnassigned.value ? unassigned.value : (active.value?.channels || []).map(c => ({ ...c, ...channelMap.value.get(c.channel_id) })));
const visibleChannels = computed(() => {
  const q = channelSearch.value.trim().toLowerCase();
  return detailChannels.value.filter(c => `${c.channel_id} ${c.channel_name} ${c.models || ''}`.toLowerCase().includes(q));
});
const selectedIds = computed(() => new Set(form.channels.map(c => c.channel_id)));
const pickerChannels = computed(() => {
  const known = new Map(channels.value.map(c => [c.channel_id, c]));
  const merged = [...channels.value, ...form.channels.filter(c => !known.has(c.channel_id)).map(c => ({ ...c, models: '', status: 0 }))];
  const q = pickerSearch.value.trim().toLowerCase();
  return merged.filter(c => (!selectedOnly.value || selectedIds.value.has(c.channel_id)) && `${c.channel_name} ${c.channel_id} ${c.models}`.toLowerCase().includes(q));
});
const ready = computed(() => !!filters.site_id && !!current.value && !state.loading.value && !state.error.value);
watch(items, list => {
  if (selected.value !== 'unassigned' && !list.some(u => u.id === selected.value)) selected.value = list[0]?.id ?? null;
});
watch(selected, () => { channelSearch.value = ''; });
watch(() => filters.site_id, () => {
  state.cancel(); state.data.value = undefined;
  selected.value = null; drawerOpen.value = false; search.value = ''; channelSearch.value = ''; statusFilter.value = 'all';
  void state.reload();
}, { immediate: true, flush: 'sync' });
void filters.loadInstances();
onBeforeUnmount(() => state.cancel());
function openEditor(row?: BillingUpstream) {
  if (!ready.value || saving.value) return;
  Object.assign(form, row ? { ...row, instance_id: filters.site_id, channels: row.channels.map(c => ({ ...c })) } : { id: 0, instance_id: filters.site_id, name: '', enabled: true, remark: '', channels: [] });
  pickerSearch.value = ''; selectedOnly.value = false; drawerOpen.value = true;
}
function occupiedBy(id: number) { const owner = owners.value.get(id); return owner && owner.id !== form.id ? owner.name : ''; }
function toggleChannel(c: BillingReadonlyChannel, checked: boolean) {
  if (occupiedBy(c.channel_id)) return;
  form.channels = checked ? [...form.channels.filter(v => v.channel_id !== c.channel_id), { channel_id: c.channel_id, channel_name: c.channel_name }] : form.channels.filter(v => v.channel_id !== c.channel_id);
}
async function save() {
  if (saving.value || !ready.value || form.instance_id !== filters.site_id) return;
  if (!form.name.trim()) { ElMessage.warning('请输入上游名称'); return; }
  const site = form.instance_id;
  const payload = { ...form, name: form.name.trim(), channels: form.channels.map(c => ({ ...c })) };
  saving.value = true;
  try {
    const saved = await dashboard.saveBillingUpstream(payload);
    if (filters.site_id !== site) return;
    drawerOpen.value = false; search.value = ''; statusFilter.value = 'all';
    await state.reload();
    if (filters.site_id === site) { selected.value = saved.id || payload.id; ElMessage.success(payload.id ? '上游已更新' : '上游已创建'); }
  } catch (error) { if (filters.site_id === site) ElMessage.error(error instanceof Error ? error.message : '保存失败，请重试'); }
  finally { saving.value = false; }
}
async function remove() {
  const row = active.value, site = filters.site_id;
  if (!row || !ready.value || deleting.value) return;
  try { await ElMessageBox.confirm(`删除上游“${row.name}”后，其渠道将变为未分配。是否继续？`, '删除上游', { type: 'warning' }); }
  catch { return; }
  if (filters.site_id !== site) return;
  deleting.value = true;
  try { await dashboard.deleteBillingUpstream(site, row.id); if (filters.site_id === site) { ElMessage.success('上游已删除'); await state.reload(); } }
  catch (error) { if (filters.site_id === site) ElMessage.error(error instanceof Error ? error.message : '删除失败，请重试'); }
  finally { deleting.value = false; }
}
</script>

<template>
  <AppShell title="上游管理">
    <template #tools><el-button :icon="Refresh" :loading="state.loading.value" @click="state.reload()">刷新</el-button><el-button type="primary" :icon="Plus" :disabled="!ready || saving" @click="openEditor()">新建上游</el-button></template>
    <div class="upstream-page">
      <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!filters.site_id" empty-text="尚未选择站点" @retry="state.reload()">
        <div class="upstream-workspace">
          <aside class="upstream-nav" aria-label="上游列表">
            <div class="list-filters"><el-input v-model="search" :prefix-icon="Search" clearable placeholder="搜索上游或渠道" aria-label="搜索上游或渠道"/><div class="status-filters"><button v-for="entry in [{key:'all',label:'全部',count:allItems.length},{key:'enabled',label:'已启用',count:enabledCount},{key:'disabled',label:'已停用',count:allItems.length-enabledCount}]" :key="entry.key" :class="{ selected: statusFilter === entry.key }" :aria-pressed="statusFilter === entry.key" @click="statusFilter = entry.key">{{ entry.label }} <span>{{ entry.count }}</span></button></div></div>
            <div class="upstream-list"><button v-for="item in items" :key="item.id" class="upstream-item" :class="{ selected: selected === item.id }" :aria-pressed="selected === item.id" @click="selected = item.id"><span class="item-info"><b>{{ item.name }}</b><small>{{ item.channels.length }} 个渠道</small></span><span class="status list-status" :class="{ enabled: item.enabled }" :title="item.enabled ? '已启用' : '已停用'" :aria-label="item.enabled ? '已启用' : '已停用'"><i/></span><el-icon><ArrowRight/></el-icon></button><div v-if="!items.length" class="list-empty">{{ allItems.length ? '没有匹配的上游' : '尚未配置上游' }}</div></div>
            <button class="unassigned-entry" :class="{ selected: isUnassigned }" :aria-pressed="isUnassigned" @click="selected = 'unassigned'"><el-icon><Connection/></el-icon><span>未分配渠道</span><b>{{ unassigned.length }}</b><el-icon><ArrowRight/></el-icon></button>
          </aside>
          <section class="upstream-detail">
            <template v-if="active || isUnassigned">
              <header class="detail-heading"><div class="detail-title"><div><h3>{{ isUnassigned ? '未分配渠道' : active?.name }}</h3><span v-if="active" class="status" :class="{ enabled: active.enabled }"><i/>{{ active.enabled ? '已启用' : '已停用' }}</span></div><p v-if="isUnassigned || active?.remark">{{ isUnassigned ? '尚未关联上游的渠道，可在上游编辑中添加关联。' : active?.remark }}</p></div><div v-if="active" class="detail-actions"><el-button :icon="Edit" :disabled="!ready || saving" @click="openEditor(active)">编辑上游</el-button><el-dropdown trigger="click" @command="remove"><el-button aria-label="更多上游操作" :disabled="!ready || deleting">···</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item command="delete">删除上游</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div></header>
              <div class="channel-toolbar"><div class="channel-title"><h4>{{ isUnassigned ? '可关联渠道' : '关联渠道' }}</h4><span>{{ detailChannels.length }}</span><el-tooltip content="渠道及模型信息来自当前站点；密钥仍由 NewAPI 管理。" placement="top"><button class="help-button" aria-label="渠道信息说明"><el-icon><InfoFilled/></el-icon></button></el-tooltip></div><div class="channel-tools"><el-input v-model="channelSearch" :prefix-icon="Search" clearable placeholder="搜索渠道名称、ID 或模型" aria-label="搜索渠道名称、ID 或模型"/><el-button v-if="active" :icon="Connection" :disabled="!ready || saving" @click="openEditor(active)">管理关联</el-button></div></div>
              <el-table v-mobile-cards :data="visibleChannels" row-key="channel_id" max-height="max(300px, calc(100vh - 210px))" class="channel-table" :empty-text="channelSearch ? '没有匹配的渠道' : isUnassigned ? '所有渠道均已分配' : '尚未关联渠道'"><el-table-column label="渠道名称" min-width="210" show-overflow-tooltip><template #default="{row}"><b>{{ row.channel_name || `渠道 ${row.channel_id}` }}</b></template></el-table-column><el-table-column label="渠道 ID" width="110"><template #default="{row}"><span class="muted">#{{ row.channel_id }}</span></template></el-table-column><el-table-column label="模型" min-width="220" show-overflow-tooltip><template #default="{row}">{{ row.models || '—' }}</template></el-table-column><el-table-column label="渠道状态" width="120"><template #default="{row}"><span v-if="row.status !== undefined" class="status" :class="{enabled:row.status===1}"><i/>{{ row.status === 1 ? '启用' : '停用' }}</span><span v-else class="muted">已不可用</span></template></el-table-column></el-table>
              <footer class="table-footer">{{ channelSearch ? `显示 ${visibleChannels.length} / ${detailChannels.length} 个渠道` : `共 ${detailChannels.length} 个${isUnassigned ? '未分配' : '关联'}渠道` }}</footer>
            </template>
            <el-empty v-else :description="allItems.length ? '没有匹配的上游，请调整筛选' : '创建第一个上游，开始管理渠道归属'"><el-button v-if="!allItems.length" type="primary" :disabled="!ready" @click="openEditor()">新建上游</el-button></el-empty>
          </section>
        </div>
      </AsyncPanel>
    </div>
    <el-drawer v-model="drawerOpen" :title="form.id ? '编辑上游' : '新建上游'" size="min(600px, 100vw)" :close-on-click-modal="!saving" :close-on-press-escape="!saving" :show-close="!saving" destroy-on-close>
      <el-form class="upstream-form" label-position="top" :disabled="saving" @submit.prevent="save"><h4>基本信息</h4><el-form-item label="上游名称" required><el-input v-model="form.name" maxlength="128" placeholder="例如：智谱官方"/></el-form-item><el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" show-word-limit placeholder="添加用途或账户说明"/></el-form-item><el-form-item label="上游状态"><el-switch v-model="form.enabled" active-text="启用" inactive-text="停用"/></el-form-item><div class="picker-heading"><h4>关联渠道</h4><span>已选 {{ form.channels.length }} 个</span></div><p class="picker-hint">每个渠道只能关联一个上游，已归属其他上游的渠道不可选。</p><el-input v-model="pickerSearch" :prefix-icon="Search" clearable placeholder="搜索渠道名称、ID 或模型"/><el-checkbox v-model="selectedOnly">仅看已选</el-checkbox><div class="channel-picker"><div v-for="channel in pickerChannels" :key="channel.channel_id" class="picker-row" :class="{ occupied: !!occupiedBy(channel.channel_id) }"><el-checkbox :model-value="selectedIds.has(channel.channel_id)" :disabled="!!occupiedBy(channel.channel_id) || saving" :aria-label="`关联 ${channel.channel_name || channel.channel_id}`" @change="toggleChannel(channel, !!$event)"/><span><b>{{ channel.channel_name || `渠道 ${channel.channel_id}` }} <small>#{{ channel.channel_id }}</small></b><small>{{ occupiedBy(channel.channel_id) ? `已属于 ${occupiedBy(channel.channel_id)}` : !channelMap.has(channel.channel_id) ? '渠道已不可用，可取消关联' : channel.models || '未配置模型' }}</small></span></div><div v-if="!pickerChannels.length" class="list-empty">没有匹配的渠道</div></div></el-form>
      <template #footer><el-button :disabled="saving" @click="drawerOpen = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!ready" @click="save">保存</el-button></template>
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.detail-actions{display:flex;align-items:center;gap:8px}
:global(body:has(.upstream-page)){min-width:0}
:global(.shell:has(.upstream-page) .workspace){min-width:0}
.upstream-page{max-width:1800px;margin:0 auto;color:var(--ct-ink)}

.success{color:var(--ct-ok)}
.upstream-workspace{display:grid;grid-template-columns:260px minmax(0,1fr);gap:12px;min-height:460px}.upstream-nav,.upstream-detail{border:1px solid var(--ct-line);border-radius:7px;background:var(--ct-surface);min-width:0}.upstream-nav{display:flex;flex-direction:column;overflow:hidden}.list-filters{padding:12px 12px 6px}.status-filters{display:flex;border:1px solid var(--ct-line);border-radius:5px;margin-top:10px;padding:3px;gap:2px}.status-filters button{flex:1;padding:7px 2px;white-space:nowrap;font-size:11px;border:0;border-radius:3px;background:none;color:var(--ct-ink-2);cursor:pointer}.status-filters button.selected{background:var(--ct-primary-solid);color:var(--ct-on-solid)}.status-filters span{margin-left:2px;font-variant-numeric:tabular-nums}
.upstream-list{flex:1;max-height:calc(100vh - 195px);min-height:230px;overflow-y:auto;padding:0 5px 10px}.upstream-item{display:flex;align-items:center;width:100%;gap:8px;text-align:left;padding:11px 10px;border:1px solid transparent;border-bottom-color:var(--ct-line);background:transparent;color:var(--ct-ink);border-radius:4px;cursor:pointer}.upstream-item:hover,.unassigned-entry:hover{background:var(--ct-surface-2)}.upstream-item.selected,.unassigned-entry.selected{background:var(--ct-accent-weak);border-color:var(--ct-accent)}.item-info{flex:1;min-width:0}.item-info b{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px;font-weight:600}.item-info small{display:block;color:var(--ct-ink-3);font-size:12px;margin-top:4px}.upstream-item>.el-icon{font-size:12px;color:var(--ct-ink-3)}.status{display:inline-flex;align-items:center;gap:6px;color:var(--ct-ink-3);font-size:12px;white-space:nowrap}.status i{width:7px;height:7px;border-radius:50%;background:currentColor;flex-shrink:0}.status.enabled{color:var(--ct-ok)}.list-status{padding:6px}.unassigned-entry{display:flex;align-items:center;gap:10px;padding:13px 14px;border:1px solid transparent;border-top-color:var(--ct-line);background:transparent;color:var(--ct-ink);text-align:left;cursor:pointer}.unassigned-entry span{flex:1}.unassigned-entry b{font-size:12px;background:var(--ct-surface-2);padding:3px 7px;border-radius:10px}.unassigned-entry>.el-icon:first-child{font-size:19px}
.upstream-detail{padding:16px;overflow:hidden}.detail-heading{display:flex;justify-content:space-between;gap:16px;padding-bottom:14px;border-bottom:1px solid var(--ct-line)}.detail-title{min-width:0}.detail-title>div{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.detail-title h3{font-size:18px;margin:0;overflow-wrap:anywhere}.detail-heading p{overflow-wrap:anywhere}.detail-actions{align-self:flex-start;flex-shrink:0}.channel-toolbar{display:flex;align-items:center;justify-content:space-between;gap:14px;padding:14px 0 10px;flex-wrap:wrap}.channel-title{display:flex;align-items:center;gap:10px}.channel-title h4{margin:0;font-size:15px}.channel-title>span{color:var(--ct-ink-3);font-size:13px}.help-button{display:flex;align-items:center;background:none;border:0;color:var(--ct-ink-3);padding:2px;cursor:help}.channel-tools{display:flex;gap:10px;align-items:center}.channel-tools>.el-input{width:260px}.channel-table{border:1px solid var(--ct-line);border-radius:5px}.channel-table :deep(td.el-table__cell){padding:10px 0}.channel-table :deep(th.el-table__cell){font-weight:500}.channel-table b{font-weight:550;font-size:13px}.table-footer{padding-top:12px;font-size:12px;color:var(--ct-ink-3)}.muted{color:var(--ct-ink-3)}.list-empty{padding:32px 12px;text-align:center;font-size:13px;color:var(--ct-ink-3)}
.upstream-form h4{margin:0 0 16px;font-size:15px}.picker-heading{display:flex;align-items:center;justify-content:space-between;border-top:1px solid var(--ct-line);padding-top:24px;margin-top:12px}.picker-heading h4{margin:0}.picker-heading span,.picker-hint{font-size:12px;color:var(--ct-ink-3)}.picker-hint{line-height:20px;margin:9px 0 14px}.channel-picker{border:1px solid var(--ct-line);border-radius:5px;max-height:380px;overflow:auto}.picker-row{display:flex;gap:10px;align-items:center;padding:12px;border-bottom:1px solid var(--ct-line)}.picker-row:last-child{border-bottom:0}.picker-row>span{min-width:0}.picker-row b{display:block;font-size:13px;font-weight:500;overflow-wrap:anywhere}.picker-row small{display:block;color:var(--ct-ink-3);font-size:12px;line-height:20px;overflow-wrap:anywhere}.picker-row b small{display:inline;margin-left:5px}.picker-row.occupied{background:var(--ct-surface-2)}button:focus-visible{outline:2px solid var(--ct-accent);outline-offset:2px}
@media(max-width:1200px){.upstream-workspace{grid-template-columns:250px minmax(0,1fr)}.upstream-detail{padding:14px}.channel-tools{width:100%}.channel-tools>.el-input{width:auto;flex:1}.upstream-item{gap:6px;padding:11px 9px}}
@media(max-width:850px){.upstream-workspace{grid-template-columns:1fr}.upstream-list{max-height:250px;min-height:0}.detail-heading{flex-wrap:wrap}.upstream-workspace{min-height:0}}
@media(max-width:520px){.channel-tools{flex-wrap:wrap}.channel-tools>.el-input{min-width:180px}}
</style>
