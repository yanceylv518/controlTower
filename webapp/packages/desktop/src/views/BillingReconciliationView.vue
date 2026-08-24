<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { useRouter } from "vue-router";
import { ApiError, type BillingReconciliationRow } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { savedGenerationRange, timeRangeShortcuts, validateGenerationRange } from "../utils/billingRange";
import { formatNumber } from "../utils/format";
import { billingTaskErrorMessage } from "../utils/httpError";

const filters = useFiltersStore();
const prefs = usePrefsStore();
const router = useRouter();
const range = ref<[string, string]>(savedGenerationRange("ct.billing.reconciliation.range"));
const selectedUser = ref<BillingReconciliationRow>();
const selectedScope = ref<BillingReconciliationRow>();
const detailOpen = ref(false);
const requestsOpen = ref(false);
const verificationPage = ref(1);
const verificationJobID = ref("");
let verificationTimer: ReturnType<typeof setTimeout> | undefined;

const report = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return undefined;
  const [from, to] = range.value;
  return dashboard.billingReconciliation({ instance_id: filters.site_id, from, to });
});
const unavailableState = computed(() => {
  const error = report.lastRefreshError.value;
  if (!(error instanceof ApiError)) return "";
  if (error.code === "billing_not_generated") return "not_generated";
  if (error.code === "billing_generating") return "generating";
  return "";
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
const verification = useAsyncData(async () => {
  const sourceJobID = report.data.value?.job.id;
  if (!sourceJobID) return undefined;
  return dashboard.billingVerification({ source_job_id: sourceJobID, job_id: verificationJobID.value || undefined, page: verificationPage.value, page_size: 50, mismatches_only: true });
});
const verificationProgress = computed(() => {
  const job = verification.data.value?.job;
  return job?.total_steps ? Math.min(100, Math.round(job.completed_steps * 100 / job.total_steps)) : 0;
});
const verificationStatusText = (status?: string) => ({ pending: "等待中", running: "核验中", complete: "已完成", failed: "失败" }[status || ""] || status || "");

const csvURL = computed(() => {
  const [from, to] = range.value;
  const params = new URLSearchParams({ instance_id: filters.site_id, from, to, format: "csv" });
  if (report.data.value?.job.id) params.set("job_id", report.data.value.job.id);
  return `/api/dashboard/billing/reconciliation?${params}`;
});
const currency = computed(() => report.data.value?.currency?.symbol || prefs.currencySymbol || "$");
const currencyRate = computed(() => {
  const rate = Number(report.data.value?.currency?.exchange_rate);
  return Number.isFinite(rate) && rate > 0 ? rate : 1;
});
const money = (value?: string) => `${currency.value}${(Number(value || 0) * currencyRate.value).toFixed(6)}`;
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
function scheduleVerificationPoll() {
  if (verificationTimer) clearTimeout(verificationTimer);
  const status = verification.data.value?.job?.status;
  if (status === "pending" || status === "running") verificationTimer = setTimeout(async () => { await verification.refresh(); scheduleVerificationPoll(); }, 2000);
}
async function loadVerification() {
  verificationJobID.value = "";
  verificationPage.value = 1;
  await verification.reload();
  verificationJobID.value = verification.data.value?.job?.id || "";
  scheduleVerificationPoll();
}
async function startVerification() {
  const sourceJobID = report.data.value?.job.id;
  if (!sourceJobID) return;
  try {
    const result = await dashboard.startBillingVerification(sourceJobID);
    verificationJobID.value = result.job.id;
    verificationPage.value = 1;
    await verification.reload();
    scheduleVerificationPoll();
    ElMessage.success(result.reused ? "已恢复现有全量核验任务" : "全量核验任务已启动");
  } catch (error) { ElMessage.warning(billingTaskErrorMessage(error, "全量核验任务创建失败")); }
}

watch(range, value => localStorage.setItem("ct.billing.reconciliation.range", JSON.stringify(value)), { deep: true });
watch(() => filters.site_id, () => { selectedUser.value = undefined; detailOpen.value = false; void report.reload(); });
watch(() => report.data.value?.job.id, value => { if (value) void loadVerification(); });
watch(verificationPage, () => { void verification.reload(); });
onBeforeUnmount(() => { if (verificationTimer) clearTimeout(verificationTimer); });
void report.reload();
</script>

<template>
  <AppShell title="计费异常">
    <template #tools>
      <span class="period-label">核对区间</span>
      <el-date-picker v-model="range" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" format="YYYY-MM-DD HH:mm:ss" range-separator="至" :shortcuts="timeRangeShortcuts" unlink-panels style="width:420px" />
      <el-button type="primary" :loading="report.loading.value" @click="reconcile">核对账单</el-button>
      <el-button v-if="report.data.value?.job.id" :loading="verification.loading.value || ['pending','running'].includes(verification.data.value?.job?.status || '')" @click="startVerification">全量核验</el-button>
      <el-button v-if="report.data.value" tag="a" :href="csvURL">导出核对 CSV</el-button>
    </template>

    <el-empty v-if="unavailableState" class="bill-empty" :description="unavailableState === 'generating' ? '该区间的账单正在生成，生成完成后即可核对' : '该区间还没有可核对的账单，请先创建账单生成任务'">
      <el-button type="primary" @click="router.push('/billing/tasks')">前往账单任务</el-button>
      <el-button v-if="unavailableState === 'generating'" @click="report.reload">刷新状态</el-button>
    </el-empty>
    <AsyncPanel v-else :loading="report.loading.value" :error="report.error.value" :empty="!report.data.value?.items.length" empty-text="该区间没有计费异常" @retry="report.reload">
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
      <p v-if="report.data.value" class="breakdown-note">
        剩余差额 = 总差额 - 异常订单差额 - 缓存写策略差额；下表“剩余差额”列合计与顶部一致。
      </p>

      <el-table :data="report.data.value?.items || []" :row-class-name="reconciliationRowClass">
        <el-table-column label="用户" min-width="180"><template #default="s"><b>{{ s.row.username || `用户 ${s.row.user_id}` }}</b><small>ID {{ s.row.user_id }}</small></template></el-table-column>
        <el-table-column prop="request_count" label="计费请求" min-width="105" align="right"><template #default="s">{{ formatNumber(s.row.request_count) }}</template></el-table-column>
        <el-table-column prop="abnormal_rows" label="异常订单" min-width="105" align="right"><template #default="s">{{ formatNumber(s.row.abnormal_rows) }}</template></el-table-column>
        <el-table-column label="CT 金额" min-width="130" align="right"><template #default="s">{{ money(s.row.ct_amount) }}</template></el-table-column>
        <el-table-column label="new-api 实扣" min-width="140" align="right"><template #default="s">{{ money(s.row.actual_amount) }}</template></el-table-column>
        <el-table-column label="总差额" min-width="125" align="right"><template #default="s"><b>{{ money(s.row.diff_amount) }}</b></template></el-table-column>
        <el-table-column label="剩余差额" min-width="125" align="right"><template #default="s"><b>{{ money(s.row.breakdown.residual) }}</b></template></el-table-column>
        <el-table-column label="差额率" min-width="100" align="right"><template #default="s">{{ percent(s.row.diff_rate) }}</template></el-table-column>
        <el-table-column label="主要原因" min-width="145"><template #default="s"><el-tag v-if="s.row.fallback_priced" type="info">回退计价（差额无参考性）</el-tag><el-tag v-else :type="s.row.classification === 'residual' ? 'danger' : 'warning'">{{ classText(s.row.classification) }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="90" fixed="right"><template #default="s"><el-button link type="primary" @click="openDetail(s.row)">下钻</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>

    <section v-if="report.data.value" class="verification-panel">
      <div class="verification-heading">
        <div><h3>后台全量核验</h3><p>分页复扫该生成批次的 new-api 消费日志，并与 CT 正常账单、异常订单逐日核对。</p></div>
        <el-tag v-if="verification.data.value?.job" :type="verification.data.value.job.status === 'complete' ? (verification.data.value.summary.mismatched_rows ? 'danger' : 'success') : verification.data.value.job.status === 'failed' ? 'danger' : 'warning'">
          {{ verificationStatusText(verification.data.value.job.status) }}
        </el-tag>
      </div>
      <el-progress v-if="['pending','running'].includes(verification.data.value?.job?.status || '')" :percentage="verificationProgress" :stroke-width="10" />
      <el-alert v-if="verification.data.value?.job?.status === 'failed'" type="error" :closable="false" :title="verification.data.value.job.error_message || '全量核验失败，可重新启动任务'" />
      <el-empty v-else-if="!verification.data.value?.job" description="尚未启动全量核验" :image-size="80" />
      <template v-else-if="verification.data.value.job.status === 'complete'">
        <div class="verification-cards">
          <span>原始日志 <b>{{ formatNumber(verification.data.value.summary.source_rows) }}</b></span>
          <span>正常 复扫/账单 <b>{{ formatNumber(verification.data.value.summary.verified_normal_rows) }} / {{ formatNumber(verification.data.value.summary.billed_normal_rows) }}</b></span>
          <span>异常 复扫/落库 <b>{{ formatNumber(verification.data.value.summary.verified_abnormal_rows) }} / {{ formatNumber(verification.data.value.summary.billed_abnormal_rows) }}</b></span>
          <span>匹配维度 <b>{{ formatNumber(verification.data.value.summary.matched_rows) }}</b></span>
          <span>差异维度 <b class="danger-text">{{ formatNumber(verification.data.value.summary.mismatched_rows) }}</b></span>
        </div>
        <el-alert v-if="!verification.data.value.summary.mismatched_rows" type="success" :closable="false" title="全量核验通过：原始日志、正常账单和异常订单一致" />
        <el-table v-else :data="verification.data.value.items" class="verification-table">
          <el-table-column prop="day" label="日期" width="110" />
          <el-table-column label="用户" min-width="150"><template #default="s">{{ s.row.username || `用户 ${s.row.user_id}` }}</template></el-table-column>
          <el-table-column prop="model_name" label="模型" min-width="150" />
          <el-table-column prop="group_name" label="分组" min-width="90" />
          <el-table-column label="原始日志" width="105" align="right"><template #default="s">{{ formatNumber(s.row.source_rows) }}</template></el-table-column>
          <el-table-column label="正常 复扫/账单" min-width="150" align="right"><template #default="s">{{ formatNumber(s.row.verified_normal_rows) }} / {{ formatNumber(s.row.billed_normal_rows) }}</template></el-table-column>
          <el-table-column label="异常 复扫/落库" min-width="150" align="right"><template #default="s">{{ formatNumber(s.row.verified_abnormal_rows) }} / {{ formatNumber(s.row.billed_abnormal_rows) }}</template></el-table-column>
          <el-table-column label="Quota 原始/正常复扫/正常账单/异常复扫/异常落库" min-width="390" align="right"><template #default="s">{{ formatNumber(s.row.source_quota) }} / {{ formatNumber(s.row.verified_normal_quota) }} / {{ formatNumber(s.row.billed_normal_quota) }} / {{ formatNumber(s.row.verified_abnormal_quota) }} / {{ formatNumber(s.row.billed_abnormal_quota) }}</template></el-table-column>
        </el-table>
        <el-pagination v-if="verification.data.value.total > verification.data.value.page_size" v-model:current-page="verificationPage" :page-size="verification.data.value.page_size" :total="verification.data.value.total" layout="total, prev, pager, next" />
      </template>
    </section>

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
.cards{display:grid;grid-template-columns:repeat(6,minmax(150px,1fr));gap:10px;margin-bottom:8px}.cards>div{border:1px solid var(--el-border-color);border-radius:8px;padding:12px;background:var(--el-fill-color-blank)}.cards .danger-card{border-color:var(--el-color-danger-light-5);background:var(--el-color-danger-light-9)}.cards .danger-card b{color:var(--el-color-danger)}.cards span{display:block;color:var(--el-text-color-secondary);font-size:12px}.cards b{display:block;margin-top:6px;font-size:17px;font-variant-numeric:tabular-nums}.breakdown-note{margin:0 2px 10px;color:var(--el-text-color-secondary);font-size:12px}.lane-totals{display:flex;gap:24px;padding:12px 2px;color:var(--el-text-color-secondary)}.lane-totals b{color:var(--el-text-color-primary);font-variant-numeric:tabular-nums}.el-table small{display:block;margin-top:3px}.request-table{margin-top:4px}:deep(.fallback-row){opacity:.55}:deep(.el-table .cell){font-variant-numeric:tabular-nums}@media(max-width:1400px){.cards{grid-template-columns:repeat(3,1fr)}}
.verification-panel{margin-top:18px;padding:16px;border:1px solid var(--el-border-color);border-radius:8px;background:var(--el-fill-color-blank)}.verification-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.verification-heading h3{margin:0 0 5px}.verification-heading p{margin:0 0 14px;color:var(--el-text-color-secondary);font-size:12px}.verification-cards{display:flex;flex-wrap:wrap;gap:12px 30px;margin:14px 0}.verification-cards span{color:var(--el-text-color-secondary)}.verification-cards b{color:var(--el-text-color-primary);font-variant-numeric:tabular-nums}.verification-cards .danger-text{color:var(--el-color-danger)}.verification-table{margin-top:12px}.verification-panel .el-pagination{justify-content:flex-end;margin-top:12px}
.bill-empty{min-height:420px}
</style>
