<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { InfoFilled } from "@element-plus/icons-vue";
import type { BillingJob } from "@ct/shared";
import AppShell from "./AppShell.vue";
import AsyncPanel from "./AsyncPanel.vue";
import { dashboard } from "../api";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { formatNumber } from "../utils/format";
import { billingReadErrorMessage, downloadBillingFile, startBillingFileDownload } from "../utils/httpError";

const props = defineProps<{ billType: "user" | "upstream" }>();
const recordsElement = ref<HTMLElement | null>(null);
const recordsWidth = ref(1000);
let recordsObserver: ResizeObserver | undefined;
watch(recordsElement, element => {
  recordsObserver?.disconnect();
  if (!element) return;
  recordsObserver = new ResizeObserver(([entry]) => { if (entry) recordsWidth.value = entry.contentRect.width; });
  recordsObserver.observe(element);
}, { flush: "post" });
onBeforeUnmount(() => recordsObserver?.disconnect());
const router = useRouter();
const detailVisible = ref(false);
const detailLoading = ref(false);
const previewTab = ref("summary");
const pricesLoading = ref(false);
const pricesLoaded = ref(false);
const pricesError = ref("");
let previewRequest: AbortController | null = null;
const downloadingBillId = ref("");
const downloadingDailyId = ref("");
const downloadingAnomalyId = ref("");
const downloadingReconciliationId = ref("");
type PreviewRow = Record<string, unknown>;
const detail = ref<{job:BillingJob;daily_files:{day:string;filename:string}[];total_orders:number;normal_orders:number;billable_orders:number;anomaly_total:number;reconciliation_total:number;review_required:boolean;count_balanced:boolean;model_summary:PreviewRow[];daily_summary:PreviewRow[];token_summary:PreviewRow[];anomalies:PreviewRow[];reconciliation:PreviewRow[]} | null>(null);
const filters = useFiltersStore();
const title = computed(() => props.billType === "user" ? "用户账单" : "上游账单");
const state = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return { items: [] as BillingJob[] };
  return dashboard.billingJobs({ instance_id: filters.site_id, status: "complete", limit: 200 });
});
const records = computed(() => (state.data.value?.items || []).filter((job) => props.billType === "user"
  ? job.job_type === "user_statement" && Number(job.user_id) > 0
  : job.job_type === "upstream_statement" && Number(job.upstream_id) > 0));
const userSearch = ref("");
const billDateRange = ref<[Date, Date] | null>(null);
const dateShortcuts = [
  { text: "本月", value: () => { const today = new Date(); return [new Date(today.getFullYear(), today.getMonth(), 1), new Date(today.getFullYear(), today.getMonth() + 1, 0)]; } },
  { text: "上月", value: () => { const today = new Date(); return [new Date(today.getFullYear(), today.getMonth() - 1, 1), new Date(today.getFullYear(), today.getMonth(), 0)]; } },
  { text: "近三个月", value: () => { const today = new Date(); return [new Date(today.getFullYear(), today.getMonth() - 2, 1), new Date(today.getFullYear(), today.getMonth() + 1, 0)]; } },
];
const selectedBillPeriod = computed(() => {
  if (!billDateRange.value || billDateRange.value.length !== 2) return null;
  const [start, end] = billDateRange.value;
  return { from: new Date(start.getFullYear(), start.getMonth(), start.getDate()).getTime(),
    to: new Date(end.getFullYear(), end.getMonth(), end.getDate() + 1).getTime() };
});
const userPage = ref(1);
const expandedUsers = ref<string[]>([]);
const usersPerPage = 20;
const generatedTime = (job: BillingJob) => new Date(job.updated_at || job.created_at || 0).getTime();
const userGroups = computed(() => {
  const groups = new Map<string, { key: string; name: string; userId: number; site: string; bills: BillingJob[]; latest: BillingJob; reviewCount: number }>();
  for (const job of [...records.value].sort((a, b) => generatedTime(b) - generatedTime(a))) {
    const key = JSON.stringify([job.instance_id, job.user_id]);
    let group = groups.get(key);
    if (!group) {
      group = { key, name: job.user_name || "用户", userId: Number(job.user_id), site: job.instance_id, bills: [], latest: job, reviewCount: 0 };
      groups.set(key, group);
    }
    if (group.name === "用户" && job.user_name) group.name = job.user_name;
    group.bills.push(job);
    if (job.mismatch_rows) group.reviewCount++;
  }
  return [...groups.values()].map(group => ({ ...group, bills: group.bills.sort((a, b) =>
    (b.range_from || "").localeCompare(a.range_from || "") || generatedTime(b) - generatedTime(a)) }));
});
const matchingUsers = computed(() => {
  const keyword = userSearch.value.trim().toLocaleLowerCase();
  return userGroups.value.filter(group => !keyword || String(group.userId).includes(keyword)
    || group.name.toLocaleLowerCase().includes(keyword)
    || group.bills.some(job => job.user_name?.toLocaleLowerCase().includes(keyword)))
    .map(group => {
      const period = selectedBillPeriod.value;
      const bills = period ? group.bills.filter(job => job.range_from && job.range_to
        && new Date(job.range_from).getTime() < period.to && new Date(job.range_to).getTime() > period.from) : group.bills;
      return { ...group, bills, reviewCount: bills.filter(job => job.mismatch_rows).length,
        latest: bills.reduce((latest, job) => generatedTime(job) > generatedTime(latest) ? job : latest, bills[0] || group.latest) };
    }).filter(group => group.bills.length > 0);
});
const matchingBillCount = computed(() => matchingUsers.value.reduce((total, group) => total + group.bills.length, 0));
const reviewFilter = ref<"all" | "review" | "ready">("all");
const upstreamGroups = computed(() => {
  const groups = new Map<string, { key: string; name: string; userId: number; site: string; bills: BillingJob[]; latest: BillingJob; reviewCount: number }>();
  const keyword = userSearch.value.trim().toLocaleLowerCase();
  const period = selectedBillPeriod.value;
  for (const job of [...records.value].sort((a, b) => generatedTime(b) - generatedTime(a))) {
    if (props.billType !== "upstream") continue;
    const identityMatch = !keyword || [job.upstream_name, String(job.upstream_id), job.bill_no].some(value => value?.toLocaleLowerCase().includes(keyword));
    if (!identityMatch) continue;
    if (period && !(job.range_from && job.range_to && new Date(job.range_from).getTime() < period.to && new Date(job.range_to).getTime() > period.from)) continue;
    if (reviewFilter.value === "review" && !job.mismatch_rows || reviewFilter.value === "ready" && job.mismatch_rows) continue;
    const key = JSON.stringify([job.instance_id, job.upstream_id]);
    let group = groups.get(key);
    if (!group) {
      group = { key, name: job.upstream_name || "上游", userId: Number(job.upstream_id), site: job.instance_id, bills: [], latest: job, reviewCount: 0 };
      groups.set(key, group);
    }
    group.bills.push(job);
    if (job.mismatch_rows) group.reviewCount++;
  }
  return [...groups.values()].map(group => ({ ...group, bills: group.bills.sort((a, b) =>
    (b.range_from || "").localeCompare(a.range_from || "") || generatedTime(b) - generatedTime(a)) }));
});
const upstreamBillCount = computed(() => upstreamGroups.value.reduce((sum, group) => sum + group.bills.length, 0));
const upstreamReviewCount = computed(() => upstreamGroups.value.reduce((sum, group) => sum + group.reviewCount, 0));
const matchingGroupCount = computed(() => props.billType === "user" ? matchingUsers.value.length : upstreamGroups.value.length);
const visibleGroups = computed(() => props.billType === "user"
  ? matchingUsers.value.slice((userPage.value - 1) * usersPerPage, userPage.value * usersPerPage)
  : upstreamGroups.value.slice((userPage.value - 1) * usersPerPage, userPage.value * usersPerPage));
function toggleUser(key: string) {
  expandedUsers.value = expandedUsers.value.includes(key)
    ? expandedUsers.value.filter(value => value !== key) : [...expandedUsers.value, key];
}
watch([userSearch, billDateRange, reviewFilter], () => { userPage.value = 1; });
watch(() => [filters.site_id, props.billType], () => { userSearch.value = ""; billDateRange.value = null; userPage.value = 1; expandedUsers.value = []; reviewFilter.value = "all"; });
watch(matchingGroupCount, count => { userPage.value = Math.min(userPage.value, Math.max(1, Math.ceil(count / usersPerPage))); });
const formatTime = (value?: string) => value ? new Date(value).toLocaleString() : "—";
const previewNumber = (value:unknown) => formatNumber(Number(value || 0));
const previewMoney = (value:unknown) => Number(value || 0).toLocaleString("zh-CN", { minimumFractionDigits: 2, maximumFractionDigits: 6 });
const previewDiscount = (value:unknown) => `${(Number(value || 0) * 100).toFixed(2)}%`;
const previewTotal = computed(() => (detail.value?.model_summary || []).reduce((sum,row)=>sum+Number(row.final_amount||0),0));
const previewDays = computed(() => new Set((detail.value?.daily_summary || []).map(row=>String(row.day||""))).size);
const dateText = (value?: string) => value ? new Date(value).toLocaleDateString() : "—";
const formatRange = (job: BillingJob) => job.range_from && job.range_to
  ? `${dateText(job.range_from)} 至 ${dateText(new Date(new Date(job.range_to).getTime() - 1).toISOString())}` : "—";
const subject = (job: BillingJob) => props.billType === "user"
  ? `${job.user_name || "用户"}（ID: ${job.user_id}）` : `${job.upstream_name || "上游"} #${job.upstream_id}`;
const createBill = () => router.push({ path: "/billing/tasks", query: { create: "1", bill_type: props.billType } });
async function viewBill(job: BillingJob) {
  previewRequest?.abort();
  const request = new AbortController();
  previewRequest = request;
  previewTab.value = "summary";
  pricesLoaded.value = false;
  pricesLoading.value = false;
  pricesError.value = "";
  detailVisible.value = true;
  detailLoading.value = true;
  detail.value = null;
  try {
    const result = await dashboard.billingStatementResult(job.id, true, request.signal);
    if (previewRequest === request && !request.signal.aborted) detail.value = result;
  } catch (error) {
    if (previewRequest === request && !request.signal.aborted) ElMessage.error(billingReadErrorMessage(error, "账单加载失败"));
  } finally {
    if (previewRequest === request) detailLoading.value = false;
  }
}
async function loadDailyPrices() {
  const request = previewRequest;
  const current = detail.value;
  if (!request || request.signal.aborted || !current || pricesLoaded.value || pricesLoading.value) return;
  pricesLoading.value = true;
  pricesError.value = "";
  try {
    const result = await dashboard.billingStatementPrices(current.job.id, request.signal);
    if (previewRequest === request && !request.signal.aborted) {
      current.daily_summary = result.daily_summary;
      pricesLoaded.value = true;
    }
  } catch (error) {
    if (previewRequest === request && !request.signal.aborted) pricesError.value = billingReadErrorMessage(error, "历史单价加载失败");
  } finally {
    if (previewRequest === request) pricesLoading.value = false;
  }
}
watch(previewTab, tab => { if (tab === "daily") void loadDailyPrices(); });
watch(detailVisible, visible => { if (!visible) previewRequest?.abort(); });
onBeforeUnmount(() => previewRequest?.abort());
const safeFilename=(value:string)=>value.replace(/[<>:"/\\|?*\x00-\x1f]/g,"-").trim();
async function downloadBill(job:BillingJob, archive=false){
  if(downloadingBillId.value)return;
  const kind=job.job_type==="upstream_statement"?"上游账单":"用户账单";
  const filename=`${kind}-${safeFilename(job.bill_no||job.id)}.${archive?"zip":"xlsx"}`;
  downloadingBillId.value=job.id;
  const preparing=ElMessage({message:archive?"正在准备完整 ZIP，请等待浏览器完成下载":"正在准备主账单 Excel，请稍候",type:"info",duration:0});
  try{
    const url=`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.id)}&download=1${archive?"":"&export=summary"}`;
    if(archive) startBillingFileDownload(url,filename);
    else await downloadBillingFile(url,"主账单下载失败",filename);
    ElMessage.success(archive?"已提交完整 ZIP 下载请求":"主账单已开始下载");
  }catch(error){
    ElMessage.error(billingReadErrorMessage(error,"账单压缩包下载失败"));
  }finally{
    preparing.close();
    downloadingBillId.value="";
  }
}
function downloadDaily(job:BillingJob, day:string){
  if(downloadingDailyId.value)return;
  downloadingDailyId.value=day;
  startBillingFileDownload(`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.id)}&export=daily&day=${encodeURIComponent(day)}`);
  ElMessage.info("已提交当日明细下载请求，请等待浏览器完成下载");
  releaseDownloadState(downloadingDailyId,day);
}
const dailyFiles=computed(()=>detail.value?.daily_files||[]);
const queryDate=(value?:string)=>{if(!value)return"";const d=new Date(value);return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`};
function releaseDownloadState(target:typeof downloadingAnomalyId, jobID:string){window.setTimeout(()=>{if(target.value===jobID)target.value=""},1500)}
function downloadAnomalies(job:BillingJob){
  if(downloadingAnomalyId.value)return;
  const url=`/api/dashboard/billing/anomalies?instance_id=${encodeURIComponent(job.instance_id)}&job_id=${encodeURIComponent(job.id)}&user_id=${job.user_id||0}&from=${queryDate(job.range_from)}&to=${queryDate(job.range_to)}&format=csv`;
  downloadingAnomalyId.value=job.id;
  startBillingFileDownload(url,`${safeFilename(job.bill_no||job.id)}-内部异常.csv`);
  ElMessage.info("异常订单 CSV 已开始生成，数据量较大时请等待浏览器完成下载");
  releaseDownloadState(downloadingAnomalyId,job.id);
}
function downloadReconciliation(job:BillingJob){
  if(downloadingReconciliationId.value)return;
  const url=`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.id)}&export=reconciliation`;
  downloadingReconciliationId.value=job.id;
  startBillingFileDownload(url,`${safeFilename(job.bill_no||job.id)}-核对差异.csv`);
  ElMessage.info("核对差异 CSV 已开始生成，请等待浏览器完成下载");
  releaseDownloadState(downloadingReconciliationId,job.id);
}
async function deleteBill(job:BillingJob){try{await ElMessageBox.confirm("删除后将同时清理该账单、生成任务和本地明细文件，且不可恢复。确定删除吗？","删除账单",{type:"warning",confirmButtonText:"删除"});await dashboard.deleteBillingStatement(job.id);if(detail.value?.job.id===job.id){detailVisible.value=false;detail.value=null}ElMessage.success("账单已删除");await state.reload()}catch(error){if(error!=="cancel"&&error!=="close")ElMessage.error(billingReadErrorMessage(error,"账单删除失败"))}}
watch(() => [filters.site_id, props.billType] as const, () => void state.reload(), { immediate: true });
</script>

<template>
  <AppShell :title="title" class="billing-records-shell" :class="{'upstream-records':props.billType === 'upstream'}">
    <template #tools><el-button type="primary" @click="createBill">生成{{ title }}</el-button><el-button @click="state.reload">刷新</el-button></template>
    <div ref="recordsElement" class="billing-records-content">
    <section v-if="props.billType === 'upstream'" class="upstream-toolbar">
      <div class="upstream-filter-row">
        <el-input v-model="userSearch" clearable placeholder="搜索上游名称、ID 或账单编号" aria-label="搜索上游名称、ID 或账单编号" class="upstream-search" />
        <div class="bill-date-filter"><span>账期</span><el-date-picker v-model="billDateRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :shortcuts="dateShortcuts" clearable /></div>
        <el-select v-model="reviewFilter" aria-label="账单复核状态" class="review-filter"><el-option label="全部状态" value="all"/><el-option label="待复核" value="review"/><el-option label="可使用" value="ready"/></el-select>
        <el-button :disabled="!userSearch && !billDateRange && reviewFilter === 'all'" @click="userSearch = ''; billDateRange = null; reviewFilter = 'all'">重置筛选</el-button>
      </div>
      <div class="upstream-summary"><div><strong>{{ upstreamGroups.length }}</strong> 家上游 <i/> <strong>{{ upstreamBillCount }}</strong> 份账单 <i/> <span :class="{warning:upstreamReviewCount}">{{ upstreamReviewCount }} 份待复核</span></div><el-button link :disabled="!expandedUsers.length" @click="expandedUsers = []">全部收起</el-button></div>
      <p class="upstream-hint">按上游查看已生成账单；账期筛选包含重叠区间。主账单可直接下载，每日明细在账单详情中按天下载。</p>
    </section>
    <el-alert v-if="(state.data.value?.items.length || 0) >= 200" class="history-notice" title="当前仅加载最近 200 条已完成任务，较早账单可能未包含在列表中。" type="warning" :closable="false" show-icon />
    <div v-if="props.billType === 'user'" class="user-toolbar">
      <el-input v-model="userSearch" placeholder="搜索用户名称或 ID" aria-label="搜索用户名称或 ID" clearable class="user-search" />
      <div class="bill-date-filter"><span>账期</span><el-date-picker v-model="billDateRange" type="daterange" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :shortcuts="dateShortcuts" clearable /></div>
      <span>{{ matchingUsers.length }} 位用户 · {{ matchingBillCount }} 份账单</span>
      <el-tooltip content="账单按用户归组，点击用户展开各账期账单，最新账期排在前面；日期筛选显示与所选日期有重叠的账期，包含结束日期，仅筛选已加载账单。" placement="top">
        <button type="button" class="group-help" aria-label="账单分组说明"><el-icon><InfoFilled /></el-icon></button>
      </el-tooltip>
      <el-button :disabled="!expandedUsers.length" @click="expandedUsers = []">全部收起</el-button>
    </div>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!records.length" :empty-text="`暂无${title}，请先创建账单任务`" @retry="state.reload">
      <el-empty v-if="!matchingGroupCount" :description="props.billType === 'user' ? '没有匹配的账单，请调整用户或账期筛选' : '没有匹配的账单，请调整上游、账期或状态筛选'" />
      <section v-for="group in visibleGroups" :key="group.key" class="user-group" :class="{'upstream-group':props.billType === 'upstream'}">
        <button type="button" class="user-group-header" :aria-expanded="expandedUsers.includes(group.key)" @click="toggleUser(group.key)">
          <span class="expand-arrow" :class="{ expanded: expandedUsers.includes(group.key) }" aria-hidden="true">›</span>
          <span class="user-identity"><strong>{{ group.name }}</strong><small>ID: {{ group.userId }} · {{ group.site }}</small></span>
          <span class="group-count">{{ group.bills.length }} 份账单</span>
          <el-tag v-if="group.reviewCount" type="warning">{{ group.reviewCount }} 份待复核</el-tag>
          <span class="group-latest">最近生成：{{ formatTime(group.latest?.updated_at || group.latest?.created_at) }}</span>
          <span class="expand-label">{{ expandedUsers.includes(group.key) ? '收起' : '展开账单' }}</span>
        </button>
      <el-table v-if="expandedUsers.includes(group.key)" :data="group.bills" row-key="id" class="records-table" scrollbar-always-on>
        <el-table-column label="账单编号" :min-width="props.billType === 'upstream' ? 220 : 300"><template #default="s"><b class="bill-number" :title="`账单 ${s.row.bill_no || s.row.id} · 内部任务 ${s.row.id}`">{{ s.row.bill_no || s.row.id }}</b><small>{{s.row.pricing_source === "newapi" ? "NewAPI 原始计费" : "重新计费"}}</small><small v-if="props.billType === 'user'">内部任务：{{s.row.id}}</small></template></el-table-column>
        <el-table-column label="账单周期" :min-width="props.billType === 'upstream' ? 195 : 210"><template #default="s">{{ formatRange(s.row) }}<small v-if="props.billType === 'upstream'">生成于 {{ formatTime(s.row.updated_at || s.row.created_at) }}</small></template></el-table-column>
        <el-table-column v-if="props.billType === 'upstream'" label="订单与核对" min-width="190"><template #default="s"><div class="bill-order-count">计费 <strong>{{ formatNumber(s.row.billed_rows || 0) }}</strong></div><div class="bill-check-counts"><span :class="{warning:s.row.abnormal_rows}">异常 {{formatNumber(s.row.abnormal_rows || 0)}}</span><span :class="{danger:s.row.mismatch_rows}">差异 {{formatNumber(s.row.mismatch_rows || 0)}}</span></div></template></el-table-column>
        <el-table-column v-if="props.billType === 'user'" label="计费订单" width="110" align="right"><template #default="s">{{ formatNumber(s.row.billed_rows || 0) }}</template></el-table-column>
        <el-table-column v-if="props.billType === 'user'" label="内部异常" width="110" align="right"><template #default="s"><span :class="{ warning: s.row.abnormal_rows }">{{ formatNumber(s.row.abnormal_rows || 0) }}</span></template></el-table-column>
        <el-table-column v-if="props.billType === 'user'" label="核对差异" width="110" align="right"><template #default="s"><span :class="{ danger: s.row.mismatch_rows }">{{ formatNumber(s.row.mismatch_rows || 0) }}</span></template></el-table-column>
        <el-table-column v-if="props.billType === 'user'" label="生成时间" min-width="170"><template #default="s">{{ formatTime(s.row.updated_at || s.row.created_at) }}</template></el-table-column>
        <el-table-column label="状态" width="96"><template #default="s"><el-tag :type="s.row.mismatch_rows ? 'warning' : 'success'">{{s.row.mismatch_rows ? '待复核' : '可使用'}}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="216" :fixed="props.billType === 'upstream' && recordsWidth < 900 ? false : 'right'"><template #default="s"><el-button link type="primary" @click="viewBill(s.row)">查看账单</el-button><el-button link type="primary" :loading="downloadingBillId===s.row.id" :disabled="!!downloadingBillId" @click="downloadBill(s.row)">下载主账单</el-button><el-button link type="danger" @click="deleteBill(s.row)">删除</el-button></template></el-table-column>
      </el-table>
      </section>
      <el-pagination v-if="matchingGroupCount > usersPerPage" v-model:current-page="userPage" :page-size="usersPerPage" :total="matchingGroupCount" layout="total, prev, pager, next" class="user-pagination" />
    </AsyncPanel>
    <el-dialog v-model="detailVisible" width="min(1180px, calc(100vw - 48px))" :top="props.billType === 'upstream' ? '4vh' : '7vh'" class="bill-preview-dialog" :class="{'upstream-bill-preview':props.billType === 'upstream'}" destroy-on-close>
      <template #header><div class="preview-title"><div class="preview-mark">账</div><div><h2>{{props.billType==='user'?'用户账单':'上游账单'}}</h2><p>{{detail?.job.bill_no || '账单内容预览'}}</p></div><el-tag v-if="detail" :type="detail.review_required?'warning':'success'" effect="light" round>{{detail.review_required?'待复核':'可使用'}}</el-tag></div></template>
      <div v-loading="detailLoading" class="preview-body">
        <template v-if="detail"><div class="preview-meta"><div><span>账单对象</span><strong>{{subject(detail.job)}}</strong></div><div><span>账单周期</span><strong>{{formatRange(detail.job)}}</strong></div><div><span>内部任务 ID</span><strong class="mono">{{detail.job.id}}</strong></div></div>
        <el-alert v-if="detail.review_required" class="review-alert" type="warning" :closable="false" show-icon title="该账单存在核对差异，请下载核对差异 CSV 并在交付前复核；完整 ZIP 也会附带差异文件。"/>
        <details v-if="props.billType === 'upstream'" class="billing-calculation-note"><summary>计费来源：{{ detail.job.pricing_source === 'newapi' ? 'NewAPI 原始扣费' : '重新计费' }}<span>查看计费与用量说明</span></summary><p>{{ detail.job.pricing_source === 'newapi' ? '历史单价与规则仅供核对，不改变金额；上游渠道折扣另行应用。' : '此账单使用任务保存的重新计费结果。' }}</p><p>{{ detail.job.usage_version ? '普通输入/输出与图像、音频分别统计，缓存单列。日志未记录的多媒体用量不估算；用量拆分不改变金额。' : '历史账单未保存多媒体用量拆分；需要明细时请新建账单任务。' }}</p></details>
        <el-alert v-else type="info" :closable="false" :title="detail.job.pricing_source === 'newapi' ? '计费来源：NewAPI 原始扣费；历史单价与规则仅供核对，不改变金额。上游渠道折扣另行应用。' : '计费来源：重新计费。'" :description="detail.job.usage_version ? '普通输入/输出与图像、音频分别统计；缓存单列。日志未记录的多媒体用量不估算；用量拆分不改变金额。' : '历史账单未保存多媒体用量拆分；需要明细时请新建账单任务。'" />
        <div class="preview-metrics"><div class="metric-card"><span>原始总订单</span><strong>{{previewNumber(detail.total_orders)}}</strong><small>{{detail.count_balanced?'三类数量核对一致':'数量核对异常'}}</small></div><div class="metric-card"><span>{{detail.job.pricing_source === "newapi" ? "计费订单" : "核对正常"}}</span><strong>{{previewNumber(detail.normal_orders)}}</strong><small>{{detail.job.pricing_source === "newapi" ? "采用原始扣费" : "金额核对一致"}}</small></div><div class="metric-card"><span>异常订单</span><strong :class="{danger:detail.anomaly_total}">{{previewNumber(detail.anomaly_total)}}</strong><small>不进入账单</small></div><div class="metric-card"><span>核对差异</span><strong :class="{danger:detail.reconciliation_total}">{{previewNumber(detail.reconciliation_total)}}</strong><small>仍计入账单，需复核</small></div><div class="metric-card"><span>账单天数</span><strong>{{previewDays}}</strong><small>{{detail.model_summary.length}} 个计费模型</small></div><div class="metric-card accent"><span>最终费用</span><strong>¥ {{previewMoney(previewTotal)}}</strong><small>计费订单 {{previewNumber(detail.billable_orders)}}</small></div></div>
        <el-tabs v-model="previewTab" class="preview-tabs">
          <el-tab-pane label="每日明细下载" name="downloads"><el-table scrollbar-always-on :data="dailyFiles" size="small" max-height="460" stripe empty-text="暂无每日明细"><el-table-column prop="day" label="日期" width="140"/><el-table-column prop="filename" label="文件名" min-width="260"/><el-table-column label="操作" width="150"><template #default="s"><el-button link type="primary" :loading="downloadingDailyId===s.row.day" :disabled="!!downloadingDailyId" @click="downloadDaily(detail.job,s.row.day)">下载当日明细</el-button></template></el-table-column></el-table></el-tab-pane>
          <el-tab-pane label="区间统计" name="summary"><el-table scrollbar-always-on :data="detail.model_summary" size="small" max-height="460" stripe><el-table-column prop="model_name" label="模型" min-width="180"><template #default="s"><b class="model-name">{{s.row.model_name}}</b></template></el-table-column><el-table-column prop="request_count" label="订单数" width="110" align="right"><template #default="s">{{previewNumber(s.row.request_count)}}</template></el-table-column><el-table-column prop="prompt_tokens" :label="detail.job.usage_version ? '普通输入 Token' : '输入 Token'" width="145" align="right"><template #default="s">{{previewNumber(s.row.prompt_tokens)}}</template></el-table-column><el-table-column prop="completion_tokens" :label="detail.job.usage_version ? '普通输出 Token' : '输出 Token'" width="145" align="right"><template #default="s">{{previewNumber(s.row.completion_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="image_input_tokens" label="图像输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="image_output_tokens" label="图像输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_output_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_input_tokens" label="音频输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_output_tokens" label="音频输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_output_tokens)}}</template></el-table-column><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="150" align="right"><template #default="s">{{previewNumber(s.row.cache_read_tokens)}}</template></el-table-column><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="150" align="right"><template #default="s">{{previewNumber(s.row.cache_write_tokens)}}</template></el-table-column><el-table-column prop="amount" label="总费用" width="130" align="right"><template #default="s">¥ {{previewMoney(s.row.amount)}}</template></el-table-column><el-table-column prop="discount" label="折扣" width="90" align="right"><template #default="s"><el-tag size="small" type="info">{{previewDiscount(s.row.discount)}}</el-tag></template></el-table-column><el-table-column prop="final_amount" label="最终费用" width="140" align="right"><template #default="s"><b class="money">¥ {{previewMoney(s.row.final_amount)}}</b></template></el-table-column></el-table></el-tab-pane>
          <el-tab-pane label="日账单统计" name="daily">
            <el-alert v-if="detail.job.pricing_source === 'newapi' && (detail.job.usage_version || 0) < 2" type="info" :closable="false" title="此旧账单未保存历史单价和计价规则，请新建账单任务生成完整明细。" />
            <el-alert type="info" :closable="false" title="单价单位：金额/百万 Token。一天内有不同价格时，查看历史计价规则逐套核对；表达式计费不强行拆成固定单价。" />
            <el-alert v-if="pricesLoading" type="info" :closable="false" show-icon title="正在加载历史单价，账单金额已可查看；大账单首次解析可能需要一些时间。" />
            <div v-if="pricesError" class="anomaly-toolbar"><el-alert type="error" :closable="false" show-icon :title="pricesError"/><el-button @click="loadDailyPrices">重试单价加载</el-button></div><el-table scrollbar-always-on :data="detail.daily_summary" size="small" max-height="520"><el-table-column prop="day" label="日期" width="110"/><el-table-column prop="model_name" label="模型" min-width="150"/><el-table-column prop="request_count" label="订单数" width="100" align="right"/><el-table-column prop="prompt_tokens" :label="detail.job.usage_version ? '普通输入 Token' : '输入 Token'" width="135" align="right"/><el-table-column prop="completion_tokens" :label="detail.job.usage_version ? '普通输出 Token' : '输出 Token'" width="135" align="right"/><el-table-column v-if="detail.job.usage_version" prop="image_input_tokens" label="图像输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="image_output_tokens" label="图像输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_output_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_input_tokens" label="音频输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_output_tokens" label="音频输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_output_tokens)}}</template></el-table-column><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="145" align="right"/><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="145" align="right"/><el-table-column prop="input_price" label="输入单价" width="110" align="right"/><el-table-column prop="output_price" label="输出单价" width="110" align="right"/><el-table-column prop="cache_read_price" label="缓存读取单价" width="130" align="right"/><el-table-column prop="cache_write_price" label="缓存写入单价" width="130" align="right"/><el-table-column label="历史计价规则" min-width="260"><template #default="s"><el-popover trigger="click" placement="bottom" width="min(720px, calc(100vw - 32px))"><template #reference><el-button link type="primary">{{ String(s.row.price_rules || '').startsWith('当日 ') ? String(s.row.price_rules).split('\n')[0] : '查看历史计价规则' }}</el-button></template><pre class="historical-price-rules">{{ s.row.price_rules || '历史计价规则未记录' }}</pre></el-popover></template></el-table-column><el-table-column prop="amount" label="总费用" width="120" align="right"/><el-table-column prop="discount" label="折扣" width="80" align="right"/><el-table-column prop="final_amount" label="最终费用" width="120" align="right"/><el-table-column prop="detail_file" label="明细文件" min-width="230"/></el-table></el-tab-pane>
          <el-tab-pane v-if="props.billType==='user'" label="按令牌统计"><el-table scrollbar-always-on :data="detail.token_summary" size="small" max-height="520"><el-table-column prop="token_id" label="令牌 ID" width="100"/><el-table-column prop="token_name" label="令牌" min-width="150"/><el-table-column prop="day" label="日期" width="110"/><el-table-column prop="model_name" label="模型" min-width="150"/><el-table-column prop="request_count" label="订单数" width="100" align="right"/><el-table-column prop="prompt_tokens" :label="detail.job.usage_version ? '普通输入 Token' : '输入 Token'" width="140" align="right"/><el-table-column prop="completion_tokens" :label="detail.job.usage_version ? '普通输出 Token' : '输出 Token'" width="140" align="right"/><el-table-column v-if="detail.job.usage_version" prop="image_input_tokens" label="图像输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="image_output_tokens" label="图像输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.image_output_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_input_tokens" label="音频输入 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_input_tokens)}}</template></el-table-column><el-table-column v-if="detail.job.usage_version" prop="audio_output_tokens" label="音频输出 Token" width="155" align="right"><template #default="s">{{previewNumber(s.row.audio_output_tokens)}}</template></el-table-column><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="145" align="right"/><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="145" align="right"/><el-table-column prop="amount" label="总费用" width="130" align="right"/></el-table></el-tab-pane>
          <el-tab-pane><template #label><span>内部异常 <el-badge :value="detail.anomaly_total" :hidden="!detail.anomaly_total" type="danger"/></span></template><div class="anomaly-toolbar"><el-alert type="warning" :closable="false" show-icon title="异常订单仅供内部核对，不会写入客户账单或账单 ZIP。"/><el-button type="danger" plain :loading="downloadingAnomalyId===detail.job.id" :disabled="!detail.anomaly_total||!!downloadingAnomalyId" @click="downloadAnomalies(detail.job)">下载异常 CSV</el-button></div><el-table scrollbar-always-on :data="detail.anomalies" size="small" max-height="430" stripe><el-table-column prop="created_at" label="请求时间" width="165"/><el-table-column prop="request_id" label="Request ID" min-width="190" show-overflow-tooltip/><el-table-column prop="upstream_request_id" label="上游 Request ID" min-width="190" show-overflow-tooltip/><el-table-column v-if="props.billType==='upstream'" prop="channel_name" label="渠道" width="150"/><el-table-column v-else prop="token_name" label="令牌" width="130"/><el-table-column prop="model_name" label="模型" width="140"/><el-table-column prop="actual_amount" label="实际扣费" width="110" align="right"/><el-table-column prop="reasons" label="异常原因" min-width="220" show-overflow-tooltip/></el-table><p v-if="detail.anomaly_total>detail.anomalies.length" class="preview-note">页面仅展示前 {{detail.anomalies.length}} 条，完整 {{previewNumber(detail.anomaly_total)}} 条请下载 CSV。</p></el-tab-pane>
          <el-tab-pane><template #label><span>核对差异 <el-badge :value="detail.reconciliation_total" :hidden="!detail.reconciliation_total" type="warning"/></span></template><div class="anomaly-toolbar"><el-alert type="warning" :closable="false" show-icon title="这些订单仍计入账单；存在记录时账单必须复核，不能直接交付。"/><el-button type="warning" plain :loading="downloadingReconciliationId===detail.job.id" :disabled="!detail.reconciliation_total||!!downloadingReconciliationId" @click="downloadReconciliation(detail.job)">下载核对差异 CSV</el-button></div><el-table scrollbar-always-on :data="detail.reconciliation" size="small" max-height="430" stripe><el-table-column prop="created_at" label="请求时间" width="165"/><el-table-column prop="request_id" label="Request ID" min-width="190" show-overflow-tooltip/><el-table-column prop="upstream_request_id" label="上游 Request ID" min-width="190" show-overflow-tooltip/><el-table-column v-if="props.billType==='upstream'" prop="channel_name" label="渠道" width="150"/><el-table-column v-else prop="token_name" label="令牌" width="130"/><el-table-column prop="model_name" label="模型" width="140"/><el-table-column prop="logged_quota" label="日志 Quota" width="115" align="right"/><el-table-column prop="calculated_quota" label="重算 Quota" width="115" align="right"/><el-table-column prop="quota_difference" label="Quota 差额" width="110" align="right"/><el-table-column prop="logged_amount" label="日志金额" width="110" align="right"/><el-table-column prop="calculated_amount" label="重算金额" width="110" align="right"/><el-table-column prop="reason" label="差异原因" min-width="220" show-overflow-tooltip/></el-table><p v-if="detail.reconciliation_total>detail.reconciliation.length" class="preview-note">页面仅展示前 {{detail.reconciliation.length}} 条，完整 {{previewNumber(detail.reconciliation_total)}} 条请下载 CSV。</p></el-tab-pane>
        </el-tabs><el-empty v-if="!detail.normal_orders" description="该账期没有查询到正常订单" :image-size="60"/></template>
      </div>
      <template #footer><div class="preview-footer"><span>主账单和每日明细可分开下载</span><div><el-button @click="detailVisible=false">关闭</el-button><el-button v-if="detail" :disabled="!!downloadingBillId" @click="downloadBill(detail.job,true)">下载完整 ZIP</el-button><el-button v-if="detail" type="primary" :loading="downloadingBillId===detail.job.id" :disabled="!!downloadingBillId" @click="downloadBill(detail.job)">下载主账单 Excel</el-button></div></div></template>
    </el-dialog>
    </div>
  </AppShell>
</template>

<style scoped>
.group-help{display:inline-flex;align-items:center;justify-content:center;padding:4px;border:0;background:transparent;color:var(--el-text-color-secondary);cursor:help;font-size:14px}.group-help:focus-visible{outline:2px solid var(--el-color-primary);border-radius:4px}.history-notice{margin-top:12px}.user-toolbar{display:flex;flex-wrap:wrap;align-items:center;gap:16px;margin:0 0 16px;color:var(--el-text-color-secondary);font-size:13px}.user-search{width:260px}.bill-date-filter{display:flex;align-items:center;gap:8px;max-width:100%}.bill-date-filter :deep(.el-date-editor){width:280px;max-width:100%;flex-grow:0}.user-toolbar>.el-button{margin-left:auto}.user-group{margin-top:12px;border:1px solid var(--el-border-color-light);border-radius:8px;overflow:hidden;background:var(--el-bg-color)}.user-group-header{display:flex;align-items:center;gap:16px;width:100%;padding:18px 20px;border:0;background:var(--el-bg-color);color:var(--el-text-color-primary);text-align:left;cursor:pointer;font:inherit}.user-group-header:hover{background:var(--el-fill-color-light)}.user-group-header:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:-2px}.user-identity{min-width:180px;flex:1}.user-identity strong{font-size:15px}.expand-arrow{font-size:24px;transition:transform .15s}.expand-arrow.expanded{transform:rotate(90deg)}.group-count,.group-latest{font-size:13px;color:var(--el-text-color-secondary)}.expand-label{font-size:13px;color:var(--el-color-primary)}.user-group .records-table{margin-top:0;border-top:1px solid var(--el-border-color-light)}.user-pagination{justify-content:flex-end;margin-top:20px}@media(max-width:900px){.user-toolbar{flex-wrap:wrap}.user-search{width:100%}.user-group-header{flex-wrap:wrap;gap:10px}.user-identity{min-width:140px}.group-latest{display:none}}

.records-table{margin-top:16px}b,small{display:block}small{margin-top:4px;color:var(--el-text-color-secondary);font-size:12px}.warning{color:var(--el-color-warning);font-weight:600}
.preview-title{display:flex;align-items:center;gap:12px}.preview-title h2{margin:0;font-size:20px;color:#172033}.preview-title p{margin:4px 0 0;color:#8490a5;font-size:12px}.preview-title .el-tag{margin-left:auto;margin-right:12px}.preview-mark{display:grid;place-items:center;width:42px;height:42px;border-radius:12px;color:#fff;font-size:20px;font-weight:700;background:linear-gradient(135deg,#315ee8,#6c8cff);box-shadow:0 7px 18px rgba(49,94,232,.24)}
.preview-body{min-height:300px}.preview-meta{display:grid;grid-template-columns:1fr 1fr 1.3fr;gap:1px;background:#e8edf5;border:1px solid #e8edf5;border-radius:10px;overflow:hidden}.preview-meta>div{padding:13px 16px;background:#f8fafd}.preview-meta span{display:block;color:#8994a7;font-size:12px;margin-bottom:5px}.preview-meta strong{color:#28344a;font-size:14px}.mono{font-family:Consolas,monospace;font-size:12px!important;font-weight:500}
.review-alert{margin-top:14px}.preview-metrics{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:16px 0}.metric-card{position:relative;padding:16px 18px;border:1px solid #e7ebf2;border-radius:12px;background:#fff;box-shadow:0 3px 12px rgba(35,54,90,.04)}.metric-card span{display:block;color:#8490a5;font-size:13px}.metric-card strong{display:block;margin:7px 0 3px;color:#1d2940;font-size:24px;line-height:1.2}.metric-card small{margin:0}.metric-card.accent{border-color:#dce5ff;background:linear-gradient(135deg,#f3f6ff,#eef3ff)}.metric-card.accent strong{color:#315ee8}
.preview-tabs{padding:0 14px 14px;border:1px solid #e7ebf2;border-radius:12px;background:#fff}.model-name{color:#34425a}.money{color:#315ee8}.preview-footer{display:flex;align-items:center;justify-content:space-between}.preview-footer>span{color:#8b96a8;font-size:12px}
.danger{color:#e45656!important}.anomaly-toolbar{display:flex;align-items:center;gap:12px;margin:12px 0}.anomaly-toolbar .el-alert{flex:1}.preview-note{margin:10px 0 0;color:#8b96a8;font-size:12px;text-align:right}
:deep(.bill-preview-dialog){border-radius:14px;overflow:hidden;box-shadow:0 24px 70px rgba(20,35,65,.2)}:deep(.bill-preview-dialog .el-dialog__header){padding:20px 24px 16px;margin:0;border-bottom:1px solid #edf0f5}:deep(.bill-preview-dialog .el-dialog__body){padding:18px 24px;background:#f6f8fb}:deep(.bill-preview-dialog .el-dialog__footer){padding:14px 24px;border-top:1px solid #edf0f5}:deep(.preview-tabs .el-tabs__header){margin-bottom:0}:deep(.preview-tabs .el-tabs__item){height:48px;font-weight:600}:deep(.preview-tabs .el-table){border-radius:8px}:deep(.preview-tabs .el-table th.el-table__cell){background:#f7f9fc;color:#65728a;font-weight:600}
@media(max-width:1000px){.preview-meta{grid-template-columns:1fr}.preview-metrics{grid-template-columns:repeat(2,1fr)}}
.historical-price-rules { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 460px; overflow: auto; font: inherit; line-height: 1.6; }
.billing-records-content{min-width:0}
.billing-calculation-note{margin:10px 0;color:var(--ct-ink-2);font-size:12px}.billing-calculation-note summary{cursor:pointer;padding:8px 0}.billing-calculation-note summary span{margin-left:12px;color:var(--ct-accent)}.billing-calculation-note p{margin:6px 0;line-height:1.7}
.upstream-toolbar{border:1px solid var(--ct-line);border-radius:8px;padding:14px 16px;background:var(--ct-surface);box-shadow:var(--ct-shadow)}
.upstream-filter-row{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.upstream-search{flex:1 1 240px;max-width:360px}.review-filter{width:130px}.upstream-filter-row .bill-date-filter{flex:0 1 auto;min-width:0}.upstream-filter-row .bill-date-filter>span{font-size:12px;color:var(--ct-ink-2);white-space:nowrap}
.upstream-summary{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-top:12px;padding-top:10px;border-top:1px solid var(--ct-line);font-size:12px;color:var(--ct-ink-2)}.upstream-summary>div{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.upstream-summary strong{color:var(--ct-ink);font-variant-numeric:tabular-nums}.upstream-summary i{height:12px;width:1px;background:var(--ct-line);margin:0 6px}.upstream-hint{margin:6px 0 0;font-size:12px;line-height:1.6;color:var(--ct-ink-2)}
.upstream-group{border-color:var(--ct-line);box-shadow:var(--ct-shadow);margin-top:12px}.upstream-group .user-group-header{padding:14px 16px;gap:12px;flex-wrap:wrap}.upstream-group .user-identity{flex:1 1 200px;min-width:0}.upstream-group .user-identity strong{font-size:14px;font-weight:600;overflow-wrap:anywhere}.upstream-group .user-identity small{font-size:11px;overflow-wrap:anywhere}.upstream-group .expand-arrow{font-size:20px;color:var(--ct-ink-3)}.upstream-group .group-count,.upstream-group .group-latest,.upstream-group .expand-label{font-size:12px}.upstream-group .group-latest{color:var(--ct-ink-3)}.upstream-group :deep(.el-tag){font-size:11px;height:22px}.upstream-group :deep(.el-table .cell){padding-left:12px;padding-right:12px}.upstream-group :deep(td.el-table__cell){padding-top:12px;padding-bottom:12px}.upstream-group .bill-number{font-size:12px;font-weight:500;overflow-wrap:anywhere}.upstream-group .records-table small{font-size:11px}.bill-order-count{font-size:12px;color:var(--ct-ink-2)}.bill-order-count strong{font-size:14px;font-weight:500;color:var(--ct-ink);font-variant-numeric:tabular-nums}.bill-check-counts{display:flex;gap:14px;flex-wrap:wrap;margin-top:4px;font-size:11px;color:var(--ct-ink-2)}
.upstream-records :deep(.el-scrollbar__bar.is-horizontal){height:8px}.upstream-records :deep(.el-scrollbar__thumb){background:var(--ct-ink-3)}
.upstream-bill-preview .preview-title h2{font-size:16px}.upstream-bill-preview .preview-title{padding-right:24px;gap:10px}.upstream-bill-preview .preview-mark{width:32px;height:32px;font-size:16px;border-radius:7px;background:var(--ct-accent-weak);color:var(--ct-accent);box-shadow:none}.upstream-bill-preview .preview-meta{grid-template-columns:1fr 1fr 1.2fr;border-radius:7px}.upstream-bill-preview .preview-meta>div{padding:10px 12px}.upstream-bill-preview .preview-meta strong{font-size:12px;overflow-wrap:anywhere}.upstream-bill-preview .preview-metrics{grid-template-columns:repeat(6,minmax(0,1fr));gap:8px;margin:12px 0}.upstream-bill-preview .metric-card{padding:12px;border-radius:7px;box-shadow:none}.upstream-bill-preview .metric-card span,.upstream-bill-preview .metric-card small{font-size:11px}.upstream-bill-preview .metric-card strong{font-size:20px;overflow-wrap:anywhere}.upstream-bill-preview .metric-card.accent{background:var(--ct-accent-weak)}.upstream-bill-preview .preview-tabs{padding:0 12px 12px;border-radius:8px}.upstream-bill-preview :deep(.preview-tabs .el-tabs__item){height:40px;font-weight:500}.upstream-bill-preview .preview-footer{gap:10px;flex-wrap:wrap}.upstream-bill-preview .preview-footer>div{display:flex;gap:8px;flex-wrap:wrap}.upstream-bill-preview .preview-footer .el-button+.el-button{margin-left:0}.upstream-bill-preview .anomaly-toolbar{flex-wrap:wrap}.upstream-bill-preview .anomaly-toolbar .el-alert{flex:1 1 280px}
@media(max-width:1100px){.upstream-bill-preview .preview-metrics{grid-template-columns:repeat(3,minmax(0,1fr))}}
@media(max-width:600px){.upstream-toolbar{padding:12px}.upstream-search{max-width:none;flex-basis:100%}.upstream-filter-row .bill-date-filter{width:100%}.upstream-filter-row .bill-date-filter :deep(.el-date-editor){min-width:0;width:100%;flex:1}.upstream-bill-preview .preview-meta{grid-template-columns:1fr}.upstream-bill-preview .preview-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.upstream-bill-preview .preview-title .el-tag{margin-left:0}.upstream-bill-preview .preview-title{flex-wrap:wrap}.upstream-bill-preview .preview-footer>span{display:none}}
</style>

<style>
body:has(.upstream-records){min-width:0}
.upstream-records .workspace,.upstream-records .content{min-width:0}
.el-dialog.bill-preview-dialog.upstream-bill-preview{padding:0;max-height:92vh;max-height:92dvh;display:flex;flex-direction:column;overflow:hidden;border:1px solid var(--ct-line);border-radius:12px}
.el-dialog.bill-preview-dialog.upstream-bill-preview .el-dialog__header{padding:16px 20px;margin:0;flex-shrink:0;border-bottom:1px solid var(--ct-line)}
.el-dialog.bill-preview-dialog.upstream-bill-preview .el-dialog__body{padding:16px 20px;min-height:0;overflow:auto;background:var(--ct-surface-2)}
.el-dialog.bill-preview-dialog.upstream-bill-preview .el-dialog__footer{padding:12px 20px;flex-shrink:0;border-top:1px solid var(--ct-line);background:var(--ct-surface)}
.upstream-bill-preview .el-dialog__headerbtn{top:10px;right:8px}
.upstream-bill-preview .el-scrollbar__bar.is-horizontal{height:8px}
</style>
