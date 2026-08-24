<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { BillingUserBillDay, BillingUserTokenBillDay } from "@ct/shared";
import { ElMessage } from "element-plus";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { downloadBillingFile } from "../utils/httpError";
import { formatNumber } from "../utils/format";

type UserBills = { user_id:number;username:string;days:BillingUserBillDay[];amount:number;anomalyAmount:number;requests:number;anomalies:number;promptTokens:number;completionTokens:number;cacheReadTokens:number;cacheWriteTokens:number };
type TokenBills = { token_id:number;token_name:string;rows:BillingUserTokenBillDay[];amount:number;anomalyAmount:number;requests:number;anomalies:number;promptTokens:number;completionTokens:number;cacheReadTokens:number;cacheWriteTokens:number };
const route = useRoute();
const router = useRouter();
const filters = useFiltersStore();
const now = new Date();
const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
const pad = (value:number) => String(value).padStart(2,"0");
const dateValue = (value:Date) => `${value.getFullYear()}-${pad(value.getMonth()+1)}-${pad(value.getDate())}`;
const routedSite = typeof route.query.site === "string" ? route.query.site : "";
if (routedSite) filters.selectSite(routedSite);
const routedUserID = typeof route.query.user_id === "string" ? Number(route.query.user_id) : 0;
const search = ref("");
const routedFrom = typeof route.query.from === "string" ? route.query.from.slice(0,10) : "";
const routedThrough = typeof route.query.through === "string" ? route.query.through.slice(0,10) : "";
const dateRange = ref<[string,string]>(routedFrom && routedThrough ? [routedFrom,routedThrough] : [dateValue(new Date(yesterday.getFullYear(),yesterday.getMonth(),1)),dateValue(yesterday)]);
const selected = ref<UserBills>();
const userOpen = ref(false);
const billView = ref<"daily"|"token">("daily");
const tokenState = useAsyncData(async () => {
  if (!filters.site_id || !selected.value) return { items:[] as BillingUserTokenBillDay[] };
  return dashboard.billingUserTokenDays({ instance_id:filters.site_id, user_id:selected.value.user_id, from:dateRange.value[0], through:dateRange.value[1] });
});
const tokenBills = computed<TokenBills[]>(() => {
  const grouped = new Map<number,TokenBills>();
  for (const row of tokenState.data.value?.items || []) {
    let token = grouped.get(row.token_id);
    if (!token) { token={token_id:row.token_id,token_name:row.token_name,rows:[],amount:0,anomalyAmount:0,requests:0,anomalies:0,promptTokens:0,completionTokens:0,cacheReadTokens:0,cacheWriteTokens:0}; grouped.set(row.token_id,token); }
    if (!token.token_name && row.token_name) token.token_name=row.token_name;
    token.rows.push(row); token.amount+=Number(row.amount||0); token.anomalyAmount+=Number(row.anomaly_amount||0); token.requests+=row.request_count; token.anomalies+=row.anomaly_rows;
    token.promptTokens+=Number(row.prompt_tokens||0); token.completionTokens+=Number(row.completion_tokens||0); token.cacheReadTokens+=Number(row.cache_read_tokens||0); token.cacheWriteTokens+=Number(row.cache_write_tokens||0);
  }
  return [...grouped.values()].sort((a,b)=>b.amount-a.amount||a.token_id-b.token_id);
});

const state = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return undefined;
  return dashboard.billingUserDays({ instance_id: filters.site_id, from:dateRange.value[0], through:dateRange.value[1], search: search.value.trim() || undefined });
});
const users = computed<UserBills[]>(() => {
  const grouped = new Map<number, UserBills>();
  for (const day of state.data.value?.items || []) {
    let user = grouped.get(day.user_id);
    if (!user) { user = { user_id:day.user_id, username:day.username, days:[], amount:0, anomalyAmount:0, requests:0, anomalies:0, promptTokens:0, completionTokens:0, cacheReadTokens:0, cacheWriteTokens:0 }; grouped.set(day.user_id, user); }
    if (!user.username && day.username) user.username = day.username;
    user.days.push(day); user.amount += Number(day.amount || 0); user.anomalyAmount += Number(day.anomaly_amount || 0); user.requests += day.request_count; user.anomalies += day.anomaly_rows;
    user.promptTokens += Number(day.prompt_tokens || 0); user.completionTokens += Number(day.completion_tokens || 0); user.cacheReadTokens += Number(day.cache_read_tokens || 0); user.cacheWriteTokens += Number(day.cache_write_tokens || 0);
  }
  return [...grouped.values()].sort((a,b) => (b.days[0]?.day || "").localeCompare(a.days[0]?.day || "") || a.user_id-b.user_id);
});
const totals = computed(() => users.value.reduce((sum,user)=>({days:sum.days+new Set(user.days.map(day=>day.day.slice(0,10))).size,requests:sum.requests+user.requests,anomalies:sum.anomalies+user.anomalies,amount:sum.amount+user.amount,anomalyAmount:sum.anomalyAmount+user.anomalyAmount,promptTokens:sum.promptTokens+user.promptTokens,completionTokens:sum.completionTokens+user.completionTokens,cacheReadTokens:sum.cacheReadTokens+user.cacheReadTokens,cacheWriteTokens:sum.cacheWriteTokens+user.cacheWriteTokens}),{days:0,requests:0,anomalies:0,amount:0,anomalyAmount:0,promptTokens:0,completionTokens:0,cacheReadTokens:0,cacheWriteTokens:0}));
const money = (value:string|number) => `$${Number(value || 0).toFixed(6)}`;
const tokenNumber = (value:unknown) => formatNumber(Number(value) || 0);
function createMissingBills() { void router.push({ path:"/billing/tasks", query:{ create:"1", site:filters.site_id, from:dateRange.value[0], through:dateRange.value[1] } }); }
async function downloadUserRange() {
  if (!selected.value) return;
  const params=new URLSearchParams({instance_id:filters.site_id,user_id:String(selected.value.user_id),from:dateRange.value[0],through:dateRange.value[1]});
  if (billView.value === "token") params.set("group_by","token");
  const suffix=billView.value === "token" ? "token-billing" : "billing";
  try{await downloadBillingFile(`/api/dashboard/billing/range-workbook?${params}`,"下载用户区间账单失败",`user-${selected.value.user_id}-${suffix}-${dateRange.value[0]}-${dateRange.value[1]}.xlsx`);}
  catch(error){ElMessage.error(error instanceof Error?error.message:"下载失败");}
}
async function downloadDailyDetail(row:BillingUserBillDay) {
  const params=new URLSearchParams({instance_id:row.instance_id,user_id:String(row.user_id),day:dayText(row.day),model_name:row.model_name});
  try { await downloadBillingFile(`/api/dashboard/billing/files?${params}`,"下载日账单明细失败",`billing-user-${row.user_id}-${dayText(row.day)}-${row.model_name}.xlsx`); }
  catch(error) { ElMessage.error(error instanceof Error?error.message:"下载失败"); }
}
const dayText = (value:string) => value.slice(0,10);
const dailyBillKey = (row:BillingUserBillDay) => `${row.job_id}:${dayText(row.day)}:${row.model_name}`;
const tokenBillKey = (row:BillingUserTokenBillDay) => `${row.job_id}:${row.token_id}:${dayText(row.day)}:${row.model_name}`;
const tokenDayCount = (token:TokenBills) => new Set(token.rows.map(row=>dayText(row.day))).size;
const userDayCount = (user:UserBills) => new Set(user.days.map(day=>dayText(day.day))).size;
async function openUser(user:UserBills) { selected.value=user; billView.value="daily"; userOpen.value=true; await tokenState.reload(); }
let searchTimer:ReturnType<typeof setTimeout>|undefined;
watch(search,()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>void state.reload(),350)});
watch(dateRange,()=>{selected.value=undefined;userOpen.value=false;void state.reload()},{deep:true});
watch(()=>filters.site_id,()=>{selected.value=undefined;userOpen.value=false;void state.reload()});
let routedOpened=false;
watch(users,(rows)=>{if(routedOpened||!routedUserID)return;const user=rows.find(item=>item.user_id===routedUserID);if(user){routedOpened=true;openUser(user);}},{flush:"post"});
void state.reload();
</script>

<template>
  <AppShell title="用户账单">
    <template #tools><el-date-picker v-model="dateRange" type="daterange" value-format="YYYY-MM-DD" format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :disabled-date="(date:Date)=>date>=new Date(new Date().setHours(0,0,0,0))" unlink-panels style="width:270px"/><el-input v-model="search" clearable placeholder="搜索用户名或用户 ID" style="width:240px"/><el-button @click="state.reload">刷新</el-button></template>
    <div class="summary"><div><span>有账单用户</span><b>{{users.length}}</b></div><div><span>用户日账单</span><b>{{totals.days}}</b></div><div><span>计费请求</span><b>{{formatNumber(totals.requests)}}</b></div><div class="token-summary"><span>Token 总量</span><section><small>输入</small><b>{{formatNumber(totals.promptTokens)}}</b><small>缓存读取</small><b>{{formatNumber(totals.cacheReadTokens)}}</b><small>缓存写入</small><b>{{formatNumber(totals.cacheWriteTokens)}}</b><small>输出</small><b>{{formatNumber(totals.completionTokens)}}</b></section></div><div><span>异常金额</span><b class="warning-money">{{money(totals.anomalyAmount)}}</b><small>{{formatNumber(totals.anomalies)}} 条异常</small></div><div><span>正常账单金额</span><b>{{money(totals.amount)}}</b></div></div>
    <div v-if="state.data.value?.coverage && state.data.value.coverage.status !== 'complete'" class="coverage-prompt"><el-alert type="warning" :closable="false" show-icon :title="`所选区间账单不完整：${state.data.value.coverage.available_days}/${state.data.value.coverage.expected_days} 天可用，当前金额仅供参考`" :description="state.data.value.coverage.missing_days.length <= 10 ? `缺失日期：${state.data.value.coverage.missing_days.join('、')}` : `缺失 ${state.data.value.coverage.missing_days.length} 天，请创建账单任务生成对应区间`" /><el-button type="warning" plain @click="createMissingBills">去生成账单</el-button></div>
    <el-alert v-else-if="state.data.value?.coverage" type="success" :closable="false" show-icon :title="`所选区间 ${state.data.value.coverage.expected_days}/${state.data.value.coverage.expected_days} 天账单完整`" />
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!users.length" empty-text="暂无用户账单" @retry="state.reload">
      <el-table :data="users" row-key="user_id" @row-click="openUser">
        <el-table-column label="用户" min-width="190"><template #default="s"><b>{{s.row.username||`用户 ${s.row.user_id}`}}</b><small>ID {{s.row.user_id}}</small></template></el-table-column>
        <el-table-column label="最近账单" min-width="220"><template #default="s"><div class="latest-bill"><b>{{dayText(s.row.days[0].day)}}</b><el-tag v-if="userDayCount(s.row)>1" type="info" size="small">共 {{userDayCount(s.row)}} 个账单日期</el-tag></div></template></el-table-column>
        <el-table-column label="请求数" width="120" align="right"><template #default="s">{{formatNumber(s.row.requests)}}</template></el-table-column>
        <el-table-column label="Token 用量" min-width="310"><template #default="s"><div class="token-metrics"><span>输入 <b>{{formatNumber(s.row.promptTokens)}}</b></span><span>缓存读取 <b>{{formatNumber(s.row.cacheReadTokens)}}</b></span><span>缓存写入 <b>{{formatNumber(s.row.cacheWriteTokens)}}</b></span><span>输出 <b>{{formatNumber(s.row.completionTokens)}}</b></span></div></template></el-table-column>
        <el-table-column label="异常" width="145" align="right"><template #default="s"><div v-if="s.row.anomalies" class="anomaly-cell"><b>{{formatNumber(s.row.anomalies)}} 条</b><small>金额 {{money(s.row.anomalyAmount)}}</small></div><span v-else class="muted">无异常</span></template></el-table-column>
        <el-table-column label="账单金额" width="150" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column>
        <el-table-column label="操作" width="130"><template #default="s"><el-button link type="primary" @click.stop="openUser(s.row)">查看区间账单</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-drawer v-model="userOpen" size="86%">
      <template #header><div class="drawer-header"><div><b>{{selected?.username||`用户 ${selected?.user_id||''}`}} · 区间账单</b><small>{{dateRange[0]}} 至 {{dateRange[1]}}</small></div><div class="drawer-actions"><el-radio-group v-model="billView"><el-radio-button value="daily">不按令牌</el-radio-button><el-radio-button value="token">按令牌统计</el-radio-button></el-radio-group><el-button type="primary" @click="downloadUserRange">下载当前账单</el-button></div></div></template>
      <el-table v-if="billView==='daily'" :data="selected?.days||[]" :row-key="dailyBillKey">
        <el-table-column label="账单日期" width="120"><template #default="s"><b>{{dayText(s.row.day)}}</b></template></el-table-column><el-table-column label="模型" min-width="190"><template #default="s"><b>{{s.row.model_name||'未知模型'}}</b></template></el-table-column><el-table-column label="请求数" width="110" align="right"><template #default="s">{{formatNumber(s.row.request_count)}}</template></el-table-column><el-table-column label="Token 用量" min-width="320"><template #default="s"><div class="token-metrics"><span>输入 <b>{{tokenNumber(s.row.prompt_tokens)}}</b></span><span>缓存读取 <b>{{tokenNumber(s.row.cache_read_tokens)}}</b></span><span>缓存写入 <b>{{tokenNumber(s.row.cache_write_tokens)}}</b></span><span>输出 <b>{{tokenNumber(s.row.completion_tokens)}}</b></span></div></template></el-table-column><el-table-column label="待确认" width="145" align="right"><template #default="s"><div v-if="s.row.anomaly_rows" class="anomaly-cell"><b>{{formatNumber(s.row.anomaly_rows)}} 条</b><small>金额 {{money(s.row.anomaly_amount)}}</small></div><span v-else class="muted">无</span></template></el-table-column><el-table-column label="正常金额" width="145" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column><el-table-column label="生成时间" min-width="175"><template #default="s">{{new Date(s.row.activated_at).toLocaleString()}}</template></el-table-column><el-table-column label="操作" width="100"><template #default="s"><el-button link type="primary" @click="downloadDailyDetail(s.row)">下载明细</el-button></template></el-table-column>
      </el-table>
      <AsyncPanel v-else :loading="tokenState.loading.value" :error="tokenState.error.value" :empty="!tokenBills.length" empty-text="所选区间暂无令牌账单" @retry="tokenState.reload">
        <el-table :data="tokenBills" row-key="token_id">
          <el-table-column type="expand" width="48"><template #default="scope"><div class="token-detail"><div class="detail-title">{{scope.row.token_name||'未命名令牌'}} · 日期 / 模型明细</div><el-table :data="scope.row.rows" :row-key="tokenBillKey" size="small"><el-table-column label="账单日期" width="120"><template #default="s"><b>{{dayText(s.row.day)}}</b></template></el-table-column><el-table-column label="模型" min-width="180"><template #default="s"><b>{{s.row.model_name||'未知模型'}}</b></template></el-table-column><el-table-column label="请求数" width="100" align="right"><template #default="s">{{formatNumber(s.row.request_count)}}</template></el-table-column><el-table-column label="Token 用量" min-width="320"><template #default="s"><div class="token-metrics"><span>输入 <b>{{tokenNumber(s.row.prompt_tokens)}}</b></span><span>缓存读取 <b>{{tokenNumber(s.row.cache_read_tokens)}}</b></span><span>缓存写入 <b>{{tokenNumber(s.row.cache_write_tokens)}}</b></span><span>输出 <b>{{tokenNumber(s.row.completion_tokens)}}</b></span></div></template></el-table-column><el-table-column label="待确认" width="145" align="right"><template #default="s"><div v-if="s.row.anomaly_rows" class="anomaly-cell"><b>{{formatNumber(s.row.anomaly_rows)}} 条</b><small>金额 {{money(s.row.anomaly_amount)}}</small></div><span v-else class="muted">无</span></template></el-table-column><el-table-column label="正常金额" width="145" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column><el-table-column label="生成时间" min-width="175"><template #default="s">{{new Date(s.row.activated_at).toLocaleString()}}</template></el-table-column></el-table></div></template></el-table-column>
          <el-table-column label="令牌" min-width="220"><template #default="s"><b>{{s.row.token_name||'未命名令牌'}}</b><small>ID {{s.row.token_id}}</small></template></el-table-column>
          <el-table-column label="账单天数" width="110" align="right"><template #default="s">{{tokenDayCount(s.row)}}</template></el-table-column>
          <el-table-column label="请求数" width="120" align="right"><template #default="s">{{formatNumber(s.row.requests)}}</template></el-table-column>
          <el-table-column label="Token 用量" min-width="320"><template #default="s"><div class="token-metrics"><span>输入 <b>{{tokenNumber(s.row.promptTokens)}}</b></span><span>缓存读取 <b>{{tokenNumber(s.row.cacheReadTokens)}}</b></span><span>缓存写入 <b>{{tokenNumber(s.row.cacheWriteTokens)}}</b></span><span>输出 <b>{{tokenNumber(s.row.completionTokens)}}</b></span></div></template></el-table-column>
          <el-table-column label="待确认" width="145" align="right"><template #default="s"><div v-if="s.row.anomalies" class="anomaly-cell"><b>{{formatNumber(s.row.anomalies)}} 条</b><small>金额 {{money(s.row.anomalyAmount)}}</small></div><span v-else class="muted">无</span></template></el-table-column>
          <el-table-column label="正常金额" width="145" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column>
        </el-table>
      </AsyncPanel>
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.summary{display:grid;grid-template-columns:repeat(7,minmax(130px,1fr));gap:12px;margin-bottom:16px}.summary>div{padding:16px;border:1px solid var(--el-border-color-light);border-radius:10px;background:var(--el-bg-color)}.summary span,.summary b{display:block}.summary span,small{color:var(--el-text-color-secondary);font-size:12px}.summary b{margin-top:8px;font-size:22px}.coverage-prompt{display:flex;align-items:center;gap:12px;margin-bottom:12px}.coverage-prompt .el-alert{flex:1}.coverage-prompt .el-button{flex:none}.token-summary{grid-column:span 2}.token-summary section{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-top:8px}.token-summary section b{margin-top:3px;font-size:14px}.warning-money{color:var(--el-color-warning)}.latest-bill{display:flex;align-items:center;gap:10px;white-space:nowrap}:deep(.el-table__row){cursor:pointer}.el-drawer :deep(.el-table__row){cursor:default}.anomaly-cell{display:flex;flex-direction:column;align-items:flex-end;line-height:1.35}.anomaly-cell b{color:var(--el-color-warning);font-variant-numeric:tabular-nums}.anomaly-cell small{margin-top:3px;white-space:nowrap}.muted{color:var(--el-text-color-secondary)}.token-metrics{display:grid;grid-template-columns:repeat(2,minmax(120px,1fr));gap:4px 14px}.token-metrics span{color:var(--el-text-color-secondary);font-size:12px}.token-metrics b{display:inline;margin-left:4px;color:var(--el-text-color-primary);font-size:12px;font-variant-numeric:tabular-nums}.metrics{display:grid;grid-template-columns:auto 1fr auto 1fr;gap:4px 10px}.metrics span{color:var(--el-text-color-secondary);font-size:12px}.metrics b{text-align:right;font-variant-numeric:tabular-nums}@media(max-width:1200px){.summary{grid-template-columns:repeat(3,1fr)}}@media(max-width:900px){.summary{grid-template-columns:repeat(2,1fr)}}
.token-detail{padding:12px 18px 18px;background:var(--el-fill-color-lighter)}.detail-title{margin-bottom:10px;font-weight:600;color:var(--el-text-color-primary)}
.drawer-header{display:flex;align-items:center;justify-content:space-between;width:100%;gap:24px}.drawer-header>div:first-child{display:flex;flex-direction:column;gap:5px}.drawer-header b{font-size:16px;color:var(--el-text-color-primary)}.drawer-actions{display:flex;align-items:center;gap:12px;margin-right:16px}
</style>
