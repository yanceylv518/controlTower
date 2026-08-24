<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import type { BillingJob } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { formatDateTime } from "../utils/billingRange";

const filters = useFiltersStore();
const router = useRouter();
const kind = ref<"all"|"generate"|"channel_generate">("all");
const state = useAsyncData(async () => {
  await filters.loadInstances();
  return dashboard.billingJobs({ status: "complete", limit: 200 });
});
const items = computed(() => (state.data.value?.items || []).filter((job) => job.status === "complete" && (job.job_type === "generate" || job.job_type === "channel_generate") && (kind.value === "all" || job.job_type === kind.value)));
const typeLabel = (job: BillingJob) => job.job_type === "channel_generate" ? "渠道账单" : "用户账单";
const local = (value?: string) => value ? formatDateTime(new Date(value)) : "";
const progressText = (job: BillingJob) => `${job.completed_steps} / ${job.total_steps}`;
async function open(job: BillingJob) {
  filters.selectSite(job.instance_id);
  const path = job.job_type === "channel_generate" ? "/billing/channels" : "/billing";
  await router.push({ path, query: { site: job.instance_id, from: local(job.range_from), to: local(job.range_to), job_id: job.id } });
}
void state.reload();
</script>

<template>
  <AppShell title="已生成账单">
    <template #tools>
      <el-radio-group v-model="kind">
        <el-radio-button value="all">全部</el-radio-button>
        <el-radio-button value="generate">用户账单</el-radio-button>
        <el-radio-button value="channel_generate">渠道账单</el-radio-button>
      </el-radio-group>
      <el-button @click="state.reload">刷新</el-button>
    </template>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!items.length" empty-text="暂无已生成账单" @retry="state.reload">
      <el-table :data="items" row-key="id" @row-click="open">
        <el-table-column label="账单类型" width="130"><template #default="s"><el-tag>{{ typeLabel(s.row) }}</el-tag></template></el-table-column>
        <el-table-column prop="instance_id" label="站点" min-width="150" />
        <el-table-column label="账单区间" min-width="330"><template #default="s">{{ local(s.row.range_from) }} 至 {{ local(s.row.range_to) }}</template></el-table-column>
        <el-table-column label="生成步骤" width="120"><template #default="s">{{ progressText(s.row) }}</template></el-table-column>
        <el-table-column prop="abnormal_rows" label="异常订单" width="110" />
        <el-table-column prop="requested_by" label="创建人" width="110" />
        <el-table-column label="完成时间" min-width="180"><template #default="s">{{ local(s.row.updated_at) }}</template></el-table-column>
        <el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click.stop="open(s.row)">查看账单</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
  </AppShell>
</template>

<style scoped>
:deep(.el-table__row){cursor:pointer}
</style>
