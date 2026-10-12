<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import type { ChannelLifeEvent, ProbeChannelStats, ProbeOverview, ProbeQuery, ProbeRecord } from '@ct/shared';
import { dashboard } from '../api';
import AppShell from '../components/AppShell.vue';
import TrendChart from '../components/TrendChart.vue';
import { useFiltersStore } from '../stores/filters';
import { formatTime } from '../utils/format';
import { lifeChanges, lifeTitle, outcomeTone, probeKinds, probeOutcomes, probeReasons, probeTrend, ratio, seconds } from '../utils/channelLifeline';
import { channelProbeFilters, channelProbeSorts, filterProbeChannels } from '../utils/channelLifeline';
import type { ChannelProbeFilter, ChannelProbeSort } from '../utils/channelLifeline';

const filters = useFiltersStore(), route = useRoute();
const overview = ref<ProbeOverview>(), loading = ref(false), error = ref('');
const hours = ref(1), customRange = ref<[Date, Date]>(), model = ref(''), kind = ref(''), search = ref('');
const applied = ref<ProbeQuery>(), selected = ref(Number(route.query.channel_id) || 0), channelPage = ref(1);
const tab = ref('life'), source = ref('important'), outcome = ref(''), autoRefresh = ref(false), refreshSeconds = ref(60);
const refreshing = ref(false), appliedFilterKey = ref('');
const channelFilter = ref<ChannelProbeFilter>('all'), channelSort = ref<ChannelProbeSort>('channel');
const channelPanel = ref<HTMLElement>(), detailPanel = ref<HTMLElement>();
const recordModel = ref(''), recordKind = ref('');
const events = ref<ChannelLifeEvent[]>([]), records = ref<ProbeRecord[]>([]), recordTotal = ref(0);
const detailLoading = ref(false), detailError = ref(''), cursors = ref(['']), nextCursor = ref('');
let summaryAbort: AbortController | undefined, detailAbort: AbortController | undefined;
let summaryGeneration = 0, detailGeneration = 0, timer: ReturnType<typeof setTimeout> | undefined, disposed = false;
const retentionDays = computed(() => overview.value?.retention_days || 7);
const channels = computed(() => overview.value?.channels || []);
const filteredChannels = computed(() => filterProbeChannels(channels.value,channelFilter.value,channelSort.value));
const visibleChannels = computed(() => filteredChannels.value.slice((channelPage.value - 1) * 20, channelPage.value * 20));
const recordModels = computed(() => [...new Set(channels.value.filter(c=>c.channel_id===selected.value).map(c=>c.model_name))].sort());
const selectedName = computed(() => channels.value.find(c => c.channel_id === selected.value)?.channel_name || `渠道 #${selected.value}`);
const points = computed(() => probeTrend(overview.value));
const countSeries = computed(() => ([['success','成功','#219c76'],['slow','慢请求','#c88726'],['errors','错误','#d65050'],['skipped','漏测','#8794ad']] as const).map(([key,name,color]) => ({name,color,smooth:false,data:points.value.map(p => [p.at,p.value?.[key] ?? null] as [string,number|null])})));
const latencySeries = computed(() => ([['mean_seconds','平均判定耗时','#5574df'],['mean_success_seconds','成功平均耗时','#219c76']] as const).map(([key,name,color]) => ({name,color,unit:'s',smooth:false,data:points.value.map(p => [p.at,p.value?.[key] ?? null] as [string,number|null])})));
const staleRecorder = computed(() => overview.value?.recorder && Date.parse(overview.value.queried_at) - Date.parse(overview.value.recorder.last_seen_at) > 60000);
const recordOutcomes = ['success','slow','error','skipped','unknown','canceled','pending','unconfirmed'];
const sourceLabels: Record<string,string> = {operation:'执行记录',decision:'调权决策',audit:'操作审计',probe:'探针'};
const filterKey = computed(() => JSON.stringify([filters.site_id,hours.value,hours.value===0?customRange.value:undefined,model.value,kind.value,search.value.trim()]));
const autoRefreshPause = computed(() => !applied.value ? '等待查询完成' : hours.value===0 ? '自定义时间范围' : filterKey.value!==appliedFilterKey.value ? '筛选条件尚未查询' : selected.value ? '正在查看渠道记录' : channelPage.value>1 ? '正在浏览渠道分页' : '');

function scheduleRefresh() {
  if(timer)clearTimeout(timer);
  timer=undefined;
  if(!disposed && autoRefresh.value)timer=setTimeout(() => { void automaticRefresh(); },refreshSeconds.value*1000);
}
async function automaticRefresh() {
  if(!autoRefresh.value || disposed)return;
  if(!document.hidden && !autoRefreshPause.value && !loading.value && !refreshing.value && !detailLoading.value)await refresh(true);
  else scheduleRefresh();
}

function cancelDetail() { detailAbort?.abort(); detailGeneration++; detailLoading.value = false; }
function resetDetail() { cancelDetail(); cursors.value=['']; nextCursor.value=''; events.value=[]; records.value=[]; detailError.value=''; }
async function loadDetail() {
  cancelDetail();
  if(!applied.value || !selected.value) return;
  const generation=detailGeneration, controller=new AbortController(); detailAbort=controller;
  detailLoading.value=true; detailError.value='';
  // Lifecycle operations have no probe model/kind. Keep them visible for the
  // selected channel even when the overview is filtered to one probe type.
  const q: ProbeQuery={...applied.value,channel_id:selected.value,q:undefined,cursor:cursors.value.at(-1),limit:50};
  const currentTab=tab.value;
  try {
    if(currentTab==='life') {
      const v=await dashboard.channelLifeline({...q,model:undefined,kind:undefined,source:source.value},controller.signal);
      if(generation!==detailGeneration)return;
      events.value=v.items; nextCursor.value=v.next_cursor || '';
    } else {
      const v=await dashboard.probeRecords({...q,model:recordModel.value,kind:recordKind.value,outcome:outcome.value},controller.signal);
      if(generation!==detailGeneration)return;
      records.value=v.items; recordTotal.value=v.total; nextCursor.value=v.next_cursor || '';
    }
  } catch {
    if(generation===detailGeneration) { detailError.value='记录读取失败，请重试。'; events.value=[]; records.value=[]; nextCursor.value=''; }
  } finally { if(generation===detailGeneration)detailLoading.value=false; }
}
async function refresh(background = false) {
  if(background && (!autoRefresh.value || autoRefreshPause.value || loading.value || refreshing.value || detailLoading.value))return;
  summaryAbort?.abort(); const generation=++summaryGeneration, controller=new AbortController(); summaryAbort=controller;
  const key=filterKey.value, sameFilters=!!applied.value && appliedFilterKey.value===key;
  if(!sameFilters) { resetDetail(); applied.value=undefined; appliedFilterKey.value=''; channelPage.value=1; recordModel.value=model.value.trim(); recordKind.value=kind.value; }
  if(!filters.site_id) { overview.value=undefined; loading.value=false; refreshing.value=false; return; }
  const now=new Date();
  const range=hours.value===0?customRange.value:[new Date(now.getTime()-hours.value*3600000),now];
  if(!range || range.length!==2 || !range[0] || !range[1] || range[0]>=range[1]) { error.value='请选择有效的开始和结束时间。'; loading.value=false; refreshing.value=false; scheduleRefresh(); return; }
  loading.value=!background; refreshing.value=background; error.value='';
  const q:ProbeQuery={site_id:filters.site_id,from:range[0].toISOString(),to:range[1].toISOString(),model:model.value,kind:kind.value,q:search.value.trim()};
  try {
    const v=await dashboard.probeOverview(q,controller.signal);
    if(generation!==summaryGeneration || q.site_id!==filters.site_id || (background && (!autoRefresh.value || autoRefreshPause.value)))return;
    overview.value=v; applied.value={...q,from:v.from,to:v.to}; appliedFilterKey.value=key;
    channelPage.value=Math.min(channelPage.value,Math.max(1,Math.ceil(filteredChannels.value.length/20)));
    if(!background)await loadDetail();
  } catch { if(generation===summaryGeneration && q.site_id===filters.site_id){error.value=sameFilters?'刷新失败，保留上次查询结果，请重试。':'统计读取失败，请重试。'; if(!sameFilters)overview.value=undefined;} }
  finally { if(generation===summaryGeneration){loading.value=false; refreshing.value=false; scheduleRefresh();} }
}
function selectChannel(row: ProbeChannelStats) { selected.value=row.channel_id; recordModel.value=row.model_name || applied.value?.model || ''; recordKind.value=applied.value?.kind || ''; resetDetail(); void loadDetail(); void nextTick(()=>detailPanel.value?.scrollIntoView({behavior:'smooth',block:'start'})); }
function openProbeRecords(row: ProbeChannelStats, result: string) {
  const watchedChange=tab.value!=='records' || outcome.value!==result;
  selected.value=row.channel_id; recordModel.value=row.model_name; recordKind.value=applied.value?.kind || '';
  resetDetail(); tab.value='records'; outcome.value=result;
  // The batched watcher loads once if tab/result changed; otherwise load the new row here.
  if(!watchedChange)void loadDetail();
  void nextTick(()=>detailPanel.value?.scrollIntoView({behavior:'smooth',block:'start'}));
}
function filterChannels(value: ChannelProbeFilter) { channelFilter.value=value; channelPage.value=1; void nextTick(()=>channelPanel.value?.scrollIntoView({behavior:'smooth',block:'start'})); }
function resetChannelFilters() { channelFilter.value='all'; channelSort.value='channel'; channelPage.value=1; }
function closeChannel() { selected.value=0; resetDetail(); scheduleRefresh(); }
function changeDetail() { resetDetail(); void loadDetail(); }
function pageDetail(next: boolean) { if(next && nextCursor.value)cursors.value.push(nextCursor.value); else if(!next && cursors.value.length>1)cursors.value.pop(); void loadDetail(); }
function futureDate(date: Date) { return date.getTime()>Date.now(); }
watch([tab,source,outcome],changeDetail);
watch([channelFilter,channelSort],()=>{channelPage.value=1;});
watch([autoRefresh,refreshSeconds],scheduleRefresh);
watch(() => filters.site_id,(_site,previous) => { overview.value=undefined; if(previous)selected.value=0; resetChannelFilters(); void refresh(); });
onMounted(async () => { try { await filters.loadInstances(); if(!applied.value && !loading.value)await refresh(); } catch { error.value='站点信息加载失败，请刷新页面。'; } });
onBeforeUnmount(() => { disposed=true; summaryGeneration++; summaryAbort?.abort(); cancelDetail(); if(timer)clearTimeout(timer); });
</script>

<template>
  <AppShell title="渠道生命线">
    <template #tools><el-button :loading="loading" @click="refresh()">刷新</el-button><router-link to="/tuning">调权设置</router-link></template>
    <div class="lifeline-page">
      <header class="intro"><div><h2>从每次探测，到每次渠道变更</h2><p>按时间查看 CT 保存的探针、调权、禁用、恢复与其他渠道操作。</p></div><div class="retention">保留最近 <strong>{{ retentionDays }}</strong> 天<br/><span>在调权中心「规则设置 → 运行规则 → 探针与恢复」配置</span></div></header>
      <section class="filter-bar" aria-label="统计筛选">
        <el-select v-model="hours" aria-label="时间范围" style="width:140px"><el-option label="最近 1 小时" :value="1"/><el-option label="最近 6 小时" :value="6"/><el-option label="最近 24 小时" :value="24"/><el-option :label="`保留期内 ${retentionDays} 天`" :value="retentionDays*24"/><el-option label="自定义" :value="0"/></el-select>
        <el-date-picker v-if="hours===0" v-model="customRange" type="datetimerange" :disabled-date="futureDate" start-placeholder="开始时间" end-placeholder="结束时间" style="max-width:360px"/>
        <el-input v-model="search" aria-label="渠道名称或 ID" placeholder="渠道名称 / ID" clearable style="width:180px" @keyup.enter="refresh()"/>
        <el-input v-model="model" aria-label="模型名称" placeholder="完整模型名称" clearable style="width:180px" @keyup.enter="refresh()"/>
        <el-select v-model="kind" aria-label="探针类型" clearable placeholder="全部探针类型" style="width:160px"><el-option v-for="(label,key) in probeKinds" :key="key" :label="label" :value="key"/></el-select>
        <el-button type="primary" :loading="loading" @click="refresh()">查询</el-button><el-checkbox v-model="autoRefresh">自动刷新概览</el-checkbox>
        <el-select v-if="autoRefresh" v-model="refreshSeconds" aria-label="自动刷新间隔" style="width:115px"><el-option label="每 30 秒" :value="30"/><el-option label="每 60 秒" :value="60"/><el-option label="每 120 秒" :value="120"/></el-select>
        <span class="muted">{{ autoRefresh ? (autoRefreshPause ? `已暂停：${autoRefreshPause}` : '查看渠道记录和翻页时暂停') : '手动刷新' }}</span>
      </section>
      <el-alert v-if="error" :title="error" type="error" :closable="false"/>
      <el-empty v-if="!filters.site_id && !error" description="请选择站点"/>
      <template v-if="overview">
        <p class="range">{{ formatTime(overview.from) }} — {{ formatTime(overview.to) }} · 最近查询 {{ formatTime(overview.queried_at) }}<span v-if="refreshing"> · 正在后台更新</span></p>
        <el-alert v-if="overview.recorder?.dropped_records" :title="`留痕写入曾丢弃 ${overview.recorder.dropped_records} 次（最近：${formatTime(overview.recorder.last_loss_at || '')}），统计可能不完整。`" type="warning" :closable="false"/>
        <p v-if="staleRecorder" class="notice">常态探针记录器超过 1 分钟未更新，可能已停止探测或记录写入受阻；空白区间不代表成功。</p>
        <section class="metrics" aria-label="探针统计">
          <article><span>应发 / 实发</span><strong>{{ overview.total.due }} / {{ overview.total.sent }}</strong></article>
          <article><span>成功率</span><strong>{{ ratio(overview.total.success_rate) }}</strong></article>
          <article><span>成功平均耗时</span><strong>{{ seconds(overview.total.mean_success_seconds) }}</strong></article>
          <article><span>慢请求 / 错误</span><strong><button class="metric-link" aria-label="筛选有慢请求的渠道" @click="filterChannels('slow')">{{ overview.total.slow }}</button> / <button class="metric-link" aria-label="筛选有渠道错误的渠道" @click="filterChannels('error')">{{ overview.total.errors }}</button></strong></article>
          <article><span>漏测</span><strong><button class="metric-link" aria-label="筛选有漏测的渠道" @click="filterChannels('skipped')">{{ overview.total.skipped }}</button></strong></article>
          <article><span>执行中 / 待确认</span><strong><button class="metric-link" aria-label="筛选有执行中探针的渠道" @click="filterChannels('pending')">{{ overview.total.pending }}</button> / <button class="metric-link" aria-label="筛选有待确认结果的渠道" @click="filterChannels('unconfirmed')">{{ overview.total.unconfirmed }}</button></strong></article>
        </section>
        <p class="help">成功率 = 成功 ÷（成功 + 慢请求 + 渠道错误）。漏测、控制异常、取消和未确认结果不计入分母。慢请求耗时记至阈值，非完整返回耗时；成功耗时为完整请求耗时。控制异常 {{ overview.total.unknown }} 次，取消 {{ overview.total.canceled }} 次。</p>
        <div class="charts"><TrendChart title="探针结果趋势" :series="countSeries"/><TrendChart title="探针耗时（秒）" :series="latencySeries"/></div>
        <section ref="channelPanel" class="panel" aria-label="渠道探测概览"><header><h3>渠道探测概览</h3><span>显示 {{ filteredChannels.length }} / {{ channels.length }} 个渠道 / 模型组合</span></header>
          <div class="channel-filters">
            <el-select v-model="channelFilter" aria-label="渠道探测状态" style="width:180px"><el-option v-for="(label,key) in channelProbeFilters" :key="key" :label="label" :value="key"/></el-select>
            <el-select v-model="channelSort" aria-label="渠道排序" style="width:180px"><el-option v-for="(label,key) in channelProbeSorts" :key="key" :label="label" :value="key"/></el-select>
            <el-button :disabled="channelFilter==='all' && channelSort==='channel'" @click="resetChannelFilters">重置列表</el-button>
            <span class="muted">点击异常次数查看对应探针</span>
          </div>
          <p class="help list-help">列表筛选和排序不改变上方统计及趋势；“有异常”包含错误、慢请求、漏测、待确认和控制异常。</p>
          <el-table :data="visibleChannels" highlight-current-row @row-click="selectChannel" :empty-text="channels.length ? '没有符合筛选条件的渠道' : '此范围内没有渠道记录'">
            <el-table-column label="渠道" min-width="180"><template #default="{row}"><button class="channel-link" @click.stop="selectChannel(row)">{{ row.channel_name || '未命名渠道' }} <small>#{{ row.channel_id }}</small></button><div class="muted">{{ row.model_name }}</div></template></el-table-column>
            <el-table-column label="应发 / 实发" min-width="110"><template #default="{row}">{{ row.due }} / {{ row.sent }}</template></el-table-column>
            <el-table-column label="成功率" min-width="100"><template #default="{row}">{{ ratio(row.success_rate) }}</template></el-table-column>
            <el-table-column label="平均 / 最慢判定" min-width="145"><template #default="{row}">{{ seconds(row.mean_seconds) }} / {{ seconds(row.max_seconds) }}</template></el-table-column>
            <el-table-column label="慢请求" width="80"><template #default="{row}"><button v-if="row.slow" class="count-link bad" :aria-label="`查看渠道 #${row.channel_id} ${row.model_name} 的慢请求探针`" @click.stop="openProbeRecords(row,'slow')">{{ row.slow }}</button><span v-else>0</span></template></el-table-column>
            <el-table-column label="错误" width="70"><template #default="{row}"><button v-if="row.errors" class="count-link bad" :aria-label="`查看渠道 #${row.channel_id} ${row.model_name} 的错误探针`" @click.stop="openProbeRecords(row,'error')">{{ row.errors }}</button><span v-else>0</span></template></el-table-column>
            <el-table-column label="漏测" width="85"><template #default="{row}"><el-tooltip :content="`全局并发 ${row.global_limited} / 渠道并发 ${row.channel_limited} / 等待结果容量 ${row.evidence_limited}`"><button v-if="row.skipped" class="count-link warn" :aria-label="`查看渠道 #${row.channel_id} ${row.model_name} 的漏测探针`" @click.stop="openProbeRecords(row,'skipped')">{{ row.skipped }}</button><span v-else>0</span></el-tooltip></template></el-table-column>
            <el-table-column label="未确认 / 控制异常" min-width="140"><template #default="{row}"><button v-if="row.unconfirmed" class="count-link warn" :aria-label="`查看渠道 #${row.channel_id} ${row.model_name} 的待确认探针`" @click.stop="openProbeRecords(row,'unconfirmed')">{{ row.unconfirmed }}</button><span v-else>0</span> / <button v-if="row.unknown" class="count-link warn" :aria-label="`查看渠道 #${row.channel_id} ${row.model_name} 的控制异常探针`" @click.stop="openProbeRecords(row,'unknown')">{{ row.unknown }}</button><span v-else>0</span></template></el-table-column>
          </el-table>
          <el-pagination v-if="filteredChannels.length>20" v-model:current-page="channelPage" :page-size="20" :total="filteredChannels.length" layout="prev, pager, next"/>
        </section>
        <section ref="detailPanel" class="panel detail" aria-label="渠道记录"><header><h3>{{ selected ? `${selectedName} · #${selected}` : '渠道记录' }}</h3><div class="detail-tools"><span>按请求发起 / 操作创建时间倒序</span><el-button v-if="selected" :loading="detailLoading" @click="loadDetail()">刷新记录</el-button><el-button v-if="selected" @click="closeChannel">关闭记录</el-button></div></header>
          <el-empty v-if="!selected" description="选择上方渠道，查看逐条探针与变更记录"/>
          <template v-else>
            <el-tabs v-model="tab"><el-tab-pane name="life" label="渠道生命线"/><el-tab-pane name="records" label="逐条探针"/></el-tabs>
            <div class="detail-tools">
              <el-select v-if="tab==='life'" v-model="source" aria-label="生命线事件类型" style="width:220px"><el-option label="操作、决策与异常探针" value="important"/><el-option label="全部事件" value="all"/><el-option label="渠道操作" value="operations"/><el-option label="未关联执行的决策" value="decisions"/><el-option label="全部探针" value="probes"/></el-select>
              <template v-else>
                <el-select v-model="outcome" clearable placeholder="全部结果" aria-label="探针结果" style="width:160px"><el-option v-for="key in recordOutcomes" :key="key" :value="key" :label="probeOutcomes[key]"/></el-select>
                <el-select v-model="recordKind" clearable placeholder="全部探针类型" aria-label="明细探针类型" style="width:180px" @change="changeDetail"><el-option v-for="(label,key) in probeKinds" :key="key" :label="label" :value="key"/></el-select>
                <el-select v-model="recordModel" filterable allow-create default-first-option clearable placeholder="全部模型 / 输入完整名称" aria-label="明细探针模型" style="width:240px" @change="changeDetail"><el-option v-for="name in recordModels" :key="name" :label="name" :value="name"/></el-select>
              </template>
              <span>{{ tab==='records' ? `共 ${recordTotal} 条` : '决策与执行分开标记，关联记录合并展示' }}</span>
            </div>
            <el-alert v-if="detailError" :title="detailError" type="error" :closable="false"/>
            <div v-loading="detailLoading" class="detail-body">
              <template v-if="tab==='life'">
                <ol v-if="events.length" class="timeline"><li v-for="event in events" :key="event.id"><div class="event-title"><time>{{ formatTime(event.at) }}</time><strong>{{ lifeTitle(event) }}</strong><span class="badge" :class="outcomeTone(event.outcome)">{{ probeOutcomes[event.outcome] || event.outcome }}</span><small>{{ sourceLabels[event.source] }}</small></div><div class="changes"><span v-for="change in lifeChanges(event)" :key="change">{{ change }}</span></div><div class="muted">{{ event.actor || '系统' }}<span v-if="event.finished_at"> · 完成 {{ formatTime(event.finished_at) }}</span></div><details><summary>关联标识</summary><code>{{ event.id }}</code><div v-if="event.command_id">命令：{{ event.command_id }}</div><div v-if="event.recommendation_id">决策：{{ event.recommendation_id }}</div></details></li></ol>
                <el-empty v-else-if="!detailError && !detailLoading" description="此范围内没有匹配的事件"/>
              </template>
              <el-table v-else :data="records" empty-text="此范围内没有匹配的探针">
                <el-table-column type="expand"><template #default="{row}"><div class="expanded">请求 ID：{{ row.id }}<br/>模型：{{ row.model_name }}<br/>判慢截止：{{ formatTime(row.deadline_at) }}<br/>判定完成：{{ row.finished_at ? formatTime(row.finished_at) : '—' }}</div></template></el-table-column>
                <el-table-column label="发起时间" min-width="180"><template #default="{row}">{{ formatTime(row.started_at) }}</template></el-table-column>
                <el-table-column label="类型" min-width="115"><template #default="{row}">{{ probeKinds[row.kind] || row.kind }}</template></el-table-column>
                <el-table-column label="结果" min-width="110"><template #default="{row}"><span class="badge" :class="outcomeTone(row.outcome)">{{ probeOutcomes[row.outcome] || row.outcome }}</span></template></el-table-column>
                <el-table-column label="耗时" width="110"><template #default="{row}">{{ row.outcome==='slow' ? '≥ ' : '' }}{{ seconds(row.duration_seconds) }}</template></el-table-column>
                <el-table-column label="说明" min-width="240"><template #default="{row}">{{ probeReasons[row.reason] || row.reason || '—' }}</template></el-table-column>
              </el-table>
            </div>
            <div class="pager"><el-button :disabled="cursors.length===1 || detailLoading" @click="pageDetail(false)">上一页</el-button><span>第 {{ cursors.length }} 页 · 每页 50 条</span><el-button :disabled="!nextCursor || detailLoading" @click="pageDetail(true)">下一页</el-button></div>
          </template>
        </section>
        <p class="help">逐条探针从本功能启用后开始留存，旧数据不会补造。CT 直连恢复探针有逐条明细，Agent 恢复任务展示已有轮次结果。页面仅展示保留期内的记录；原有共享操作审计仍按原模块规则保存。</p>
      </template>
    </div>
  </AppShell>
</template>

<style scoped>
.channel-filters{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.list-help{margin:10px 0!important}.metric-link,.count-link{border:0;background:none;padding:0;color:inherit;font:inherit;cursor:pointer;text-decoration:underline;text-decoration-style:dotted;text-underline-offset:4px}.count-link{font-weight:600;min-width:24px}.metric-link:hover,.count-link:hover{text-decoration-style:solid}.panel{scroll-margin-top:80px;min-width:0}.metric-link:focus-visible,.count-link:focus-visible{outline:2px solid var(--ct-accent);outline-offset:3px}
.lifeline-page{padding:20px;display:grid;gap:18px;color:var(--ct-ink)}.intro,.panel>header{display:flex;align-items:center;justify-content:space-between;gap:16px}.intro h2{font-size:20px;margin:0 0 8px}.intro p,.help,.range,.muted,.panel>header>span,.detail-tools>span{font-size:12px;line-height:1.7;color:var(--ct-ink-3)}.intro p,.help,.range{margin:0}.retention{text-align:right;line-height:1.7;white-space:nowrap;font-size:13px}.retention span{font-size:11px;color:var(--ct-ink-3)}.filter-bar,.detail-tools{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.metrics{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:12px}.metrics article,.panel{border:1px solid var(--ct-line);border-radius:12px;background:var(--ct-surface);padding:16px}.metrics span{display:block;font-size:12px;color:var(--ct-ink-3)}.metrics strong{display:block;font-size:22px;margin-top:12px;font-variant-numeric:tabular-nums}.charts{display:grid;grid-template-columns:1fr 1fr;gap:16px}.charts>*{min-width:0}.panel h3{margin:0 0 12px;font-size:15px}.channel-link{border:0;padding:0;background:none;color:var(--ct-accent,#5574df);cursor:pointer;text-align:left;font:inherit}.channel-link small{color:var(--ct-ink-3)}.detail-body{min-height:140px;margin-top:16px}.timeline{list-style:none;padding:0 0 0 18px;margin:0;border-left:2px solid var(--ct-line)}.timeline li{position:relative;padding:0 0 24px 12px}.timeline li:before{content:'';position:absolute;left:-24px;top:6px;width:10px;height:10px;border-radius:50%;background:#8794ad}.event-title{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.event-title time{font-size:12px;color:var(--ct-ink-3)}.event-title strong{font-size:14px}.event-title small{color:var(--ct-ink-3)}.badge{display:inline-block;font-size:12px;padding:2px 7px;border-radius:5px;background:var(--ct-bg);white-space:nowrap}.good{color:#219c76}.bad{color:#d65050}.warn,.notice{color:#b4771e}.changes{display:flex;gap:8px 20px;flex-wrap:wrap;margin:8px 0;font-size:13px;overflow-wrap:anywhere}.timeline details{font-size:11px;color:var(--ct-ink-3);margin-top:6px;overflow-wrap:anywhere}.timeline summary{cursor:pointer}.expanded{padding:12px 30px;font-size:12px;line-height:1.9}.pager{display:flex;align-items:center;justify-content:flex-end;gap:12px;margin-top:16px;font-size:12px}.notice{font-size:12px;margin:0}.panel :deep(.el-pagination){margin-top:14px;justify-content:flex-end}
@media(max-width:1100px){.metrics{grid-template-columns:repeat(3,minmax(0,1fr))}.charts{grid-template-columns:1fr}}@media(max-width:650px){.lifeline-page{padding:12px}.intro{align-items:flex-start;flex-direction:column}.retention{text-align:left}.metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.panel>header{align-items:flex-start;flex-direction:column;gap:0}.panel{padding:12px}.event-title{gap:6px}.pager{justify-content:center}}
</style>
