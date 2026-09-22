<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { siteOf, type ChannelSnapshot, type MetricItem } from "@ct/shared";
import { Refresh, Search } from "@element-plus/icons-vue";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import StatusTag from "../components/StatusTag.vue";
import ChannelOperations from "../components/ChannelOperations.vue";
import { formatTokens } from "../utils/format";
import { usePrefsStore } from "../stores/prefs";
import CustomerCompareChart from "../components/CustomerCompareChart.vue";
import CustomerTokenChart from "../components/CustomerTokenChart.vue";
import MiniSparkline from "../components/MiniSparkline.vue";
import MonitorCopyButton from "../components/MonitorCopyButton.vue";
import { closedMonitorBuckets } from "../utils/monitorBuckets";

const props = defineProps<{ kind: "channels" | "models" }>();
const filters = useFiltersStore();
const prefs = usePrefsStore();
void prefs.load();
const route = useRoute();
const router = useRouter();
const search = ref("");
const hours = ref(1);
const activeTab = ref<"charts" | "ranking">("charts");
const activeMetric = ref<"ttft" | "tpm" | "otps">("tpm");
const ttftThresholds = computed(() => [
  { name: "P50", value: prefs.ttftP50Threshold, color: "#2f6fed" },
  { name: "P90", value: prefs.ttftP90Threshold, color: "#16a6b6" },
  { name: "P95", value: prefs.ttftP95Threshold, color: "#7357d8" },
]);
const selectedKeys = ref<string[]>([]);
const history = ref<MetricItem[]>([]);
const asOf = ref(Date.now());
const chartTimeRange = computed<[number, number]>(() => {
  const end = Math.floor(asOf.value / 60_000) * 60_000;
  return [end - hours.value * 3_600_000, end];
});
const activeKinds = ref<string[]>([]);
const snapshots = ref<ChannelSnapshot[]>([]);
const dimensionType = computed(() =>
  props.kind === "channels" ? "instance_channel" : "instance_model",
);
let initialized = false;
const title = computed(
  () => ({ channels: "渠道监控", models: "模型监控" })[props.kind],
);

// 旧链接兼容：/channels?key=... 一律跳到详情子页
watch(
  () => route.query.key,
  (key) => {
    if (typeof key === "string" && key)
      void router.replace(`/${props.kind}/${encodeURIComponent(key)}`);
  },
  { immediate: true },
);

const state = useAsyncData(async () => {
  await filters.loadInstances();
  const instanceIDs = filters.instances
    .filter((item) => item.enabled && siteOf(item) === filters.site_id)
    .map((item) => item.instance_id);
  const window = hours.value === 24 ? "5m" : "1m";
  const requestedAt = Date.now();
  const prefix = (instanceID: string) => `${instanceID}:${props.kind === "channels" ? "channel" : "model"}:`;
  const metricResponses = await Promise.all(instanceIDs.flatMap((instanceID) => [
    dashboard.metricHistory({ instance_id: instanceID, window, dimension_type: dimensionType.value, dimension_key_prefix: prefix(instanceID), hours: hours.value, aggregate: true }),
    dashboard.metricHistory({ instance_id: instanceID, window, dimension_type: dimensionType.value, dimension_key_prefix: prefix(instanceID), hours: hours.value }),
  ]));
  const summaries: MetricItem[] = [];
  const points: MetricItem[] = [];
  metricResponses.forEach((response, index) => index % 2 === 0 ? summaries.push(...response.items) : points.push(...response.items));
  history.value = points;
  asOf.value = requestedAt;
  const channelData = await (
    props.kind === "channels"
      ? Promise.all(instanceIDs.map((instanceID) => dashboard.channelSnapshots({
          instance_id: instanceID,
          latest_only: true,
          limit: 500,
        }))).then((responses) => ({ items: responses.flatMap((response) => response.items) }))
      : Promise.resolve({ items: [] as ChannelSnapshot[] })
  );
  snapshots.value = channelData.items;
  initialized = true;
  return summaries.sort((a, b) => totalTokens(b) - totalTokens(a));
});
watch(
  () => [props.kind, filters.site_id, hours.value],
  ([kind], [previousKind]) => {
    if (kind !== previousKind) {
      // 渠道和模型共用组件，切换维度时不能沿用上一页的筛选与选中项。
      activeKinds.value = [];
      search.value = "";
      selectedKeys.value = [];
    }
    if (initialized) void state.reload();
  },
);
useAutoRefresh(state.reload);

type DimRow = MetricItem & { channelStatus?: string };
// 时间窗内无流量的渠道不产生指标行，靠快照兜底补出"无流量/已禁用"行，
// 否则被禁用的渠道会从页面上整体消失（B2 渠道清晰化的健康墙前提）。
function snapshotFallbackRow(s: ChannelSnapshot): DimRow {
  return {
    instance_id: s.instance_id,
    instance_name: s.instance_name,
    bucket_time: s.captured_at,
    dimension_type: "instance_channel",
    dimension_key: `${s.instance_id}:channel:${s.channel_id}`,
    display_key: String(s.channel_id),
    display_name: s.channel_name,
    request_count: 0,
    success_count: 0,
    error_count: 0,
    success_rate: null,
    error_rate: null,
    tpm: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    quota: 0,
    avg_use_time: null,
    p50_use_time: null,
    p95_use_time: null,
    p99_use_time: null,
    stream_rate: null,
    cache_token_rate: null,
    big_input_count: null,
    big_input_cache_hits: null,
    cache_hit_rate: null,
    ttft_count: null,
    ttft_avg_ms: null,
    ttft_p50_ms: null,
    ttft_p90_ms: null,
    ttft_p95_ms: null,
    otps: null,
    otps_sample_tokens: 0,
    channelStatus: s.status || "enabled",
  };
}
const rows = computed<DimRow[]>(() => {
  const metricRows = (state.data.value || []).map((item) => ({
    ...item,
    channelStatus:
      props.kind === "channels"
        ? snapshots.value.find(
            (s) =>
              s.channel_id === Number(item.dimension_key.split(":").pop()) &&
              s.instance_id === item.instance_id,
          )?.status || "enabled"
        : undefined,
  }));
  if (props.kind !== "channels") return metricRows;
  const present = new Set(metricRows.map((item) => item.dimension_key));
  const fallback = snapshots.value
    .filter((s) => !present.has(`${s.instance_id}:channel:${s.channel_id}`))
    .map(snapshotFallbackRow);
  return [...metricRows, ...fallback];
});
function totalTokens(item: MetricItem) {
  return item.prompt_tokens + item.completion_tokens;
}
function rowKind(item: DimRow): string {
  // Agent 旧快照使用 enabled，直连渠道同步使用 new-api 的数字状态 1。
  if (item.channelStatus && !["enabled", "1"].includes(String(item.channelStatus))) return "disabled";
  if (item.request_count === 0) return "idle";
  if ((item.error_rate || 0) >= 0.1) return "crit";
  if ((item.error_rate || 0) > 0) return "warn";
  return "ok";
}
const kindLabels: Record<string, string> = {
  crit: "异常",
  warn: "注意",
  ok: "正常",
  idle: "无流量",
  disabled: "已禁用",
};
const searched = computed(() =>
  rows.value.filter((item) =>
    `${item.display_name || ""} ${item.display_key} ${item.dimension_key}`
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
);
const counts = computed(() =>
  Object.fromEntries(
    Object.keys(kindLabels).map((key) => [
      key,
      searched.value.filter((item) => rowKind(item) === key).length,
    ]),
  ),
);
const visibleRows = computed(() =>
  searched.value
    .filter((item) =>
      activeKinds.value.length
        ? activeKinds.value.includes(rowKind(item))
        : !["idle", "disabled"].includes(rowKind(item)),
    )
    .sort((a, b) => totalTokens(b) - totalTokens(a)),
);
const grandTotal = computed(() => visibleRows.value.reduce((sum, item) => sum + totalTokens(item), 0));
const totalPrompt = computed(() => visibleRows.value.reduce((sum, item) => sum + item.prompt_tokens, 0));
const totalCompletion = computed(() => visibleRows.value.reduce((sum, item) => sum + item.completion_tokens, 0));
const totalRequests = computed(() => visibleRows.value.reduce((sum, item) => sum + item.request_count, 0));
const activeCount = computed(() => visibleRows.value.filter((item) => item.request_count > 0).length);
// 与图表阈值同源：P95 实测值对照配置的 P95 阈值。
const ttftP95ThresholdMs = computed(() => prefs.ttftP95Threshold * 1000);
const overThreshold = computed(() => visibleRows.value.filter((item) => (item.ttft_p95_ms || 0) >= ttftP95ThresholdMs.value).length);
const weightedOTPS = computed(() => {
  let tokens = 0;
  let duration = 0;
  visibleRows.value.forEach((item) => {
    if (item.otps != null && item.otps_sample_tokens > 0 && (item.otps_duration_seconds ?? 0) > 0) {
      tokens += item.otps_sample_tokens;
      duration += item.otps_duration_seconds!;
    }
  });
  return duration ? tokens / duration : null;
});
const topTen = computed(() => visibleRows.value.slice(0, 10).map((item) => ({
  name: item.display_name || item.display_key || item.dimension_key,
  prompt: item.prompt_tokens,
  completion: item.completion_tokens,
})));
watch(visibleRows, (items) => {
  const available = new Set(items.map((item) => item.dimension_key));
  const kept = selectedKeys.value.filter((key) => available.has(key));
  selectedKeys.value = kept.length ? kept : items.slice(0, 8).map((item) => item.dimension_key);
}, { immediate: true });
const bucketMinutes = computed(() => hours.value === 24 ? 5 : 1);
// 按 dimension_key 预分组并排好时间序，避免表格每行渲染都全量扫描 history。
const historyByKey = computed(() => {
  const map = new Map<string, MetricItem[]>();
  closedMonitorBuckets(history.value, bucketMinutes.value, asOf.value).forEach((item) => {
    const list = map.get(item.dimension_key);
    if (list) list.push(item);
    else map.set(item.dimension_key, [item]);
  });
  map.forEach((list) => list.sort((a, b) => Date.parse(a.bucket_time) - Date.parse(b.bucket_time)));
  return map;
});
function dimensionSeries(key: string, field: "ttft_p50_ms" | "ttft_p90_ms" | "ttft_p95_ms" | "tpm" | "otps", scale = 1, name?: string) {
  return [{
    name: name || field,
    data: (historyByKey.value.get(key) || [])
      .map((item) => [item.bucket_time, item[field] == null ? null : Number(item[field]) / scale] as [string, number | null]),
  }];
}
function peakDimTPM(key: string) {
  return Math.max(...(historyByKey.value.get(key) || []).map((item) => item.tpm / bucketMinutes.value), 0);
}
function metricHeadline(key: string, row?: DimRow) {
  if (activeMetric.value === "ttft") return { label: "时段 P95", value: msFmt(row?.ttft_p95_ms), detail: "所选时段首字响应 P95" };
  if (activeMetric.value === "otps") return { label: "时段 OTPS", value: row?.otps == null ? "—" : `${row.otps.toFixed(2)} token/s`, detail: "所选时段有效输出 Token ÷ 总耗时（含非流式）" };
  const point = [...(historyByKey.value.get(key) || [])].reverse().find(item => Date.parse(item.bucket_time) + bucketMinutes.value * 60_000 <= asOf.value);
  const time = point ? Date.parse(point.bucket_time) : 0;
  const stale = point && asOf.value - time - bucketMinutes.value * 60_000 > 120_000;
  const range = point ? `${new Date(time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}–${new Date(time + bucketMinutes.value * 60_000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "等待采集";
  return { label: bucketMinutes.value === 5 ? "TPM · 5分钟均值" : "TPM", value: point ? formatTokens(point.tpm / bucketMinutes.value) : "—", detail: `${range}${stale ? " · 数据滞后" : ""}` };
}
// TPM 展示当前筛选下的全部项；TTFT、OTPS 沿用最多 8 项的图表选择。
const chartKeys = computed(() => activeMetric.value === "tpm"
  ? visibleRows.value.map((item) => item.dimension_key)
  : selectedKeys.value);
const chartTrendGroups = computed(() => chartKeys.value.map((key) => {
  const row = rows.value.find((item) => item.dimension_key === key);
  return {
    key,
    name: row?.display_name || row?.display_key || key,
    id: key.split(":").pop() || key,
    row,
    headline: metricHeadline(key, row),
    tpm: dimensionSeries(key, "tpm", bucketMinutes.value, "TPM"),
    otps: dimensionSeries(key, "otps", 1, "OTPS"),
    ttft: [
      ...dimensionSeries(key, "ttft_p50_ms", 1000, "P50"),
      ...dimensionSeries(key, "ttft_p90_ms", 1000, "P90"),
      ...dimensionSeries(key, "ttft_p95_ms", 1000, "P95"),
    ],
  };
}));
function toggleChart(key: string, checked: boolean) {
  if (checked && selectedKeys.value.length < 8) selectedKeys.value = [...selectedKeys.value, key];
  if (!checked) selectedKeys.value = selectedKeys.value.filter((item) => item !== key);
}
function historyPoints(key: string, field: "ttft_p95_ms" | "otps" | "prompt_tokens") {
  return (historyByKey.value.get(key) || []).map((item) => item[field]);
}
function toggleKind(key: string) {
  activeKinds.value = activeKinds.value.includes(key)
    ? activeKinds.value.filter((x) => x !== key)
    : [...activeKinds.value, key];
}
const opChannel = ref<DimRow | null>(null);
function openOps(row: DimRow) {
  opChannel.value = row;
}
function openDetail(row: DimRow) {
  void router.push(`/${props.kind}/${encodeURIComponent(row.dimension_key)}`);
}
const pct = (v: number | null | undefined) =>
  v == null ? "—" : `${(v * 100).toFixed(1)}%`;
const secondsFmt = (v: number | null | undefined) =>
  v == null ? "—" : `${v.toFixed(2)}s`;
const msFmt = (v: number | null | undefined) =>
  v == null ? "—" : `${(v / 1000).toFixed(2)}s`;
function rowClass({ row }: { row: DimRow }) {
  return rowKind(row) === "crit" ? "row-crit" : "";
}
</script>
<template>
  <AppShell :title="title">
    <template #tools>
      <el-segmented v-model="hours" :options="[{ label: '1小时', value: 1 }, { label: '6小时', value: 6 }, { label: '24小时', value: 24 }]" size="small" />
      <el-input
        v-model="search"
        :prefix-icon="Search"
        placeholder="搜索名称或 ID"
        clearable
        size="small"
        style="width: 170px"
      />
      <span class="status-chips">
        <span
          v-for="(label, key) in kindLabels"
          v-show="kind === 'channels' || key !== 'disabled'"
          :key="key"
          :class="[
            'status-chip',
            key,
            { active: activeKinds.includes(String(key)) },
          ]"
          @click="toggleKind(String(key))"
          >{{ label }} {{ counts[key] || 0 }}</span
        >
      </span>
      <el-button :icon="Refresh" circle size="small" :loading="state.loading.value" title="刷新" @click="state.reload" />
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
    >
      <div class="dimension-toolbar" :aria-label="`${title}工具栏`">
        <div class="dimension-view-switch" role="group" aria-label="监控视图">
          <button type="button" :class="{ active: activeTab === 'charts' }" :aria-pressed="activeTab === 'charts'" @click="activeTab = 'charts'">指标图表</button>
          <button type="button" :class="{ active: activeTab === 'ranking' }" :aria-pressed="activeTab === 'ranking'" @click="activeTab = 'ranking'">排名与明细</button>
        </div>
        <template v-if="activeTab === 'charts'">
          <span class="dimension-toolbar-divider" aria-hidden="true" />
          <el-segmented v-model="activeMetric" class="dimension-metric-switch" :options="[{ label: 'TTFT', value: 'ttft' }, { label: 'TPM', value: 'tpm' }, { label: 'OTPS', value: 'otps' }]" size="small" aria-label="监控指标" />
        </template>
        <span class="dimension-toolbar-count">按总 Token 排序 · {{ visibleRows.length }} 个{{ kind === 'channels' ? '渠道' : '模型' }}</span>
      </div>
      <section v-show="activeTab === 'charts'" class="dimension-metric-view">
        <div class="dimension-chart-grid">
          <article v-for="group in chartTrendGroups" :key="group.key" class="dimension-chart-card">
            <header>
              <div class="dimension-heading">
                <div class="dimension-name"><h2 :title="group.name">{{ group.name }}</h2><MonitorCopyButton :value="kind === 'channels' ? group.id : group.name" :label="kind === 'channels' ? '复制渠道 ID' : '复制模型名称'" /></div>
                <span v-if="kind === 'channels'" class="dimension-id">ID {{ group.id }}</span>
              </div>
              <div class="dimension-headline" :title="group.headline.detail"><span>{{ group.headline.label }}</span><strong>{{ group.headline.value }}</strong><small v-if="activeMetric === 'tpm'">{{ group.headline.detail }}</small></div>
              <el-button v-if="group.row" link type="primary" @click="openDetail(group.row)">详情</el-button>
            </header>
            <section v-if="activeMetric === 'ttft'" class="dimension-chart"><CustomerCompareChart :series="group.ttft" :time-range="chartTimeRange" unit="s" :thresholds="ttftThresholds" /></section>
            <section v-else-if="activeMetric === 'tpm'" class="dimension-chart"><CustomerCompareChart :series="group.tpm" :time-range="chartTimeRange" compact /></section>
            <section v-else class="dimension-chart"><CustomerCompareChart :series="group.otps" :time-range="chartTimeRange" unit=" token/s" /></section>
          </article>
        </div>
      </section>
      <section v-show="activeTab === 'ranking'" class="dimension-kpis">
        <article class="dimension-kpi"><span>总 Token</span><strong>{{ formatTokens(grandTotal) }}</strong><small>当前 {{ hours }} 小时</small></article>
        <article class="dimension-kpi"><span>Token In</span><strong>{{ formatTokens(totalPrompt) }}</strong><small>{{ grandTotal ? `${(totalPrompt / grandTotal * 100).toFixed(1)}%` : '—' }} 占比</small></article>
        <article class="dimension-kpi out"><span>Token Out</span><strong>{{ formatTokens(totalCompletion) }}</strong><small>{{ grandTotal ? `${(totalCompletion / grandTotal * 100).toFixed(1)}%` : '—' }} 占比</small></article>
        <article class="dimension-kpi otps"><span>OTPS</span><strong>{{ weightedOTPS == null ? '—' : weightedOTPS.toFixed(2) }}</strong><small>有效输出总 Token ÷ 总耗时</small></article>
        <article class="dimension-kpi"><span>活跃{{ kind === 'channels' ? '渠道' : '模型' }}</span><strong>{{ activeCount }}</strong><small>{{ totalRequests.toLocaleString() }} 次请求</small></article>
        <article class="dimension-kpi danger"><span>TTFT 超阈值</span><strong>{{ overThreshold }}</strong><small>P95 阈值 ≥ {{ prefs.ttftP95Threshold }} 秒</small></article>
      </section>
      <section v-show="activeTab === 'ranking'" class="dimension-ranking-chart">
        <header><div><h2>Token 消耗 Top 10</h2><p>按 Token In + Token Out 降序</p></div></header>
        <CustomerTokenChart :items="topTen" />
      </section>
      <div v-show="activeTab === 'ranking'" class="dim-table dimension-ranking">
        <el-table v-mobile-cards
          :data="visibleRows"
          :row-class-name="rowClass"
          :max-height="720"
          @row-click="openDetail"
        >
          <el-table-column width="46" align="center">
            <template #default="{ row }"><el-checkbox :model-value="selectedKeys.includes(row.dimension_key)" :disabled="!selectedKeys.includes(row.dimension_key) && selectedKeys.length >= 8" @click.stop @change="toggleChart(row.dimension_key, Boolean($event))" /></template>
          </el-table-column>
          <el-table-column label="名称" min-width="280">
            <template #default="{ row }">
              <span class="dim-name">
                <i :class="['dim-dot', rowKind(row)]" />
                <b>{{ row.display_name || row.display_key || row.dimension_key }}</b><MonitorCopyButton :value="kind === 'channels' ? row.dimension_key.split(':').pop() || '' : row.display_name || row.display_key || row.dimension_key" :label="kind === 'channels' ? '复制渠道 ID' : '复制模型名称'" />
                <el-tooltip :content="row.dimension_key" placement="top">
                  <i class="dim-id">{{ row.dimension_key.split(":").pop() }}</i>
                </el-tooltip>
                <StatusTag
                  v-if="row.channelStatus && !['enabled', '1'].includes(String(row.channelStatus))"
                  :value="row.channelStatus"
                />
              </span>
            </template>
          </el-table-column>
          <el-table-column label="总 Token" min-width="110" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => totalTokens(a) - totalTokens(b)">
            <template #default="{ row }"><b>{{ formatTokens(totalTokens(row)) }}</b></template>
          </el-table-column>
          <el-table-column label="请求数" min-width="100" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => a.request_count - b.request_count">
            <template #default="{ row }">{{
              row.request_count.toLocaleString()
            }}</template>
          </el-table-column>
          <el-table-column label="错误率" min-width="130" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => (a.error_rate || 0) - (b.error_rate || 0)">
            <template #default="{ row }">
              <span class="err-cell">
                <span class="track"
                  ><span
                    :class="[
                      'fill',
                      rowKind(row) === 'crit'
                        ? 'crit'
                        : rowKind(row) === 'warn'
                          ? 'warn'
                          : '',
                    ]"
                    :style="{
                      width: `${Math.min((row.error_rate || 0) * 100 * 5, 100)}%`,
                    }"
                  ></span
                ></span>
                <span
                  :class="[
                    'err-num',
                    rowKind(row) === 'crit'
                      ? 'crit'
                      : rowKind(row) === 'warn'
                        ? 'warn'
                        : '',
                  ]"
                  >{{ pct(row.error_rate) }}</span
                >
              </span>
            </template>
          </el-table-column>
          <el-table-column label="成功率" min-width="90" align="right">
            <template #default="{ row }">{{ pct(row.success_rate) }}</template>
          </el-table-column>
          <el-table-column label="P95" min-width="90" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => (a.p95_use_time || 0) - (b.p95_use_time || 0)">
            <template #default="{ row }">{{
              secondsFmt(row.p95_use_time)
            }}</template>
          </el-table-column>
          <el-table-column label="TTFT P95" min-width="100" align="right">
            <template #default="{ row }">
              <span :class="{ 'dim-muted': row.ttft_p95_ms == null }">{{
                msFmt(row.ttft_p95_ms)
              }}</span>
            </template>
          </el-table-column>
          <el-table-column label="峰值 TPM" min-width="100" align="right">
            <template #default="{ row }">{{ formatTokens(peakDimTPM(row.dimension_key)) }}</template>
          </el-table-column>
          <el-table-column label="OTPS" min-width="100" align="right">
            <template #default="{ row }"><span :class="{ 'dim-muted': row.otps == null }">{{ row.otps == null ? '—' : row.otps.toFixed(2) }}</span></template>
          </el-table-column>
          <el-table-column label="缓存命中" min-width="100" align="right">
            <template #default="{ row }">
              <span :class="{ 'dim-muted': row.cache_hit_rate == null }">{{
                pct(row.cache_hit_rate)
              }}</span>
            </template>
          </el-table-column>
          <el-table-column label="Token 入/出" min-width="150" align="right">
            <template #default="{ row }"
              >{{ formatTokens(row.prompt_tokens) }} /
              {{ formatTokens(row.completion_tokens) }}</template
            >
          </el-table-column>
          <el-table-column label="OTPS 趋势" min-width="120"><template #default="{ row }"><MiniSparkline :values="historyPoints(row.dimension_key, 'otps')" color="#7a5af8" /></template></el-table-column>
          <el-table-column
            label=""
            :width="kind === 'channels' ? 184 : 112"
            fixed="right"
            align="right"
          >
            <template #default="{ row }">
              <span class="dim-row-actions">
                <el-button
                  v-if="kind === 'channels'"
                  size="small"
                  @click.stop="openOps(row)"
                  >操作</el-button
                >
                <span class="rowlink">详情 ›</span>
              </span>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </AsyncPanel>
    <el-drawer
      :model-value="Boolean(opChannel)"
      :title="`渠道操作 · ${opChannel?.display_name || opChannel?.display_key || ''}`"
      size="640px"
      @update:model-value="opChannel = null"
    >
      <ChannelOperations
        v-if="opChannel"
        :channel-id="Number(opChannel.dimension_key.split(':').pop())"
      />
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.dimension-toolbar { display: flex; align-items: center; flex-wrap: wrap; gap: 10px 18px; min-height: 52px; padding: 8px 12px; margin-bottom: 14px; border: 1px solid var(--ct-line); border-radius: 8px; background: var(--ct-surface); }
.dimension-view-switch { display: flex; align-items: center; gap: 16px; }
.dimension-view-switch button { border: 0; border-radius: 3px; background: none; padding: 5px 0; font: inherit; font-size: 13px; color: var(--ct-ink-3); cursor: pointer; white-space: nowrap; }
.dimension-view-switch button:hover { color: var(--ct-primary); }
.dimension-view-switch button.active { color: var(--ct-primary); font-weight: 500; }
.dimension-view-switch button:focus-visible { outline: 2px solid var(--ct-primary); outline-offset: 3px; }
.dimension-toolbar-divider { width: 1px; height: 20px; background: var(--ct-line); }
.dimension-metric-switch { --el-segmented-bg-color: var(--ct-surface-2); --el-segmented-item-selected-bg-color: var(--ct-surface); --el-segmented-item-selected-color: var(--ct-primary); font-size: 12px; }
.dimension-metric-switch :deep(.el-segmented__item) { padding: 0 12px; }
.dimension-toolbar-count { margin-left: auto; color: var(--ct-ink-3); font-size: 12px; white-space: nowrap; }
@media (max-width: 1000px) { .dimension-toolbar { gap: 10px; } }
@media (max-width: 480px) {
  .dimension-toolbar-divider { display: none; }
  .dimension-metric-switch :deep(.el-segmented__item) { padding: 0 9px; }
}
.dimension-chart-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; padding-bottom: 12px; }
.dimension-chart-card { min-width: 0; padding: 12px 14px 8px; border: 1px solid var(--ct-line); border-radius: 8px; background: var(--ct-surface); }
.dimension-chart-card > header { display: flex; align-items: center; gap: 12px; min-height: 42px; margin-bottom: 8px; }
.dimension-heading { flex: 1; min-width: 0; }
.dimension-name { display: flex; align-items: center; min-width: 0; gap: 3px; }
.dimension-name h2 { margin: 0; font-size: 13px; font-weight: 600; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.dimension-id { color: var(--ct-ink-3); font-size: 11px; }
.dimension-headline { text-align: right; flex-shrink: 0; font-variant-numeric: tabular-nums; }
.dimension-headline > span { color: var(--ct-ink-3); font-size: 11px; margin-right: 6px; }
.dimension-headline strong { font-size: 15px; font-weight: 500; }
.dimension-headline small { display: block; color: var(--ct-ink-3); font-size: 10px; margin-top: 3px; }
.dimension-chart { min-width: 0; }
.dimension-chart :deep(.customer-chart-canvas) { height: 230px; }
@media (max-width: 480px) { .dimension-chart-card > header { flex-wrap: wrap; gap: 6px; }.dimension-heading { flex-basis: 55%; }.dimension-headline strong { font-size: 13px; } }
.dimension-kpis { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 10px; margin-bottom: 12px; }
.dimension-kpi { position: relative; overflow: hidden; min-height: 92px; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); display: flex; flex-direction: column; }
.dimension-kpi::before { content: ""; position: absolute; inset: 0 auto 0 0; width: 3px; background: var(--ct-primary-solid); }
.dimension-kpi.out::before { background: var(--ct-warning-solid); }.dimension-kpi.otps::before { background: var(--ct-purple); }.dimension-kpi.danger::before { background: var(--ct-danger-solid); }
.dimension-kpi span { color: var(--ct-ink-2); font-size: 12px; }.dimension-kpi strong { margin-top: 4px; font-size: 24px; line-height: 1.2; }.dimension-kpi small { margin-top: auto; color: var(--ct-ink-3); font-size: 11px; }
.dimension-ranking-chart { min-width: 0; margin-bottom: 12px; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
.dimension-ranking-chart header { margin-bottom: 4px; }.dimension-ranking-chart h2 { margin: 0; font-size: 14px; }.dimension-ranking-chart p { margin: 2px 0 0; color: var(--ct-ink-3); font-size: 11px; }
.dimension-ranking-chart :deep(.customer-chart-canvas) { height: 270px; }
.dimension-ranking { overflow: hidden; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
@media (max-width: 1500px) { .dimension-chart-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 1380px) { .dimension-kpis { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 1000px) { .dimension-chart-grid { grid-template-columns: 1fr; } }
</style>
