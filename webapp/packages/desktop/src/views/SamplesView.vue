<script setup lang="ts">
import { reactive, ref, watch } from "vue";
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
function search() {
  page.value = 1;
  void state.reload();
}
watch([page, pageSize], () => void state.reload());
</script>
<template>
  <AppShell title="样本分析"
    ><div class="filter-row">
      <el-select v-model="form.sample_kind" clearable placeholder="样本类型"
        ><el-option label="错误" value="error" /><el-option
          label="慢请求"
          value="slow" /></el-select
      ><el-input v-model="form.model_name" placeholder="模型" /><el-input
        v-model="form.user_id"
        placeholder="用户 ID"
      /><el-input
        v-model="form.request_id"
        placeholder="Request ID"
      /><el-button type="primary" @click="search">查询</el-button>
    </div>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!state.data.value?.length"
      @retry="state.reload"
      ><el-table :data="state.data.value"
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
  ></AppShell>
</template>
