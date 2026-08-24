<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import type { BillingUserBillDay } from "@ct/shared";
import { ElMessage } from "element-plus";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { downloadBillingFile } from "../utils/httpError";
import { formatNumber } from "../utils/format";

type UserBills = { user_id:number;username:string;days:BillingUserBillDay[];amount:number;requests:number;anomalies:number };
const route = useRoute();
const filters = useFiltersStore();
const now = new Date();
const routedSite = typeof route.query.site === "string" ? route.query.site : "";
if (routedSite) filters.selectSite(routedSite);
const routedUserID = typeof route.query.user_id === "string" ? Number(route.query.user_id) : 0;
const search = ref("");
const month = ref(typeof route.query.from === "string" ? route.query.from.slice(0,7) : `${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,"0")}`);
const selected = ref<UserBills>();
const selectedDay = ref<BillingUserBillDay>();
const userOpen = ref(false);
const detailOpen = ref(false);

const state = useAsyncData(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return undefined;
  return dashboard.billingUserDays({ instance_id: filters.site_id, month:month.value, search: search.value.trim() || undefined });
});
const users = computed<UserBills[]>(() => {
  const grouped = new Map<number, UserBills>();
  for (const day of state.data.value?.items || []) {
    let user = grouped.get(day.user_id);
    if (!user) { user = { user_id:day.user_id, username:day.username, days:[], amount:0, requests:0, anomalies:0 }; grouped.set(day.user_id, user); }
    if (!user.username && day.username) user.username = day.username;
    user.days.push(day); user.amount += Number(day.amount || 0); user.requests += day.request_count; user.anomalies += day.anomaly_rows;
  }
  return [...grouped.values()].sort((a,b) => (b.days[0]?.day || "").localeCompare(a.days[0]?.day || "") || a.user_id-b.user_id);
});
const totals = computed(() => users.value.reduce((sum,user)=>({days:sum.days+user.days.length,requests:sum.requests+user.requests,anomalies:sum.anomalies+user.anomalies,amount:sum.amount+user.amount}),{days:0,requests:0,anomalies:0,amount:0}));
const detail = useAsyncData(async () => {
  const day = selectedDay.value;
  if (!day) return undefined;
  const from = `${day.day.slice(0,10)} 00:00:00`;
  const date = new Date(`${day.day.slice(0,10)}T00:00:00`); date.setDate(date.getDate()+1);
  const to = `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,"0")}-${String(date.getDate()).padStart(2,"0")} 00:00:00`;
  return dashboard.billingDetail({ instance_id:day.instance_id,user_id:day.user_id,from,to,job_id:day.job_id });
});
const money = (value:string|number) => `$${Number(value || 0).toFixed(6)}`;
const dayText = (value:string) => value.slice(0,10);
const billKey = (day:BillingUserBillDay) => `${day.job_id}:${dayText(day.day)}:${day.user_id}`;
const unitPrice = (value?:string) => value ? `$${Number(value).toFixed(6)}` : "—";
function openUser(user:UserBills) { selected.value=user; userOpen.value=true; }
async function openDay(day:BillingUserBillDay) { selectedDay.value=day; detailOpen.value=true; await detail.reload(); }
async function downloadDay(day:BillingUserBillDay) {
  const params=new URLSearchParams({instance_id:day.instance_id,user_id:String(day.user_id),day:dayText(day.day)});
  try { await downloadBillingFile(`/api/dashboard/billing/files?${params}`,"下载用户日账单失败",`billing-user-${day.user_id}-${dayText(day.day)}.xlsx`); }
  catch(error){ ElMessage.error(error instanceof Error?error.message:"下载失败"); }
}
let searchTimer:ReturnType<typeof setTimeout>|undefined;
watch(search,()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>void state.reload(),350)});
watch(month,()=>{selected.value=undefined;userOpen.value=false;void state.reload()});
watch(()=>filters.site_id,()=>{selected.value=undefined;userOpen.value=false;void state.reload()});
let routedOpened=false;
watch(users,(rows)=>{if(routedOpened||!routedUserID)return;const user=rows.find(item=>item.user_id===routedUserID);if(user){routedOpened=true;openUser(user);if(user.days.length===1)void openDay(user.days[0]);}},{flush:"post"});
void state.reload();
</script>

<template>
  <AppShell title="用户账单">
    <template #tools><el-date-picker v-model="month" type="month" value-format="YYYY-MM" placeholder="选择月份" style="width:150px"/><el-input v-model="search" clearable placeholder="搜索用户名或用户 ID" style="width:240px"/><router-link to="/billing/tasks"><el-button>账单任务</el-button></router-link><el-button @click="state.reload">刷新</el-button></template>
    <div class="summary"><div><span>有账单用户</span><b>{{users.length}}</b></div><div><span>用户日账单</span><b>{{totals.days}}</b></div><div><span>计费请求</span><b>{{formatNumber(totals.requests)}}</b></div><div><span>计费异常</span><b>{{formatNumber(totals.anomalies)}}</b></div><div><span>账单金额</span><b>{{money(totals.amount)}}</b></div></div>
    <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!users.length" empty-text="暂无用户账单" @retry="state.reload">
      <el-table :data="users" row-key="user_id" @row-click="openUser">
        <el-table-column label="用户" min-width="190"><template #default="s"><b>{{s.row.username||`用户 ${s.row.user_id}`}}</b><small>ID {{s.row.user_id}}</small></template></el-table-column>
        <el-table-column label="最近账单" min-width="220"><template #default="s"><div class="latest-bill"><b>{{dayText(s.row.days[0].day)}}</b><el-tag v-if="s.row.days.length>1" type="info" size="small">共 {{s.row.days.length}} 个账单日期</el-tag></div></template></el-table-column>
        <el-table-column label="请求数" width="120" align="right"><template #default="s">{{formatNumber(s.row.requests)}}</template></el-table-column>
        <el-table-column label="异常" width="90" align="right"><template #default="s"><el-tag v-if="s.row.anomalies" type="warning">{{s.row.anomalies}}</el-tag><span v-else>0</span></template></el-table-column>
        <el-table-column label="账单金额" width="150" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column>
        <el-table-column label="操作" width="120"><template #default="s"><el-button link type="primary" @click.stop="openUser(s.row)">查看账单日期</el-button></template></el-table-column>
      </el-table>
    </AsyncPanel>
    <el-drawer v-model="userOpen" :title="`${selected?.username||`用户 ${selected?.user_id||''}`} · 可用账单日期`" size="72%">
      <el-table :data="selected?.days||[]" :row-key="billKey" @row-click="openDay">
        <el-table-column label="账单日期" min-width="140"><template #default="s"><b>{{dayText(s.row.day)}}</b></template></el-table-column><el-table-column prop="request_count" label="请求数" width="120" align="right"/><el-table-column label="异常" width="100" align="right"><template #default="s">{{s.row.anomaly_rows}}</template></el-table-column><el-table-column label="金额" width="150" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column><el-table-column label="生成时间" min-width="180"><template #default="s">{{new Date(s.row.activated_at).toLocaleString()}}</template></el-table-column><el-table-column label="操作" width="190"><template #default="s"><el-button link type="primary" @click.stop="openDay(s.row)">查看明细</el-button><el-button link type="primary" @click.stop="downloadDay(s.row)">下载 Excel</el-button></template></el-table-column>
      </el-table>
    </el-drawer>
    <el-drawer v-model="detailOpen" :title="`${selectedDay?.username||`用户 ${selectedDay?.user_id||''}`} · ${selectedDay?dayText(selectedDay.day):''}`" size="82%">
      <AsyncPanel :loading="detail.loading.value" :error="detail.error.value" :empty="!detail.data.value?.items.length" empty-text="该日没有计费明细" @retry="detail.reload">
        <el-table :data="detail.data.value?.items||[]"><el-table-column prop="model_name" label="模型" min-width="180"/><el-table-column label="Token 用量" min-width="250"><template #default="s"><div class="metrics"><span>输入</span><b>{{formatNumber(s.row.prompt_tokens)}}</b><span>缓存读取</span><b>{{formatNumber(s.row.cache_tokens)}}</b><span>缓存写入</span><b>{{formatNumber(s.row.cache_write_tokens)}}</b><span>输出</span><b>{{formatNumber(s.row.completion_tokens)}}</b></div></template></el-table-column><el-table-column label="模型单价 / 1M" min-width="250"><template #default="s"><div class="metrics"><span>输入</span><b>{{unitPrice(s.row.input_price)}}</b><span>缓存读取</span><b>{{unitPrice(s.row.cache_price)}}</b><span>缓存写入</span><b>{{unitPrice(s.row.cache_write_price)}}</b><span>输出</span><b>{{unitPrice(s.row.output_price)}}</b></div></template></el-table-column><el-table-column prop="request_count" label="请求数" width="100" align="right"/><el-table-column label="金额" width="140" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column></el-table>
      </AsyncPanel>
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.summary{display:grid;grid-template-columns:repeat(5,minmax(140px,1fr));gap:12px;margin-bottom:16px}.summary>div{padding:16px;border:1px solid var(--el-border-color-light);border-radius:10px;background:var(--el-bg-color)}.summary span,.summary b{display:block}.summary span,small{color:var(--el-text-color-secondary);font-size:12px}.summary b{margin-top:8px;font-size:22px}.latest-bill{display:flex;align-items:center;gap:10px;white-space:nowrap}:deep(.el-table__row){cursor:pointer}.metrics{display:grid;grid-template-columns:auto 1fr auto 1fr;gap:4px 10px}.metrics span{color:var(--el-text-color-secondary);font-size:12px}.metrics b{text-align:right;font-variant-numeric:tabular-nums}@media(max-width:900px){.summary{grid-template-columns:repeat(2,1fr)}}
</style>
