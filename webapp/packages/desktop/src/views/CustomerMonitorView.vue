<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Refresh, Search } from "@element-plus/icons-vue";
import type { MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import CustomerTokenChart from "../components/CustomerTokenChart.vue";
import CustomerCompareChart from "../components/CustomerCompareChart.vue";
import MiniSparkline from "../components/MiniSparkline.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { formatTokens } from "../utils/format";
import { useRouter } from "vue-router";

const filters = useFiltersStore();
const prefs = usePrefsStore();
void prefs.load();
const router = useRouter();
const hours = ref(1);
const activeTab = ref<"charts" | "ranking">("charts");
const activeMetric = ref<"ttft" | "tpm" | "otps">("ttft");
const ttftThresholds = computed(() => [
  { name: "P50", value: prefs.ttftP50Threshold, color: "#2f6fed" },
  { name: "P90", value: prefs.ttftP90Threshold, color: "#16a6b6" },
  { name: "P95", value: prefs.ttftP95Threshold, color: "#7357d8" },
]);
const search = ref("");
const selectedKeys = ref<string[]>([]);
const page = ref(1);
const pageSize = ref(50);
const history = ref<MetricItem[]>([]);
let initialized = false;

const state = useAsyncData(async () => {
  await filters.loadInstances();
  const instanceIDs = filters.instance_id
    ? [filters.instance_id]
    : filters.instances.filter(item => item.enabled).map(item => item.instance_id);
  const window = hours.value === 24 ? "5m" : "1m";
  const responses = await Promise.all(instanceIDs.flatMap(instanceID => [
    dashboard.metricHistory({
      instance_id: instanceID,
      window,
      dimension_type: "instance_user",
      dimension_key_prefix: `${instanceID}:user:`,
      hours: hours.value,
      aggregate: true,
    }),
    dashboard.metricHistory({
      instance_id: instanceID,
      window,
      dimension_type: "instance_user",
      dimension_key_prefix: `${instanceID}:user:`,
      hours: hours.value,
    }),
  ]));
  const summaries: MetricItem[] = [];
  const points: MetricItem[] = [];
  responses.forEach((response, index) => {
    if (index % 2 === 0) summaries.push(...response.items);
    else points.push(...response.items);
  });
  history.value = points;
  initialized = true;
  return summaries.sort((a, b) => totalTokens(b) - totalTokens(a));
});

watch([hours, () => filters.instance_id], () => {
  page.value = 1;
  if (initialized) void state.reload();
});
useAutoRefresh(state.reload);

function totalTokens(item: MetricItem) {
  return item.prompt_tokens + item.completion_tokens;
}
function customerName(item: MetricItem) {
  return item.display_name || item.display_key || `客户 ${item.dimension_key.split(":").pop()}`;
}
function customerID(item: MetricItem) {
  return item.dimension_key.split(":").pop() || item.dimension_key;
}
function ms(value: number | null | undefined) {
  return value == null ? "—" : value >= 1000 ? `${(value / 1000).toFixed(2)}s` : `${Math.round(value)}ms`;
}
function ttftStatus(value: number | null | undefined) {
  if (value == null) return { key: "empty", label: "无样本" };
  if (value >= 2000) return { key: "crit", label: "超阈值" };
  if (value >= 1500) return { key: "warn", label: "接近阈值" };
  return { key: "ok", label: "正常" };
}

const allRows = computed(() => state.data.value || []);
const grandTotal = computed(() => allRows.value.reduce((sum, item) => sum + totalTokens(item), 0));
const totalPrompt = computed(() => allRows.value.reduce((sum, item) => sum + item.prompt_tokens, 0));
const totalCompletion = computed(() => allRows.value.reduce((sum, item) => sum + item.completion_tokens, 0));
const totalRequests = computed(() => allRows.value.reduce((sum, item) => sum + item.request_count, 0));
const bucketMinutes = computed(() => hours.value === 24 ? 5 : 1);
// 按 dimension_key 预分组并排好时间序，避免表格每行渲染都全量扫描 history。
const historyByKey = computed(() => {
  const map = new Map<string, MetricItem[]>();
  history.value.forEach(item => {
    const list = map.get(item.dimension_key);
    if (list) list.push(item);
    else map.set(item.dimension_key, [item]);
  });
  map.forEach(list => list.sort((a, b) => Date.parse(a.bucket_time) - Date.parse(b.bucket_time)));
  return map;
});
function customerTPM(key: string) {
  return (historyByKey.value.get(key) || []).map(item => item.tpm / bucketMinutes.value);
}
function peakCustomerTPM(key: string) {
  return Math.max(...customerTPM(key), 0);
}
const peakTPM = computed(() => {
  const buckets = new Map<string, number>();
  history.value.forEach(item => {
    buckets.set(item.bucket_time, (buckets.get(item.bucket_time) || 0) + item.tpm / bucketMinutes.value);
  });
  return Math.max(...buckets.values(), 0);
});
const weightedTTFT = computed(() => {
  let sum = 0;
  let count = 0;
  allRows.value.forEach(item => {
    if (item.ttft_p95_ms != null) {
      const weight = item.ttft_count || item.request_count || 1;
      sum += item.ttft_p95_ms * weight;
      count += weight;
    }
  });
  return count ? sum / count : null;
});
const overThreshold = computed(() => allRows.value.filter(item => (item.ttft_p95_ms || 0) >= 2000).length);
const filteredRows = computed(() => {
  const keyword = search.value.trim().toLowerCase();
  return allRows.value.filter(item => !keyword || `${customerName(item)} ${item.dimension_key}`.toLowerCase().includes(keyword));
});
const pagedRows = computed(() => filteredRows.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
const topTen = computed(() => filteredRows.value.slice(0, 10).map(item => ({ name: customerName(item), prompt: item.prompt_tokens, completion: item.completion_tokens })));

watch(allRows, rows => {
  const available = new Set(rows.map(item => item.dimension_key));
  const kept = selectedKeys.value.filter(key => available.has(key));
  selectedKeys.value = kept.length ? kept : rows.slice(0, 8).map(item => item.dimension_key);
}, { immediate: true });

function toggleCompare(key: string, checked: boolean) {
  if (checked) {
    if (selectedKeys.value.length < 8) selectedKeys.value = [...selectedKeys.value, key];
  } else selectedKeys.value = selectedKeys.value.filter(item => item !== key);
}
function pointsFor(key: string, field: "ttft_p50_ms" | "ttft_p95_ms" | "prompt_tokens" | "completion_tokens") {
  return (historyByKey.value.get(key) || []).map(item => item[field]);
}
function tokenTrendPoints(key: string) {
  return (historyByKey.value.get(key) || []).map(item => item.prompt_tokens + item.completion_tokens);
}
function customerSeries(key: string, field: "ttft_p50_ms" | "ttft_p90_ms" | "ttft_p95_ms" | "tpm" | "otps", scale = 1, seriesName?: string) {
  const row = allRows.value.find(item => item.dimension_key === key);
  const data = (historyByKey.value.get(key) || [])
    .map(item => [item.bucket_time, item[field] == null ? null : Number(item[field]) / scale] as [string, number | null]);
  return [{ name: seriesName || (row ? customerName(row) : key), data }];
}
const selectedTrendGroups = computed(() => selectedKeys.value.map(key => {
  const row = allRows.value.find(item => item.dimension_key === key);
  return {
    key,
    name: row ? customerName(row) : key,
    id: row ? customerID(row) : key,
    tpm: customerSeries(key, "tpm", bucketMinutes.value, "TPM"),
    otps: customerSeries(key, "otps", 1, "OTPS"),
    ttft: [
      ...customerSeries(key, "ttft_p50_ms", 1000, "P50"),
      ...customerSeries(key, "ttft_p90_ms", 1000, "P90"),
      ...customerSeries(key, "ttft_p95_ms", 1000, "P95"),
    ],
  };
}));

function openDetail(row: MetricItem) {
  void router.push(`/customers/${encodeURIComponent(row.dimension_key)}`);
}
</script>

<template>
  <AppShell title="客户监控">
    <template #tools>
      <el-segmented v-model="hours" :options="[{ label: '1小时', value: 1 }, { label: '6小时', value: 6 }, { label: '24小时', value: 24 }]" size="small" />
      <el-input v-model="search" :prefix-icon="Search" placeholder="搜索客户名称或 ID" clearable size="small" class="customer-search" @input="page = 1" />
      <el-button :icon="Refresh" circle size="small" :loading="state.loading.value" title="刷新" @click="state.reload" />
    </template>

    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!allRows.length" @retry="state.reload">
      <el-tabs v-model="activeTab" class="customer-view-tabs">
        <el-tab-pane label="客户图表" name="charts" />
        <el-tab-pane label="排名与明细" name="ranking" />
      </el-tabs>

      <section v-show="activeTab === 'ranking'" class="customer-kpis">
        <article class="customer-kpi"><span>总 Token</span><strong>{{ formatTokens(grandTotal) }}</strong><small>当前 {{ hours }} 小时</small></article>
        <article class="customer-kpi"><span>Token In</span><strong>{{ formatTokens(totalPrompt) }}</strong><small>{{ grandTotal ? `${(totalPrompt / grandTotal * 100).toFixed(1)}%` : "—" }} 占比</small></article>
        <article class="customer-kpi"><span>Token Out</span><strong>{{ formatTokens(totalCompletion) }}</strong><small>{{ grandTotal ? `${(totalCompletion / grandTotal * 100).toFixed(1)}%` : "—" }} 占比</small></article>
        <article class="customer-kpi"><span>TTFT P95</span><strong>{{ ms(weightedTTFT) }}</strong><small>按样本量加权</small></article>
        <article class="customer-kpi tpm"><span>峰值 TPM</span><strong>{{ formatTokens(peakTPM) }}</strong><small>按时间桶折算的每分钟峰值</small></article>
        <article class="customer-kpi"><span>活跃客户</span><strong>{{ allRows.filter(item => item.request_count > 0).length }}</strong><small>{{ totalRequests.toLocaleString() }} 次请求</small></article>
        <article class="customer-kpi danger"><span>TTFT 超阈值</span><strong>{{ overThreshold }}</strong><small>阈值 ≥ 2 秒</small></article>
      </section>

      <section v-show="activeTab === 'ranking'" class="customer-chart-grid">
        <article class="customer-panel token-ranking-panel">
          <header><div><h2>Token 消耗 Top 10</h2><p>按总 Token 降序 · 蓝色 In，末端橙色小段为 Out</p></div></header>
          <CustomerTokenChart :items="topTen" />
        </article>
      </section>

      <section v-show="activeTab === 'charts'" class="customer-metric-view">
        <el-tabs v-model="activeMetric" class="customer-metric-tabs">
          <el-tab-pane label="TTFT" name="ttft" />
          <el-tab-pane label="TPM" name="tpm" />
          <el-tab-pane label="OTPS" name="otps" />
        </el-tabs>
        <div class="customer-trend-groups">
          <article v-for="group in selectedTrendGroups" :key="group.key" class="customer-trend-group">
            <header><div><h2>{{ group.name }}</h2><p>客户 ID {{ group.id }} · 按 Token 排名</p></div><el-button link type="primary" @click="openDetail(allRows.find(item => item.dimension_key === group.key)!)">详情</el-button></header>
            <section v-if="activeMetric === 'ttft'" class="customer-metric-card"><h3>TTFT</h3><p>P50 / P90 / P95 首字响应分位数</p><CustomerCompareChart :series="group.ttft" unit="s" :thresholds="ttftThresholds" /></section>
            <section v-else-if="activeMetric === 'tpm'" class="customer-metric-card"><h3>TPM</h3><p>每分钟 Token</p><CustomerCompareChart :series="group.tpm" compact /></section>
            <section v-else class="customer-metric-card"><h3>OTPS</h3><p>流式请求生成阶段每秒输出 Token</p><CustomerCompareChart :series="group.otps" unit=" token/s" /></section>
          </article>
        </div>
      </section>

      <section v-show="activeTab === 'ranking'" class="customer-panel customer-table-panel">
        <header>
          <div><h2>全部客户 · 按总 Token 降序</h2><p>勾选客户可加入上方独立趋势图，最多 8 个</p></div>
          <span class="customer-count">共 {{ filteredRows.length }} 个客户</span>
        </header>
        <el-table :data="pagedRows" class="customer-table" @row-click="openDetail">
          <el-table-column width="46" align="center">
            <template #default="{ row }">
              <el-checkbox :model-value="selectedKeys.includes(row.dimension_key)" :disabled="!selectedKeys.includes(row.dimension_key) && selectedKeys.length >= 8" @click.stop @change="toggleCompare(row.dimension_key, Boolean($event))" />
            </template>
          </el-table-column>
          <el-table-column label="客户" min-width="190" fixed="left">
            <template #default="{ row }"><div class="customer-name"><b>{{ customerName(row) }}</b><span>ID {{ customerID(row) }} · {{ row.instance_name }}</span></div></template>
          </el-table-column>
          <el-table-column label="总 Token" width="110" align="right" sortable :sort-method="(a: MetricItem, b: MetricItem) => totalTokens(a) - totalTokens(b)">
            <template #default="{ row }"><b class="token-total">{{ formatTokens(totalTokens(row)) }}</b></template>
          </el-table-column>
          <el-table-column label="占比" width="120">
            <template #default="{ row }"><div class="share-cell"><span>{{ grandTotal ? `${(totalTokens(row) / grandTotal * 100).toFixed(1)}%` : "—" }}</span><i><b :style="{ width: `${grandTotal ? Math.min(totalTokens(row) / grandTotal * 100, 100) : 0}%` }" /></i></div></template>
          </el-table-column>
          <el-table-column label="Token In" width="100" align="right"><template #default="{ row }">{{ formatTokens(row.prompt_tokens) }}</template></el-table-column>
          <el-table-column label="Token Out" width="100" align="right" class-name="token-out-cell" label-class-name="token-out-head"><template #default="{ row }">{{ formatTokens(row.completion_tokens) }}</template></el-table-column>
          <el-table-column label="峰值 TPM" width="105" align="right" sortable :sort-method="(a: MetricItem, b: MetricItem) => peakCustomerTPM(a.dimension_key) - peakCustomerTPM(b.dimension_key)"><template #default="{ row }"><b>{{ formatTokens(peakCustomerTPM(row.dimension_key)) }}</b></template></el-table-column>
          <el-table-column label="TTFT P50" width="100" align="right"><template #default="{ row }">{{ ms(row.ttft_p50_ms) }}</template></el-table-column>
          <el-table-column label="TTFT P95" width="118" align="right" sortable :sort-method="(a: MetricItem, b: MetricItem) => (a.ttft_p95_ms || 0) - (b.ttft_p95_ms || 0)">
            <template #default="{ row }"><span :class="['ttft-pill', ttftStatus(row.ttft_p95_ms).key]">{{ ms(row.ttft_p95_ms) }}</span></template>
          </el-table-column>
          <el-table-column label="Token 趋势" width="120"><template #default="{ row }"><MiniSparkline :values="tokenTrendPoints(row.dimension_key)" bars /></template></el-table-column>
          <el-table-column label="TTFT 趋势" width="120"><template #default="{ row }"><MiniSparkline :values="pointsFor(row.dimension_key, 'ttft_p95_ms')" color="#16a6b6" /></template></el-table-column>
          <el-table-column label="请求数" width="100" align="right"><template #default="{ row }">{{ row.request_count.toLocaleString() }}</template></el-table-column>
          <el-table-column label="状态" width="92"><template #default="{ row }"><span :class="['status-label', ttftStatus(row.ttft_p95_ms).key]">{{ ttftStatus(row.ttft_p95_ms).label }}</span></template></el-table-column>
          <el-table-column label="操作" width="70" fixed="right"><template #default="{ row }"><el-button link type="primary" @click.stop="openDetail(row)">详情</el-button></template></el-table-column>
        </el-table>
        <footer class="customer-pagination">
          <span>共 {{ filteredRows.length }} 个客户</span>
          <el-pagination v-model:current-page="page" v-model:page-size="pageSize" :total="filteredRows.length" :page-sizes="[20, 50, 100]" layout="prev, pager, next, sizes" small background />
        </footer>
      </section>
    </AsyncPanel>
  </AppShell>
</template>

<style scoped>
.customer-search { width: 220px; }
.customer-kpis { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: 10px; margin-bottom: 12px; }
.customer-kpi { position: relative; overflow: hidden; min-height: 94px; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); display: flex; flex-direction: column; }
.customer-kpi::before { content: ""; position: absolute; inset: 0 auto 0 0; width: 3px; background: #2f6fed; opacity: .8; }
.customer-kpi:nth-child(2)::before { background: #2f6fed; }.customer-kpi:nth-child(3)::before { background: #f08a24; }.customer-kpi:nth-child(4)::before { background: #7357d8; }.customer-kpi.tpm::before { background: #0f8fa3; }.customer-kpi:nth-child(6)::before { background: #36a269; }.customer-kpi.danger::before { background: #e47b22; }
.customer-kpi span { color: var(--ct-ink-2); font-size: 12px; }.customer-kpi strong { margin-top: 4px; font-size: 24px; line-height: 1.2; font-weight: 700; font-variant-numeric: tabular-nums; }.customer-kpi small { margin-top: auto; color: var(--ct-ink-3); font-size: 11px; }
.customer-chart-grid { display: grid; grid-template-columns: minmax(380px, .9fr) minmax(520px, 1.35fr); gap: 12px; margin-bottom: 12px; }
.token-ranking-panel { grid-column: 1 / -1; }
.customer-panel { min-width: 0; padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
.customer-panel > header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 4px; }.customer-panel h2 { margin: 0; font-size: 14px; }.customer-panel header p { margin: 2px 0 0; color: var(--ct-ink-3); font-size: 11px; }
:deep(.customer-chart-canvas) { width: 100%; height: 270px; }
.customer-view-tabs :deep(.el-tabs__header) { margin: 0 0 12px; }
.customer-view-tabs :deep(.el-tabs__content) { display: none; }
.customer-metric-tabs :deep(.el-tabs__header) { margin: 0 0 10px; }
.customer-metric-tabs :deep(.el-tabs__content) { display: none; }
.customer-trend-groups { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 12px; margin-bottom: 12px; }
.customer-trend-group { padding: 13px 15px; border: 1px solid var(--ct-line); border-radius: var(--ct-r-card); background: var(--ct-surface); box-shadow: var(--ct-shadow); }
.customer-trend-group > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.customer-trend-group h2 { margin: 0; font-size: 14px; }.customer-trend-group header p { margin: 2px 0 0; color: var(--ct-ink-3); font-size: 11px; }
.customer-metric-card { min-width: 0; padding: 10px 11px 4px; border: 1px solid var(--ct-line); border-radius: 8px; background: var(--ct-surface-2); }
.customer-metric-card h3 { margin: 0; font-size: 12px; }.customer-metric-card > p { margin: 1px 0 2px; color: var(--ct-ink-3); font-size: 10px; }.customer-metric-card :deep(.customer-chart-canvas) { height: 190px; }
.customer-table-panel { padding: 0; overflow: hidden; }.customer-table-panel > header { padding: 12px 14px 8px; margin: 0; }.customer-count { color: var(--ct-ink-3); font-size: 12px; }
.customer-table { --el-table-header-bg-color: var(--ct-surface-2); --el-table-row-hover-bg-color: #f4f7fc; font-size: 12px; }.customer-table :deep(.el-table__row) { cursor: pointer; }.customer-table :deep(th.el-table__cell) { padding: 7px 0; color: var(--ct-ink-3); font-size: 11px; }.customer-table :deep(td.el-table__cell) { padding: 7px 0; }.customer-table :deep(.token-out-head) { color: #b85c00; }.customer-table :deep(.token-out-cell) { color: #9a560f; font-weight: 600; }
.customer-name { display: flex; flex-direction: column; min-width: 0; }.customer-name b { overflow: hidden; text-overflow: ellipsis; }.customer-name span { color: var(--ct-ink-3); font-size: 10px; overflow: hidden; text-overflow: ellipsis; }.token-total { color: var(--ct-ink); }
.share-cell { display: flex; align-items: center; gap: 7px; }.share-cell span { width: 38px; text-align: right; font-variant-numeric: tabular-nums; }.share-cell i { width: 46px; height: 5px; overflow: hidden; border-radius: 3px; background: #e9edf3; }.share-cell i b { display: block; height: 100%; border-radius: inherit; background: #2f6fed; }
.ttft-pill,.status-label { display: inline-flex; align-items: center; justify-content: center; border-radius: 999px; padding: 2px 7px; white-space: nowrap; }.ttft-pill.ok,.status-label.ok { color: var(--ct-ok); background: var(--ct-ok-weak); }.ttft-pill.warn,.status-label.warn { color: var(--ct-warn); background: var(--ct-warn-weak); }.ttft-pill.crit,.status-label.crit { color: var(--ct-crit); background: var(--ct-crit-weak); }.ttft-pill.empty,.status-label.empty { color: var(--ct-ink-3); background: var(--ct-surface-2); }
.status-label { font-size: 10px; }.mini-spark { display: block; width: 92px; height: 28px; }
.customer-pagination { display: flex; align-items: center; justify-content: space-between; padding: 10px 14px; border-top: 1px solid var(--ct-line); color: var(--ct-ink-3); font-size: 11px; }
@media (max-width: 1500px) { .customer-trend-groups { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 1380px) { .customer-kpis { grid-template-columns: repeat(3, 1fr); }.customer-chart-grid { grid-template-columns: 1fr; } }
@media (max-width: 1000px) { .customer-trend-groups { grid-template-columns: 1fr; } }
@media (max-width: 900px) { .customer-kpis { grid-template-columns: repeat(2, 1fr); }.customer-search { width: 170px; } }
</style>
