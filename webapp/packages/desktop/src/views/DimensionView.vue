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
const search = ref("");
const activeKinds = ref<string[]>([]);
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

type DimRow = MetricItem & { channelStatus?: string };
const rows = computed<DimRow[]>(() =>
  (state.data.value || []).map((item) => ({
    ...item,
    channelStatus:
      props.kind === "channels"
        ? snapshots.value.find(
            (s) =>
              s.channel_id === Number(item.dimension_key.split(":").pop()) &&
              s.instance_id === item.instance_id,
          )?.status || "enabled"
        : undefined,
  })),
);

// 状态分类：异常/注意/正常/无流量/已禁用（与告警阈值同口径）
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
// 默认视图隐藏无流量/已禁用；点签可任意组合；异常/注意永远置顶
const kindWeight: Record<string, number> = {
  crit: 0,
  warn: 1,
  ok: 2,
  idle: 3,
  disabled: 4,
};
const visibleRows = computed(() =>
  searched.value
    .filter((item) =>
      activeKinds.value.length
        ? activeKinds.value.includes(rowKind(item))
        : !["idle", "disabled"].includes(rowKind(item)),
    )
    .sort(
      (a, b) =>
        (kindWeight[rowKind(a)] ?? 5) - (kindWeight[rowKind(b)] ?? 5) ||
        b.request_count - a.request_count,
    ),
);
function toggleKind(key: string) {
  activeKinds.value = activeKinds.value.includes(key)
    ? activeKinds.value.filter((x) => x !== key)
    : [...activeKinds.value, key];
}

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
    color: "#2f5fe0",
    data: points("request_count"),
    unit: " 次",
  },
  {
    name: "错误数",
    color: "#ce3b44",
    data: points("error_count"),
    unit: " 次",
  },
]);
const rateSeries = computed<TrendSeries[]>(() => [
  {
    name: "成功率",
    color: "#178a5e",
    data: points("success_rate", 100),
    unit: "%",
  },
  {
    name: "错误率",
    color: "#ce3b44",
    data: points("error_rate", 100),
    unit: "%",
  },
]);
const latencySeries = computed<TrendSeries[]>(() => [
  { name: "P50", color: "#2f5fe0", data: points("p50_use_time"), unit: "s" },
  { name: "P95", color: "#b96e0c", data: points("p95_use_time"), unit: "s" },
  { name: "P99", color: "#1391a5", data: points("p99_use_time"), unit: "s" },
]);
const tokenSeries = computed<TrendSeries[]>(() => [
  { name: "Token 入", color: "#2f5fe0", data: points("prompt_tokens") },
  { name: "Token 出", color: "#1391a5", data: points("completion_tokens") },
]);
const cacheHitSeries = computed<TrendSeries[]>(() => [
  {
    name: "缓存命中率",
    color: "#1391a5",
    data: points("cache_hit_rate", 100),
    unit: "%",
    sparse: true,
  },
]);
const ttftSeries = computed<TrendSeries[]>(() => [
  {
    name: "P50",
    color: "#2f5fe0",
    data: points("ttft_p50_ms", 0.001),
    unit: "s",
    sparse: true,
  },
  {
    name: "P90",
    color: "#1391a5",
    data: points("ttft_p90_ms", 0.001),
    unit: "s",
    sparse: true,
  },
  {
    name: "P95",
    color: "#b96e0c",
    data: points("ttft_p95_ms", 0.001),
    unit: "s",
    sparse: true,
  },
]);
const bucketLabel = computed(() =>
  historyWindow.value === "5m" ? "5m 桶（延迟为近似值）" : "1m 桶",
);
const fmt = (v: number | null | undefined, s = "") =>
  v == null ? "—" : `${v.toFixed(2)}${s}`;
const pct = (v: number | null | undefined) =>
  v == null ? "—" : `${(v * 100).toFixed(1)}%`;
const seconds = (v: number | null | undefined) =>
  v == null ? "—" : `${v.toFixed(2)}s`;
const ms = (v: number | null | undefined) =>
  v == null ? "—" : `${(v / 1000).toFixed(2)}s`;
function rowClass({ row }: { row: DimRow }) {
  const classes: string[] = [];
  if (row.dimension_key === selectedKey.value) classes.push("row-selected");
  if (rowKind(row) === "crit") classes.push("row-crit");
  return classes.join(" ");
}
</script>
<template>
  <AppShell :title="title">
    <template #tools>
      <el-input
        v-model="search"
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
      <HoursSelect v-model="hours" />
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
    >
      <!-- 列表即表格：全宽、多指标并排、异常置顶 -->
      <div class="dim-table">
        <el-table
          :data="visibleRows"
          :row-class-name="rowClass"
          :max-height="Math.min(64 + visibleRows.length * 34, 420)"
          @row-click="(row: DimRow) => (selectedKey = row.dimension_key)"
        >
          <el-table-column label="名称" min-width="220">
            <template #default="{ row }">
              <span class="dim-name">
                <i :class="['dim-dot', rowKind(row)]" />
                <b>{{ row.display_name || row.display_key }}</b>
                <el-tooltip :content="row.dimension_key" placement="top">
                  <i class="dim-id">{{
                    row.dimension_key.split(":").pop()
                  }}</i>
                </el-tooltip>
                <StatusTag
                  v-if="row.channelStatus && row.channelStatus !== 'enabled'"
                  :value="row.channelStatus"
                />
              </span>
            </template>
          </el-table-column>
          <el-table-column label="请求数" width="100" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => a.request_count - b.request_count">
            <template #default="{ row }">{{
              row.request_count.toLocaleString()
            }}</template>
          </el-table-column>
          <el-table-column label="错误率" width="130" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => (a.error_rate || 0) - (b.error_rate || 0)">
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
          <el-table-column label="成功率" width="90" align="right">
            <template #default="{ row }">{{ pct(row.success_rate) }}</template>
          </el-table-column>
          <el-table-column label="P95" width="90" align="right" sortable :sort-method="(a: DimRow, b: DimRow) => (a.p95_use_time || 0) - (b.p95_use_time || 0)">
            <template #default="{ row }">{{
              seconds(row.p95_use_time)
            }}</template>
          </el-table-column>
          <el-table-column label="TTFT" width="90" align="right">
            <template #default="{ row }">
              <span :class="{ 'dim-muted': row.ttft_avg_ms == null }">{{
                ms(row.ttft_avg_ms)
              }}</span>
            </template>
          </el-table-column>
          <el-table-column label="缓存命中" width="100" align="right">
            <template #default="{ row }">
              <span :class="{ 'dim-muted': row.cache_hit_rate == null }">{{
                pct(row.cache_hit_rate)
              }}</span>
            </template>
          </el-table-column>
          <el-table-column label="Token 入/出" width="140" align="right">
            <template #default="{ row }"
              >{{ row.prompt_tokens.toLocaleString() }} /
              {{ row.completion_tokens.toLocaleString() }}</template
            >
          </el-table-column>
        </el-table>
      </div>

      <!-- 详情：紧跟表格，无空转区 -->
      <template v-if="selected">
        <div class="detail-head">
          <h2>
            {{
              snapshot
                ? snapshot.channel_name
                : selected.display_name || selected.display_key
            }}
          </h2>
          <span class="detail-sub">
            {{ selected.display_key
            }}<template v-if="models.length">
              · {{ models.slice(0, 4).join(" · ")
              }}<template v-if="models.length > 4">
                · +{{ models.length - 4 }}</template
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
          <MetricMini label="请求数" :value="selected.request_count" />
          <MetricMini label="错误率" :value="pct(selected.error_rate)" />
          <MetricMini
            label="P95（P50 / P99）"
            :value="`${fmt(selected.p95_use_time, 's')}（${fmt(selected.p50_use_time, 's')} / ${fmt(selected.p99_use_time, 's')}）`"
          />
          <MetricMini
            label="TTFT P50 / P90 / P95"
            :value="`${ms(selected.ttft_p50_ms)} / ${ms(selected.ttft_p90_ms)} / ${ms(selected.ttft_p95_ms)}`"
          />
        </div>
        <div class="quality-bars">
          <RateBar label="错误率" :value="selected.error_rate" tone="danger" />
          <RateBar
            label="成功率"
            :value="selected.success_rate"
            tone="success"
          />
          <RateBar
            label="缓存命中率（>512）"
            :value="selected.cache_hit_rate"
          />
          <RateBar label="流式请求占比" :value="selected.stream_rate" />
        </div>
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
          <TrendChart
            :title="`TTFT（${bucketLabel}）`"
            :series="ttftSeries"
          />
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
        <ChannelOperations
          v-if="kind === 'channels'"
          :channel-id="Number(selectedKey.split(':').pop())"
        />
      </template>
    </AsyncPanel>
  </AppShell>
</template>
