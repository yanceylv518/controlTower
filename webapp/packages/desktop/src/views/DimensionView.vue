<script setup lang="ts">
import { useRoute } from "vue-router";
import { computed, ref, watch } from "vue";
import type { ChannelSnapshot, MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import DimensionWorkspace from "../components/DimensionWorkspace.vue";
import MetricMini from "../components/MetricMini.vue";
import RateBar from "../components/RateBar.vue";
import StatusTag from "../components/StatusTag.vue";
import HoursSelect from "../components/HoursSelect.vue";
import ChannelOperations from "../components/ChannelOperations.vue";
import TrendChart, { type TrendSeries } from "../components/TrendChart.vue";
const props = defineProps<{ kind: "customers" | "channels" | "models" }>();
const filters = useFiltersStore();
const route = useRoute();
const selectedKey = ref("");
const hours = ref(1);
const sortMode = ref<"requests" | "errors">("requests");
const history = ref<MetricItem[]>([]);
const rangeSummary = ref<MetricItem>();
const historyLoading = ref(false);
const snapshots = ref<ChannelSnapshot[]>([]);
const dimensionType = computed(() =>
  props.kind === "customers"
    ? "instance_user"
    : props.kind === "channels"
      ? "instance_channel"
      : "instance_model",
);
const title = computed(
  () =>
    ({ customers: "客户监控", channels: "渠道监控", models: "模型监控" })[
      props.kind
    ],
);
const historyWindow = computed(() => (hours.value >= 6 ? "5m" : "1m"));
let historyRequest = 0;
const state = useAsyncData(async () => {
  const [metrics, channelData] = await Promise.all([
    dashboard.metrics({
      window: "1m",
      latest: true,
      dimension_type: dimensionType.value,
    }),
    props.kind === "channels"
      ? dashboard.channelSnapshots({
          instance_id: filters.instance_id || undefined,
          latest_only: true,
          limit: 500,
        })
      : Promise.resolve({ items: [] as ChannelSnapshot[] }),
  ]);
  snapshots.value = channelData.items;
  const items = metrics.items.filter(
    (x) => !filters.instance_id || x.instance_id === filters.instance_id,
  );
  const requested = typeof route.query.key === "string" ? route.query.key : "";
  if (requested && items.some((x) => x.dimension_key === requested))
    selectedKey.value = requested;
  else if (!items.some((x) => x.dimension_key === selectedKey.value))
    selectedKey.value = items[0]?.dimension_key || "";
  void loadHistory(state.data.value !== undefined);
  return items;
});
const sortedItems = computed(() =>
  [...(state.data.value || [])]
    .map((item) => ({
      ...item,
      status:
        props.kind === "channels"
          ? snapshots.value.find(
              (s) =>
                s.channel_id === Number(item.dimension_key.split(":").pop()) &&
                s.instance_id === item.instance_id,
            )?.status || "enabled"
          : undefined,
    }))
    .sort((a, b) =>
      sortMode.value === "errors"
        ? b.error_count - a.error_count
        : b.request_count - a.request_count,
    ),
);
const latestSelected = computed(() =>
  state.data.value?.find((x) => x.dimension_key === selectedKey.value),
);
const selected = computed(() => rangeSummary.value || latestSelected.value);
const snapshot = computed(() => {
  const id = Number(selectedKey.value.split(":").pop());
  return snapshots.value.find(
    (x) =>
      x.channel_id === id &&
      (!filters.instance_id || x.instance_id === filters.instance_id),
  );
});
const models = computed(
  () =>
    snapshot.value?.models_text
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean) || [],
);
async function loadHistory(silent = false) {
  if (!selectedKey.value) {
    history.value = [];
    rangeSummary.value = undefined;
    return;
  }
  const request = ++historyRequest;
  const background = silent && history.value.length > 0;
  if (!background) historyLoading.value = true;
  try {
    const [response, aggregate] = await Promise.all([
      dashboard.metricHistory({
        window: historyWindow.value,
        dimension_type: dimensionType.value,
        dimension_key: selectedKey.value,
        hours: hours.value,
      }),
      dashboard.metricHistory({
        window: historyWindow.value,
        dimension_type: dimensionType.value,
        dimension_key: selectedKey.value,
        hours: hours.value,
        aggregate: true,
      }),
    ]);
    if (request === historyRequest) {
      history.value = response.items;
      rangeSummary.value = aggregate.items[0];
    }
  } catch {
    if (request === historyRequest && !background) {
      history.value = [];
      rangeSummary.value = undefined;
    }
  } finally {
    if (request === historyRequest && !background) historyLoading.value = false;
  }
}
watch(
  () => [props.kind, filters.instance_id],
  () => void state.reload(),
);
watch([selectedKey, hours], () => void loadHistory());
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
  {
    name: "请求量",
    color: "#246bfe",
    data: points("request_count"),
    unit: " 次",
  },
  {
    name: "错误数",
    color: "#f56c6c",
    data: points("error_count"),
    unit: " 次",
  },
]);
const rateSeries = computed<TrendSeries[]>(() => [
  {
    name: "成功率",
    color: "#2fb344",
    data: points("success_rate", 100),
    unit: "%",
  },
  {
    name: "错误率",
    color: "#f56c6c",
    data: points("error_rate", 100),
    unit: "%",
  },
]);
const latencySeries = computed<TrendSeries[]>(() => [
  { name: "P50", color: "#36a2eb", data: points("p50_use_time"), unit: "s" },
  { name: "P95", color: "#ff9f43", data: points("p95_use_time"), unit: "s" },
  { name: "P99", color: "#9b59b6", data: points("p99_use_time"), unit: "s" },
]);
const tokenSeries = computed<TrendSeries[]>(() => [
  { name: "Token 入", color: "#246bfe", data: points("prompt_tokens") },
  { name: "Token 出", color: "#17a2b8", data: points("completion_tokens") },
]);
const bucketLabel = computed(() =>
  historyWindow.value === "5m" ? "5m 桶（延迟为近似值）" : "1m 桶",
);
const fmt = (v: number | null | undefined, s = "") =>
  v == null ? "—" : `${v.toFixed(2)}${s}`;
const cacheHitSeries = computed<TrendSeries[]>(() => [
  {
    name: "缓存命中率",
    color: "#246bfe",
    data: points("cache_hit_rate", 100),
    unit: "%",
    sparse: true,
  },
]);
const ttftSeries = computed<TrendSeries[]>(() => [
  {
    name: "TTFT 平均",
    color: "#36a2eb",
    data: points("ttft_avg_ms", 0.001),
    unit: "s",
    sparse: true,
  },
  {
    name: "TTFT P95",
    color: "#ff9f43",
    data: points("ttft_p95_ms", 0.001),
    unit: "s",
    sparse: true,
  },
]);
</script>
<template>
  <AppShell :title="title"
    ><AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><div class="dimension-toolbar">
        <el-radio-group v-model="sortMode" size="small"
          ><el-radio-button value="requests">按请求量</el-radio-button
          ><el-radio-button value="errors"
            >按错误数</el-radio-button
          ></el-radio-group
        >
      </div>
      <DimensionWorkspace
        v-if="state.data.value"
        v-model:selected-key="selectedKey"
        :items="sortedItems"
        ><template #row="{ item }"
          ><div class="dimension-row">
            <strong
              ><i
                class="dimension-dot"
                :class="
                  item.error_rate && item.error_rate >= 0.1
                    ? 'danger'
                    : item.error_rate && item.error_rate > 0
                      ? 'warning'
                      : ''
                "
              />{{ item.display_name || item.display_key }}</strong
            ><span
              >{{ item.request_count }} 请求 ·
              {{
                fmt(
                  item.success_rate
                    ? item.success_rate * 100
                    : item.success_rate,
                  "%",
                )
              }}</span
            ><RateBar
              label="错误率"
              :value="item.error_rate"
              tone="danger"
            /></div></template
        ><template #detail
          ><div v-if="selected" class="dimension-content">
            <div class="panel-title">
              <div>
                <h2>
                  {{
                    snapshot
                      ? `${snapshot.channel_name} (ID ${snapshot.channel_id})`
                      : selected.display_name || selected.display_key
                  }}
                </h2>
                <small class="dimension-raw-id"
                  >原始维度：{{ selected.display_key }}</small
                >
                <StatusTag v-if="snapshot" :value="snapshot.status" /><span
                  v-if="snapshot"
                  >权重 {{ snapshot.weight }}</span
                ><span v-if="snapshot"
                  >分组 {{ snapshot.group_name ?? "—" }}</span
                ><span v-if="snapshot"
                  >优先级 {{ snapshot.priority ?? "—" }}</span
                >
              </div>
              <HoursSelect v-model="hours" />
            </div>
            <div v-if="models.length" class="chips">
              <el-tag v-for="m in models.slice(0, 4)" :key="m" type="info">{{
                m
              }}</el-tag
              ><el-tag v-if="models.length > 4"
                >+{{ models.length - 4 }}</el-tag
              >
            </div>
            <h3 class="latest-title">所选时间范围汇总</h3>
            <div class="mini-grid metric-priority-grid">
              <MetricMini
                label="请求数"
                :value="selected.request_count"
              /><MetricMini
                label="错误率"
                :value="
                  fmt(
                    selected.error_rate
                      ? selected.error_rate * 100
                      : selected.error_rate,
                    '%',
                  )
                "
              /><MetricMini
                label="成功率"
                :value="
                  fmt(
                    selected.success_rate
                      ? selected.success_rate * 100
                      : selected.success_rate,
                    '%',
                  )
                "
              /><MetricMini
                label="P95（P50 / P99）"
                :value="`${fmt(selected.p95_use_time, 's')}（${fmt(selected.p50_use_time, 's')} / ${fmt(selected.p99_use_time, 's')}）`"
              /><MetricMini
                label="TTFT 平均 / P95"
                :value="`${fmt(selected.ttft_avg_ms == null ? null : selected.ttft_avg_ms / 1000, 's')} / ${fmt(selected.ttft_p95_ms == null ? null : selected.ttft_p95_ms / 1000, 's')}`"
              /><MetricMini
                label="缓存命中率（Prompt > 512）"
                :value="
                  fmt(
                    selected.cache_hit_rate == null
                      ? null
                      : selected.cache_hit_rate * 100,
                    '%',
                  )
                "
              /><MetricMini
                label="Token In / Out"
                :value="`${selected.prompt_tokens} / ${selected.completion_tokens}`"
              /><MetricMini label="Quota" :value="selected.quota" />
            </div>
            <div class="quality-bars">
              <RateBar
                label="错误率"
                :value="selected.error_rate"
                tone="danger"
              /><RateBar
                label="成功率"
                :value="selected.success_rate"
                tone="success"
              /><RateBar
                label="缓存 Token 占比"
                :value="selected.cache_token_rate"
              /><RateBar label="流式请求占比" :value="selected.stream_rate" />
            </div>
            <div v-loading="historyLoading" class="trend-grid">
              <TrendChart
                :title="`请求与错误（${bucketLabel}）`"
                :series="requestSeries"
              /><TrendChart
                :title="`延迟（秒，${bucketLabel}）`"
                :series="latencySeries"
              /><TrendChart
                :title="`成功率 / 错误率（${bucketLabel}）`"
                :series="rateSeries"
                percent
              /><TrendChart
                :title="`TTFT（${bucketLabel}，无流式流量的时段无数据）`"
                :series="ttftSeries"
              /><TrendChart
                :title="`缓存命中率（${bucketLabel}）`"
                :series="cacheHitSeries"
                percent
              /><TrendChart
                :title="`Token 消耗（${bucketLabel}）`"
                :series="tokenSeries"
              />
            </div>
            <div class="mini-grid">
              <MetricMini
                label="大输入样本数"
                :value="selected.big_input_count ?? '—'"
              /><MetricMini
                label="TTFT 样本数"
                :value="selected.ttft_count ?? '—'"
              />
            </div>
            <ChannelOperations
              v-if="kind === 'channels'"
              :channel-id="Number(selectedKey.split(':').pop())"
            /></div></template></DimensionWorkspace></AsyncPanel
  ></AppShell>
</template>
