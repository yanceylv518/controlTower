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

const filters = useFiltersStore();
const prefs = usePrefsStore();
const auth = useAuthStore();
const month = ref(new Date().toISOString().slice(0, 7));
const search = ref("");
const page = ref(1);
const pageSize = ref(50);
const detailOpen = ref(false);
const selected = ref<BillingUserSummary>();
const generating = ref(false);
const tierSettings = ref<Record<string, { instance_id: string; user_id: number; use_tiered_pricing: boolean }>>({});
const jobProgress = ref(0);
void prefs.load();
const state = useAsyncData(async () => {
  await filters.loadInstances(); if (!filters.site_id) return undefined;
  const keyword=search.value.trim()||undefined;
  const [bill,users,settings]=await Promise.all([
    dashboard.billingSummary({instance_id:filters.site_id,month:month.value,page:page.value,page_size:pageSize.value,search:keyword}),
    passthrough.users({site:filters.site_id,keyword,limit:pageSize.value,offset:(page.value-1)*pageSize.value}),
    dashboard.billingUserSettings(filters.site_id),
  ]);
  tierSettings.value=settings.items||{};
  if((bill.items?.length||0)>0)return bill;
  const items:BillingUserSummary[]=users.items.map((user)=>({user_id:user.id,username:user.display_name||user.username,request_count:0,prompt_tokens:0,completion_tokens:0,cache_tokens:0,quota:0,amount:"0.000000",balance:user.quota,unpriced_models:[],price_sources:[]}));
  return {...bill,items,total:users.total,summary:{...bill.summary,users:users.total}};
});
const detail = useAsyncData(async () => selected.value ? dashboard.billingDetail({ instance_id: filters.site_id, user_id: selected.value.user_id, month: month.value }) : undefined);
const currency = computed(() => prefs.currencySymbol || "$");
const money = (value: string | number | undefined) => `${currency.value}${Number(value || 0).toFixed(4)}`;
const balanceMoney = (quota: number | undefined) => money(Number(quota || 0) / (prefs.quotaPerUnit || 500000));
const unitPrice = (value: string | undefined) => value ? `${currency.value}${Number(value).toFixed(4)}` : "—";
const exportURL = computed(() => `/api/dashboard/billing/summary?instance_id=${encodeURIComponent(filters.site_id)}&month=${month.value}&search=${encodeURIComponent(search.value)}&format=csv`);
const detailExportURL = computed(() => selected.value ? `/api/dashboard/billing/workbook?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${selected.value.user_id}&month=${month.value}` : "#");
const anomalyExportURL = computed(() => {const start=new Date(`${month.value}-01T00:00:00`);const end=new Date(start.getFullYear(),start.getMonth()+1,0);return selected.value?`/api/dashboard/billing/anomalies?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${selected.value.user_id}&from=${localDate(start)}&to=${localDate(end)}&format=csv&limit=5000`:"#"});
const userDetailExportURL = (row: BillingUserSummary) => `/api/dashboard/billing/workbook?instance_id=${encodeURIComponent(filters.site_id)}&user_id=${row.user_id}&month=${month.value}`;
function openDetail(row: BillingUserSummary) { selected.value = row; detailOpen.value = true; void detail.reload(); }
function localDate(value: Date) { const y=value.getFullYear(); const m=String(value.getMonth()+1).padStart(2,"0"); const d=String(value.getDate()).padStart(2,"0"); return `${y}-${m}-${d}`; }
async function generateBill(force=false){
  const start=new Date(`${month.value}-01T00:00:00`); const end=new Date(start.getFullYear(),start.getMonth()+1,0); const today=new Date();
  if(start>today){ElMessage.warning("不能生成未来月份账单");return;}
  const through=end<today?end:today; generating.value=true;
  try{const result=await dashboard.generateBilling({instance_id:filters.site_id,from:localDate(start),to:localDate(through),force});ElMessage.success(result.reused?"已存在相同账期任务，正在复用结果":"账单任务已创建，可离开页面后台继续处理");let job=result.job;while(job.status==="pending"||job.status==="running"){jobProgress.value=job.total_steps?Math.round(job.completed_steps*100/job.total_steps):0;await new Promise(resolve=>setTimeout(resolve,1500));job=await dashboard.billingJob(job.id)}if(job.status==="failed")throw new Error(job.error_message||"账单任务失败");jobProgress.value=100;ElMessage.success(result.reused?"该账期账单已生成，无需重复处理":`账单生成完成，排除异常订单 ${job.abnormal_rows} 条`);await state.reload();}
  finally{generating.value=false;}
}
function useTiered(userID:number){return tierSettings.value[String(userID)]?.use_tiered_pricing!==false}
async function changeTiered(userID:number,value:boolean){await dashboard.saveBillingUserSetting({instance_id:filters.site_id,user_id:userID,use_tiered_pricing:value});tierSettings.value={...tierSettings.value,[String(userID)]:{instance_id:filters.site_id,user_id:userID,use_tiered_pricing:value}};ElMessage.success("阶梯计价设置已保存，下次生成账单时生效")}
watch([month, search, () => filters.site_id, pageSize], () => { page.value = 1; void state.reload(); });
watch(page, () => void state.reload());
void state.reload();
</script>
<template>
  <AppShell title="用户账单">
    <template #tools><el-date-picker v-model="month" type="month" value-format="YYYY-MM" format="YYYY-MM" :clearable="false" /><el-input v-model="search" clearable placeholder="搜索用户名或用户 ID" style="width:220px" /><span v-if="generating" class="job-progress">生成 {{ jobProgress }}%</span><el-button v-if="auth.user?.role === 'admin'" type="primary" :loading="generating" @click="generateBill()">生成账单</el-button><el-popconfirm v-if="auth.user?.role === 'admin'" title="强制重新生成会创建新的账单版本，确定继续？" @confirm="generateBill(true)"><template #reference><el-button :disabled="generating">强制重新生成</el-button></template></el-popconfirm><el-button tag="a" :href="exportURL">导出汇总 CSV</el-button></template>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!(state.data.value?.items?.length || 0)" @retry="state.reload">
      <div class="billing-total" v-if="state.data.value?.summary"><span>用户 <b>{{ state.data.value.summary.users }}</b></span><span>请求 <b>{{ state.data.value.summary.request_count }}</b></span><span>合计 <b>{{ money(state.data.value.summary.amount) }}</b></span></div>
      <el-table :data="state.data.value?.items || []" @row-click="openDetail">
        <el-table-column prop="username" label="用户" min-width="150"><template #default="s"><b>{{ s.row.username || `用户 ${s.row.user_id}` }}</b><small class="sub">ID {{ s.row.user_id }}</small></template></el-table-column>
        <el-table-column prop="amount" label="消费金额" min-width="120"><template #default="s">{{ money(s.row.amount) }}</template></el-table-column><el-table-column prop="request_count" label="请求数" min-width="100" /><el-table-column prop="prompt_tokens" label="输入 Token" min-width="120" /><el-table-column prop="completion_tokens" label="输出 Token" min-width="120" /><el-table-column prop="cache_tokens" label="缓存 Token" min-width="120" /><el-table-column prop="quota" label="Quota 对照" min-width="110" /><el-table-column prop="balance" label="当前余额" min-width="130"><template #default="s">{{ balanceMoney(s.row.balance) }}</template></el-table-column>
        <el-table-column label="阶梯计价" width="115"><template #default="s"><el-switch v-if="auth.user?.role === 'admin'" :model-value="useTiered(s.row.user_id)" @click.stop @change="changeTiered(s.row.user_id, Boolean($event))" /><span v-else>{{ useTiered(s.row.user_id)?"启用":"停用" }}</span></template></el-table-column><el-table-column label="计价状态" min-width="150"><template #default="s"><el-tag v-if="s.row.unpriced_models?.length" type="warning">{{ s.row.unpriced_models.length }} 个模型无法取价</el-tag><el-tag v-else type="success">已计价</el-tag></template></el-table-column><el-table-column label="操作" width="170"><template #default="s"><el-button link type="primary" @click.stop="openDetail(s.row)">查看</el-button><el-button link type="primary" tag="a" :href="userDetailExportURL(s.row)" @click.stop>导出账单</el-button></template></el-table-column>
      </el-table><ListPager v-model:page="page" v-model:page-size="pageSize" :item-count="state.data.value?.items?.length || 0" :total="state.data.value?.total || 0" />
    </AsyncPanel>
    <el-drawer v-model="detailOpen" :title="`${selected?.username || '用户'} · ${month}`" size="78%">
      <div class="drawer-actions"><el-button tag="a" :href="detailExportURL">导出完整账单 Excel</el-button><el-button tag="a" :href="anomalyExportURL">导出异常订单</el-button><router-link :to="`/readonly-logs?user_id=${selected?.user_id || ''}&month=${month}`"><el-button>查看使用日志</el-button></router-link></div>
      <AsyncPanel :loading="detail.loading.value" :error="detail.error.value" :empty="!detail.data.value?.items?.length" @retry="detail.reload"><el-table :data="detail.data.value?.items || []"><el-table-column prop="day" label="日期" width="110" /><el-table-column prop="model_name" label="模型" min-width="160" /><el-table-column prop="group_name" label="分组" min-width="90" /><el-table-column prop="request_count" label="请求" width="75" /><el-table-column prop="prompt_tokens" label="输入 Token" min-width="105" /><el-table-column prop="completion_tokens" label="输出 Token" min-width="105" /><el-table-column label="输入价/1M" width="115"><template #default="s">{{ unitPrice(s.row.input_price) }}</template></el-table-column><el-table-column label="缓存价/1M" width="115"><template #default="s">{{ unitPrice(s.row.cache_price) }}</template></el-table-column><el-table-column label="输出价/1M" width="115"><template #default="s">{{ unitPrice(s.row.output_price) }}</template></el-table-column><el-table-column prop="amount" label="金额" width="110"><template #default="s">{{ s.row.unpriced ? "无法取价" : money(s.row.amount) }}</template></el-table-column></el-table></AsyncPanel>
    </el-drawer>
  </AppShell>
</template>
<style scoped>.billing-total{display:flex;gap:28px;padding:12px 4px}.billing-total b{font-size:18px;color:var(--el-color-primary)}.sub{display:block;color:var(--el-text-color-secondary);margin-top:3px}.drawer-actions{display:flex;gap:8px;justify-content:flex-end;margin-bottom:12px}.job-progress{color:var(--el-color-primary);font-variant-numeric:tabular-nums}</style>
