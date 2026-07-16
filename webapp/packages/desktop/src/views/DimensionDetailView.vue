<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ArrowLeft, ArrowRight } from "@element-plus/icons-vue";
import type { AlertItem, ChannelSnapshot, LogSample, MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import MetricMini from "../components/MetricMini.vue";
import RateBar from "../components/RateBar.vue";
import StatusTag from "../components/StatusTag.vue";
import HoursSelect from "../components/HoursSelect.vue";
import ChannelOperations from "../components/ChannelOperations.vue";
import ListPager from "../components/ListPager.vue";
import TrendChart, { type TrendSeries } from "../components/TrendChart.vue";
import { formatQuota, formatTime, formatTokens } from "../utils/format";

const props = defineProps<{
  kind: "customers" | "channels" | "models";
  dimensionKey: string;
}>();
const router = useRouter();
const filters = useFiltersStore();
const prefs = usePrefsStore();
onMounted(() => void prefs.load());

const hours = ref(1);
const tab = ref("trends");
const history = ref<MetricItem[]>([]);
const rangeSummary = ref<MetricItem>();
const historyLoading = ref(false);
const snapshots = ref<ChannelSnapshot[]>([]);
const samples = ref<LogSample[]>([]);
const samplesLoading = ref(false);
const samplePage = ref(1);
const samplePageSize = ref(20);
const alerts = ref<AlertItem[]>([]);
const crossMetrics = ref<MetricItem[]>([]);

const dimensionType = computed(() =>
  props.kind === "customers"
    ? "instance_user"
    : props.kind === "channels"
      ? "instance_channel"
      : "instance_model",
);
const crossType = computed(() =>
  props.kind === "customers"
    ? "instance_user_model"
    : props.kind === "channels"
      ? "instance_channel_model"
      : "instance_model_user",
);
const listPath = computed(() => `/${props.kind}`);
const listTitle = computed(
  () =>
    ({ customers: "客户监控", channels: "渠道监控", models: "模型监控" })[
      props.kind
    ],
);
const historyWindow = computed(() => (hours.value >= 6 ? "5m" : "1m"));
const idPart = computed(() => props.dimensionKey.split(":").pop() || "");
const instancePart = computed(() => props.dimensionKey.split(":")[0] || "");

const state = useAsyncData(async () => {
  const [metrics, channelData, alertData, cross] = await Promise.all([
    dashboard.metrics({
      window: "1m",
      latest: true,
      dimension_type: dimensionType.value,
    }),
    props.kind === "channels"
      ? dashboard.channelSnapshots({
          instance_id: instancePart.value || undefined,
          latest_only: true,
          limit: 500,
        })
      : Promise.resolve({ items: [] as ChannelSnapshot[] }),
    dashboard.alerts({ limit: 200 }),
    dashboard.metrics({
      window: "1m",
      latest: true,
      dimension_type: crossType.value,
    }),
  ]);
  snapshots.value = channelData.items;
  alerts.value = alertData.items.filter(
    (item) => item.dimension_key === props.dimensionKey,
  );
  crossMetrics.value = cross.items;
  void loadHistory(state.data.value !== undefined);
  return metrics.items
    .filter((x) => !filters.instance_id || x.instance_id === filters.instance_id)
    .sort((a, b) => b.request_count - a.request_count);
});

const current = computed(() =>
  state.data.value?.find((x) => x.dimension_key === props.dimensionKey),
);
const summary = computed(() => rangeSummary.value || current.value);
const orderedKeys = computed(
  () => state.data.value?.map((x) => x.dimension_key) || [],
);
const currentIndex = computed(() =>
  orderedKeys.value.indexOf(props.dimensionKey),
);
function goSibling(offset: number) {
  const target = orderedKeys.value[currentIndex.value + offset];
  if (target)
    void router.replace(`${listPath.value}/${encodeURIComponent(target)}`);
}
const snapshot = computed(() => {
  if (props.kind !== "channels") return undefined;
  const id = Number(idPart.value);
  return snapshots.value.find(
    (x) => x.channel_id === id && x.instance_id === instancePart.value,
  );
});
const modelsList = computed(
  () =>
    snapshot.value?.models_text
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean) || [],
);

// 交叉维度行：客户详情 → 名下模型；渠道详情 → 名下模型；模型详情 → 使用它的客户
const crossPrefix = computed(() => {
  if (props.kind === "customers")
    return `${instancePart.value}:user:${idPart.value}:model:`;
  if (props.kind === "channels")
    return `${instancePart.value}:channel:${idPart.value}:model:`;
  return `${props.dimensionKey}:user:`;
});
const crossLabel = computed(() => (props.kind === "models" ? "客户" : "模型"));
const crossRows = computed(() =>
  crossMetrics.value
    .filter((item) => item.dimension_key.startsWith(crossPrefix.value))
    .map((item) => ({
      ...item,
      crossName:
        props.kind === "models"
          ? userName(item.dimension_key.slice(crossPrefix.value.length))
          : item.dimension_key.slice(crossPrefix.value.length),
    }))
    .sort((a, b) => b.request_count - a.request_count),
);
function userName(userID: string) {
  const match = state.data.value?.find((x) =>
    x.dimension_key.endsWith(`:user:${userID}`),
  );
  return match?.display_name || `用户 ${userID}`;
}

async function loadHistory(silent = false) {
  const request = Date.now();
  historyToken = request;
  const background = silent && history.value.length > 0;
  if (!background) historyLoading.value = true;
  try {
    const [response, aggregate] = await Promise.all([
      dashboard.metricHistory({
        window: historyWindow.value,
        dimension_type: dimensionType.value,
        dimension_key: props.dimensionKey,
        hours: hours.value,
      }),
      dashboard.metricHistory({
        window: historyWindow.value,
        dimension_type: dimensionType.value,
        dimension_key: props.dimensionKey,
        hours: hours.value,
        aggregate: true,
      }),
    ]);
    if (historyToken === request) {
      history.value = response.items;
      rangeSummary.value = aggregate.items[0];
    }
  } catch {
    if (historyToken === request && !background) {
      history.value = [];
      rangeSummary.value = undefined;
    }
  } finally {
    if (historyToken === request && !background) historyLoading.value = false;
  }
}
let historyToken = 0;

async function loadSamples() {
  samplesLoading.value = true;
  try {
    const params: Record<string, string | number | undefined> = {
      instance_id: instancePart.value || undefined,
      limit: samplePageSize.value,
      offset: (samplePage.value - 1) * samplePageSize.value,
    };
    if (props.kind === "customers") params.user_id = idPart.value;
    if (props.kind === "channels") params.channel_id = Number(idPart.value);
    if (props.kind === "models") params.model_name = idPart.value;
    samples.value = (
      await dashboard.logSamples(params as Parameters<typeof dashboard.logSamples>[0])
    ).items;
  } finally {
    samplesLoading.value = false;
  }
}
watch([tab, samplePage, samplePageSize], () => {
  if (tab.value === "samples") void loadSamples();
});
watch([() => props.dimensionKey, hours], () => void loadHistory());
watch(
  () => props.dimensionKey,
  () => {
    samplePage.value = 1;
    if (tab.value === "samples") void loadSamples();
    void state.reload();
  },
);
useAutoRefresh(state.reload);

const points = (field: keyof MetricItem, multiply = 1) =>
  history.value.map(
    (item) =>
      [
        item.bucket_time,
        item[field] == null ? null : Number(item[field]) * multiply,
      ] as [string, number | null],
  );
const requestSeries = computed<TrendSeries[]>(() => [
  { name: "请求量", color: "#2f5fe0", data: points("request_count"), unit: " 次" },
  { name: "错误数", color: "#ce3b44", data: points("error_count"), unit: " 次" },
]);
const latencySeries = computed<TrendSeries[]>(() => [
  { name: "P50", color: "#2f5fe0", data: points("p50_use_time"), unit: "s" },
  { name: "P95", color: "#b96e0c", data: points("p95_use_time"), unit: "s" },
  { name: "P99", color: "#1391a5", data: points("p99_use_time"), unit: "s" },
]);
const rateSeries = computed<TrendSeries[]>(() => [
  { name: "成功率", color: "#178a5e", data: points("success_rate", 100), unit: "%" },
  { name: "错误率", color: "#ce3b44", data: points("error_rate", 100), unit: "%" },
]);
const ttftSeries = computed<TrendSeries[]>(() => [
  { name: "P50", color: "#2f5fe0", data: points("ttft_p50_ms", 0.001), unit: "s", sparse: true },
  { name: "P90", color: "#1391a5", data: points("ttft_p90_ms", 0.001), unit: "s", sparse: true },
  { name: "P95", color: "#b96e0c", data: points("ttft_p95_ms", 0.001), unit: "s", sparse: true },
]);
const cacheHitSeries = computed<TrendSeries[]>(() => [
  { name: "缓存命中率", color: "#1391a5", data: points("cache_hit_rate", 100), unit: "%", sparse: true },
]);
const tokenSeries = computed<TrendSeries[]>(() => [
  { name: "Token 入", color: "#2f5fe0", data: points("prompt_tokens") },
  { name: "Token 出", color: "#1391a5", data: points("completion_tokens") },
]);
const bucketLabel = computed(() =>
  historyWindow.value === "5m" ? "5m 桶（延迟为近似值）" : "1m 桶",
);
const fmt = (v: number | null | undefined, s = "") =>
  v == null ? "—" : `${v.toFixed(2)}${s}`;
const pct = (v: number | null | undefined) =>
  v == null ? "—" : `${(v * 100).toFixed(1)}%`;
const secondsFmt = (v: number | null | undefined) =>
  v == null ? "—" : `${v.toFixed(2)}s`;
const msFmt = (v: number | null | undefined) =>
  v == null ? "—" : `${(v / 1000).toFixed(2)}s`;
const quota = (v: number | null | undefined) =>
  formatQuota(v, prefs.quotaPerUnit, prefs.currencySymbol);
const firingCount = computed(
  () => alerts.value.filter((a) => a.status === "firing").length,
);
const displayName = computed(
  () =>
    snapshot.value?.channel_name ||
    current.value?.display_name ||
    current.value?.display_key ||
    idPart.value,
);
</script>
<template>
  <AppShell :title="listTitle">
    <template #tools>
      <router-link :to="listPath" class="crumb-back">‹ 返回列表</router-link>
      <el-button
        size="small"
        :icon="ArrowLeft"
        :disabled="currentIndex <= 0"
        @click="goSibling(-1)"
        >上一个</el-button
      >
      <el-button
        size="small"
        :disabled="
          currentIndex < 0 || currentIndex >= orderedKeys.length - 1
        "
        @click="goSibling(1)"
        >下一个<el-icon class="el-icon--right"><ArrowRight /></el-icon
      ></el-button>
      <HoursSelect v-model="hours" />
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="Boolean(state.data.value) && !current"
      @retry="state.reload"
    >
      <template #empty>
        <el-empty description="该维度近 24 小时无数据，可能已无流量或键已变更" />
      </template>
      <template v-if="current">
        <div class="detail-head">
          <h2>{{ displayName }}</h2>
          <span class="detail-sub">
            {{ dimensionKey
            }}<template v-if="modelsList.length">
              · {{ modelsList.slice(0, 4).join(" · ")
              }}<template v-if="modelsList.length > 4">
                +{{ modelsList.length - 4 }}</template
              ></template
            >
          </span>
          <div class="detail-chips">
            <StatusTag v-if="snapshot" :value="snapshot.status" />
            <span v-if="snapshot" class="pill plain"
              >权重 {{ snapshot.weight }}</span
            >
            <span v-if="snapshot" class="pill plain"
              >优先级 {{ snapshot.priority ?? "—" }}</span
            >
            <span v-if="snapshot" class="pill plain"
              >分组 {{ snapshot.group_name ?? "—" }}</span
            >
          </div>
        </div>
        <h3 class="latest-title">所选时间范围汇总</h3>
        <div class="mini-grid">
          <MetricMini
            label="请求数"
            :value="summary ? summary.request_count.toLocaleString() : '—'"
          />
          <MetricMini label="错误率" :value="pct(summary?.error_rate)" />
          <MetricMini
            label="P95（P50 / P99）"
            :value="`${fmt(summary?.p95_use_time, 's')}（${fmt(summary?.p50_use_time, 's')} / ${fmt(summary?.p99_use_time, 's')}）`"
          />
          <MetricMini
            label="TTFT P50 / P90 / P95"
            :value="`${msFmt(summary?.ttft_p50_ms)} / ${msFmt(summary?.ttft_p90_ms)} / ${msFmt(summary?.ttft_p95_ms)}`"
          />
        </div>
        <el-tabs v-model="tab" class="detail-tabs">
          <el-tab-pane label="趋势" name="trends">
            <div v-loading="historyLoading" class="trend-grid">
              <TrendChart
                :title="`请求与错误（${bucketLabel}）`"
                :series="requestSeries"
              />
              <TrendChart
                :title="`延迟（秒，${bucketLabel}）`"
                :series="latencySeries"
              />
              <TrendChart
                :title="`成功率 / 错误率（${bucketLabel}）`"
                :series="rateSeries"
                percent
              />
              <TrendChart :title="`TTFT（${bucketLabel}）`" :series="ttftSeries" />
              <TrendChart
                :title="`缓存命中率（${bucketLabel}）`"
                :series="cacheHitSeries"
                percent
              />
              <TrendChart
                :title="`Token 消耗（${bucketLabel}）`"
                :series="tokenSeries"
              />
            </div>
            <div class="quality-bars">
              <RateBar label="错误率" :value="summary?.error_rate ?? null" tone="danger" />
              <RateBar
                label="成功率"
                :value="summary?.success_rate ?? null"
                tone="success"
              />
              <RateBar
                label="缓存命中率（>512）"
                :value="summary?.cache_hit_rate ?? null"
              />
              <RateBar label="流式请求占比" :value="summary?.stream_rate ?? null" />
            </div>
          </el-tab-pane>
          <el-tab-pane :label="crossLabel" name="cross">
            <div class="dim-table">
              <el-table :data="crossRows" :max-height="480">
                <el-table-column :label="crossLabel" min-width="200">
                  <template #default="{ row }">
                    <span class="dim-name"
                      ><i
                        :class="[
                          'dim-dot',
                          (row.error_rate || 0) >= 0.1
                            ? 'crit'
                            : (row.error_rate || 0) > 0
                              ? 'warn'
                              : 'ok',
                        ]"
                      /><b>{{ row.crossName }}</b></span
                    >
                  </template>
                </el-table-column>
                <el-table-column label="请求数" width="100" align="right" sortable :sort-method="(a: MetricItem, b: MetricItem) => a.request_count - b.request_count">
                  <template #default="{ row }">{{
                    row.request_count.toLocaleString()
                  }}</template>
                </el-table-column>
                <el-table-column label="错误率" width="90" align="right" sortable :sort-method="(a: MetricItem, b: MetricItem) => (a.error_rate || 0) - (b.error_rate || 0)">
                  <template #default="{ row }">
                    <span
                      :class="[
                        'err-num',
                        (row.error_rate || 0) >= 0.1
                          ? 'crit'
                          : (row.error_rate || 0) > 0
                            ? 'warn'
                            : '',
                      ]"
                      >{{ pct(row.error_rate) }}</span
                    >
                  </template>
                </el-table-column>
                <el-table-column label="P95" width="90" align="right">
                  <template #default="{ row }">{{
                    secondsFmt(row.p95_use_time)
                  }}</template>
                </el-table-column>
                <el-table-column label="TTFT P95" width="100" align="right">
                  <template #default="{ row }">{{
                    msFmt(row.ttft_p95_ms)
                  }}</template>
                </el-table-column>
                <el-table-column label="缓存命中" width="100" align="right">
                  <template #default="{ row }">{{
                    pct(row.cache_hit_rate)
                  }}</template>
                </el-table-column>
                <el-table-column label="Token 入/出" width="150" align="right">
                  <template #default="{ row }"
                    >{{ formatTokens(row.prompt_tokens) }} /
                    {{ formatTokens(row.completion_tokens) }}</template
                  >
                </el-table-column>
                <el-table-column label="消费" width="100" align="right">
                  <template #default="{ row }">{{ quota(row.quota) }}</template>
                </el-table-column>
              </el-table>
            </div>
          </el-tab-pane>
          <el-tab-pane label="慢样本" name="samples">
            <div v-loading="samplesLoading" class="dim-table">
              <el-table :data="samples" :max-height="480">
                <el-table-column label="时间" width="160">
                  <template #default="s">{{
                    formatTime(s.row.created_at)
                  }}</template>
                </el-table-column>
                <el-table-column prop="sample_kind" label="样本" width="70" />
                <el-table-column label="结果" width="90">
                  <template #default="s"
                    ><StatusTag :value="s.row.log_type"
                  /></template>
                </el-table-column>
                <el-table-column prop="model_name" label="模型" min-width="140" />
                <el-table-column prop="username" label="用户" width="110" />
                <el-table-column label="Token" width="90" align="right">
                  <template #default="s">{{
                    formatTokens(s.row.total_tokens)
                  }}</template>
                </el-table-column>
                <el-table-column label="耗时" width="80" align="right">
                  <template #default="s"
                    ><span :class="{ hot: s.row.use_time >= 10 }"
                      >{{ s.row.use_time.toFixed(2) }}s</span
                    ></template
                  >
                </el-table-column>
                <el-table-column
                  prop="request_id"
                  label="Request ID"
                  min-width="180"
                  show-overflow-tooltip
                />
                <el-table-column
                  prop="error_summary"
                  label="错误摘要"
                  min-width="180"
                  show-overflow-tooltip
                />
              </el-table>
              <ListPager
                v-model:page="samplePage"
                v-model:page-size="samplePageSize"
                :item-count="samples.length"
              />
            </div>
          </el-tab-pane>
          <el-tab-pane name="alerts">
            <template #label>
              告警<span v-if="firingCount" class="tab-badge">{{
                firingCount
              }}</span>
            </template>
            <div class="dim-table">
              <el-table :data="alerts" :max-height="480">
                <el-table-column label="级别" width="90">
                  <template #default="s"
                    ><StatusTag :value="s.row.severity"
                  /></template>
                </el-table-column>
                <el-table-column label="状态" width="110">
                  <template #default="s"
                    ><StatusTag :value="s.row.status"
                  /></template>
                </el-table-column>
                <el-table-column prop="title" label="标题" min-width="160" />
                <el-table-column
                  prop="summary"
                  label="摘要"
                  min-width="240"
                  show-overflow-tooltip
                />
                <el-table-column label="最近出现" width="160">
                  <template #default="s">{{
                    formatTime(s.row.last_seen_at)
                  }}</template>
                </el-table-column>
              </el-table>
              <el-empty
                v-if="!alerts.length"
                :image-size="48"
                description="该维度暂无告警记录"
              />
            </div>
          </el-tab-pane>
          <el-tab-pane v-if="kind === 'channels'" label="操作" name="ops">
            <ChannelOperations :channel-id="Number(idPart)" />
          </el-tab-pane>
        </el-tabs>
      </template>
    </AsyncPanel>
  </AppShell>
</template>
