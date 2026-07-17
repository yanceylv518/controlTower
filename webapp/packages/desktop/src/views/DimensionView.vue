<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ChannelSnapshot, MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import StatusTag from "../components/StatusTag.vue";
import ChannelOperations from "../components/ChannelOperations.vue";
import { formatTokens } from "../utils/format";

const props = defineProps<{ kind: "customers" | "channels" | "models" }>();
const filters = useFiltersStore();
const route = useRoute();
const router = useRouter();
const search = ref("");
const activeKinds = ref<string[]>([]);
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
  return metrics.items.filter(
    (x) => !filters.instance_id || x.instance_id === filters.instance_id,
  );
});
watch(
  () => [props.kind, filters.instance_id],
  () => void state.reload(),
);
useAutoRefresh(state.reload);

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
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
    >
      <div class="dim-table">
        <el-table
          :data="visibleRows"
          :row-class-name="rowClass"
          :max-height="720"
          @row-click="openDetail"
        >
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
