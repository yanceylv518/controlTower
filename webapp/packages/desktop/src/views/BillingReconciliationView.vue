<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { BillingReconciliationRow } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { savedGenerationRange, timeRangeShortcuts, validateGenerationRange } from "../utils/billingRange";
import { formatNumber } from "../utils/format";

const filters = useFiltersStore();
const range = ref<[string, string]>(savedGenerationRange("ct.billing.reconciliation.range"));
const selectedUser = ref<BillingReconciliationRow>();
const selectedScope = ref<BillingReconciliationRow>();
const detailOpen = ref(false);
const requestsOpen = ref(false);

const report = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return undefined;
  const [from, to] = range.value;
  return dashboard.billingReconciliation({ instance_id: filters.site_id, from, to });
});
const detail = useAsyncData(async () => {
  if (!selectedUser.value || !report.data.value?.job.id) return undefined;
  const [from, to] = range.value;
  return dashboard.billingReconciliation({
    instance_id: filters.site_id,
    from,
    to,
    job_id: report.data.value.job.id,
    user_id: selectedUser.value.user_id,
  });
});
const requests = useAsyncData(async () => {
  const row = selectedScope.value;
  if (!row?.day || !row.model_name || !report.data.value?.job.id) return undefined;
  const [from, to] = range.value;
  return dashboard.billingReconciliationRequests({
    instance_id: filters.site_id,
    from,
    to,
    job_id: report.data.value.job.id,
    user_id: row.user_id,
    day: row.day,
    model_name: row.model_name,
  });
});

const csvURL = computed(() => {
  const [from, to] = range.value;
  const params = new URLSearchParams({ instance_id: filters.site_id, from, to, format: "csv" });
  if (report.data.value?.job.id) params.set("job_id", report.data.value.job.id);
  return `/api/dashboard/billing/reconciliation?${params}`;
});
const money = (value?: string) => `$${Number(value || 0).toFixed(6)}`;
const percent = (value?: string) => `${(Number(value || 0) * 100).toFixed(3)}%`;
const classText = (value: string) => ({ anomaly: "异常订单", cache_write_policy: "缓存写策略", residual: "剩余差额" }[value] || value);
const reconciliationRowClass = ({ row }: { row: BillingReconciliationRow }) => row.fallback_priced ? "fallback-row" : "";

async function reconcile() {
  const invalid = validateGenerationRange(range.value);
  if (invalid) { ElMessage.warning(invalid); return; }
  await report.reload();
}
async function openDetail(row: BillingReconciliationRow) {
  selectedUser.value = row;
  detailOpen.value = true;
  await detail.reload();
}
async function openRequests(row: BillingReconciliationRow) {
  selectedScope.value = row;
  requestsOpen.value = true;
  await requests.reload();
}

watch(range, value => localStorage.setItem("ct.billing.reconciliation.range", JSON.stringify(value)), { deep: true });
watch(() => filters.site_id, () => { selectedUser.value = undefined; detailOpen.value = false; void report.reload(); });
void report.reload();
</script>

<template>
  <AppShell title="账单核对">
    <template #tools>
      <span class="period-label">核对区间</span>
      <el-date-picker v-model="range" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" format="YYYY-MM-DD HH:mm:ss" range-separator="至" :shortcuts="timeRangeShortcuts" unlink-panels style="width:420px" />
      <el-button type="primary" :loading="report.loading.value" @click="reconcile">核对账单</el-button>
      <el-button v-if="report.data.value" tag="a" :href="csvURL">导出核对 CSV</el-button>
    </template>

    <AsyncPanel :loading="report.loading.value" :error="report.error.value" :empty="!report.data.value?.items.length" @retry="report.reload">
      <div v-if="report.data.value" class="range-note">
        核对批次 <b>{{ report.data.value.job.id }}</b> · [{{ report.data.value.range_from }}, {{ report.data.value.range_to }})
      </div>
      <div v-if="report.data.value" class="cards">
        <div><span>CT 当前账单</span><b>{{ money(report.data.value.totals.ct_amount) }}</b></div>
        <div><span>new-api 实扣</span><b>{{ money(report.data.value.totals.actual_amount) }}</b></div>
        <div><span>总差额</span><b>{{ money(report.data.value.totals.diff_amount) }}</b></div>
        <div><span>异常订单差额</span><b>{{ money(report.data.value.totals.breakdown.anomaly) }}</b></div>
        <div><span>缓存写策略差额（估算）</span><b>{{ money(report.data.value.totals.breakdown.cache_write_policy) }}</b></div>
        <div :class="{ 'danger-card': Number(report.data.value.totals.breakdown.residual) !== 0 }"><span>剩余差额</span><b>{{ money(report.data.value.totals.breakdown.residual) }}</b></div>
      </div>

      <el-table :data="report.data.value?.items || []" :row-class-name="reconciliationRowClass">
        <el-table-column label="用户" min-width="180"><template #default="s"><b>{{ s.row.username || `用户 ${s.row.user_id}` }}</b><small>ID {{ s.row.user_id }}</small></template></el-table-column>
        <el-table-column prop="request_count" label="计费请求" min-width="105" align="right"><template #default="s">{{ formatNumber(s.row.request_count) }}</template></el-table-column>
        <el-table-column prop="abnormal_rows" label="异常订单" min-width="105" align="right"><template #default="s">{{ formatNumber(s.row.abnormal_rows) }}</template></el-table-column>
        <el-table-column label="CT 金额" min-width="130" align="right"><template #default="s">{{ money(s.row.ct_amount) }}</template></el-table-column>
        <el-table-column label="new-api 实扣" min-width="140" align="right"><template #default="s">{{ money(s.row.actual_amount) }}</template></el-table-column>
        <el-table-column label="差额" min-width="125" align="right"><template #default="s"><b>{{ money(s.row.diff_amount) }}</b></template></el-table-column>
        <el-table-column label="差额率" min-width="100" align="right"><template #default="s">{{ percent(s.row.diff_rate) }}</template></el-table-column>
        <el-table-column label="主要分类" min-width="145"><template #default="s"><el-tag v-if="s.row.fallback_priced" type="info">回退计价（差额无参考性）</el-tag><el-tag v-else :type="s.row.classification === 'residual' ? 'danger' : 'warning'">{{ classText(s.row.classification) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="90" fixed="right"><template #default="s"><el-button link type="primary" @click="openDetail(s.row)">下钻</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>

    <el-drawer v-model="detailOpen" :title="`${selectedUser?.username || '用户'} · L2 日/模型/分组核对`" size="86%">
      <AsyncPanel :loading="detail.loading.value" :error="detail.error.value" :empty="!detail.data.value?.items.length" @retry="detail.reload">
        <el-table :data="detail.data.value?.items || []" :row-class-name="reconciliationRowClass">
          <el-table-column prop="day" label="日期" width="110" />
          <el-table-column prop="model_name" label="模型" min-width="170" />
          <el-table-column prop="group_name" label="分组" min-width="105" />
          <el-table-column prop="request_count" label="请求" min-width="90" align="right"><template #default="s">{{ formatNumber(s.row.request_count) }}</template></el-table-column>
          <el-table-column label="CT 金额" min-width="120" align="right"><template #default="s">{{ money(s.row.ct_amount) }}</template></el-table-column>
          <el-table-column label="实扣" min-width="120" align="right"><template #default="s">{{ money(s.row.actual_amount) }}</template></el-table-column>
          <el-table-column label="差额" min-width="120" align="right"><template #default="s">{{ money(s.row.diff_amount) }}</template></el-table-column>
          <el-table-column label="分解（异常 / 缓存写 / 剩余）" min-width="260"><template #default="s">{{ money(s.row.breakdown.anomaly) }} / {{ money(s.row.breakdown.cache_write_policy) }} / {{ money(s.row.breakdown.residual) }}</template></el-table-column>
          <el-table-column label="分类" min-width="145"><template #default="s">{{ s.row.fallback_priced ? "回退计价（无参考性）" : classText(s.row.classification) }}</template></el-table-column>
          <el-table-column label="操作" width="110" fixed="right"><template #default="s"><el-button link type="primary" :disabled="s.row.fallback_priced" @click="openRequests(s.row)">请求核对</el-button></template></el-table-column>
        </el-table>
      </AsyncPanel>
    </el-drawer>

    <el-drawer v-model="requestsOpen" :title="`${selectedScope?.day || ''} · ${selectedScope?.model_name || ''} · L3 请求核对`" size="88%">
      <AsyncPanel :loading="requests.loading.value" :error="requests.error.value" :empty="!requests.data.value?.items.length" @retry="requests.reload">
        <el-alert v-if="requests.data.value" :type="requests.data.value.truncated || Number(requests.data.value.rebuild_residual) !== 0 ? 'warning' : 'info'" :closable="false" :title="`扫描 ${formatNumber(requests.data.value.scanned)} 条，匹配 ${formatNumber(requests.data.value.matched)} 条，重建残差 ${money(requests.data.value.rebuild_residual)}${Number(requests.data.value.rebuild_residual) !== 0 ? '；行内倍率不完整，重建口径不可全信' : ''}${requests.data.value.truncated ? `；仅分析前 ${formatNumber(requests.data.value.scanned)} 条（已达到扫描上限）` : ''}`" />
        <div v-if="requests.data.value" class="lane-totals"><span>输入差额 <b>{{ money(requests.data.value.component_diffs.input) }}</b></span><span>输出差额 <b>{{ money(requests.data.value.component_diffs.output) }}</b></span><span>缓存读差额 <b>{{ money(requests.data.value.component_diffs.cache_read) }}</b></span><span>缓存写差额 <b>{{ money(requests.data.value.component_diffs.cache_write) }}</b></span><span>分组差额 <b>{{ money(requests.data.value.component_diffs.group) }}</b></span></div>
        <el-table :data="requests.data.value?.items || []" class="request-table">
          <el-table-column prop="created_at" label="时间" min-width="165" />
          <el-table-column prop="request_id" label="Request ID" min-width="250" show-overflow-tooltip />
          <el-table-column label="实扣" min-width="110" align="right"><template #default="s">{{ money(s.row.actual_amount) }}</template></el-table-column>
          <el-table-column label="重建" min-width="110" align="right"><template #default="s">{{ money(s.row.rebuilt_amount) }}</template></el-table-column>
          <el-table-column label="CT" min-width="110" align="right"><template #default="s">{{ money(s.row.ct_amount) }}</template></el-table-column>
          <el-table-column label="差额" min-width="110" align="right"><template #default="s"><b>{{ money(s.row.diff_amount) }}</b></template></el-table-column>
          <el-table-column label="分量差额（输入 / 输出 / 读取 / 写入 / 分组）" min-width="360"><template #default="s">{{ money(s.row.input_diff) }} / {{ money(s.row.output_diff) }} / {{ money(s.row.cache_read_diff) }} / {{ money(s.row.cache_write_diff) }} / {{ money(s.row.group_diff) }}</template></el-table-column>
          <el-table-column label="状态" min-width="100"><template #default="s"><el-tag v-if="s.row.unexplained" type="danger">无法解释</el-tag><el-tag v-else type="success">已分解</el-tag></template></el-table-column>
        </el-table>
      </AsyncPanel>
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.period-label,.range-note,small{color:var(--el-text-color-secondary);font-size:12px}.range-note{padding:2px 2px 10px}.range-note b{color:var(--el-text-color-primary)}
.cards{display:grid;grid-template-columns:repeat(6,minmax(150px,1fr));gap:10px;margin-bottom:14px}.cards>div{border:1px solid var(--el-border-color);border-radius:8px;padding:12px;background:var(--el-fill-color-blank)}.cards .danger-card{border-color:var(--el-color-danger-light-5);background:var(--el-color-danger-light-9)}.cards .danger-card b{color:var(--el-color-danger)}.cards span{display:block;color:var(--el-text-color-secondary);font-size:12px}.cards b{display:block;margin-top:6px;font-size:17px;font-variant-numeric:tabular-nums}.lane-totals{display:flex;gap:24px;padding:12px 2px;color:var(--el-text-color-secondary)}.lane-totals b{color:var(--el-text-color-primary);font-variant-numeric:tabular-nums}.el-table small{display:block;margin-top:3px}.request-table{margin-top:4px}:deep(.fallback-row){opacity:.55}:deep(.el-table .cell){font-variant-numeric:tabular-nums}@media(max-width:1400px){.cards{grid-template-columns:repeat(3,1fr)}}
</style>
