<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import type { BillingJob } from "@ct/shared";
import AppShell from "./AppShell.vue";
import AsyncPanel from "./AsyncPanel.vue";
import { dashboard } from "../api";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { formatNumber } from "../utils/format";
import { billingReadErrorMessage, downloadBillingFile } from "../utils/httpError";

const props = defineProps<{ billType: "user" | "upstream" }>();
const router = useRouter();
const detailVisible = ref(false);
const detailLoading = ref(false);
const downloadingBillId = ref("");
type PreviewRow = Record<string, unknown>;
const detail = ref<{job:BillingJob;total_orders:number;normal_orders:number;billable_orders:number;anomaly_total:number;reconciliation_total:number;review_required:boolean;count_balanced:boolean;model_summary:PreviewRow[];daily_summary:PreviewRow[];token_summary:PreviewRow[];anomalies:PreviewRow[];reconciliation:PreviewRow[]} | null>(null);
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
async function viewBill(job:BillingJob){detailVisible.value=true;detailLoading.value=true;detail.value=null;try{detail.value=await dashboard.billingStatementResult(job.id)}catch(error){ElMessage.error(billingReadErrorMessage(error,"账单加载失败"))}finally{detailLoading.value=false}}
const safeFilename=(value:string)=>value.replace(/[<>:"/\\|?*\x00-\x1f]/g,"-").trim();
async function downloadBill(job:BillingJob){
  if(downloadingBillId.value)return;
  const kind=job.job_type==="upstream_statement"?"上游账单":"用户账单";
  const filename=`${kind}-${safeFilename(job.bill_no||job.id)}.zip`;
  downloadingBillId.value=job.id;
  const preparing=ElMessage({message:"正在准备账单 ZIP，大账单可能需要一些时间，请勿重复点击",type:"info",duration:0});
  try{
    await downloadBillingFile(`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.id)}&download=1`,"账单压缩包下载失败",filename);
    ElMessage.success("账单 ZIP 已开始下载");
  }catch(error){
    ElMessage.error(billingReadErrorMessage(error,"账单压缩包下载失败"));
  }finally{
    preparing.close();
    downloadingBillId.value="";
  }
}
const queryDate=(value?:string)=>{if(!value)return"";const d=new Date(value);return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`};
async function downloadAnomalies(job:BillingJob){const url=`/api/dashboard/billing/anomalies?instance_id=${encodeURIComponent(job.instance_id)}&job_id=${encodeURIComponent(job.id)}&user_id=${job.user_id||0}&from=${queryDate(job.range_from)}&to=${queryDate(job.range_to)}&format=csv`;try{await downloadBillingFile(url,"异常订单下载失败",`${job.bill_no||job.id}-内部异常.csv`)}catch(error){ElMessage.error(billingReadErrorMessage(error,"异常订单下载失败"))}}
async function downloadReconciliation(job:BillingJob){const url=`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.id)}&export=reconciliation`;try{await downloadBillingFile(url,"核对差异下载失败",`${job.bill_no||job.id}-核对差异.csv`)}catch(error){ElMessage.error(billingReadErrorMessage(error,"核对差异下载失败"))}}
async function deleteBill(job:BillingJob){try{await ElMessageBox.confirm("删除后将同时清理该账单、生成任务和本地明细文件，且不可恢复。确定删除吗？","删除账单",{type:"warning",confirmButtonText:"删除"});await dashboard.deleteBillingStatement(job.id);if(detail.value?.job.id===job.id){detailVisible.value=false;detail.value=null}ElMessage.success("账单已删除");await state.reload()}catch(error){if(error!=="cancel"&&error!=="close")ElMessage.error(billingReadErrorMessage(error,"账单删除失败"))}}
watch(() => [filters.site_id, props.billType] as const, () => void state.reload(), { immediate: true });
</script>

<template>
  <AppShell :title="title">
    <template #tools><el-button type="primary" @click="createBill">生成{{ title }}</el-button><el-button @click="state.reload">刷新</el-button></template>
    <el-alert :title="`${title}只展示账单任务已经生成的可交付结果；每条记录对应一个对象、一个账期和一个账单压缩包。`" type="info" :closable="false" show-icon />
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!records.length" :empty-text="`暂无${title}，请先创建账单任务`" @retry="state.reload">
      <el-table :data="records" row-key="id" class="records-table">
        <el-table-column label="账单编号" min-width="300"><template #default="s"><b>{{ s.row.bill_no || s.row.id }}</b><small>内部任务：{{s.row.id}}</small></template></el-table-column>
        <el-table-column :label="props.billType === 'user' ? '用户' : '上游'" min-width="150"><template #default="s">{{ subject(s.row) }}</template></el-table-column>
        <el-table-column prop="instance_id" label="站点" min-width="130" />
        <el-table-column label="账单周期" min-width="210"><template #default="s">{{ formatRange(s.row) }}</template></el-table-column>
        <el-table-column label="计费订单" width="110" align="right"><template #default="s">{{ formatNumber(s.row.billed_rows || 0) }}</template></el-table-column>
        <el-table-column label="内部异常" width="110" align="right"><template #default="s"><span :class="{ warning: s.row.abnormal_rows }">{{ formatNumber(s.row.abnormal_rows || 0) }}</span></template></el-table-column>
        <el-table-column label="核对差异" width="110" align="right"><template #default="s"><span :class="{ danger: s.row.mismatch_rows }">{{ formatNumber(s.row.mismatch_rows || 0) }}</span></template></el-table-column>
        <el-table-column label="生成时间" min-width="170"><template #default="s">{{ formatTime(s.row.updated_at || s.row.created_at) }}</template></el-table-column>
        <el-table-column label="状态" width="110"><template #default="s"><el-tag :type="s.row.mismatch_rows ? 'warning' : 'success'">{{s.row.mismatch_rows ? '待复核' : '可使用'}}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="230" fixed="right"><template #default="s"><el-button link type="primary" @click="viewBill(s.row)">查看账单</el-button><el-button link type="primary" :loading="downloadingBillId===s.row.id" :disabled="!!s.row.mismatch_rows||!!downloadingBillId" @click="downloadBill(s.row)">下载 ZIP</el-button><el-button link type="danger" @click="deleteBill(s.row)">删除</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-dialog v-model="detailVisible" width="min(1180px, calc(100vw - 48px))" top="7vh" class="bill-preview-dialog" destroy-on-close>
      <template #header><div class="preview-title"><div class="preview-mark">账</div><div><h2>{{props.billType==='user'?'用户账单':'上游账单'}}</h2><p>{{detail?.job.bill_no || '账单内容预览'}}</p></div><el-tag v-if="detail" :type="detail.review_required?'warning':'success'" effect="light" round>{{detail.review_required?'待复核':'可使用'}}</el-tag></div></template>
      <div v-loading="detailLoading" class="preview-body">
        <template v-if="detail"><div class="preview-meta"><div><span>账单对象</span><strong>{{subject(detail.job)}}</strong></div><div><span>账单周期</span><strong>{{formatRange(detail.job)}}</strong></div><div><span>内部任务 ID</span><strong class="mono">{{detail.job.id}}</strong></div></div>
        <el-alert v-if="detail.review_required" class="review-alert" type="warning" :closable="false" show-icon title="该账单存在核对差异，当前不可直接交付。请下载差异列表复核并修正后重新生成。"/>
        <div class="preview-metrics"><div class="metric-card"><span>原始总订单</span><strong>{{previewNumber(detail.total_orders)}}</strong><small>{{detail.count_balanced?'三类数量核对一致':'数量核对异常'}}</small></div><div class="metric-card"><span>核对正常</span><strong>{{previewNumber(detail.normal_orders)}}</strong><small>金额核对一致</small></div><div class="metric-card"><span>异常订单</span><strong :class="{danger:detail.anomaly_total}">{{previewNumber(detail.anomaly_total)}}</strong><small>不进入账单</small></div><div class="metric-card"><span>核对差异</span><strong :class="{danger:detail.reconciliation_total}">{{previewNumber(detail.reconciliation_total)}}</strong><small>仍计入账单，需复核</small></div><div class="metric-card"><span>账单天数</span><strong>{{previewDays}}</strong><small>{{detail.model_summary.length}} 个计费模型</small></div><div class="metric-card accent"><span>最终费用</span><strong>¥ {{previewMoney(previewTotal)}}</strong><small>计费订单 {{previewNumber(detail.billable_orders)}}</small></div></div>
        <el-tabs class="preview-tabs">
          <el-tab-pane label="区间统计"><el-table :data="detail.model_summary" size="small" max-height="460" stripe><el-table-column prop="model_name" label="模型" min-width="180"><template #default="s"><b class="model-name">{{s.row.model_name}}</b></template></el-table-column><el-table-column prop="request_count" label="订单数" width="110" align="right"><template #default="s">{{previewNumber(s.row.request_count)}}</template></el-table-column><el-table-column prop="prompt_tokens" label="输入 Token" width="145" align="right"><template #default="s">{{previewNumber(s.row.prompt_tokens)}}</template></el-table-column><el-table-column prop="completion_tokens" label="输出 Token" width="145" align="right"><template #default="s">{{previewNumber(s.row.completion_tokens)}}</template></el-table-column><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="150" align="right"><template #default="s">{{previewNumber(s.row.cache_read_tokens)}}</template></el-table-column><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="150" align="right"><template #default="s">{{previewNumber(s.row.cache_write_tokens)}}</template></el-table-column><el-table-column prop="amount" label="总费用" width="130" align="right"><template #default="s">¥ {{previewMoney(s.row.amount)}}</template></el-table-column><el-table-column prop="discount" label="折扣" width="90" align="right"><template #default="s"><el-tag size="small" type="info">{{previewDiscount(s.row.discount)}}</el-tag></template></el-table-column><el-table-column prop="final_amount" label="最终费用" width="140" align="right"><template #default="s"><b class="money">¥ {{previewMoney(s.row.final_amount)}}</b></template></el-table-column></el-table></el-tab-pane>
          <el-tab-pane label="日账单统计"><el-table :data="detail.daily_summary" size="small" max-height="520"><el-table-column prop="day" label="日期" width="110"/><el-table-column prop="model_name" label="模型" min-width="150"/><el-table-column prop="request_count" label="订单数" width="100" align="right"/><el-table-column prop="prompt_tokens" label="输入 Token" width="135" align="right"/><el-table-column prop="completion_tokens" label="输出 Token" width="135" align="right"/><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="145" align="right"/><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="145" align="right"/><el-table-column prop="input_price" label="输入单价" width="110" align="right"/><el-table-column prop="output_price" label="输出单价" width="110" align="right"/><el-table-column prop="cache_read_price" label="缓存读取单价" width="130" align="right"/><el-table-column prop="cache_write_price" label="缓存写入单价" width="130" align="right"/><el-table-column prop="amount" label="总费用" width="120" align="right"/><el-table-column prop="discount" label="折扣" width="80" align="right"/><el-table-column prop="final_amount" label="最终费用" width="120" align="right"/><el-table-column prop="detail_file" label="明细文件" min-width="230"/></el-table></el-tab-pane>
          <el-tab-pane v-if="props.billType==='user'" label="按令牌统计"><el-table :data="detail.token_summary" size="small" max-height="520"><el-table-column prop="token_id" label="令牌 ID" width="100"/><el-table-column prop="token_name" label="令牌" min-width="150"/><el-table-column prop="day" label="日期" width="110"/><el-table-column prop="model_name" label="模型" min-width="150"/><el-table-column prop="request_count" label="订单数" width="100" align="right"/><el-table-column prop="prompt_tokens" label="输入 Token" width="140" align="right"/><el-table-column prop="completion_tokens" label="输出 Token" width="140" align="right"/><el-table-column prop="cache_read_tokens" label="缓存读取 Token" width="145" align="right"/><el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="145" align="right"/><el-table-column prop="amount" label="总费用" width="130" align="right"/></el-table></el-tab-pane>
          <el-tab-pane><template #label><span>内部异常 <el-badge :value="detail.anomaly_total" :hidden="!detail.anomaly_total" type="danger"/></span></template><div class="anomaly-toolbar"><el-alert type="warning" :closable="false" show-icon title="异常订单仅供内部核对，不会写入客户账单或账单 ZIP。"/><el-button type="danger" plain :disabled="!detail.anomaly_total" @click="downloadAnomalies(detail.job)">下载异常 CSV</el-button></div><el-table :data="detail.anomalies" size="small" max-height="430" stripe><el-table-column prop="created_at" label="请求时间" width="165"/><el-table-column prop="request_id" label="Request ID" min-width="190" show-overflow-tooltip/><el-table-column v-if="props.billType==='upstream'" prop="username" label="用户" width="120"/><el-table-column v-if="props.billType==='upstream'" prop="channel_name" label="渠道" width="130"/><el-table-column prop="token_name" label="令牌" width="130"/><el-table-column prop="model_name" label="模型" width="140"/><el-table-column prop="actual_amount" label="实际扣费" width="110" align="right"/><el-table-column prop="reasons" label="异常原因" min-width="220" show-overflow-tooltip/></el-table><p v-if="detail.anomaly_total>detail.anomalies.length" class="preview-note">页面仅展示前 {{detail.anomalies.length}} 条，完整 {{previewNumber(detail.anomaly_total)}} 条请下载 CSV。</p></el-tab-pane>
          <el-tab-pane><template #label><span>核对差异 <el-badge :value="detail.reconciliation_total" :hidden="!detail.reconciliation_total" type="warning"/></span></template><div class="anomaly-toolbar"><el-alert type="warning" :closable="false" show-icon title="这些订单仍计入账单；存在记录时账单必须复核，不能直接交付。"/><el-button type="warning" plain :disabled="!detail.reconciliation_total" @click="downloadReconciliation(detail.job)">下载核对差异 CSV</el-button></div><el-table :data="detail.reconciliation" size="small" max-height="430" stripe><el-table-column prop="created_at" label="请求时间" width="165"/><el-table-column prop="request_id" label="Request ID" min-width="190" show-overflow-tooltip/><el-table-column prop="model_name" label="模型" width="140"/><el-table-column prop="logged_quota" label="日志 Quota" width="115" align="right"/><el-table-column prop="calculated_quota" label="重算 Quota" width="115" align="right"/><el-table-column prop="quota_difference" label="Quota 差额" width="110" align="right"/><el-table-column prop="logged_amount" label="日志金额" width="110" align="right"/><el-table-column prop="calculated_amount" label="重算金额" width="110" align="right"/><el-table-column prop="reason" label="差异原因" min-width="220" show-overflow-tooltip/></el-table><p v-if="detail.reconciliation_total>detail.reconciliation.length" class="preview-note">页面仅展示前 {{detail.reconciliation.length}} 条，完整 {{previewNumber(detail.reconciliation_total)}} 条请下载 CSV。</p></el-tab-pane>
        </el-tabs><el-empty v-if="!detail.normal_orders" description="该账期没有查询到正常订单" :image-size="60"/></template>
      </div>
      <template #footer><div class="preview-footer"><span>{{detail?.review_required?'账单待复核，暂不可下载正式 ZIP':'下载包包含主账单及每日明细文件'}}</span><div><el-button @click="detailVisible=false">关闭</el-button><el-button v-if="detail" type="primary" :loading="downloadingBillId===detail.job.id" :disabled="detail.review_required||!!downloadingBillId" @click="downloadBill(detail.job)">下载账单 ZIP</el-button></div></div></template>
    </el-dialog>
  </AppShell>
</template>

<style scoped>
.records-table{margin-top:16px}b,small{display:block}small{margin-top:4px;color:var(--el-text-color-secondary);font-size:12px}.warning{color:var(--el-color-warning);font-weight:600}
.preview-title{display:flex;align-items:center;gap:12px}.preview-title h2{margin:0;font-size:20px;color:#172033}.preview-title p{margin:4px 0 0;color:#8490a5;font-size:12px}.preview-title .el-tag{margin-left:auto;margin-right:12px}.preview-mark{display:grid;place-items:center;width:42px;height:42px;border-radius:12px;color:#fff;font-size:20px;font-weight:700;background:linear-gradient(135deg,#315ee8,#6c8cff);box-shadow:0 7px 18px rgba(49,94,232,.24)}
.preview-body{min-height:300px}.preview-meta{display:grid;grid-template-columns:1fr 1fr 1.3fr;gap:1px;background:#e8edf5;border:1px solid #e8edf5;border-radius:10px;overflow:hidden}.preview-meta>div{padding:13px 16px;background:#f8fafd}.preview-meta span{display:block;color:#8994a7;font-size:12px;margin-bottom:5px}.preview-meta strong{color:#28344a;font-size:14px}.mono{font-family:Consolas,monospace;font-size:12px!important;font-weight:500}
.review-alert{margin-top:14px}.preview-metrics{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:16px 0}.metric-card{position:relative;padding:16px 18px;border:1px solid #e7ebf2;border-radius:12px;background:#fff;box-shadow:0 3px 12px rgba(35,54,90,.04)}.metric-card span{display:block;color:#8490a5;font-size:13px}.metric-card strong{display:block;margin:7px 0 3px;color:#1d2940;font-size:24px;line-height:1.2}.metric-card small{margin:0}.metric-card.accent{border-color:#dce5ff;background:linear-gradient(135deg,#f3f6ff,#eef3ff)}.metric-card.accent strong{color:#315ee8}
.preview-tabs{padding:0 14px 14px;border:1px solid #e7ebf2;border-radius:12px;background:#fff}.model-name{color:#34425a}.money{color:#315ee8}.preview-footer{display:flex;align-items:center;justify-content:space-between}.preview-footer>span{color:#8b96a8;font-size:12px}
.danger{color:#e45656!important}.anomaly-toolbar{display:flex;align-items:center;gap:12px;margin:12px 0}.anomaly-toolbar .el-alert{flex:1}.preview-note{margin:10px 0 0;color:#8b96a8;font-size:12px;text-align:right}
:deep(.bill-preview-dialog){border-radius:14px;overflow:hidden;box-shadow:0 24px 70px rgba(20,35,65,.2)}:deep(.bill-preview-dialog .el-dialog__header){padding:20px 24px 16px;margin:0;border-bottom:1px solid #edf0f5}:deep(.bill-preview-dialog .el-dialog__body){padding:18px 24px;background:#f6f8fb}:deep(.bill-preview-dialog .el-dialog__footer){padding:14px 24px;border-top:1px solid #edf0f5}:deep(.preview-tabs .el-tabs__header){margin-bottom:0}:deep(.preview-tabs .el-tabs__item){height:48px;font-weight:600}:deep(.preview-tabs .el-table){border-radius:8px}:deep(.preview-tabs .el-table th.el-table__cell){background:#f7f9fc;color:#65728a;font-weight:600}
@media(max-width:1000px){.preview-meta{grid-template-columns:1fr}.preview-metrics{grid-template-columns:repeat(2,1fr)}}
</style>
