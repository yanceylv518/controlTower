<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import type { BillingJob, BillingJobStep, BillingUpstream, ReadonlyUser } from "@ct/shared";
import { dashboard, passthrough } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { billingReadErrorMessage, billingTaskErrorMessage } from "../utils/httpError";
import { formatNumber } from "../utils/format";

type TaskTab = "active" | "failed" | "complete";
const filters = useFiltersStore();
const router = useRouter();
const route = useRoute();
const activeTab = ref<TaskTab>("active");
const createVisible = ref(false);
const creating = ref(false);
const userLoading = ref(false);
const userOptions = ref<ReadonlyUser[]>([]);
const upstreamLoading = ref(false);
const upstreamOptions = ref<BillingUpstream[]>([]);
const jobSteps = ref<Record<string, BillingJobStep[]>>({});
const stepLoading = ref<Record<string, boolean>>({});
const expandedJobIDs = ref<string[]>([]);
const createForm = ref<{ instance_id: string; bill_type: "user" | "upstream"; user_id?: number; upstream_id?: number; range: [Date, Date] | [] }>({ instance_id: "", bill_type: "user", range: [] });
const state = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return { items: [] as BillingJob[] };
  return dashboard.billingJobs({ instance_id: filters.site_id, limit: 200 });
});
const items = computed(() => (state.data.value?.items || []).filter((job) => job.job_type === "user_statement" || job.job_type === "upstream_statement"));
const isCancelled = (job: BillingJob) => job.status === "failed" && job.error_message === "cancelled manually";
const counts = computed(() => ({
  active: items.value.filter((job) => job.status === "pending" || job.status === "running" || job.status === "publishing").length,
  queued: items.value.filter((job) => job.status === "pending").length,
  running: items.value.filter((job) => job.status === "running" || job.status === "publishing").length,
  failed: items.value.filter((job) => job.status === "failed").length,
  complete: items.value.filter((job) => job.status === "complete").length,
}));
const visibleItems = computed(() => items.value.filter((job) => activeTab.value === "active"
  ? job.status === "pending" || job.status === "running" || job.status === "publishing"
  : activeTab.value === "failed" ? job.status === "failed" : job.status === "complete"));
const typeLabel = (value?: string) => ({ user_statement: "用户账单", upstream_statement: "上游账单" }[value || ""] || value || "账单任务");
const statusMeta = (job: BillingJob) => isCancelled(job) ? { label: "已取消", type: "info" as const } : ({
  pending: { label: "等待中", type: "warning" as const }, running: { label: "计算中", type: "primary" as const }, publishing: { label: "生成文件", type: "primary" as const },
  complete: { label: "已完成", type: "success" as const }, failed: { label: "失败", type: "danger" as const },
}[job.status]);
const progress = (job: BillingJob) => job.total_steps ? Math.min(100, Math.round(job.completed_steps * 100 / job.total_steps)) : 0;
const queuePosition = (job: BillingJob) => items.value.filter((item) => item.status === "pending").findIndex((item) => item.id === job.id) + 1;
const stepStatus = (status:BillingJobStep["status"]) => ({pending:["等待中","warning"],running:["处理中","primary"],complete:["已完成","success"],failed:["失败","danger"]} as const)[status];
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : "—";
const dateText = (value?: string) => value ? new Date(value).toLocaleDateString() : "—";
const formatRange = (job: BillingJob) => job.range_from && job.range_to ? `${dateText(job.range_from)} 至 ${dateText(new Date(new Date(job.range_to).getTime() - 1).toISOString())}` : "—";
const pad = (value: number) => String(value).padStart(2, "0");
const dayStart = (value: Date) => `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())} 00:00:00`;
const dayAfter = (value: Date) => { const next = new Date(value); next.setHours(0, 0, 0, 0); next.setDate(next.getDate() + 1); return dayStart(next); };
const shortError = (job: BillingJob) => {
  if (isCancelled(job)) return "管理员手动停止";
  const error = job.error_message || "未知错误";
  if (/Incorrect decimal value|Error 1366/i.test(error)) return "计费数据格式异常";
  if (/timeout|deadline exceeded/i.test(error)) return "上游请求超时";
  return error.length > 60 ? `${error.slice(0, 60)}…` : error;
};
async function loadUsers(keyword = "") {
  if (!createForm.value.instance_id) { userOptions.value = []; return; }
  userLoading.value = true;
  try { userOptions.value = (await passthrough.users({ site: createForm.value.instance_id, keyword: keyword.trim() || undefined, limit: 100, offset: 0 })).items; }
  catch (error) { userOptions.value = []; ElMessage.warning(billingReadErrorMessage(error, "用户列表加载失败")); }
  finally { userLoading.value = false; }
}
async function loadUpstreams() {
  if (!createForm.value.instance_id) { upstreamOptions.value = []; return; }
  upstreamLoading.value = true;
  try { upstreamOptions.value = (await dashboard.billingUpstreams(createForm.value.instance_id)).items.filter((item) => item.enabled); }
  catch (error) { upstreamOptions.value = []; ElMessage.warning(billingReadErrorMessage(error, "上游列表加载失败")); }
  finally { upstreamLoading.value = false; }
}
async function openCreate(job?: BillingJob) {
  const yesterday = new Date(); yesterday.setHours(0, 0, 0, 0); yesterday.setDate(yesterday.getDate() - 1);
  const queryFrom = typeof route.query.from === "string" ? route.query.from.slice(0, 10) : "";
  const queryThrough = typeof route.query.through === "string" ? route.query.through.slice(0, 10) : "";
  const querySite = typeof route.query.site === "string" ? route.query.site : "";
  const queryBillType = route.query.bill_type === "upstream" ? "upstream" : "user";
  const from = job?.range_from ? new Date(job.range_from) : queryFrom ? new Date(`${queryFrom}T00:00:00`) : yesterday;
  const through = job?.range_to ? new Date(new Date(job.range_to).getTime() - 1) : queryThrough ? new Date(`${queryThrough}T00:00:00`) : yesterday;
  createForm.value = { instance_id: job?.instance_id || querySite || filters.site_id || "", bill_type: job ? (job.job_type === "upstream_statement" ? "upstream" : "user") : queryBillType, user_id: job?.user_id || undefined, upstream_id: job?.upstream_id || undefined, range: [from, through] };
  createVisible.value = true;
  await Promise.all([loadUsers(job?.user_id ? String(job.user_id) : ""), loadUpstreams()]);
}
async function changeCreateSite() { createForm.value.user_id = undefined; createForm.value.upstream_id = undefined; await Promise.all([loadUsers(), loadUpstreams()]); }
async function createJob() {
  const form = createForm.value;
  if (!form.instance_id || form.range.length !== 2 || (form.bill_type === "user" ? !form.user_id : !form.upstream_id)) return;
  if (counts.value.queued >= 5) { ElMessage.warning("等待队列已满，最多允许 5 个任务排队"); return; }
  const wantedType = form.bill_type === "user" ? "user_statement" : "upstream_statement";
  const wantedFrom = dayStart(form.range[0]);
  const wantedTo = dayAfter(form.range[1]);
  const duplicate = items.value.some((job) => job.status !== "failed" && job.job_type === wantedType
    && (form.bill_type === "user" ? job.user_id === form.user_id : job.upstream_id === form.upstream_id)
    && job.range_from?.slice(0, 10) === wantedFrom.slice(0, 10)
    && job.range_to?.slice(0, 10) === wantedTo.slice(0, 10));
  if (duplicate) { ElMessage.warning("相同对象和账期的账单任务已存在，不能重复创建"); return; }
  creating.value = true;
  try {
    await dashboard.createBillingStatement({instance_id:form.instance_id,statement_type:form.bill_type,user_id:form.bill_type==="user"?form.user_id:undefined,upstream_id:form.bill_type==="upstream"?form.upstream_id:undefined,from:wantedFrom,to:wantedTo});
    createVisible.value=false;activeTab.value="active";ElMessage.success("账单任务已加入队列");await state.reload();
  } catch(error) { ElMessage.error(billingTaskErrorMessage(error,"账单任务创建失败")); }
  finally { creating.value=false; }
}
async function stopJob(job: BillingJob) {
  try { await ElMessageBox.confirm("确定停止当前账单任务吗？已生效的历史账单不会受影响。", "停止任务", { type: "warning" }); await dashboard.cancelBillingJob(job.id); await state.reload(); }
  catch (error) { if (error !== "cancel" && error !== "close") ElMessage.error(billingTaskErrorMessage(error, "停止任务失败")); }
}
async function deleteFailedJob(job: BillingJob) {
  try { await ElMessageBox.confirm("确定删除这条任务吗？任务记录和未生效的中间数据将被清理。", "删除任务", { type: "warning", confirmButtonText: "删除" }); await dashboard.deleteFailedBillingJob(job.id); ElMessage.success("任务已删除"); await state.reload(); }
  catch (error) { if (error !== "cancel" && error !== "close") ElMessage.error(billingReadErrorMessage(error, "任务删除失败")); }
}
async function viewBill(job: BillingJob) {
	if (!job.range_from || !job.range_to) return;
	filters.selectSite(job.instance_id);
	const actualDay = job.output_latest_day;
	await router.push({ path: job.job_type === "upstream_statement" ? "/billing/channels" : "/billing", query: { site: job.instance_id, bill_id: job.id } });
}
async function loadJobSteps(jobID:string, force=false) {
  if ((!force && jobSteps.value[jobID]) || stepLoading.value[jobID]) return;
  stepLoading.value={...stepLoading.value,[jobID]:true};
  try { jobSteps.value={...jobSteps.value,[jobID]:(await dashboard.billingJobSteps(jobID)).items}; }
  catch(error){ ElMessage.error(billingReadErrorMessage(error,"日任务加载失败")); }
  finally { stepLoading.value={...stepLoading.value,[jobID]:false}; }
}
async function expandJob(job:BillingJob, expanded:BillingJob[]) {
  expandedJobIDs.value=expanded.map((item)=>item.id);
  if (!expandedJobIDs.value.includes(job.id)) return;
  await loadJobSteps(job.id);
}
let disposed = false;
async function poll() {
  while (!disposed) {
    await new Promise((resolve) => setTimeout(resolve, 3000));
    if (disposed) continue;
    // Only track live work. A full reload toggles the panel loading state and
    // makes the whole task table flash every three seconds.
    if (!counts.value.active) continue;
    await state.refresh();
    await Promise.all(expandedJobIDs.value.map((jobID)=>loadJobSteps(jobID,true)));
  }
}
onUnmounted(() => { disposed = true; });
watch(()=>filters.site_id,()=>void state.reload());
void state.reload().then(async()=>{if(route.query.create==="1")await openCreate();await poll()});
</script>

<template>
  <AppShell title="账单任务">
    <template #tools><el-button type="primary" :disabled="counts.queued >= 5" @click="openCreate()">新增账单任务</el-button><el-button @click="state.reload">刷新</el-button></template>
    <el-alert v-if="counts.active" type="warning" :title="counts.running ? `当前 1 个任务正在执行，${counts.queued} / 5 个任务排队等待` : `当前有 ${counts.queued} / 5 个任务等待执行`" description="系统同一时间只执行一个账单任务，等待队列最多 5 个；相同站点、类型、对象和账期的任务不能重复创建。" :closable="false" show-icon />
    <el-alert v-else type="success" title="当前没有正在处理的账单任务" :closable="false" show-icon />
    <el-tabs v-model="activeTab" class="task-tabs">
      <el-tab-pane name="active"><template #label>进行中 <el-badge :value="counts.active" :hidden="!counts.active" /></template></el-tab-pane>
      <el-tab-pane name="failed"><template #label>失败与取消 <el-badge :value="counts.failed" :hidden="!counts.failed" type="danger" /></template></el-tab-pane>
      <el-tab-pane name="complete"><template #label>已完成 <el-badge :value="counts.complete" :hidden="!counts.complete" type="success" /></template></el-tab-pane>
    </el-tabs>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!visibleItems.length" :empty-text="activeTab === 'active' ? '当前没有正在处理的任务' : activeTab === 'failed' ? '没有需要处理的失败任务' : '暂无已完成任务'" @retry="state.reload">
      <el-table :data="visibleItems" row-key="id" @expand-change="expandJob">
        <el-table-column type="expand" width="48"><template #default="s"><div class="step-panel"><p>按自然日执行；已有有效账单的日期会自动复用。</p><el-table v-loading="stepLoading[s.row.id]" :data="jobSteps[s.row.id]||[]" size="small"><el-table-column label="账单日期" min-width="130"><template #default="d"><b>{{dateText(d.row.range_from)}}</b></template></el-table-column><el-table-column label="状态" width="100"><template #default="d"><el-tag :type="stepStatus(d.row.status)[1]">{{stepStatus(d.row.status)[0]}}</el-tag></template></el-table-column><el-table-column label="计费请求" width="120" align="right"><template #default="d">{{formatNumber(d.row.processed_rows-d.row.abnormal_rows)}}</template></el-table-column><el-table-column prop="abnormal_rows" label="异常请求" width="110" align="right"/><el-table-column prop="attempts" label="尝试次数" width="100"/><el-table-column prop="error_message" label="失败原因" min-width="220" show-overflow-tooltip/></el-table></div></template></el-table-column>
        <el-table-column label="任务" min-width="180"><template #default="s"><b>{{ typeLabel(s.row.job_type) }}</b><small>{{ s.row.requested_by === 'scheduler' ? '系统定时' : '管理员手动' }} · {{ s.row.id }}</small></template></el-table-column>
        <el-table-column prop="instance_id" label="站点" min-width="125" />
        <el-table-column label="账单对象" min-width="180"><template #default="s">{{ s.row.upstream_id ? `${s.row.upstream_name || '上游'} #${s.row.upstream_id}` : s.row.user_id ? `${s.row.user_name || '用户'}（ID: ${s.row.user_id}）` : "历史整站任务" }}</template></el-table-column>
        <el-table-column label="账单日期" min-width="210"><template #default="s">{{ formatRange(s.row) }}</template></el-table-column>
        <el-table-column label="状态" width="120"><template #default="s"><el-tag :type="statusMeta(s.row).type">{{ statusMeta(s.row).label }}</el-tag><small v-if="s.row.status==='pending'">队列第 {{ queuePosition(s.row) }} 位</small></template></el-table-column>
        <el-table-column v-if="activeTab !== 'failed'" label="进度" min-width="170"><template #default="s"><el-progress :percentage="progress(s.row)" :status="s.row.status === 'complete' ? 'success' : undefined" /><small>{{ s.row.completed_steps }} / {{ s.row.total_steps }}</small></template></el-table-column>
        <el-table-column v-if="activeTab === 'complete'" label="账单结果" min-width="210"><template #default="s"><span v-if="Number(s.row.output_days) > 0">已生成 1 条可交付账单 · {{ formatNumber(s.row.billed_rows || 0) }} 条正常订单</span><el-tag v-else-if="s.row.output_days === 0 && !s.row.abnormal_rows" type="info">账期内没有正常订单</el-tag><el-tag v-else-if="s.row.output_days === 0 && s.row.abnormal_rows" type="warning">仅有异常订单</el-tag><span v-else class="muted">任务已完成</span><small v-if="s.row.abnormal_rows">内部保留异常订单 {{ formatNumber(s.row.abnormal_rows) }} 条</small></template></el-table-column>
        <el-table-column v-if="activeTab === 'failed'" label="原因" min-width="240" show-overflow-tooltip><template #default="s">{{ shortError(s.row) }}</template></el-table-column>
        <el-table-column label="创建时间" min-width="165"><template #default="s">{{ formatTime(s.row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" :width="activeTab === 'failed' ? 190 : 130" fixed="right"><template #default="s"><el-button v-if="s.row.status === 'complete' && s.row.job_type !== 'verify'" link type="primary" @click="viewBill(s.row)">查看账单</el-button><el-button v-if="s.row.status === 'pending' || s.row.status === 'running' || s.row.status === 'publishing'" link type="danger" @click="stopJob(s.row)">停止</el-button><template v-if="s.row.status === 'failed'"><el-button v-if="!isCancelled(s.row) && (s.row.job_type === 'generate' || s.row.job_type === 'upstream_generate')" link type="primary" @click="openCreate(s.row)">重新生成</el-button><el-button link type="danger" @click="deleteFailedJob(s.row)">删除</el-button></template></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-dialog v-model="createVisible" title="创建账单任务" width="560px">
      <el-form label-width="100px">
        <el-form-item label="站点"><el-select v-model="createForm.instance_id" filterable style="width:100%" @change="changeCreateSite"><el-option v-for="item in filters.instances.filter(item=>item.enabled&&item.logs_readonly_configured)" :key="item.instance_id" :label="item.name || item.instance_id" :value="item.instance_id" /></el-select></el-form-item>
        <el-form-item label="账单类型"><el-radio-group v-model="createForm.bill_type"><el-radio-button value="user">用户账单</el-radio-button><el-radio-button value="upstream">上游账单</el-radio-button></el-radio-group></el-form-item>
        <el-form-item v-if="createForm.bill_type === 'user'" label="选择用户"><el-select v-model="createForm.user_id" filterable remote clearable :remote-method="loadUsers" :loading="userLoading" placeholder="每个任务只能选择一个用户" style="width:100%"><el-option v-for="user in userOptions" :key="user.id" :label="`${user.display_name || user.username || `用户 ${user.id}`}（ID: ${user.id}）`" :value="user.id" /></el-select></el-form-item>
        <el-form-item v-else label="选择上游"><el-select v-model="createForm.upstream_id" filterable clearable :loading="upstreamLoading" placeholder="每个任务只能选择一个上游" style="width:100%"><el-option v-for="item in upstreamOptions" :key="item.id" :label="`${item.name}（${item.channels.length} 个渠道）`" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="账单日期"><el-date-picker v-model="createForm.range" type="daterange" start-placeholder="开始日期" end-placeholder="结束日期（包含）" :disabled-date="(date: Date) => date >= new Date(new Date().setHours(0,0,0,0))" style="width:100%" /></el-form-item>
        <el-alert type="info" :closable="false" title="每次任务都会完整生成一条新账单；相同站点、账单类型、对象和账期的任务或账单已经存在时，不能重复创建。" />
      </el-form>
      <template #footer><el-button @click="createVisible=false">取消</el-button><el-button type="primary" :loading="creating" :disabled="counts.queued >= 5 || !createForm.instance_id || createForm.range.length !== 2 || (createForm.bill_type === 'user' ? !createForm.user_id : !createForm.upstream_id)" @click="createJob">{{ counts.queued >= 5 ? '等待队列已满' : '创建任务' }}</el-button></template>
    </el-dialog>
  </AppShell>
</template>

<style scoped>
.task-tabs{margin-top:16px}.task-tabs :deep(.el-tabs__header){margin-bottom:12px}.task-tabs :deep(.el-badge){margin-left:6px;vertical-align:middle}.step-panel{padding:8px 20px 18px 48px}.step-panel>p{margin:0 0 10px;color:var(--el-text-color-secondary);font-size:12px}b,small{display:block}small,.muted{color:var(--el-text-color-secondary)}small{margin-top:4px;font-size:12px;font-variant-numeric:tabular-nums}.el-form .el-alert{margin-top:4px}
</style>
