<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { Search, Refresh, Plus, Edit, ArrowRight, Connection, InfoFilled } from '@element-plus/icons-vue';
import type { BillingDiscountRule, BillingReadonlyChannel, BillingUpstream, BillingUpstreamChannel } from '@ct/shared';
import { dashboard } from '../api';
import ChannelDiscountEditor from '../components/ChannelDiscountEditor.vue';
import AppShell from '../components/AppShell.vue';
import AsyncPanel from '../components/AsyncPanel.vue';
import { useAsyncData } from '../composables/useAsyncData';
import { useFiltersStore } from '../stores/filters';
import { formatBillingDiscount } from '../utils/billingDiscount';
import { normalizeUpstreamUrl } from '../utils/upstreamUrl';

const filters = useFiltersStore();
const search = ref(''), channelSearch = ref('');
let contextVersion = 0, editorVersion = 0;
const statusFilter = ref('all'), selected = ref<number | 'unassigned' | null>(null);
const configuredChannelIDs = ref<number[]>([]), originalChannelIDs = ref<number[]>([]), configSearch = ref('');
const drawerOpen = ref(false), saving = ref(false), syncing = ref(false), restoringChannel = ref(0);
const syncFeedback = ref(''), saveError = ref(''), revisionConflict = ref(false);
const form = reactive<BillingUpstream>({ id: 0, instance_id: '', name: '', url: '', urls: [], channel_prefixes: [], enabled: true, remark: '', channels: [] });
const state = useAsyncData(async (signal?: AbortSignal) => {
  const site = filters.site_id;
  if (!site) return { site, items: [] as BillingUpstream[], channels: [] as BillingReadonlyChannel[], discounts: [] as BillingDiscountRule[], discountError: '', source_available: false, sync_error: '', synced_at: '' };
  const [result, discounts] = await Promise.all([
    dashboard.billingUpstreams(site, signal),
    dashboard.billingDiscounts(site, 'upstream_channel').then(value => ({ items: value.items, error: '' }), () => ({ items: [] as BillingDiscountRule[], error: '折扣暂时无法读取，可重新刷新。' })),
  ]);
  // All site data passes through the same request-version check in useAsyncData.
  return { ...result, site, items: result.items || [], channels: result.channels || [], discounts: discounts.items, discountError: discounts.error };
});
const current = computed(() => state.data.value?.site === filters.site_id ? state.data.value : undefined);
const allItems = computed(() => current.value?.items || []);
const discountRules = computed(() => current.value?.discounts || []);
const sourceAvailable = computed(() => !!current.value && current.value.source_available !== false);
const channels = computed(() => [...new Map((current.value?.channels || []).map(c => [c.channel_id, c])).values()]);
const channelMap = computed(() => new Map(channels.value.map(c => [c.channel_id, c])));
const owners = computed(() => new Map(allItems.value.flatMap(u => u.channels.map(c => [c.channel_id, u] as const))));
const unassigned = computed(() => channels.value.filter(c => !c.source_missing && !owners.value.has(c.channel_id)));
const enabledCount = computed(() => allItems.value.filter(u => u.enabled).length);
const items = computed(() => {
  const q = search.value.trim().toLowerCase();
  return allItems.value.filter(u => (statusFilter.value === 'all' || u.enabled === (statusFilter.value === 'enabled')) &&
    (!q || `${u.name} ${(u.channel_prefixes || []).join(' ')} ${(u.urls || []).join(' ')} ${u.remark} ${u.channels.map(c => `${c.channel_id} ${c.channel_name}`).join(' ')}`.toLowerCase().includes(q)))
    .sort((a, b) => b.channels.length - a.channels.length);
});
const active = computed(() => allItems.value.find(u => u.id === selected.value));
const isUnassigned = computed(() => selected.value === 'unassigned');
type ChannelRow = BillingUpstreamChannel & Partial<BillingReadonlyChannel>;
const detailChannels = computed<ChannelRow[]>(() => isUnassigned.value ? unassigned.value : (active.value?.channels || []).map(c => ({ ...c, ...channelMap.value.get(c.channel_id) })));
const visibleChannels = computed(() => {
  const q = channelSearch.value.trim().toLowerCase();
  return detailChannels.value.filter(c => `${c.channel_id} ${c.channel_name} ${c.selected_models?.join(' ') || ''} ${c.models || ''} ${c.base_url || ''}`.toLowerCase().includes(q));
});
const cleanPrefix = (prefix: string) => prefix.trim().replace(/_+$/, '');
const formPrefixes = computed(() => [...new Set((form.channel_prefixes || []).map(cleanPrefix).filter(Boolean))]);
const prefixOwners = computed(() => new Map(allItems.value.flatMap(u => (u.channel_prefixes || []).map(prefix => [prefix, u] as const))));
const prefixSuggestions = computed(() => [...new Set(form.suggested_prefixes || [])].filter(prefix => !formPrefixes.value.includes(prefix)));
const prefixTransfers = computed(() => formPrefixes.value.flatMap(prefix => {
  const owner = prefixOwners.value.get(prefix);
  return owner && owner.id !== form.id ? [{ prefix, owner }] : [];
}));
const removedChannelIDs = computed(() => originalChannelIDs.value.filter(id => !configuredChannelIDs.value.includes(id)));
const addedChannelIDs = computed(() => configuredChannelIDs.value.filter(id => !originalChannelIDs.value.includes(id)));
const channelTransfers = computed(() => addedChannelIDs.value.flatMap(id => {
  const owner = owners.value.get(id), channel = channelMap.value.get(id);
  return owner && owner.id !== form.id && channel ? [{ channel, owner }] : [];
}));
const draftRules = computed(() => [
  ...allItems.value.filter(u => u.id !== form.id).flatMap(u => (u.channel_prefixes || []).filter(prefix => !formPrefixes.value.includes(prefix)).map(prefix => ({ prefix, owner: u.id }))),
  ...formPrefixes.value.map(prefix => ({ prefix, owner: form.id })),
].sort((a, b) => b.prefix.length - a.prefix.length));
function matchingPrefix(channel: BillingReadonlyChannel) {
  const rule = draftRules.value.find(rule => channel.channel_name === rule.prefix || channel.channel_name.startsWith(rule.prefix + '_'));
  return rule?.owner === form.id ? rule.prefix : '';
}
const prefixPreview = computed(() => {
  const matched = sourceAvailable.value ? channels.value.filter(c => !c.source_missing && matchingPrefix(c)) : [];
  return {
    matched,
    automatic: matched.filter(c => !owners.value.has(c.channel_id) && !c.auto_excluded && !configuredChannelIDs.value.includes(c.channel_id)),
    excluded: matched.filter(c => !owners.value.has(c.channel_id) && c.auto_excluded && !configuredChannelIDs.value.includes(c.channel_id)),
    occupied: matched.filter(c => { const owner = owners.value.get(c.channel_id); return owner && owner.id !== form.id && !configuredChannelIDs.value.includes(c.channel_id); }),
  };
});
const configurableChannels = computed<ChannelRow[]>(() => {
  const directory = new Map<number, ChannelRow>(channels.value.map(c => [c.channel_id, c]));
  for (const c of form.channels) directory.set(c.channel_id, { ...c, ...directory.get(c.channel_id) });
  const q = configSearch.value.trim().toLowerCase();
  return [...directory.values()].filter(c => !q || `${c.channel_id} ${c.channel_name} ${c.base_url || ''}`.toLowerCase().includes(q));
});
function occupiedByOther(id: number) { const owner = owners.value.get(id); return owner && owner.id !== form.id ? owner.name : ''; }
function associationLabel(channel: ChannelRow) {
  if (isUnassigned.value && channel.auto_excluded) return '已暂停自动匹配';
  if (isUnassigned.value) return '待匹配';
  return channel.association_source === 'manual' ? '手动配置' : channel.association_source === 'auto' ? '前缀匹配' : '历史关联';
}
function configurationLabel(channel: ChannelRow) {
  const owner = occupiedByOther(channel.channel_id);
  if (owner) return configuredChannelIDs.value.includes(channel.channel_id) ? `将从 ${owner} 转入` : `当前属于 ${owner} · 勾选可转入`;
  if (originalChannelIDs.value.includes(channel.channel_id)) return configuredChannelIDs.value.includes(channel.channel_id) ? '当前关联' : '将移出，并暂停自动匹配';
  if (configuredChannelIDs.value.includes(channel.channel_id)) return '将手动关联';
  if (channel.auto_excluded) return '已暂停自动匹配 · 可手动关联';
  const live = channelMap.value.get(channel.channel_id);
  return live && matchingPrefix(live) ? `保存后按 ${matchingPrefix(live)} 自动关联` : '可手动关联';
}
const urlRecords = computed(() => (form.urls || []).map(url => {
  const count = sourceAvailable.value ? form.channels.filter(c => normalizeUpstreamUrl(channelMap.value.get(c.channel_id)?.base_url) === normalizeUpstreamUrl(url)).length : 0;
  const shared = allItems.value.filter(u => u.id !== form.id && (u.urls || []).includes(url)).length;
  return { url, count, shared, label: !sourceAvailable.value ? '使用状态未知' : count ? `当前使用 · ${count} 个渠道` : '历史 / 手工记录' };
}));
const ready = computed(() => !!filters.site_id && !!current.value && !state.loading.value && !syncing.value && !state.error.value);
watch(items, list => {
  if (selected.value !== 'unassigned' && !list.some(u => u.id === selected.value)) selected.value = list[0]?.id ?? null;
});
watch(selected, () => { channelSearch.value = ''; });
watch(drawerOpen, () => { editorVersion++; }, { flush: 'sync' });
watch(() => filters.site_id, () => {
  contextVersion++; saving.value = false; syncing.value = false; restoringChannel.value = 0;
  state.cancel(); state.data.value = undefined;
  selected.value = null; drawerOpen.value = false; search.value = ''; channelSearch.value = ''; statusFilter.value = 'all'; syncFeedback.value = ''; saveError.value = '';
  if (filters.site_id) void synchronize([], true);
}, { immediate: true, flush: 'sync' });
void filters.loadInstances();
onBeforeUnmount(() => { contextVersion++; state.cancel(); });
async function synchronize(restoreIDs: number[] = [], initial = false) {
  if (!filters.site_id || syncing.value || saving.value) return;
  const site = filters.site_id, version = contextVersion;
  syncing.value = true; syncFeedback.value = ''; restoringChannel.value = restoreIDs[0] || 0;
  try {
    const result = await dashboard.syncBillingUpstreams(site, restoreIDs);
    if (version !== contextVersion) return;
    if (result.sync_error === 'billing_upstream_audit_failed') syncFeedback.value = '渠道同步已完成，操作记录写入失败。';
    else if (result.sync_error || result.source_available === false) syncFeedback.value = '渠道源暂不可用，自动同步未完成；已保存的上游信息仍可查看和编辑。';
    else if (!initial) ElMessage.success(restoreIDs.length ? '已恢复自动匹配并同步渠道' : '渠道已同步');
  } catch {
    if (version === contextVersion) syncFeedback.value = '渠道同步未完成，已保留现有配置。可稍后重试同步。';
  } finally {
    if (version === contextVersion) {
      await state.reload();
      if (version === contextVersion) { syncing.value = false; restoringChannel.value = 0; }
    }
  }
}
function openEditor(row?: BillingUpstream) {
  if (!ready.value || saving.value) return;
  editorVersion++;
  Object.assign(form, row ? { ...row, url: '', urls: [...(row.urls || [])], channel_prefixes: [...(row.channel_prefixes || [])], suggested_prefixes: [...(row.suggested_prefixes || [])], review_prefixes: [...(row.review_prefixes || [])], instance_id: filters.site_id, channels: row.channels.map(c => ({ ...c })) } : { id: 0, revision: undefined, instance_id: filters.site_id, name: '', url: '', urls: [], channel_prefixes: [], suggested_prefixes: [], review_prefixes: [], enabled: true, remark: '', channels: [] });
  originalChannelIDs.value = form.channels.map(c => c.channel_id);
  configuredChannelIDs.value = [...originalChannelIDs.value]; configSearch.value = ''; saveError.value = ''; revisionConflict.value = false;
  drawerOpen.value = true;
}
function addSuggestedPrefix(prefix: string) { form.channel_prefixes = [...formPrefixes.value, prefix]; }
async function reloadEditor() {
  const id = form.id, version = contextVersion, editor = editorVersion;
  await state.reload();
  if (version !== contextVersion || editor !== editorVersion || !drawerOpen.value) return;
  const row = allItems.value.find(item => item.id === id);
  if (row) openEditor(row);
}
const discountTime = (value: string) => new Date(value).toLocaleString('sv-SE', { timeZone: 'Asia/Shanghai', hour12: false }).slice(0, 16);
function currentDiscount(id: number) { const now = Date.now(); return discountRules.value.find(r => r.subject_id === selected.value && r.channel_id === id && new Date(r.effective_from).getTime() <= now && (!r.effective_to || new Date(r.effective_to).getTime() > now)); }
async function save() {
  if (saving.value || !ready.value || !drawerOpen.value || form.instance_id !== filters.site_id) return;
  if (!form.name.trim()) { ElMessage.warning('请输入上游名称'); return; }
  if (form.url?.trim() && !normalizeUpstreamUrl(form.url)) { ElMessage.warning('请输入有效的 HTTP(S) URL，不包含账号密码、查询参数或片段'); return; }
  if (formPrefixes.value.length > 50 || formPrefixes.value.some(prefix => [...prefix].length > 128)) { ElMessage.warning('最多配置 50 个渠道前缀，每个不超过 128 字'); return; }
  if (!sourceAvailable.value && (addedChannelIDs.value.length || removedChannelIDs.value.length)) { ElMessage.warning('渠道源不可用，请恢复连接后再调整渠道'); return; }
  if (addedChannelIDs.value.some(id => !channelMap.value.has(id) || channelMap.value.get(id)?.source_missing)) { ElMessage.warning('部分渠道已不在当前目录，请刷新后重试'); return; }
  const site = form.instance_id, version = contextVersion;
  const transferIDs = new Set(channelTransfers.value.map(item => item.channel.channel_id));
  const payload: BillingUpstream = {
    id: form.id, revision: form.revision, instance_id: site, name: form.name.trim(), channel_prefixes: [...formPrefixes.value], enabled: form.enabled, remark: form.remark.trim(), url: form.url?.trim() || '', channels: [],
    add_channel_ids: addedChannelIDs.value.filter(id => !transferIDs.has(id)), remove_channel_ids: [...removedChannelIDs.value],
    channel_transfers: channelTransfers.value.map(item => ({ channel_id: item.channel.channel_id, from_upstream_id: item.owner.id })),
    prefix_transfers: prefixTransfers.value.map(item => ({ prefix: item.prefix, from_upstream_id: item.owner.id })),
  };
  const reviewPrefixes = (form.review_prefixes || []).filter(prefix => formPrefixes.value.includes(prefix));
  const transferring = prefixTransfers.value.length > 0 || channelTransfers.value.length > 0;
  const changes = [
    ...reviewPrefixes.map(prefix => `确认启用历史推导前缀「${prefix}」：用于 ${payload.name} 的自动匹配`),
    ...prefixTransfers.value.map(item => `前缀「${item.prefix}」：${item.owner.name} → ${payload.name}`),
    ...channelTransfers.value.map(item => `渠道「${item.channel.channel_name}」#${item.channel.channel_id}：${item.owner.name} → ${payload.name}`),
  ];
  saving.value = true; saveError.value = ''; revisionConflict.value = false;
  try {
    if (changes.length) {
      const notes = (transferring ? '前缀转移只更改后续匹配规则，已有渠道仅按本次勾选转移。\n渠道原有折扣不迁移；按目标上游的规则结算，目标未设置折扣时按原价。\n' : '确认后，保留的历史前缀将用于新渠道自动匹配。已有渠道归属不变。\n') + '已生成账单保持不变；若要修正历史账单，请在上游账单中覆盖生成。';
      try { await ElMessageBox.confirm(changes.join('\n') + '\n\n' + notes, transferring ? '确认转移配置' : '确认前缀规则', { type: 'warning', confirmButtonText: transferring ? '确认转移并保存' : '确认规则并保存', cancelButtonText: '返回修改', customClass: 'upstream-transfer-confirm' }); }
      catch { return; }
      if (version !== contextVersion || !drawerOpen.value) return;
    }
    const saved = await dashboard.saveBillingUpstream(payload);
    if (version !== contextVersion) return;
    drawerOpen.value = false; search.value = ''; statusFilter.value = 'all';
    const auditFailed = saved.sync_error === 'billing_upstream_audit_failed';
    syncFeedback.value = auditFailed ? '配置已保存，操作记录写入失败。' : saved.sync_error ? '上游信息已保存，但自动同步未完成。请稍后点击“同步渠道”。' : '';
    await state.reload();
    if (version === contextVersion) { selected.value = saved.id || payload.id; ElMessage.success(auditFailed ? '配置已保存' : saved.sync_error ? '配置已保存，渠道待同步' : '上游已更新'); }
  } catch (error) {
    if (version === contextVersion) {
      const message = error instanceof Error ? error.message : '';
      const labels: Record<string, string> = {
        upstream_revision_conflict: '此上游已被其他操作更新，请重新载入后再保存。',
        upstream_transfer_conflict: '待转移的归属已变化，请重新载入信息后确认转移。',
        upstream_prefix_conflict: '前缀归属已变化，请重新载入信息后确认转移。',
        upstream_channel_conflict: '渠道归属已变化，请重新载入信息后确认转移。',
        invalid_channel_prefix: '渠道前缀格式无效，每个最多 128 字、最多 50 个。',
        invalid_channel_selection: '部分渠道已不在当前目录，请刷新后重试。',
        invalid_upstream_url: 'URL 格式无效。',
        readonly_source_unavailable: '渠道源不可用，请恢复连接后再调整渠道。',
        readonly_channels_query_failed: '渠道读取失败，请重试。',
        upstream_save_failed: '保存失败，请检查上游名称是否重复后重试。',
      };
      revisionConflict.value = message.includes('upstream_transfer_conflict') || message.includes('upstream_revision_conflict') || message.includes('upstream_prefix_conflict') || message.includes('upstream_channel_conflict');
      saveError.value = Object.entries(labels).find(([key]) => message.includes(key))?.[1] || message || '保存失败，请重试。';
      ElMessage.error(saveError.value);
    }
  } finally { if (version === contextVersion) saving.value = false; }
}
</script>

<template>
  <AppShell title="上游管理">
    <template #tools>
      <el-button :icon="Refresh" :loading="state.loading.value" :disabled="syncing || saving" @click="state.reload()">刷新</el-button>
      <el-button :loading="syncing" :disabled="saving || !filters.site_id || state.loading.value" @click="synchronize()">同步渠道</el-button>
      <el-button type="primary" :icon="Plus" :disabled="!ready || saving" @click="openEditor()">新建上游</el-button>
    </template>
    <div class="upstream-page">
      <el-alert v-if="syncFeedback || current?.sync_error || (current && !sourceAvailable)" type="warning" :closable="false" class="source-notice" :title="syncFeedback || (current?.sync_error === 'billing_upstream_audit_failed' ? '操作已完成，操作记录写入失败。' : '渠道源暂不可用，显示已保存的上游配置。可编辑基本信息，渠道状态和 URL 使用情况暂时未知。')"/>
      <el-alert v-if="current?.discountError" type="warning" :closable="false" class="source-notice" :title="current.discountError"/>
      <AsyncPanel :loading="state.loading.value || syncing" :error="state.error.value" :empty="!filters.site_id" empty-text="尚未选择站点" @retry="state.reload()">
        <div class="upstream-workspace">
          <aside class="upstream-nav" aria-label="上游列表">
            <div class="list-filters">
              <el-input v-model="search" :prefix-icon="Search" clearable placeholder="搜索上游、前缀或渠道" aria-label="搜索上游、前缀或渠道"/>
              <div class="status-filters"><button v-for="entry in [{key:'all',label:'全部',count:allItems.length},{key:'enabled',label:'自动出账',count:enabledCount},{key:'disabled',label:'已关闭',count:allItems.length-enabledCount}]" :key="entry.key" :class="{ selected: statusFilter === entry.key }" :aria-pressed="statusFilter === entry.key" @click="statusFilter = entry.key">{{ entry.label }} <span>{{ entry.count }}</span></button></div>
            </div>
            <div class="upstream-list">
              <button v-for="item in items" :key="item.id" class="upstream-item" :class="{ selected: selected === item.id }" :aria-pressed="selected === item.id" @click="selected = item.id">
                <span class="item-info"><b>{{ item.name }}</b><small>{{ item.channels.length }} 个渠道</small></span>
                <span class="status list-status" :class="{ enabled: item.enabled }" :title="item.enabled ? '自动出账已开启' : '自动出账已关闭'" :aria-label="item.enabled ? '自动出账已开启' : '自动出账已关闭'"><i/></span><el-icon><ArrowRight/></el-icon>
              </button>
              <div v-if="!items.length" class="list-empty">{{ allItems.length ? '没有匹配的上游' : '尚未配置上游' }}</div>
            </div>
            <button class="unassigned-entry" :class="{ selected: isUnassigned }" :aria-pressed="isUnassigned" @click="selected = 'unassigned'"><el-icon><Connection/></el-icon><span>未分配渠道</span><b>{{ sourceAvailable ? unassigned.length : '—' }}</b><el-icon><ArrowRight/></el-icon></button>
          </aside>
          <section class="upstream-detail">
            <template v-if="active || isUnassigned">
              <header class="detail-heading">
                <div class="detail-title">
                  <div><h3>{{ isUnassigned ? '未分配渠道' : active?.name }}</h3><span v-if="active" class="status" :class="{ enabled: active.enabled }"><i/>{{ active.enabled ? '自动出账已开启' : '自动出账已关闭' }}</span></div>
                  <p v-if="isUnassigned || active?.remark">{{ isUnassigned ? '渠道按前缀自动关联；手动移出的渠道可在这里恢复自动匹配，也可从上游信息中手动指定归属。' : active?.remark }}</p>
                </div>
                <div v-if="active" class="detail-actions"><el-button :icon="Edit" :disabled="!ready || saving" @click="openEditor(active)">上游信息</el-button></div>
              </header>
              <div class="channel-toolbar">
                <div class="channel-title"><h4>{{ isUnassigned ? '可关联渠道' : '关联渠道' }}</h4><span>{{ sourceAvailable || !isUnassigned ? detailChannels.length : '—' }}</span><el-tooltip content="前缀匹配来自自动同步；手动配置优先保留，历史关联表示旧数据未记录来源。" placement="top"><button class="help-button" aria-label="关联来源说明"><el-icon><InfoFilled/></el-icon></button></el-tooltip></div>
                <div class="channel-tools"><el-input v-model="channelSearch" :prefix-icon="Search" clearable placeholder="搜索渠道名称、ID 或模型" aria-label="搜索渠道名称、ID 或模型"/></div>
              </div>
              <el-table v-mobile-cards :data="visibleChannels" row-key="channel_id" max-height="max(300px, calc(100vh - 210px))" class="channel-table" :empty-text="channelSearch ? '没有匹配的渠道' : isUnassigned ? sourceAvailable ? '所有渠道均已分配' : '渠道源不可用，暂无法读取未分配渠道' : '尚未关联渠道'">
                <el-table-column label="渠道名称" min-width="190" show-overflow-tooltip><template #default="{row}"><b>{{ row.channel_name || `渠道 ${row.channel_id}` }}</b><small class="channel-sub">#{{ row.channel_id }}</small></template></el-table-column>
                <el-table-column label="关联来源" min-width="145"><template #default="{row}"><span>{{ associationLabel(row) }}</span><small v-if="row.matched_prefix" class="channel-sub">{{ row.matched_prefix }}</small></template></el-table-column>
                <el-table-column label="渠道 URL" min-width="220" show-overflow-tooltip><template #default="{row}">{{ row.base_url || (sourceAvailable ? '—' : '暂未知') }}</template></el-table-column>
                <el-table-column label="模型" min-width="180" show-overflow-tooltip><template #default="{row}">{{ row.selected_models?.join(', ') || row.models || '—' }}</template></el-table-column>
                <el-table-column v-if="active" label="折扣" width="100"><template #default="{row}"><span v-if="current?.discountError" class="muted">暂未知</span><ChannelDiscountEditor v-else :site="filters.site_id" :upstream="active.id" :channel="row.channel_id" :name="row.channel_name" :label="currentDiscount(row.channel_id) ? formatBillingDiscount(currentDiscount(row.channel_id)!.discount) : '原价'" @changed="state.reload()"/></template></el-table-column>
                <el-table-column v-if="active" label="折扣有效期" min-width="185"><template #default="{row}"><div v-if="currentDiscount(row.channel_id)" class="discount-period"><span>{{ discountTime(currentDiscount(row.channel_id)!.effective_from) }}</span><span class="muted">至 {{ currentDiscount(row.channel_id)!.effective_to ? discountTime(currentDiscount(row.channel_id)!.effective_to!) : '长期有效' }}</span></div><span v-else>—</span></template></el-table-column>
                <el-table-column label="渠道状态" width="120"><template #default="{row}"><span v-if="sourceAvailable && !row.source_missing && row.status !== undefined" class="status" :class="{enabled:row.status===1}"><i/>{{ row.status === 1 ? '启用' : '停用' }}</span><span v-else class="muted">{{ sourceAvailable ? '已不在目录' : '状态未知' }}</span></template></el-table-column>
                <el-table-column v-if="isUnassigned" label="操作" width="160"><template #default="{row}"><el-button v-if="row.auto_excluded" link type="primary" :loading="restoringChannel === row.channel_id" :disabled="!ready || saving || !sourceAvailable" @click="synchronize([row.channel_id])">恢复自动匹配</el-button><span v-else class="muted">等待同步</span></template></el-table-column>
              </el-table>
              <footer class="table-footer"><span>{{ !sourceAvailable && isUnassigned ? '渠道目录暂不可用' : channelSearch ? `显示 ${visibleChannels.length} / ${detailChannels.length} 个渠道` : `共 ${detailChannels.length} 个${isUnassigned ? '未分配' : '关联'}渠道` }}</span><span v-if="current?.synced_at">最近同步 {{ discountTime(current.synced_at) }}</span></footer>
            </template>
            <el-empty v-else :description="allItems.length ? '没有匹配的上游，请调整筛选' : '创建第一个上游，开始管理渠道归属'"><el-button v-if="!allItems.length" type="primary" :disabled="!ready" @click="openEditor()">新建上游</el-button></el-empty>
          </section>
        </div>
      </AsyncPanel>
    </div>
    <el-dialog v-model="drawerOpen" :title="form.id ? `上游信息 · ${form.name}` : '新建上游'" width="min(1160px, calc(100vw - 32px))" align-center class="upstream-dialog" :close-on-click-modal="!saving" :close-on-press-escape="!saving" :show-close="!saving" destroy-on-close>
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" class="source-notice"><el-button v-if="revisionConflict" link type="primary" :disabled="saving || state.loading.value" @click="reloadEditor">放弃更改并重新载入</el-button></el-alert>
      <el-form class="upstream-form info-layout" label-position="top" :disabled="saving || state.loading.value" @submit.prevent="save">
        <section class="info-basics">
          <el-form-item label="上游名称" required><el-input v-model="form.name" maxlength="128" placeholder="上游的显示名称"/><small v-if="form.id" class="muted">ID #{{ form.id }}</small></el-form-item>
          <el-form-item label="渠道前缀"><el-select v-model="form.channel_prefixes" multiple filterable allow-create default-first-option :reserve-keyword="false" placeholder="输入前缀后按回车，可添加多个" style="width:100%"><el-option v-for="prefix in form.channel_prefixes || []" :key="prefix" :label="prefix" :value="prefix"/></el-select></el-form-item>
          <p class="picker-hint">如 pindu 匹配 pindu 及 pindu_模型。区分大小写，多条命中时取最长前缀。</p>
          <p v-if="form.review_prefixes?.length" class="prefix-review">待确认前缀：{{ form.review_prefixes.join('、') }}。这些历史推导规则尚未自动关联新渠道，确认保存后启用；不属于此上游的请移除。</p>
          <div v-if="prefixSuggestions.length" class="prefix-suggestions"><small>根据已有渠道建议，点击添加</small><div><el-button v-for="prefix in prefixSuggestions" :key="prefix" size="small" plain @click="addSuggestedPrefix(prefix)">{{ prefix }}<span v-if="prefixOwners.get(prefix) && prefixOwners.get(prefix)!.id !== form.id"> · 转移规则</span><el-icon><Plus/></el-icon></el-button></div></div>
          <div v-if="prefixTransfers.length" class="prefix-review"><div v-for="item in prefixTransfers" :key="item.prefix">前缀 {{ item.prefix }} 将从 {{ item.owner.name }} 转入。</div><small>保存时确认；原有渠道只转移本次勾选项。</small></div>
          <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" maxlength="500" show-word-limit placeholder="用途或账户说明"/></el-form-item>
          <el-form-item label="自动出账"><el-switch v-model="form.enabled" active-text="开启" inactive-text="关闭"/></el-form-item>
          <div class="url-summary">
            <h4>关联 URL <small class="muted">{{ urlRecords.length }} 个</small></h4><p class="picker-hint">随渠道收集，历史地址保留；同一地址可由多个上游使用。</p>
            <ul v-if="urlRecords.length" class="info-url-list"><li v-for="record in urlRecords" :key="record.url"><span>{{ record.url }}</span><small>{{ record.label }}<template v-if="record.shared"> · 另 {{ record.shared }} 个上游有记录</template></small></li></ul><p v-else class="muted">暂无 URL</p>
            <details class="url-extra"><summary>手动补充 URL</summary><el-input v-model="form.url" maxlength="2048" placeholder="https://api.example.com" clearable/><p class="picker-hint">仅补充地址记录；自动关联依据渠道前缀。</p></details>
          </div>
        </section>
        <section class="info-channels">
          <div class="config-heading"><h4>渠道列表配置</h4><span class="muted">已选 {{ configuredChannelIDs.length }} 个</span></div>
          <p class="picker-hint">手动配置不受前缀限制；勾选其他上游的渠道可转入。移出渠道后会暂停其自动匹配。</p>
          <div class="prefix-preview" aria-label="前缀匹配预览">
            <template v-if="sourceAvailable"><div><b>保存后的自动匹配预览</b><span>{{ formPrefixes.length }} 个前缀 · 命中 {{ prefixPreview.matched.length }} 个渠道</span></div><p>新增自动关联 <strong>{{ prefixPreview.automatic.length }}</strong> 个<span v-if="prefixPreview.occupied.length"> · 已属其他上游 {{ prefixPreview.occupied.length }} 个</span><span v-if="prefixPreview.excluded.length"> · 已暂停 {{ prefixPreview.excluded.length }} 个</span></p><details v-if="prefixPreview.automatic.length"><summary>查看将自动关联的渠道</summary><ul><li v-for="channel in prefixPreview.automatic" :key="channel.channel_id">{{ channel.channel_name }} <small>#{{ channel.channel_id }} · {{ matchingPrefix(channel) }}</small></li></ul></details></template>
            <p v-else>渠道源不可用，暂无法预览匹配或调整渠道。基本信息仍可保存。</p>
          </div>
          <el-input v-model="configSearch" :prefix-icon="Search" clearable placeholder="搜索渠道名称、ID 或 URL" aria-label="搜索可配置渠道"/>
          <el-checkbox-group v-model="configuredChannelIDs" class="channel-config-list" :disabled="!sourceAvailable">
            <div v-for="channel in configurableChannels" :key="channel.channel_id" class="channel-config-row" :class="{ 'pending-transfer': occupiedByOther(channel.channel_id) && configuredChannelIDs.includes(channel.channel_id) }"><el-checkbox :value="channel.channel_id" :disabled="channel.source_missing && !originalChannelIDs.includes(channel.channel_id)"><span>{{ channel.channel_name || `渠道 ${channel.channel_id}` }} <small>#{{ channel.channel_id }}</small></span></el-checkbox><small>{{ configurationLabel(channel) }}</small><small>{{ channel.base_url || (sourceAvailable ? '无当前 URL' : '当前 URL 未知') }}</small></div>
            <div v-if="!configurableChannels.length" class="list-empty">{{ sourceAvailable ? '没有匹配的渠道' : '渠道目录暂不可用' }}</div>
          </el-checkbox-group>
          <div v-if="channelTransfers.length || removedChannelIDs.length" class="change-summary"><span v-if="channelTransfers.length">将从其他上游转入 {{ channelTransfers.length }} 个渠道。</span><span v-if="removedChannelIDs.length">将移出 {{ removedChannelIDs.length }} 个渠道，并暂停其自动匹配。</span></div>
          <p class="configuration-note">配置只影响后续生成的账单。已有账单若需修正，请在上游账单中选择“覆盖已有账单”。</p>
        </section>
      </el-form>
      <template #footer><el-button :disabled="saving" @click="drawerOpen = false">取消</el-button><el-button type="primary" :loading="saving" :disabled="!ready" @click="save">保存</el-button></template>
    </el-dialog>
  </AppShell>
</template>

<style scoped>
.info-layout{display:grid;grid-template-columns:minmax(270px, 0.8fr) minmax(0,1.4fr);gap:28px;height:min(620px,68vh);overflow:hidden}.info-basics{overflow-y:auto;padding-right:14px;min-width:0}.info-channels{display:flex;flex-direction:column;min-width:0;min-height:0;border-left:1px solid var(--ct-line);padding-left:24px}.config-heading{display:flex;justify-content:space-between;align-items:center;gap:12px}.config-heading h4{margin:0}.info-channels .channel-config-list{flex:1;min-height:0;max-height:none;margin-bottom:0}.url-summary{border-top:1px solid var(--ct-line);padding-top:16px}.url-summary h4{margin:0}.url-extra summary{cursor:pointer;font-size:12px;color:var(--ct-ink-3);padding:10px 0}.url-extra .el-input{margin-top:6px}
@media(max-width:760px){.info-layout{display:block;height:auto;max-height:70vh;overflow:auto}.info-basics{overflow:visible;padding:0}.info-channels{border-left:0;border-top:1px solid var(--ct-line);padding:20px 0 0;margin-top:20px}.info-channels .channel-config-list{flex:none;max-height:340px}}

.channel-config-list{max-height:320px;overflow:auto;margin:10px 0 20px;border:1px solid var(--ct-line);border-radius:5px}.channel-config-row{padding:8px 12px;border-bottom:1px solid var(--ct-line);display:flex;flex-direction:column;min-width:0}.channel-config-row:last-child{border-bottom:0}.channel-config-row>small{padding-left:24px;overflow-wrap:anywhere;color:var(--ct-ink-3)}.channel-config-row :deep(.el-checkbox){height:auto;min-height:28px;white-space:normal}.channel-config-row :deep(.el-checkbox__label){overflow-wrap:anywhere}


.info-url-list{list-style:none;padding:0;margin:8px 0;width:100%;display:grid;gap:6px}.info-url-list li{padding:9px 12px;border:1px solid var(--ct-line);border-radius:5px;background:var(--ct-surface);font-size:13px;overflow-wrap:anywhere;line-height:1.6}

.discount-period{display:grid;gap:3px;font-size:12px;font-variant-numeric:tabular-nums;white-space:nowrap}
.detail-actions{display:flex;align-items:center;gap:8px}
:global(body:has(.upstream-page)){min-width:0}
:global(.shell:has(.upstream-page) .workspace){min-width:0}
.upstream-page{max-width:1800px;margin:0 auto;color:var(--ct-ink)}

.success{color:var(--ct-ok)}
.upstream-workspace{display:grid;grid-template-columns:260px minmax(0,1fr);gap:12px;min-height:460px}.upstream-nav,.upstream-detail{border:1px solid var(--ct-line);border-radius:7px;background:var(--ct-surface);min-width:0}.upstream-nav{display:flex;flex-direction:column;overflow:hidden}.list-filters{padding:12px 12px 6px}.status-filters{display:flex;border:1px solid var(--ct-line);border-radius:5px;margin-top:10px;padding:3px;gap:2px}.status-filters button{flex:1;padding:7px 2px;white-space:nowrap;font-size:11px;border:0;border-radius:3px;background:none;color:var(--ct-ink-2);cursor:pointer}.status-filters button.selected{background:var(--ct-primary-solid);color:var(--ct-on-solid)}.status-filters span{margin-left:2px;font-variant-numeric:tabular-nums}
.upstream-list{flex:1;max-height:calc(100vh - 195px);min-height:230px;overflow-y:auto;padding:0 5px 10px}.upstream-item{display:flex;align-items:center;width:100%;gap:8px;text-align:left;padding:11px 10px;border:1px solid transparent;border-bottom-color:var(--ct-line);background:transparent;color:var(--ct-ink);border-radius:4px;cursor:pointer}.upstream-item:hover,.unassigned-entry:hover{background:var(--ct-surface-2)}.upstream-item.selected,.unassigned-entry.selected{background:var(--ct-accent-weak);border-color:var(--ct-accent)}.item-info{flex:1;min-width:0}.item-info b{display:block;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px;font-weight:600}.item-info small{display:block;color:var(--ct-ink-3);font-size:12px;margin-top:4px}.upstream-item>.el-icon{font-size:12px;color:var(--ct-ink-3)}.status{display:inline-flex;align-items:center;gap:6px;color:var(--ct-ink-3);font-size:12px;white-space:nowrap}.status i{width:7px;height:7px;border-radius:50%;background:currentColor;flex-shrink:0}.status.enabled{color:var(--ct-ok)}.list-status{padding:6px}.unassigned-entry{display:flex;align-items:center;gap:10px;padding:13px 14px;border:1px solid transparent;border-top-color:var(--ct-line);background:transparent;color:var(--ct-ink);text-align:left;cursor:pointer}.unassigned-entry span{flex:1}.unassigned-entry b{font-size:12px;background:var(--ct-surface-2);padding:3px 7px;border-radius:10px}.unassigned-entry>.el-icon:first-child{font-size:19px}
.upstream-detail{padding:16px;overflow:hidden}.detail-heading{display:flex;justify-content:space-between;gap:16px;padding-bottom:14px;border-bottom:1px solid var(--ct-line)}.detail-title{min-width:0}.detail-title>div{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.detail-title h3{font-size:18px;margin:0;overflow-wrap:anywhere}.detail-heading p{overflow-wrap:anywhere}.detail-actions{align-self:flex-start;flex-shrink:0}.channel-toolbar{display:flex;align-items:center;justify-content:space-between;gap:14px;padding:14px 0 10px;flex-wrap:wrap}.channel-title{display:flex;align-items:center;gap:10px}.channel-title h4{margin:0;font-size:15px}.channel-title>span{color:var(--ct-ink-3);font-size:13px}.help-button{display:flex;align-items:center;background:none;border:0;color:var(--ct-ink-3);padding:2px;cursor:help}.channel-tools{display:flex;gap:10px;align-items:center}.channel-tools>.el-input{width:260px}.channel-table{border:1px solid var(--ct-line);border-radius:5px}.channel-table :deep(td.el-table__cell){padding:10px 0}.channel-table :deep(th.el-table__cell){font-weight:500}.channel-table b{font-weight:550;font-size:13px}.table-footer{padding-top:12px;font-size:12px;color:var(--ct-ink-3)}.muted{color:var(--ct-ink-3)}.list-empty{padding:32px 12px;text-align:center;font-size:13px;color:var(--ct-ink-3)}
.upstream-form h4{font-size:15px}.picker-hint{font-size:12px;color:var(--ct-ink-3);line-height:20px;margin:9px 0 14px}button:focus-visible{outline:2px solid var(--ct-accent);outline-offset:2px}
@media(max-width:1200px){.upstream-workspace{grid-template-columns:250px minmax(0,1fr)}.upstream-detail{padding:14px}.channel-tools{width:100%}.channel-tools>.el-input{width:auto;flex:1}.upstream-item{gap:6px;padding:11px 9px}}
@media(max-width:850px){.upstream-workspace{grid-template-columns:1fr}.upstream-list{max-height:250px;min-height:0}.detail-heading{flex-wrap:wrap}.upstream-workspace{min-height:0}}
@media(max-width:520px){.channel-tools{flex-wrap:wrap}.channel-tools>.el-input{min-width:180px}}

.source-notice{margin-bottom:12px}.channel-sub{display:block;font-size:11px;color:var(--ct-ink-3);line-height:1.6}.table-footer{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}.detail-title p{font-size:12px;line-height:1.7;color:var(--ct-ink-3);margin-bottom:0}
.prefix-preview{padding:12px 14px;background:var(--ct-surface-2);border:1px solid var(--ct-line);border-radius:6px;margin-bottom:12px;font-size:12px;flex-shrink:0;max-height:170px;overflow:auto}.prefix-preview>div{display:flex;justify-content:space-between;gap:10px;flex-wrap:wrap}.prefix-preview b{font-weight:500}.prefix-preview p{margin:8px 0 0;color:var(--ct-ink-2)}.prefix-preview strong{color:var(--ct-accent);font-weight:600}.prefix-preview summary{cursor:pointer;margin-top:7px;color:var(--ct-accent)}.prefix-preview ul{padding-left:18px;margin:6px 0 0;line-height:1.8}.prefix-preview small{color:var(--ct-ink-3)}
.prefix-suggestions{margin:0 0 18px}.prefix-suggestions>small{color:var(--ct-ink-3);font-size:12px}.prefix-suggestions>div{display:flex;flex-wrap:wrap;gap:6px;margin-top:7px}.prefix-suggestions .el-button{margin:0}.prefix-suggestions .el-icon{margin-left:5px}.prefix-review{padding:9px 11px;border-radius:5px;background:var(--el-color-warning-light-9);color:var(--el-color-warning-dark-2);font-size:12px;line-height:1.8;margin:0 0 14px}.prefix-review small{font-size:11px}.pending-transfer{background:var(--el-color-warning-light-9)}.change-summary{font-size:12px;color:var(--el-color-warning-dark-2);line-height:1.7;margin-top:8px}.change-summary span{display:block}.configuration-note{font-size:12px;color:var(--ct-ink-3);line-height:1.7;margin:10px 0 0}.info-url-list li>small{display:block;color:var(--ct-ink-3);font-size:11px;margin-top:4px}:global(.upstream-transfer-confirm){width:580px;max-width:calc(100vw - 32px)}:global(.upstream-transfer-confirm .el-message-box__message p){white-space:pre-line;overflow-wrap:anywhere}
</style>
