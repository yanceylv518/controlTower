<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { NginxSlowSample, NginxTimingResponse } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import HoursSelect from "../components/HoursSelect.vue";
import MetricMini from "../components/MetricMini.vue";
import TrendChart, { type TrendSeries } from "../components/TrendChart.vue";
import ListPager from "../components/ListPager.vue";

const filters = useFiltersStore();
const hours = ref(1);
const userID = ref("");
const channelID = ref("");
const modelName = ref("");
const matchStatus = ref("");
const page = ref(1);
const pageSize = ref(20);
const emptySummary = () => ({
  total_requests: 0,
  status_5xx: 0,
  status_504: 0,
  slow_count: 0,
  slow_ttft_count: 0,
  slow_transfer_count: 0,
  slow_ttft_percent: 0,
  slow_transfer_percent: 0,
});
const timing = ref<NginxTimingResponse>({ items: [], summary: emptySummary() });
const samples = ref<NginxSlowSample[]>([]);
const state = useAsyncData(async () => {
  if (!filters.instance_id) {
    timing.value = { items: [], summary: emptySummary() };
    samples.value = [];
    return false;
  }
  const [timingResponse, sampleResponse] = await Promise.all([
    dashboard.nginxTiming({
      instance_id: filters.instance_id,
      hours: hours.value,
    }),
    dashboard.nginxSlowSamples({
      instance_id: filters.instance_id,
      hours: hours.value,
      limit: pageSize.value,
      offset: (page.value - 1) * pageSize.value,
      user_id: userID.value,
      channel_id: channelID.value,
      model_name: modelName.value,
      match_status: matchStatus.value,
    }),
  ]);
  timing.value = timingResponse;
  samples.value = sampleResponse.items;
  return timingResponse.items.length > 0;
});
watch(
  () => filters.loaded,
  (loaded) => {
    if (loaded && !filters.instance_id && filters.instances.length) {
      const online = filters.instances.find((item) =>
        item.agents?.some((agent) => agent.online),
      );
      filters.instance_id = (online || filters.instances[0]).instance_id;
    }
  },
  { immediate: true },
);
watch(
  () => [
    filters.instance_id,
    hours.value,
    userID.value,
    channelID.value,
    modelName.value,
    matchStatus.value,
  ],
  () => {
    page.value = 1;
    void state.reload();
  },
  { immediate: true },
);
watch([page, pageSize], () => void state.reload());
const points = (field: keyof NginxTimingResponse["items"][number]) =>
  timing.value.items.map(
    (item) => [item.bucket_at, Number(item[field])] as [string, number],
  );
const ttft = computed<TrendSeries[]>(() => [
  { name: "UHT P50", color: "#36a2eb", unit: "s", data: points("uht_p50") },
  { name: "UHT P95", color: "#ff9f43", unit: "s", data: points("uht_p95") },
]);
const transfer = computed<TrendSeries[]>(() => [
  {
    name: "传输 P50",
    color: "#17a2b8",
    unit: "s",
    data: points("transfer_p50"),
  },
  {
    name: "传输 P95",
    color: "#9b59b6",
    unit: "s",
    data: points("transfer_p95"),
  },
]);
const volume = computed<TrendSeries[]>(() => [
  {
    name: "请求量",
    color: "#246bfe",
    type: "bar",
    data: points("request_count"),
  },
  { name: "5xx", color: "#f56c6c", data: points("status_5xx") },
  { name: "504", color: "#e6a23c", data: points("status_504") },
]);
const formatBytes = (value: number) =>
  value < 1024
    ? `${value} B`
    : value < 1048576
      ? `${(value / 1024).toFixed(1)} KiB`
      : `${(value / 1048576).toFixed(1)} MiB`;
const matchLabel = (row: NginxSlowSample) =>
  row.match_status === "matched"
    ? "已关联"
    : row.match_status === "multiple"
      ? `多条重试/尝试 (${row.match_count})`
      : "未关联";
const dimensionLink = (
  kind: "customers" | "channels" | "models",
  key: string | number,
) => `/${kind}?key=${encodeURIComponent(`${filters.instance_id}:${key}`)}`;
async function copyRequestID(value: string) {
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    ElMessage.success("Request ID 已复制");
  } catch {
    ElMessage.error("复制失败，请手动选择 Request ID");
  }
}
</script>

<template>
  <AppShell title="延时分诊">
    <template #tools>
      <el-input
        v-model="userID"
        placeholder="用户 ID"
        clearable
        style="width: 120px"
      />
      <el-input
        v-model="channelID"
        placeholder="渠道 ID"
        clearable
        style="width: 120px"
      />
      <el-input
        v-model="modelName"
        placeholder="模型名称"
        clearable
        style="width: 180px"
      />
      <el-select
        v-model="matchStatus"
        placeholder="关联状态"
        clearable
        style="width: 150px"
        ><el-option label="已关联" value="matched" /><el-option
          label="未关联"
          value="unmatched" /><el-option label="多条匹配" value="multiple"
      /></el-select>
      <HoursSelect v-model="hours" />
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="Boolean(filters.instance_id) && !timing.items.length"
      @retry="state.reload"
    >
      <template #empty
        ><el-empty
          description="未启用 Nginx timing 采集，请配置 CT_NGINX_ACCESS_LOG"
      /></template>
      <div class="latency-cards">
        <MetricMini
          label="慢请求"
          :value="timing.summary.slow_count"
        /><MetricMini
          label="首响应阶段主导"
          :value="`${timing.summary.slow_ttft_count} / ${timing.summary.slow_ttft_percent.toFixed(1)}%`"
        /><MetricMini
          label="传输阶段主导"
          :value="`${timing.summary.slow_transfer_count} / ${timing.summary.slow_transfer_percent.toFixed(1)}%`"
        /><MetricMini
          label="5xx / 504"
          :value="`${timing.summary.status_5xx} / ${timing.summary.status_504}`"
        />
      </div>
      <div class="latency-trends">
        <TrendChart title="UHT（上游首响应时间）" :series="ttft" /><TrendChart
          title="响应传输阶段"
          :series="transfer"
        /><TrendChart title="请求量与 5xx / 504" :series="volume" />
      </div>
      <section class="panel latency-table">
        <h3>慢样本</h3>
        <p class="muted">
          RT：请求总耗时；UHT：上游首响应时间；URT：上游总耗时。Request ID
          为空或采样日志未保留对应记录时会显示未关联。
        </p>
        <el-table :data="samples">
          <el-table-column
            prop="occurred_at"
            label="时间"
            width="190"
          /><el-table-column
            prop="path"
            label="Path"
            min-width="190"
            show-overflow-tooltip
          /><el-table-column prop="status" label="状态" width="75" />
          <el-table-column label="RT" width="90"
            ><template #default="{ row }"
              ><span :class="{ hot: row.rt >= 10 }"
                >{{ row.rt.toFixed(3) }}s</span
              ></template
            ></el-table-column
          ><el-table-column label="UHT" width="90"
            ><template #default="{ row }"
              ><span :class="{ hot: row.uht >= 5 }"
                >{{ row.uht.toFixed(3) }}s</span
              ></template
            ></el-table-column
          ><el-table-column prop="urt" label="URT(s)" width="90" />
          <el-table-column label="关联状态" width="155"
            ><template #default="{ row }"
              ><el-tag
                :type="
                  row.match_status === 'matched'
                    ? 'success'
                    : row.match_status === 'multiple'
                      ? 'warning'
                      : 'info'
                "
                >{{ matchLabel(row) }}</el-tag
              ></template
            ></el-table-column
          >
          <el-table-column label="用户" width="130"
            ><template #default="{ row }"
              ><router-link
                v-if="row.match_status === 'matched'"
                :to="dimensionLink('customers', row.user_id)"
                >{{ row.user_name || row.user_id }}</router-link
              ><span v-else>—</span></template
            ></el-table-column
          >
          <el-table-column label="渠道" width="150"
            ><template #default="{ row }"
              ><router-link
                v-if="row.match_status === 'matched'"
                :to="dimensionLink('channels', row.channel_id)"
                >{{ row.channel_name || row.channel_id }}</router-link
              ><span v-else>—</span></template
            ></el-table-column
          >
          <el-table-column label="模型" min-width="150"
            ><template #default="{ row }"
              ><router-link
                v-if="row.match_status === 'matched'"
                :to="dimensionLink('models', row.model_name)"
                >{{ row.model_name }}</router-link
              ><span v-else>—</span></template
            ></el-table-column
          >
          <el-table-column
            prop="token_name"
            label="令牌"
            width="130"
          /><el-table-column label="Request ID" min-width="180"
            ><template #default="{ row }"
              ><el-button
                v-if="row.request_id"
                link
                type="primary"
                @click="copyRequestID(row.request_id)"
                >{{ row.request_id }}</el-button
              ><span v-else>—</span></template
            ></el-table-column
          >
          <el-table-column label="Bytes" width="100"
            ><template #default="{ row }">{{
              formatBytes(row.bytes)
            }}</template></el-table-column
          >
        </el-table>
        <ListPager
          v-model:page="page"
          v-model:page-size="pageSize"
          :item-count="samples.length"
        />
      </section>
    </AsyncPanel>
  </AppShell>
</template>
