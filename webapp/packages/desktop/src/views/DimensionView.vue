<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ChannelSnapshot, MetricItem } from "@ct/shared";
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
import CustomerCompareChart from "../components/CustomerCompareChart.vue";
import CustomerTokenChart from "../components/CustomerTokenChart.vue";
import MiniSparkline from "../components/MiniSparkline.vue";

const props = defineProps<{ kind: "channels" | "models" }>();
const filters = useFiltersStore();
const route = useRoute();
const router = useRouter();
const search = ref("");
const hours = ref(1);
const activeTab = ref<"charts" | "ranking">("charts");
const activeMetric = ref<"ttft" | "tpm" | "otps">("ttft");
const selectedKeys = ref<string[]>([]);
const history = ref<MetricItem[]>([]);
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
  const instanceIDs = filters.instance_id
    ? [filters.instance_id]
    : filters.instances.filter((item) => item.enabled).map((item) => item.instance_id);
  const window = hours.value === 24 ? "5m" : "1m";
  const prefix = (instanceID: string) => `${instanceID}:${props.kind === "channels" ? "channel" : "model"}:`;
  const metricResponses = await Promise.all(instanceIDs.flatMap((instanceID) => [
    dashboard.metricHistory({ instance_id: instanceID, window, dimension_type: dimensionType.value, dimension_key_prefix: prefix(instanceID), hours: hours.value, aggregate: true }),
    dashboard.metricHistory({ instance_id: instanceID, window, dimension_type: dimensionType.value, dimension_key_prefix: prefix(instanceID), hours: hours.value }),
  ]));
  const summaries: MetricItem[] = [];
  const points: MetricItem[] = [];
  metricResponses.forEach((response, index) => index % 2 === 0 ? summaries.push(...response.items) : points.push(...response.items));
  history.value = points;
  const channelData = await (
    props.kind === "channels"
      ? dashboard.channelSnapshots({
          instance_id: filters.instance_id || undefined,
          latest_only: true,
          limit: 500,
        })
      : Promise.resolve({ items: [] as ChannelSnapshot[] })
  );
  snapshots.value = channelData.items;
  initialized = true;
  return summaries.sort((a, b) => totalTokens(b) - totalTokens(a));
});
watch(
  () => [props.kind, filters.instance_id, hours.value],
  () => {
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
  if (item.channelStatus && item.channelStatus !== "enabled") return "disabled";
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
const overThreshold = computed(() => visibleRows.value.filter((item) => (item.ttft_p95_ms || 0) >= 2000).length);
const weightedOTPS = computed(() => {
  let tokens = 0;
  let weighted = 0;
  visibleRows.value.forEach((item) => {
    if (item.otps != null && item.otps_sample_tokens > 0) {
      tokens += item.otps_sample_tokens;
      weighted += item.otps * item.otps_sample_tokens;
    }
  });
  return tokens ? weighted / tokens : null;
});
const topTen = computed(() => visibleRows.value.slice(0, 10).map((item) => ({
  name: item.display_name || item.display_key || item.dimension_key,
  prompt: item.prompt_tokens,
  completion: item.completion_tokens,
})));
watch(rows, (items) => {
  const available = new Set(items.map((item) => item.dimension_key));
  const kept = selectedKeys.value.filter((key) => available.has(key));
  selectedKeys.value = kept.length ? kept : items.slice(0, 8).map((item) => item.dimension_key);
}, { immediate: true });
const bucketMinutes = computed(() => hours.value === 24 ? 5 : 1);
// 按 dimension_key 预分组并排好时间序，避免表格每行渲染都全量扫描 history。
const historyByKey = computed(() => {
  const map = new Map<string, MetricItem[]>();
  history.value.forEach((item) => {
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
const selectedTrendGroups = computed(() => selectedKeys.value.map((key) => {
  const row = rows.value.find((item) => item.dimension_key === key);
  return {
    key,
    name: row?.display_name || row?.display_key || key,
    id: key.split(":").pop() || key,
    row,
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
      <el-tabs v-model="activeTab" class="dimension-view-tabs">
        <el-tab-pane label="指标图表" name="charts" />
        <el-tab-pane label="排名与明细" name="ranking" />
      </el-tabs>
      <section v-show="activeTab === 'charts'" class="dimension-metric-view">
        <el-tabs v-model="activeMetric" class="dimension-metric-tabs">
          <el-tab-pane label="TTFT" name="ttft" />
          <el-tab-pane label="TPM" name="tpm" />
          <el-tab-pane label="OTPS" name="otps" />
        </el-tabs>
        <div class="dimension-chart-grid">
          <article v-for="group in selectedTrendGroups" :key="group.key" class="dimension-chart-card">
            <header><div><h2>{{ group.name }}</h2><p>{{ kind === 'channels' ? '渠道' : '模型' }} ID {{ group.id }} · 按 Token 排名</p></div><el-button v-if="group.row" link type="primary" @click="openDetail(group.row)">详情</el-button></header>
            <section v-if="activeMetric === 'ttft'" class="dimension-chart"><h3>TTFT</h3><p>P50 / P90 / P95</p><CustomerCompareChart :series="group.ttft" unit="s" :threshold="2" /></section>
            <section v-else-if="activeMetric === 'tpm'" class="dimension-chart"><h3>TPM</h3><p>每分钟 Token</p><CustomerCompareChart :series="group.tpm" compact /></section>
            <section v-else class="dimension-chart"><h3>OTPS</h3><p>流式请求生成阶段每秒输出 Token</p><CustomerCompareChart :series="group.otps" unit=" token/s" /></section>
          </article>
        </div>
      </section>
      <section v-show="activeTab === 'ranking'" class="dimension-kpis">
        <article class="dimension-kpi"><span>总 Token</span><strong>{{ formatTokens(grandTotal) }}</strong><small>当前 {{ hours }} 小时</small></article>
        <article class="dimension-kpi"><span>Token In</span><strong>{{ formatTokens(totalPrompt) }}</strong><small>{{ grandTotal ? `${(totalPrompt / grandTotal * 100).toFixed(1)}%` : '—' }} 占比</small></article>
        <article class="dimension-kpi out"><span>Token Out</span><strong>{{ formatTokens(totalCompletion) }}</strong><small>{{ grandTotal ? `${(totalCompletion / grandTotal * 100).toFixed(1)}%` : '—' }} 占比</small></article>
        <article class="dimension-kpi otps"><span>OTPS</span><strong>{{ weightedOTPS == null ? '—' : weightedOTPS.toFixed(2) }}</strong><small>按有效输出 Token 加权</small></article>
        <article class="dimension-kpi"><span>活跃{{ kind === 'channels' ? '渠道' : '模型' }}</span><strong>{{ activeCount }}</strong><small>{{ totalRequests.toLocaleString() }} 次请求</small></article>
        <article class="dimension-kpi danger"><span>TTFT 超阈值</span><strong>{{ overThreshold }}</strong><small>阈值 ≥ 2 秒</small></article>
      </section>
      <section v-show="activeTab === 'ranking'" class="dimension-ranking-chart">
        <header><div><h2>Token 消耗 Top 10</h2><p>按 Token In + Token Out 降序</p></div></header>
        <CustomerTokenChart :items="topTen" />
      </section>
      <div v-show="activeTab === 'ranking'" class="dim-table dimension-ranking">
        <el-table
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
                <b>{{ row.display_name || row.display_key }}</b>
                <el-tooltip :content="row.dimension_key" placement="top">
                  <i class="dim-id">{{ row.dimension_key.split(":").pop() }}</i>
                </el-tooltip>
                <StatusTag
                  v-if="row.channelStatus && row.channelStatus !== 'enabled'"
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
.dimension-view-tabs :deep(.el-tabs__header),
.dimension-metric-tabs :deep(.el-tabs__header) { margin: 0 0 10px; }
.dimension-view-tabs :deep(.el-tabs__content),
.dimension-metric-tabs :deep(.el-tabs__content) { display: none; }
.dimension-chart-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; padding-bottom: 12px; }
.dimension-chart-card { min-width: 0; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
.dimension-chart-card > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 8px; }
.dimension-chart-card h2,.dimension-chart h3 { margin: 0; font-size: 14px; }
.dimension-chart-card header p,.dimension-chart > p { margin: 2px 0; color: var(--ct-ink-3); font-size: 10px; }
.dimension-chart { min-width: 0; padding: 10px 11px 4px; border: 1px solid var(--ct-line); border-radius: 8px; background: var(--ct-surface-2); }
.dimension-chart :deep(.customer-chart-canvas) { height: 190px; }
.dimension-kpis { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 10px; margin-bottom: 12px; }
.dimension-kpi { position: relative; overflow: hidden; min-height: 92px; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); display: flex; flex-direction: column; }
.dimension-kpi::before { content: ""; position: absolute; inset: 0 auto 0 0; width: 3px; background: #2f6fed; }
.dimension-kpi.out::before { background: #f08a24; }.dimension-kpi.otps::before { background: #7a5af8; }.dimension-kpi.danger::before { background: #ce3b44; }
.dimension-kpi span { color: var(--ct-ink-2); font-size: 12px; }.dimension-kpi strong { margin-top: 4px; font-size: 24px; line-height: 1.2; }.dimension-kpi small { margin-top: auto; color: var(--ct-ink-3); font-size: 11px; }
.dimension-ranking-chart { min-width: 0; margin-bottom: 12px; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
.dimension-ranking-chart header { margin-bottom: 4px; }.dimension-ranking-chart h2 { margin: 0; font-size: 14px; }.dimension-ranking-chart p { margin: 2px 0 0; color: var(--ct-ink-3); font-size: 11px; }
.dimension-ranking-chart :deep(.customer-chart-canvas) { height: 270px; }
.dimension-ranking { overflow: hidden; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
@media (max-width: 1500px) { .dimension-chart-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 1380px) { .dimension-kpis { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 1000px) { .dimension-chart-grid { grid-template-columns: 1fr; } }
</style>
