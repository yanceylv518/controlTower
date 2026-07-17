<script setup lang="ts">
import { onMounted, reactive, ref, watch } from "vue";
import { useRoute } from "vue-router";
import type { LogSample, NginxSlowSample } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import StatusTag from "../components/StatusTag.vue";
import { formatNumber, formatTime } from "../utils/format";
const filters = useFiltersStore();
const form = reactive({
  sample_kind: "",
  model_name: "",
  user_id: "",
  request_id: "",
});
const page = ref(1);
const pageSize = ref(20);
const state = useAsyncData(
  async () =>
    (
      await dashboard.logSamples({
        ...form,
        instance_id: filters.instance_id || undefined,
        limit: pageSize.value,
        offset: (page.value - 1) * pageSize.value,
      })
    ).items,
);
watch(
  () => filters.instance_id,
  () => {
    page.value = 1;
    void state.reload();
  },
);
useAutoRefresh(state.reload);
const route = useRoute();
onMounted(() => {
  if (typeof route.query.request_id === "string" && route.query.request_id) {
    form.request_id = route.query.request_id;
    void state.reload();
  }
});
// 样本详情抽屉：业务账 + 网关分段账（request_id 关联 nginx 慢样本）
const drawerOpen = ref(false);
const detail = ref<LogSample>();
const gateway = ref<NginxSlowSample>();
const gatewayLoading = ref(false);
async function openDetail(row: LogSample) {
  detail.value = row;
  gateway.value = undefined;
  drawerOpen.value = true;
  if (!row.request_id) return;
  gatewayLoading.value = true;
  try {
    const response = await dashboard.nginxSlowSamples({
      instance_id: row.instance_id,
      hours: 168,
      limit: 1,
      request_id: row.request_id,
    });
    gateway.value = response.items[0];
  } finally {
    gatewayLoading.value = false;
  }
}
const seg = (v: number | null | undefined) =>
  v == null ? "—" : `${v.toFixed(2)}s`;
function attribution(g: NginxSlowSample) {
  const transfer = g.urt - g.uht;
  if (g.uht >= g.rt / 2) return "首字节段慢（new-api / 上游首响应前）";
  if (transfer > g.uht) return "传输段慢（流式长输出或出站链路）";
  return "各段均衡";
}
function search() {
  page.value = 1;
  void state.reload();
}
watch([page, pageSize], () => void state.reload());
</script>
<template>
  <AppShell title="样本分析">
    <template #tools>
      <el-select v-model="form.sample_kind" clearable placeholder="样本类型" style="width: 110px"
        ><el-option label="错误" value="error" /><el-option
          label="慢请求"
          value="slow" /></el-select
      ><el-input v-model="form.model_name" placeholder="模型" style="width: 130px" /><el-input
        v-model="form.user_id"
        placeholder="用户 ID"
        style="width: 100px"
      /><el-input
        v-model="form.request_id"
        placeholder="Request ID"
        style="width: 150px"
      /><el-button type="primary" @click="search">查询</el-button>
    </template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><el-table :data="state.data.value" @row-click="openDetail"
        ><el-table-column label="时间" width="180"
          ><template #default="s">{{
            formatTime(s.row.created_at)
          }}</template></el-table-column
        ><el-table-column prop="sample_kind" label="样本" /><el-table-column
          label="结果"
          ><template #default="s"
            ><StatusTag :value="s.row.log_type" /></template></el-table-column
        ><el-table-column prop="model_name" label="模型" /><el-table-column
          prop="username"
          label="用户" /><el-table-column label="Token"
          ><template #default="s">{{
            formatNumber(s.row.total_tokens)
          }}</template></el-table-column
        ><el-table-column label="耗时"
          ><template #default="s"
            ><span :class="{ hot: s.row.use_time >= 10 }"
              >{{ s.row.use_time.toFixed(2) }}s</span
            ></template
          ></el-table-column
        ><el-table-column
          prop="request_id"
          label="Request ID"
          show-overflow-tooltip /><el-table-column
          prop="error_summary"
          label="错误摘要"
          show-overflow-tooltip
      /></el-table>
      <ListPager
        v-model:page="page"
        v-model:page-size="pageSize"
        :item-count="state.data.value?.length || 0" /></AsyncPanel
    >
    <el-drawer v-model="drawerOpen" title="样本详情 · 业务账与网关账对齐" size="520px">
      <template v-if="detail">
        <h3 class="latest-title">业务侧（new-api 日志）</h3>
        <el-descriptions :column="1" border size="small">
          <el-descriptions-item label="时间">{{ formatTime(detail.created_at) }}</el-descriptions-item>
          <el-descriptions-item label="模型">{{ detail.model_name }}</el-descriptions-item>
          <el-descriptions-item label="用户">{{ detail.username }}</el-descriptions-item>
          <el-descriptions-item label="全链路耗时 use_time">{{ detail.use_time.toFixed(2) }}s（含内部重试与流式传输）</el-descriptions-item>
          <el-descriptions-item label="Token">{{ formatNumber(detail.total_tokens) }}</el-descriptions-item>
          <el-descriptions-item label="Request ID">{{ detail.request_id || "—" }}</el-descriptions-item>
          <el-descriptions-item v-if="detail.error_summary" label="错误摘要">{{ detail.error_summary }}</el-descriptions-item>
        </el-descriptions>
        <h3 class="latest-title" style="margin-top: 16px">网关侧（Nginx timing）</h3>
        <div v-loading="gatewayLoading">
          <el-descriptions v-if="gateway" :column="1" border size="small">
            <el-descriptions-item label="RT 总耗时">{{ seg(gateway.rt) }}（客户端视角全程）</el-descriptions-item>
            <el-descriptions-item label="UHT 首响应">{{ seg(gateway.uht) }}（排队 + new-api + 上游首字节）</el-descriptions-item>
            <el-descriptions-item label="URT 上游总耗时">{{ seg(gateway.urt) }}（new-api 全程,含内部重试）</el-descriptions-item>
            <el-descriptions-item label="传输段 URT−UHT">{{ seg(gateway.urt - gateway.uht) }}（流式输出/链路）</el-descriptions-item>
            <el-descriptions-item label="客户端段 RT−URT">{{ seg(gateway.rt - gateway.urt) }}（入站网络/客户端）</el-descriptions-item>
            <el-descriptions-item label="归因">{{ attribution(gateway) }}</el-descriptions-item>
          </el-descriptions>
          <el-empty
            v-else-if="!gatewayLoading"
            :image-size="48"
            :description="detail.request_id ? '网关未采样（nginx 侧仅保留每分钟最慢 5 条）' : '该样本无 Request ID,无法关联网关计时'"
          />
        </div>
      </template>
    </el-drawer>
  </AppShell>
</template>
