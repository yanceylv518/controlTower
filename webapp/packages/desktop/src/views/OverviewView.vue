<script setup lang="ts">
import { computed, nextTick, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import type { OverviewRequests, OverviewRuntime, OverviewAlerts } from '@ct/shared';
import AppShell from '../components/AppShell.vue';
import { dashboard } from '../api';
import { useFiltersStore } from '../stores/filters';
import { useAuthStore } from '../stores/auth';
import { canVisit } from '../permissions';
import { useTheme } from '../composables/useTheme';
import { withChartTheme } from '../utils/chartTheme';
import { formatNumber, formatPercent, formatSeconds, formatBeijingTime, formatTokens } from '../utils/format';
import { overviewFresh, overviewLoader, overviewTrend, overviewWindow } from '../utils/overview';

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer]);
const filters = useFiltersStore();
const auth = useAuthStore();
const { themeSignature } = useTheme();
const requests = ref<OverviewRequests | null>(null);
const runtime = ref<OverviewRuntime | null>(null);
const alerts = ref<OverviewAlerts | null>(null);
type Section = 'requests' | 'runtime' | 'alerts';
const errors = reactive<Record<Section, string>>({ requests: '', runtime: '', alerts: '' });
const loading = reactive({ requests: false, runtime: false, alerts: false });
const auto = ref(true);
const hours = ref(1);
const details = ref(false);
const now = ref(Date.now());
const tpmChartEl = ref<HTMLDivElement>();
const successChartEl = ref<HTMLDivElement>();
let tpmChart: echarts.ECharts | undefined;
let successChart: echarts.ECharts | undefined;
const loader = overviewLoader();
let ready = false;
let disposed = false;
let timer: ReturnType<typeof setInterval> | undefined;
let resizeObserver: ResizeObserver | undefined;
const recent = computed(() => requests.value?.recent);
const metric = computed(() => recent.value?.metrics);
const staleRequests = computed(() => !!requests.value && !overviewFresh(requests.value.queried_at, now.value));
const requestNotice = computed(() => {
  if (errors.requests) return `${errors.requests}${requests.value ? '，保留上次数据。' : '。'}`;
  if (staleRequests.value) return '页面数据已过期，请刷新后查看。';
  if (!requests.value) return loading.requests ? '' : '尚未读取请求数据。';
  if (!requests.value.sources.length) return '尚未发现请求日志采集来源，无法判断请求量。';
  if (!recent.value?.complete) return metric.value ? '最近一分钟仍在汇总，仅展示已收到的请求和 Token；比率、耗时及趋势完整点待采集确认。' : '最近一分钟尚未确认采集完成，等待汇总。';
  return '';
});
const alertFresh = computed(() => !!alerts.value && !errors.alerts && alerts.value.fresh && overviewFresh(alerts.value.evaluated_at, now.value));
const can = (path: string) => canVisit(auth.user, path);
const requestLink = computed(() => ({ path: '/readonly-logs', query: { from: recent.value?.from, to: recent.value?.to } }));
const errorLink = computed(() => ({ path: '/readonly-logs', query: { ...requestLink.value.query, log_type: '5' } }));
const alertLink = computed(() => ({ path: '/alerts', query: { site: filters.site_id, active_only: 'true' } }));
function channelLink(key: string) { const id = key.match(/:channel:(\d+)$/)?.[1]; return id ? {path: '/channel-lifeline', query: {channel_id: id}} : alertLink.value; }
function fullTime(value?: string | null) { return value ? formatBeijingTime(value) : '尚未确认'; }
function healthText(row: OverviewRuntime['items'][number]) {
  if (!row.health?.length) return '未上报';
  if (row.health.some(x => !overviewFresh(x.checked_at, now.value, runtime.value?.stale_after_seconds))) return '检查已过期';
  if (row.health.some(x => x.status !== 'up')) return '检查异常';
  return `正常 · HTTP ${[...new Set(row.health.map(x => x.http_status_code))].join('/')}`;
}
function dockerText(row: OverviewRuntime['items'][number]) {
  if (!row.docker?.length) return '未上报 / 配置未确认';
  if (row.docker.some(x => !overviewFresh(x.collected_at, now.value, runtime.value?.stale_after_seconds))) return '记录已过期';
  return `${row.docker.filter(x => x.running).length} 运行 / ${row.docker.filter(x => !x.running).length} 停止`;
}
async function refresh(silent = false) {
  if (!ready || disposed || document.hidden || (silent && (!auto.value || loader.busy))) return;
  const site = filters.site_id;
  if (!site) return;
  const job = loader.start();
  now.value = Date.now();
  const valid = () => job.current() && !disposed && site === filters.site_id;
  async function read<T extends { site: string }>(section: Section, fetcher: () => Promise<T>, assign: (v: T) => void) {
    loading[section] = true;
    try {
      const value = await fetcher();
      if (!valid() || value.site !== site) return;
      assign(value);
      errors[section] = '';
      if (section === 'requests') { await nextTick(); if (valid()) renderCharts(); }
    } catch {
      if (valid()) errors[section] = '读取失败或超时，请重试';
    } finally { if (valid()) loading[section] = false; }
  }
  await Promise.allSettled([
    read('requests', () => dashboard.overviewRequests(site, hours.value, job.signal), v => { requests.value = v; }),
    read('runtime', () => dashboard.overviewRuntime(site, job.signal), v => { runtime.value = v; }),
    read('alerts', () => dashboard.overviewAlerts(site, job.signal), v => { alerts.value = v; }),
  ]);
  job.finish();
}
function renderCharts() {
  if (disposed) return;
  const points = requests.value?.points ?? [];
  const axis = points.map(p => overviewWindow(p.from, p.to));
  const values = points.map(overviewTrend);
  const render = (el: HTMLDivElement | undefined, chart: echarts.ECharts | undefined, success: boolean) => {
    if (!el) return chart;
    chart ??= echarts.init(el);
    chart.setOption(withChartTheme({
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => v == null ? '未确认' : success ? `${Number(v).toFixed(1)}%` : `${formatNumber(Number(v))} Token/分钟` },
      grid: { left: 56, right: 18, top: 16, bottom: 32 },
      xAxis: { type: 'category', data: axis, boundaryGap: false, axisLabel: { formatter: (v: string) => v.slice(0, 5) } },
      yAxis: { type: 'value', min: 0, ...(success ? { max: 100, axisLabel: { formatter: '{value}%' } } : { axisLabel: { formatter: (v: number) => formatTokens(v) } }) },
      series: [{ name: success ? '成功率（%）' : 'TPM', type: 'line', connectNulls: false, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: .06 }, data: values.map(v => success ? v.success : v.tpm) }],
    }), true);
    return chart;
  };
  tpmChart = render(tpmChartEl.value, tpmChart, false);
  successChart = render(successChartEl.value, successChart, true);
}
watch(() => filters.site_id, () => {
  loader.cancel(); requests.value = null; runtime.value = null; alerts.value = null;
  for (const key of ['requests', 'runtime', 'alerts'] as Section[]) { errors[key] = ''; loading[key] = false; }
  renderCharts(); void refresh();
});
watch(hours, () => { requests.value = null; renderCharts(); void refresh(); });
watch(themeSignature, () => renderCharts());
function visible() { now.value = Date.now(); if (!document.hidden && auto.value) void refresh(true); }
function resize() { tpmChart?.resize(); successChart?.resize(); }
onMounted(async () => {
  resizeObserver = new ResizeObserver(resize);
  if (tpmChartEl.value) resizeObserver.observe(tpmChartEl.value);
  if (successChartEl.value) resizeObserver.observe(successChartEl.value);
  document.addEventListener('visibilitychange', visible);
  timer = setInterval(() => { now.value = Date.now(); void refresh(true); }, 30000);
  try { await filters.loadInstances(); ready = true; await refresh(); }
  catch { errors.requests = '站点信息读取失败，请重新进入页面'; }
});
onBeforeUnmount(() => { disposed = true; loader.cancel(); clearInterval(timer); resizeObserver?.disconnect(); document.removeEventListener('visibilitychange', visible); tpmChart?.dispose(); successChart?.dispose(); });
</script>

<template>
  <AppShell title="总览">
    <div class="overview-page">
      <section aria-label="请求表现">
        <div class="section-heading compact-heading">
          <div class="heading-info"><h2>请求表现</h2><span class="muted">{{ recent ? overviewWindow(recent.from, recent.to) : '最近已结束的 1 分钟' }}</span><button v-if="requests" class="text-button" @click="details = true">{{ recent?.complete ? '已更新' : '汇总中' }} ⓘ</button></div>
          <div class="actions"><el-checkbox v-model="auto">每 30 秒刷新</el-checkbox><el-button size="small" :loading="loading.requests" @click="refresh()">刷新</el-button></div>
        </div>
        <el-alert v-if="requestNotice" :title="requestNotice" :type="errors.requests ? 'error' : 'warning'" :closable="false" class="notice" />
        <div class="metric-grid" :aria-busy="loading.requests">
          <article class="metric-card"><span>请求数 <small v-if="metric && !recent?.complete">· 已收到</small></span><strong>{{ formatNumber(metric?.request_count) }}</strong><div class="muted">成功 {{ formatNumber(metric?.success_count) }} · 错误 {{ formatNumber(metric?.error_count) }}</div><router-link v-if="can('/readonly-logs') && recent" :to="requestLink">查看使用日志 →</router-link></article>
          <article class="metric-card"><span>成功率</span><strong>{{ formatPercent(metric?.success_rate) }}</strong><div class="muted">错误率 {{ formatPercent(metric?.error_rate) }}</div><router-link v-if="can('/readonly-logs') && recent" :to="errorLink">查看错误日志 →</router-link></article>
          <article class="metric-card"><span>TPM <small v-if="metric && !recent?.complete">· 已收到</small></span><strong :title="formatNumber(metric?.tpm)">{{ formatTokens(metric?.tpm) }}</strong><div class="muted">输入 {{ formatTokens(metric?.prompt_tokens) }} · 输出 {{ formatTokens(metric?.completion_tokens) }}</div><button class="text-button" @click="details = true">统计口径 ⓘ</button></article>
          <article class="metric-card"><span>P95 耗时 <small>· 估算</small></span><strong>{{ metric?.p95_lower_bound && metric.p95_use_time != null ? '≥ ' : '' }}{{ formatSeconds(metric?.p95_use_time) }}</strong><div class="muted">平均 {{ formatSeconds(metric?.avg_use_time) }}</div><button class="text-button" @click="details = true">耗时口径 ⓘ</button></article>
        </div>
      </section>
      <section aria-label="请求趋势">
        <div class="section-heading"><div class="heading-info"><h2>请求趋势</h2><span class="muted">缺失或未完成汇总的区间留空 · {{ hours === 24 ? '每点 5 分钟' : '每点 1 分钟' }}</span></div><el-radio-group v-model="hours" size="small"><el-radio-button :value="1">1 小时</el-radio-button><el-radio-button :value="6">6 小时</el-radio-button><el-radio-button :value="24">24 小时</el-radio-button></el-radio-group></div>
        <div class="trend-grid"><article class="panel"><h3>TPM（Token / 分钟）</h3><div ref="tpmChartEl" class="chart" /></article><article class="panel"><h3>成功率</h3><div ref="successChartEl" class="chart" /></article></div>
      </section>
      <section class="panel" aria-label="需要关注">
        <div class="section-heading"><div class="heading-info"><h2>需要关注</h2><span class="muted">{{ loading.alerts ? '读取中…' : alertFresh ? `${alerts?.total} 项当前告警` : '当前告警尚未确认' }}</span></div><router-link v-if="can('/alerts')" :to="alertLink">告警中心 →</router-link></div>
        <el-alert v-if="errors.alerts" :title="errors.alerts" type="error" :closable="false" />
        <p v-else-if="alerts && !alertFresh" class="muted">最近评估 {{ fullTime(alerts.evaluated_at) }}，等待新的评估结果。</p>
        <div v-if="alertFresh" class="alert-list"><article v-for="item in alerts?.items" :key="item.id" class="alert-row"><div><b>{{ item.title }}</b><p>{{ item.summary }}</p><span class="muted">{{ item.instance_name || item.instance_id }}</span> <router-link v-if="item.dimension_type === 'instance_channel' && can('/channel-lifeline')" :to="channelLink(item.dimension_key)">查看渠道 →</router-link></div><div class="muted">最后确认 {{ fullTime(item.confirmed_at) }}<br>首次发生 {{ fullTime(item.first_seen_at) }}</div></article><p v-if="!alerts?.total" class="muted">本次评估未发现当前告警。</p><p v-if="(alerts?.total ?? 0) > 5" class="muted">展示优先级最高的 5 项，其他告警可在告警中心查看。</p></div>
        <p v-if="alerts?.unconfirmed" class="muted">另有 {{ alerts.unconfirmed }} 条历史未恢复记录尚未获得新的确认，不计入当前告警。</p>
      </section>
      <section class="panel" aria-label="服务实例">
        <div class="section-heading"><div class="heading-info"><h2>服务实例</h2><router-link v-if="can('/channel-lifeline')" to="/channel-lifeline">渠道生命线 →</router-link><span class="muted">服务检查结果不代表模型、渠道可用性</span></div><span class="muted">{{ loading.runtime ? '读取中…' : runtime ? `读取于 ${fullTime(runtime.queried_at)}` : '尚未读取' }}</span></div>
        <el-alert v-if="errors.runtime || (runtime && !overviewFresh(runtime.queried_at, now))" :title="errors.runtime || '实例数据已过期，请刷新。'" type="warning" :closable="false" />
        <div class="table-scroll"><table><thead><tr><th>实例</th><th>Agent 心跳</th><th>服务健康检查</th><th>容器</th><th>最后心跳（北京时间）</th></tr></thead><tbody><tr v-for="row in runtime?.items" :key="row.instance_id"><td><b>{{ row.name || row.instance_id }}</b><div class="muted">{{ row.instance_id }}</div></td><td>{{ !row.enabled ? '已停用' : overviewFresh(row.last_seen_at, now, runtime?.stale_after_seconds) ? '在线' : row.last_seen_at ? '心跳超时' : '尚未上报' }}</td><td>{{ healthText(row) }}</td><td>{{ dockerText(row) }}</td><td>{{ fullTime(row.last_seen_at) }}</td></tr><tr v-if="runtime && !runtime.items.length"><td colspan="5" class="muted">本站点暂无实例</td></tr></tbody></table></div>
      </section>
      <el-dialog v-model="details" title="数据时间与统计口径" width="min(620px, 92vw)">
        <p>请求统计来自本站点的 new-api 请求日志，不是 HTTP 接入层请求量。请求包含消费与错误日志，零输出消费记录也计入错误；“错误日志”入口仅筛选错误类型，使用日志还可查看零输出记录。共享日志库应配置单个采集来源，服务实例数量不会用作倍数。</p>
        <p>统计区间：{{ fullTime(recent?.from) }} 至 {{ fullTime(recent?.to) }}（不含结束时刻）。</p>
        <p>读取时间：{{ fullTime(requests?.queried_at) }}；采集确认：{{ fullTime(recent?.confirmed_at) }}。</p>
        <p>已发现 {{ requests?.sources.length ?? 0 }} 个采集来源，本分钟收到 {{ recent?.received_sources ?? 0 }} 个来源的数据或覆盖确认。只有覆盖确认齐全才显示完整统计；空记录不直接视为零请求。升级前没有覆盖证据的历史区间留空。</p>
        <p>TPM 为该完整分钟输入与输出 Token 合计。零请求时成功率、错误率和耗时显示“—”。平均耗时按有耗时数据的请求数加权；P95 合并所有来源的耗时分布估算，落入开放尾桶时显示下界（≥）。</p>
        <p>趋势使用同一站点、同一统计口径；TPM 为输入与输出 Token 总量除以区间分钟数。24 小时视图每点为 5 分钟平均 TPM，成功率按请求数计算。</p>
      </el-dialog>
    </div>
  </AppShell>
</template>

<style scoped>
.overview-page { display:grid; gap:20px; }
.section-heading,.heading-info,.actions { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }
.section-heading { justify-content:space-between; margin-bottom:12px; min-height:32px; }
.compact-heading { margin-bottom:10px; }
h2,h3,p { margin:0; } h2 { font-size:15px; } h3 { font-size:13px; margin-bottom:8px; }
.muted,small { color:var(--el-text-color-secondary); font-size:12px; } small { font-weight:normal; }
.metric-grid { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:12px; }
.metric-card,.panel { background:var(--el-bg-color); border:1px solid var(--el-border-color-lighter); border-radius:8px; padding:16px; min-width:0; }
.metric-card { display:flex; flex-direction:column; align-items:flex-start; gap:9px; font-size:13px; }
.metric-card strong { font-size:27px; line-height:1.25; font-variant-numeric:tabular-nums; }
.metric-card a,.text-button { font-size:12px; color:var(--el-color-primary); }
.text-button { border:0; padding:0; background:none; cursor:pointer; line-height:1.5; }
.trend-grid { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:12px; }
.panel { min-height:0; }
.chart { height:220px; }
.notice { margin-bottom:12px; }
.alert-row { display:flex; justify-content:space-between; gap:16px; padding:12px 0; border-bottom:1px solid var(--el-border-color-lighter); font-size:13px; }
.alert-row p { margin:5px 0; } .panel > p,.alert-list > p { margin-top:10px; }
.table-scroll { overflow:auto; } table { width:100%; border-collapse:collapse; white-space:nowrap; font-size:13px; text-align:left; }
th { font-size:12px; color:var(--el-text-color-secondary); font-weight:normal; } td,th { padding:12px 14px 12px 0; border-bottom:1px solid var(--el-border-color-lighter); }
:deep(.el-dialog__body p) { line-height:1.8; margin-bottom:12px; }
@media(max-width:700px) { .metric-grid { grid-template-columns:repeat(2,minmax(0,1fr)); } .trend-grid { grid-template-columns:1fr; } .metric-card strong { font-size:20px; } .heading-info { gap:6px; } .alert-row { flex-direction:column; } }
</style>
