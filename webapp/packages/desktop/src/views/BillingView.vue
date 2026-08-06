<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import type { BillingUserSummary } from "@ct/shared";
import { dashboard, passthrough } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { useAuthStore } from "../stores/auth";
import { formatNumber, formatQuota } from "../utils/format";
import { savedGenerationRange, timeRangeShortcuts, validateGenerationRange } from "../utils/billingRange";

const filters = useFiltersStore();
const prefs = usePrefsStore();
const auth = useAuthStore();
const generationRange = ref<[string, string]>(savedGenerationRange("ct.billing.user.range"));
const search = ref("");
const page = ref(1);
const pageSize = ref(50);
const detailOpen = ref(false);
const selected = ref<BillingUserSummary>();
const generating = ref(false);
const tierSettings = ref<Record<string, { instance_id: string; user_id: number; use_tiered_pricing: boolean }>>({});
const jobProgress = ref(0);
const exporting = ref<Record<number, boolean>>({});
void prefs.load();
const state = useAsyncData(async () => {
  await filters.loadInstances(); if (!filters.site_id) return undefined;
  const keyword=search.value.trim()||undefined;
  const [from,to]=generationRange.value;
  const [bill,users,settings]=await Promise.all([
    dashboard.billingSummary({instance_id:filters.site_id,from,to,page:page.value,page_size:pageSize.value,search:keyword}),
    passthrough.users({site:filters.site_id,keyword,limit:pageSize.value,offset:(page.value-1)*pageSize.value}),
    dashboard.billingUserSettings(filters.site_id),
  ]);
  tierSettings.value=settings.items||{};
  if((bill.items?.length||0)>0)return {...bill,generated:true};
  const items:BillingUserSummary[]=users.items.map((user)=>({user_id:user.id,username:user.display_name||user.username,request_count:0,prompt_tokens:0,completion_tokens:0,cache_tokens:0,cache_write_tokens:0,abnormal_rows:0,quota:0,amount:"0.000000",balance:user.quota,unpriced_models:[],price_sources:[]}));
  return {...bill,items,total:users.total,summary:{...bill.summary,users:users.total},generated:false};
});
const generated = computed(() => state.data.value?.generated !== false);
const detail = useAsyncData(async () => {const[from,to]=generationRange.value;return selected.value ? dashboard.billingDetail({ instance_id: filters.site_id, user_id: selected.value.user_id, from, to }) : undefined;});
const currency = computed(() => prefs.currencySymbol || "$");
const money = (value: string | number | undefined) => `${currency.value}${Number(value || 0).toFixed(4)}`;
const balanceMoney = (quota: number | undefined) => formatQuota(quota ?? 0, prefs.quotaPerUnit, currency.value);
const unitPrice = (value: string | undefined) => value ? `${currency.value}${Number(value).toFixed(4)}` : "—";
const periodText = computed(() => `[${generationRange.value[0]}, ${generationRange.value[1]})`);
const exportURL = computed(() => {const[from,to]=generationRange.value;return`/api/dashboard/billing/summary?instance_id=${encodeURIComponent(filters.site_id)}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&search=${encodeURIComponent(search.value)}&format=csv`;});
const dailyExportURL = computed(() => {const[from,to]=generationRange.value;return selected.value?`/api/dashboard/billing/detail?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${selected.value.user_id}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&format=csv`:"#";});
const anomalyExportURL = computed(() => {const[from,to]=generationRange.value;return selected.value?`/api/dashboard/billing/anomalies?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${selected.value.user_id}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&format=csv`:"#"});
async function exportUser(row: BillingUserSummary){exporting.value={...exporting.value,[row.user_id]:true};try{const[from,to]=generationRange.value;const res=await fetch("/api/dashboard/billing/workbook-jobs",{method:"POST",credentials:"same-origin",headers:{"Content-Type":"application/json"},body:JSON.stringify({instance_id:filters.site_id,user_id:String(row.user_id),from,to})});if(!res.ok)throw new Error("创建导出任务失败");let task=await res.json();while(task.status==="pending"||task.status==="running"){await new Promise(r=>setTimeout(r,1500));task=await(await fetch(`/api/dashboard/billing/workbook-jobs?id=${task.id}`,{credentials:"same-origin"})).json()}if(task.status!=="complete")throw new Error(task.error||"导出任务失败");window.location.assign(`/api/dashboard/billing/workbook-jobs?id=${task.id}&download=1`)}catch(e){ElMessage.error(e instanceof Error?e.message:"导出失败")}finally{exporting.value={...exporting.value,[row.user_id]:false}}}
function openDetail(row: BillingUserSummary) { selected.value = row; detailOpen.value = true; void detail.reload(); }
async function generateBill(force=false){
  const invalid=validateGenerationRange(generationRange.value);if(invalid){ElMessage.warning(invalid);return;}generating.value=true;jobProgress.value=0;
  try{const [from,to]=generationRange.value;const result=await dashboard.generateBilling({instance_id:filters.site_id,from,to,force});let job=result.job;while(job.status==="pending"||job.status==="running"){jobProgress.value=job.total_steps?Math.round(job.completed_steps*100/job.total_steps):0;await new Promise(resolve=>setTimeout(resolve,1500));job=await dashboard.billingJob(job.id)}if(job.status==="failed")throw new Error(job.error_message||"账单任务失败");jobProgress.value=100;ElMessage.success(result.reused?"该区间已有账单，已直接加载":`账单生成完成，排除异常订单 ${job.abnormal_rows} 条`);await state.reload();}
  finally{generating.value=false;}
}
function useTiered(userID:number){return tierSettings.value[String(userID)]?.use_tiered_pricing!==false}
async function changeTiered(userID:number,value:boolean){await dashboard.saveBillingUserSetting({instance_id:filters.site_id,user_id:userID,use_tiered_pricing:value});tierSettings.value={...tierSettings.value,[String(userID)]:{instance_id:filters.site_id,user_id:userID,use_tiered_pricing:value}};ElMessage.success("阶梯计价设置已保存，下次生成账单时生效")}
watch(generationRange,(value)=>localStorage.setItem("ct.billing.user.range",JSON.stringify(value)),{deep:true});
let searchTimer: ReturnType<typeof setTimeout> | undefined;
watch(search, () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { page.value = 1; void state.reload(); }, 400); });
watch([generationRange, () => filters.site_id, pageSize], () => { page.value = 1; void state.reload(); if(detailOpen.value)void detail.reload(); });
watch(page, () => void state.reload());
void state.reload();
</script>
<template>
  <AppShell title="用户账单">
    <template #tools><span class="period-label">账单周期</span><el-date-picker v-model="generationRange" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" format="YYYY-MM-DD HH:mm:ss" range-separator="至" start-placeholder="开始时间" end-placeholder="结束时间" :shortcuts="timeRangeShortcuts" unlink-panels style="width:420px" /><el-input v-model="search" clearable placeholder="搜索用户名或用户 ID" style="width:220px" /><span v-if="generating" class="job-progress">生成 {{ jobProgress }}%</span><el-button v-if="auth.user?.role === 'admin'" type="primary" :loading="generating" @click="generateBill()">生成账单</el-button><el-popconfirm v-if="auth.user?.role === 'admin'" title="强制重新生成会创建新的账单版本，确定继续？" @confirm="generateBill(true)"><template #reference><el-button :disabled="generating">强制重新生成</el-button></template></el-popconfirm><el-button tag="a" :href="exportURL">导出汇总 CSV</el-button></template>
    <el-alert v-if="!generated && (state.data.value?.items?.length || 0) > 0" class="pending-alert" type="warning" title="该区间账单尚未生成" description="下方先显示用户列表；生成账单后才会出现请求量、Token 和金额。" :closable="false" show-icon />
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!(state.data.value?.items?.length || 0)" @retry="state.reload">
      <div class="billing-total" v-if="state.data.value?.summary"><span class="billing-period">下方数据区间 <b>{{ periodText }}</b><small>{{ generated ? "严格对应所选区间，仅统计消费日志并排除异常订单" : "该区间尚未生成，下方仅显示用户列表和 0 值" }}</small></span><span>用户 <b>{{ formatNumber(state.data.value.summary.users) }}</b></span><span>计费请求 <b>{{ formatNumber(state.data.value.summary.request_count) }}</b></span><span>合计 <b>{{ money(state.data.value.summary.amount) }}</b></span></div>
      <el-table :data="state.data.value?.items || []" class="billing-table" @row-click="openDetail">
        <el-table-column prop="username" label="用户" min-width="150"><template #default="s"><b>{{ s.row.username || `用户 ${s.row.user_id}` }}</b><small class="sub">ID {{ s.row.user_id }}</small></template></el-table-column>
        <el-table-column prop="amount" label="消费金额" min-width="120" align="right"><template #default="s">{{ money(s.row.amount) }}</template></el-table-column><el-table-column prop="request_count" label="请求数" min-width="100" align="right"><template #default="s">{{ formatNumber(s.row.request_count) }}</template></el-table-column><el-table-column prop="abnormal_rows" label="异常订单数" min-width="105" align="right"><template #default="s">{{ formatNumber(s.row.abnormal_rows) }}</template></el-table-column><el-table-column prop="prompt_tokens" label="普通输入 Token" min-width="130" align="right"><template #default="s">{{ formatNumber(s.row.prompt_tokens) }}</template></el-table-column><el-table-column prop="cache_tokens" label="缓存读取 Token" min-width="130" align="right"><template #default="s">{{ formatNumber(s.row.cache_tokens) }}</template></el-table-column><el-table-column prop="cache_write_tokens" label="缓存写入 Token" min-width="130" align="right"><template #default="s">{{ formatNumber(s.row.cache_write_tokens) }}</template></el-table-column><el-table-column prop="completion_tokens" label="输出 Token" min-width="120" align="right"><template #default="s">{{ formatNumber(s.row.completion_tokens) }}</template></el-table-column><el-table-column prop="quota" label="Quota 对照" min-width="110" align="right"><template #default="s">{{ formatNumber(s.row.quota) }}</template></el-table-column><el-table-column prop="balance" label="当前余额" min-width="130" align="right"><template #default="s">{{ balanceMoney(s.row.balance) }}</template></el-table-column>
        <el-table-column label="阶梯计价" width="115"><template #default="s"><el-switch v-if="auth.user?.role === 'admin'" :model-value="useTiered(s.row.user_id)" @click.stop @change="changeTiered(s.row.user_id, Boolean($event))" /><span v-else>{{ useTiered(s.row.user_id)?"启用":"停用" }}</span></template></el-table-column><el-table-column label="计价状态" min-width="150"><template #default="s"><el-tag v-if="!generated" type="info">未生成</el-tag><el-tag v-else-if="s.row.unpriced_models?.length" type="warning">{{ s.row.unpriced_models.length }} 个模型无法取价</el-tag><el-tag v-else type="success">已计价</el-tag></template></el-table-column><el-table-column label="操作" width="170"><template #default="s"><el-button link type="primary" @click.stop="openDetail(s.row)">查看</el-button><el-button link type="primary" :loading="exporting[s.row.user_id]" @click.stop="exportUser(s.row)">导出账单</el-button></template></el-table-column>
      </el-table><ListPager v-model:page="page" v-model:page-size="pageSize" :item-count="state.data.value?.items?.length || 0" :total="state.data.value?.total || 0" />
    </AsyncPanel>
    <el-drawer v-model="detailOpen" :title="`${selected?.username || '用户'} · ${generationRange[0]} 至 ${generationRange[1]}`" size="78%">
      <div class="drawer-actions"><el-button :loading="Boolean(selected && exporting[selected.user_id])" @click="selected && exportUser(selected)">{{ selected && exporting[selected.user_id] ? "后台生成中" : "导出完整账单 Excel" }}</el-button><el-button tag="a" :href="dailyExportURL">导出日账单</el-button><el-button tag="a" :href="anomalyExportURL">导出异常订单</el-button><router-link :to="`/readonly-logs?username=${encodeURIComponent(selected?.username || '')}&from=${generationRange[0]}&to=${generationRange[1]}`"><el-button>查看使用日志</el-button></router-link></div>
      <AsyncPanel :loading="detail.loading.value" :error="detail.error.value" :empty="!detail.data.value?.items?.length" @retry="detail.reload"><el-table :data="detail.data.value?.items || []" class="billing-detail-table" table-layout="fixed"><el-table-column prop="day" label="日期" width="105" /><el-table-column label="模型信息" min-width="165"><template #default="s"><b class="cell-primary">{{ s.row.model_name }}</b><small class="cell-secondary">{{ s.row.group_name || "默认分组" }} · {{ s.row.tier_from > 0 ? `档位 ≥${formatNumber(s.row.tier_from)}` : "基础价" }}</small></template></el-table-column><el-table-column label="订单" min-width="125"><template #default="s"><div class="metric-pairs"><span>总数</span><b>{{ formatNumber(s.row.request_count + s.row.abnormal_rows) }}</b><span>计费</span><b>{{ formatNumber(s.row.request_count) }}</b><span>异常</span><b :class="{ danger: s.row.abnormal_rows > 0 }">{{ formatNumber(s.row.abnormal_rows) }}</b></div></template></el-table-column><el-table-column label="Token 用量" min-width="230"><template #default="s"><div class="metric-grid"><span>普通</span><b>{{ formatNumber(s.row.prompt_tokens) }}</b><span>读取</span><b>{{ formatNumber(s.row.cache_tokens) }}</b><span>写入</span><b>{{ formatNumber(s.row.cache_write_tokens) }}</b><span>输出</span><b>{{ formatNumber(s.row.completion_tokens) }}</b></div></template></el-table-column><el-table-column label="单价 / 1M" min-width="205"><template #default="s"><div class="metric-grid"><span>输入</span><b>{{ unitPrice(s.row.input_price) }}</b><span>读取</span><b>{{ unitPrice(s.row.cache_price) }}</b><span>写入</span><b>{{ unitPrice(s.row.cache_write_price) }}</b><span>输出</span><b>{{ unitPrice(s.row.output_price) }}</b></div></template></el-table-column><el-table-column prop="amount" label="金额" min-width="110" align="right"><template #default="s"><b>{{ s.row.unpriced ? "无法取价" : money(s.row.amount) }}</b></template></el-table-column></el-table></AsyncPanel>
    </el-drawer>
  </AppShell>
</template>
<style scoped>.billing-total{display:flex;align-items:center;gap:28px;padding:12px 4px}.billing-total b{font-size:18px;color:var(--el-color-primary);font-variant-numeric:tabular-nums}.billing-period{padding-right:20px;border-right:1px solid var(--el-border-color)}.billing-period b{font-size:14px}.billing-period small{display:block;margin-top:3px;color:var(--el-text-color-secondary)}.pending-alert{margin-bottom:10px}.billing-table :deep(.el-table__row){cursor:pointer}.billing-table :deep(.cell),.billing-detail-table :deep(.cell){font-variant-numeric:tabular-nums}.sub,.cell-secondary{display:block;color:var(--el-text-color-secondary);margin-top:3px}.cell-primary{color:var(--el-text-color-primary)}.metric-pairs,.metric-grid{display:grid;grid-template-columns:auto 1fr;column-gap:8px;row-gap:3px;align-items:center}.metric-grid{grid-template-columns:auto minmax(0,1fr) auto minmax(0,1fr)}.metric-pairs span,.metric-grid span{color:var(--el-text-color-secondary);font-size:12px}.metric-pairs b,.metric-grid b{text-align:right;white-space:nowrap}.danger{color:var(--el-color-danger)}.drawer-actions{display:flex;gap:8px;justify-content:flex-end;margin-bottom:12px}.job-progress{color:var(--el-color-primary);font-variant-numeric:tabular-nums}.period-label{color:var(--el-text-color-secondary);font-size:13px}</style>
