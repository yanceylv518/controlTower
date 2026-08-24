<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import type { BillingJob } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";

const filters = useFiltersStore();
// The billing worker is global and serial, so the default view must expose a
// job from another site that can block creation on the currently selected site.
const showAllSites = ref(true);
const state = useAsyncData(async () => {
  await filters.loadInstances();
  return dashboard.billingJobs({ instance_id: showAllSites.value ? undefined : filters.site_id || undefined, limit: 200 });
});
const items = computed(() => state.data.value?.items || []);
const activeCount = computed(() => items.value.filter((job) => job.status === "pending" || job.status === "running").length);
const typeLabel = (value?: string) => ({ generate: "用户账单生成", channel_generate: "渠道账单生成", verify: "账单核验" }[value || ""] || value || "后台任务");
const statusMeta = (value: BillingJob["status"]) => ({
  pending: { label: "等待中", type: "warning" as const },
  running: { label: "运行中", type: "primary" as const },
  complete: { label: "已完成", type: "success" as const },
  failed: { label: "失败", type: "danger" as const },
}[value]);
const progress = (job: BillingJob) => job.total_steps ? Math.min(100, Math.round(job.completed_steps * 100 / job.total_steps)) : 0;
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : "—";
const formatRange = (job: BillingJob) => job.range_from && job.range_to ? `${formatTime(job.range_from)} 至 ${formatTime(job.range_to)}` : "—";

let disposed = false;
async function poll() {
  while (!disposed) {
    await new Promise((resolve) => setTimeout(resolve, 3000));
    if (!disposed) await state.reload();
  }
}
onUnmounted(() => { disposed = true; });
void state.reload().then(poll);
</script>

<template>
  <AppShell title="后台任务中心">
    <template #tools>
      <el-switch v-model="showAllSites" active-text="全部站点" inactive-text="当前站点" @change="state.reload" />
      <el-button @click="state.reload">刷新</el-button>
    </template>
    <el-alert v-if="activeCount" type="warning" :title="`当前有 ${activeCount} 个活动任务`" description="账单任务串行执行；当前任务结束前不能创建新的账单后台任务。" :closable="false" show-icon />
    <el-alert v-else type="success" title="当前没有运行中或等待中的账单任务" :closable="false" show-icon />
    <AsyncPanel class="tasks-panel" :loading="state.loading.value" :error="state.error.value" :empty="!items.length" empty-text="暂无后台任务" @retry="state.reload">
      <el-table :data="items" row-key="id">
        <el-table-column label="任务" min-width="185"><template #default="s"><b>{{ typeLabel(s.row.job_type) }}</b><small>{{ s.row.id }}</small></template></el-table-column>
        <el-table-column prop="instance_id" label="站点" min-width="130" />
        <el-table-column label="状态" width="100"><template #default="s"><el-tag :type="statusMeta(s.row.status).type">{{ statusMeta(s.row.status).label }}</el-tag></template></el-table-column>
        <el-table-column label="进度" min-width="190"><template #default="s"><el-progress :percentage="progress(s.row)" :status="s.row.status === 'failed' ? 'exception' : s.row.status === 'complete' ? 'success' : undefined" /><small>{{ s.row.completed_steps }} / {{ s.row.total_steps }}</small></template></el-table-column>
        <el-table-column label="任务区间" min-width="300"><template #default="s">{{ formatRange(s.row) }}</template></el-table-column>
        <el-table-column prop="requested_by" label="创建人" width="110" />
        <el-table-column label="创建时间" min-width="170"><template #default="s">{{ formatTime(s.row.created_at) }}</template></el-table-column>
        <el-table-column label="错误" min-width="220" show-overflow-tooltip><template #default="s">{{ s.row.error_message || "—" }}</template></el-table-column>
      </el-table>
    </AsyncPanel>
  </AppShell>
</template>

<style scoped>
.tasks-panel{margin-top:12px}b,small{display:block}small{margin-top:4px;color:var(--el-text-color-secondary);font-size:12px;font-variant-numeric:tabular-nums}
</style>
