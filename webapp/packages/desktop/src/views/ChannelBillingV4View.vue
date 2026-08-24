<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import { type BillingChannelSummary, type BillingCoverage, type BillingJob, type BillingUpstreamGroup } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { formatNumber } from "../utils/format";
import { billingReadErrorMessage, downloadBillingFile, httpError } from "../utils/httpError";

type PageData = { items: BillingChannelSummary[]; job: BillingJob | null; channelError: string; currencySymbol: string; currencyRate: number; coverage?:BillingCoverage };
const filters = useFiltersStore(), prefs = usePrefsStore(), route = useRoute();
const router = useRouter();
const page = ref(1), pageSize = ref(20);
const exporting = ref<Record<number, boolean>>({});
const detailOpen = ref(false), selected = ref<BillingChannelSummary>();
const viewMode = ref<"channel"|"upstream">("channel"), upstreamSearch = ref("");
const upstreamOpen = ref(false), selectedUpstream = ref<BillingUpstreamGroup>();
const routedSite = typeof route.query.site === "string" ? route.query.site : "";
if (routedSite) filters.selectSite(routedSite);
const two=(value:number)=>String(value).padStart(2,"0");
const dateValue=(value:Date)=>`${value.getFullYear()}-${two(value.getMonth()+1)}-${two(value.getDate())}`;
const yesterday=new Date();yesterday.setHours(0,0,0,0);yesterday.setDate(yesterday.getDate()-1);
const routedRange: [string, string] | undefined = typeof route.query.from === "string" && typeof route.query.through === "string" ? [route.query.from.slice(0,10), route.query.through.slice(0,10)] : undefined;
const generationRange = ref<[string, string]>(routedRange || [dateValue(new Date(yesterday.getFullYear(),yesterday.getMonth(),1)),dateValue(yesterday)]);
function rangeTimes(){const[from,through]=generationRange.value;const end=new Date(`${through}T00:00:00`);end.setDate(end.getDate()+1);return{from:`${from} 00:00:00`,to:`${dateValue(end)} 00:00:00`};}
void prefs.load();
const state = useAsyncData<PageData>(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return { items: [], job: null, channelError: "", currencySymbol: "", currencyRate: 1, coverage:undefined };
  const [from,through]=generationRange.value;
  const bill = await dashboard.billingChannels({ instance_id: filters.site_id, from, through });
  return {
    items: (bill.items || []).sort((a, b) => Number(b.amount) - Number(a.amount) || a.channel_id - b.channel_id),
    job: null,
    channelError: bill.warning || "",
    currencySymbol: bill.currency?.type ? bill.currency.symbol : (prefs.currencySymbol || "$"),
    currencyRate: (() => { const rate = Number(bill.currency?.exchange_rate); return Number.isFinite(rate) && rate > 0 ? rate : 1; })(),
    coverage: bill.coverage,
  };
});
const upstreamState = useAsyncData(async()=>{if(viewMode.value!=="upstream"||!filters.site_id)return undefined;const[from,to]=generationRange.value;return dashboard.billingUpstreamChannels({instance_id:filters.site_id,from,to,job_id:job.value?.id})});

const items = computed(() => state.data.value?.items || []), job = computed(() => state.data.value?.job || null);
const channelSearch = ref("");
const showZeroChannels = ref(false);
const filteredItems = computed(() => {
  const q = channelSearch.value.trim().toLowerCase();
  return items.value.filter(row => (showZeroChannels.value || row.request_count > 0 || row.abnormal_rows > 0) && (!q || String(row.channel_id).includes(q) || (row.channel_name || "").toLowerCase().includes(q)));
});
const upstreamItems = computed(()=>{const q=upstreamSearch.value.trim().toLowerCase();return (upstreamState.data.value?.items||[]).filter(g=>!q||g.base_url.toLowerCase().includes(q)||g.display_name.toLowerCase().includes(q)||g.members.some(m=>m.channel_name.toLowerCase().includes(q)||m.model_name.toLowerCase().includes(q))).map(g=>({...g,row_id:`g:${g.upstream_fp||'unmapped'}`,children:g.members.map(m=>({...m,row_id:`c:${m.channel_id}`,display_name:`${m.model_name||'未知模型'} · ${m.channel_name||`渠道 ${m.channel_id}`} · #${m.channel_id}`,request_count:m.totals.request_count,prompt_tokens:m.totals.prompt_tokens,completion_tokens:m.totals.completion_tokens,cache_tokens:m.totals.cache_tokens,cache_write_tokens:m.totals.cache_write_tokens,quota:m.totals.quota,amount:m.totals.amount})) ,request_count:g.totals.request_count,prompt_tokens:g.totals.prompt_tokens,completion_tokens:g.totals.completion_tokens,cache_tokens:g.totals.cache_tokens,cache_write_tokens:g.totals.cache_write_tokens,quota:g.totals.quota,amount:g.totals.amount}))});
const upstreamExpanded=computed(()=>upstreamItems.value.filter(g=>g.upstream_fp).map(g=>g.row_id));
const detail = useAsyncData(async () => {
  if (!selected.value || !filters.site_id) return undefined;
  const [from, through] = generationRange.value;
  return dashboard.billingChannels({ instance_id: filters.site_id, channel_id: selected.value.channel_id, from, through });
});
const upstreamDetail=useAsyncData(async()=>{if(!selectedUpstream.value)return undefined;const[from,to]=generationRange.value;return dashboard.billingUpstreamDetail({instance_id:filters.site_id,fp:selectedUpstream.value.upstream_fp,from,to,job_id:job.value?.id})});
const actualPeriod = computed(() => `${generationRange.value[0]} 至 ${generationRange.value[1]}`);
function createMissingBills(){void router.push({path:"/billing/tasks",query:{create:"1",site:filters.site_id,from:generationRange.value[0],through:generationRange.value[1]}})}
const pagedItems = computed(() => filteredItems.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
const currency = computed(() => state.data.value ? state.data.value.currencySymbol : (prefs.currencySymbol || "$"));
// USD-based amounts must be converted by the site exchange rate, not just relabeled.
const currencyRate = computed(() => state.data.value?.currencyRate || 1);
const money = (value: string) => `${currency.value}${(Number(value || 0) * currencyRate.value).toFixed(4)}`;
const unitPrice = (value: string | undefined) => value ? `${currency.value}${(Number(value) * currencyRate.value).toFixed(4)}` : "—";
const discountedMoney = (value: string | undefined) => money(String(Number(value || 0) * Number(selected.value?.discount || 1)));
async function save(row: BillingChannelSummary, value: string) {
  const discount = Number(value);
  if (!Number.isFinite(discount) || discount < 0 || discount > 1) { ElMessage.warning("折扣必须在 0 到 1 之间"); return; }
  await dashboard.saveBillingChannelDiscount({ instance_id: filters.site_id, channel_id: row.channel_id, discount: String(discount) });
  row.discount = String(discount); row.discounted_amount = (Number(row.amount) * discount).toFixed(6); ElMessage.success("渠道折扣已保存");
}
function openDetail(row: BillingChannelSummary) { selected.value = row; detailOpen.value = true; void detail.reload(); }
function openUpstream(row: BillingUpstreamGroup){selectedUpstream.value=row;upstreamOpen.value=true;void upstreamDetail.reload()}
function upstreamURL(row:BillingUpstreamGroup){const{from,to}=rangeTimes();const p=new URLSearchParams({instance_id:filters.site_id,fp:row.upstream_fp,from,to,format:"csv"});return`/api/dashboard/billing/upstream-channels/detail?${p}`}
async function exportUpstream(row:BillingUpstreamGroup){try{await downloadBillingFile(upstreamURL(row),"导出上游渠道 CSV 失败")}catch(error){ElMessage.error(billingReadErrorMessage(error,"导出上游渠道 CSV 失败"))}}
function channelReadURL(path: string, row: BillingChannelSummary, extra = "") {
  const {from, to} = rangeTimes();
  const params = new URLSearchParams({ instance_id: filters.site_id, channel_id: String(row.channel_id), from, to });
  if (job.value?.id) params.set("job_id", job.value.id);
  return `${path}?${params.toString()}${extra}`;
}
async function exportDaily(row: BillingChannelSummary) {
  try { await downloadBillingFile(channelReadURL("/api/dashboard/billing/channels", row, "&format=csv"), "导出日账单失败"); }
  catch (error) { ElMessage.error(billingReadErrorMessage(error, "导出日账单失败")); }
}
async function exportAnomalies(row: BillingChannelSummary) {
  try { await downloadBillingFile(channelReadURL("/api/dashboard/billing/anomalies", row, "&format=csv"), "导出异常订单失败"); }
  catch (error) { ElMessage.error(billingReadErrorMessage(error, "导出异常订单失败")); }
}
async function exportChannel(row: BillingChannelSummary) {
  exporting.value = { ...exporting.value, [row.channel_id]: true };
  try {
    const [from, to] = generationRange.value;
    const createResponse = await fetch("/api/dashboard/billing/channel-workbook-jobs", {
      method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest" },
      body: JSON.stringify({ instance_id: filters.site_id, channel_id: String(row.channel_id), from, to, job_id: job.value?.id || "" }),
    });
    if (!createResponse.ok) throw await httpError(createResponse, "创建导出任务失败");
    let task = await createResponse.json();
    while (task.status === "pending" || task.status === "running") {
      await new Promise((resolve) => setTimeout(resolve, 1500));
      const statusResponse = await fetch(`/api/dashboard/billing/channel-workbook-jobs?id=${task.id}`, { credentials: "same-origin" });
      if (!statusResponse.ok) throw await httpError(statusResponse, "查询导出任务失败");
      task = await statusResponse.json();
    }
    if (task.status !== "complete") throw new Error(task.error || "导出任务失败");
    await downloadBillingFile(`/api/dashboard/billing/channel-workbook-jobs?id=${task.id}&download=1`, "下载渠道账单失败", `channel-billing-${row.channel_id}.xlsx`);
  } catch (error) {
    ElMessage.error(billingReadErrorMessage(error, "导出失败"));
  } finally {
    exporting.value = { ...exporting.value, [row.channel_id]: false };
  }
}
watch(generationRange,(value)=>{localStorage.setItem("ct.billing.channel.range",JSON.stringify(value))},{deep:true});
watch([generationRange, () => filters.site_id], () => { page.value = 1; void state.reload(); if(viewMode.value==="upstream")void upstreamState.reload(); if (detailOpen.value) void detail.reload(); });
watch(viewMode,mode=>{if(mode==="upstream")void upstreamState.reload()});
watch(channelSearch, () => { page.value = 1; });
watch(pageSize, () => { page.value = 1; });
void state.reload();
</script>

<template>
  <AppShell title="渠道账单">
    <template #tools>
      <span class="period-label">账单日期</span><el-date-picker v-model="generationRange" type="daterange" value-format="YYYY-MM-DD" format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :disabled-date="(date:Date)=>date>=new Date(new Date().setHours(0,0,0,0))" unlink-panels style="width:270px" />
      <el-button @click="state.reload">刷新</el-button>
    </template>
    <div class="page">
      <div class="period-summary"><span>账单日期</span><b>{{ actualPeriod }}</b><span class="actual-period">账单由任务生成，页面仅查询已生效账单</span></div>
      <div v-if="state.data.value?.coverage && state.data.value.coverage.status !== 'complete'" class="coverage-prompt"><el-alert type="warning" :closable="false" show-icon :title="`所选区间账单不完整：${state.data.value.coverage.available_days}/${state.data.value.coverage.expected_days} 天可用，当前金额仅供参考`" :description="state.data.value.coverage.missing_days.length <= 10 ? `缺失日期：${state.data.value.coverage.missing_days.join('、')}` : `缺失 ${state.data.value.coverage.missing_days.length} 天，请创建账单任务生成对应区间`" /><el-button type="warning" plain @click="createMissingBills">去生成账单</el-button></div>
      <el-alert v-else-if="state.data.value?.coverage" type="success" :closable="false" show-icon :title="`所选区间 ${state.data.value.coverage.expected_days}/${state.data.value.coverage.expected_days} 天账单完整`" />
      <el-alert v-if="state.data.value?.channelError" type="warning" title="部分渠道配置加载失败" :description="state.data.value.channelError" :closable="false" show-icon />
      <div class="view-switch"><el-radio-group v-model="viewMode" size="small"><el-radio-button value="channel">按渠道</el-radio-button><el-radio-button value="upstream">按上游 key</el-radio-button></el-radio-group><el-input v-if="viewMode==='upstream'" v-model="upstreamSearch" clearable placeholder="搜索渠道名、模型或 base_url" style="width:280px" /><template v-else><el-input v-model="channelSearch" clearable placeholder="搜索渠道名或 ID" style="width:240px" /><el-switch v-model="showZeroChannels" active-text="显示零用量渠道" /></template></div>
      <AsyncPanel v-if="viewMode==='channel'" class="content" :loading="state.loading.value" :error="state.error.value" :empty="!filteredItems.length" :empty-text="!filters.site_id ? '尚未选择站点' : channelSearch.trim() ? '没有匹配的渠道' : '当前站点没有渠道配置'" @retry="state.reload">
        <div class="table-wrap"><el-table :data="pagedItems" height="100%" class="channel-table" @row-click="openDetail">
          <el-table-column prop="channel_name" label="渠道" min-width="180"><template #default="scope"><b>{{ scope.row.channel_name || `渠道 ${scope.row.channel_id}` }}</b><small class="sub">ID {{ scope.row.channel_id }}</small></template></el-table-column>
          <el-table-column prop="request_count" label="计费请求数" min-width="110" align="right"><template #default="scope">{{ formatNumber(scope.row.request_count) }}</template></el-table-column><el-table-column prop="abnormal_rows" label="异常订单数" min-width="105" align="right"><template #default="scope">{{ formatNumber(scope.row.abnormal_rows) }}</template></el-table-column><el-table-column prop="abnormal_amount" label="异常总金额" min-width="120" align="right"><template #default="scope">{{ money(scope.row.abnormal_amount) }}</template></el-table-column><el-table-column prop="prompt_tokens" label="普通输入 Token" min-width="135" align="right"><template #default="scope">{{ formatNumber(scope.row.prompt_tokens) }}</template></el-table-column><el-table-column prop="cache_tokens" label="缓存读取 Token" min-width="135" align="right"><template #default="scope">{{ formatNumber(scope.row.cache_tokens) }}</template></el-table-column><el-table-column prop="cache_write_tokens" label="缓存写入 Token" min-width="135" align="right"><template #default="scope">{{ formatNumber(scope.row.cache_write_tokens) }}</template></el-table-column><el-table-column prop="completion_tokens" label="输出 Token" min-width="125" align="right"><template #default="scope">{{ formatNumber(scope.row.completion_tokens) }}</template></el-table-column>
          <el-table-column label="账单原价" min-width="115" align="right"><template #default="scope">{{ money(scope.row.amount) }}</template></el-table-column>
          <el-table-column label="折扣" width="140"><template #default="scope"><el-input-number :model-value="Number(scope.row.discount)" :min="0" :max="1" :step="0.01" :precision="2" size="small" @click.stop @change="save(scope.row, String($event))" /></template></el-table-column>
          <el-table-column label="折扣总金额" min-width="125" align="right"><template #default="scope"><b>{{ money(scope.row.discounted_amount) }}</b></template></el-table-column>
          <el-table-column label="操作" width="220"><template #default="scope"><el-button link type="primary" @click.stop="openDetail(scope.row)">日账单</el-button><el-button link type="primary" :loading="exporting[scope.row.channel_id]" @click.stop="exportChannel(scope.row)">导出账单</el-button><el-button link type="primary" @click.stop="exportAnomalies(scope.row)">异常订单</el-button></template></el-table-column>
        </el-table></div>
        <ListPager v-model:page="page" v-model:page-size="pageSize" :item-count="pagedItems.length" :total="filteredItems.length" />
      </AsyncPanel>
      <AsyncPanel v-else class="content" :loading="upstreamState.loading.value" :error="upstreamState.error.value" :empty="!upstreamItems.length" empty-text="该区间没有可归组的渠道账单" @retry="upstreamState.reload">
        <div class="table-wrap"><el-table :data="upstreamItems" row-key="row_id" :expand-row-keys="upstreamExpanded" height="100%" class="channel-table">
          <el-table-column prop="display_name" label="上游 key / 成员渠道" min-width="300"><template #default="s"><b>{{s.row.display_name}}</b><small v-if="s.row.member_count!==undefined" class="sub">{{s.row.upstream_fp ? `${s.row.member_count} 个成员渠道` : '快照缺失，未归组'}}</small></template></el-table-column>
          <el-table-column prop="request_count" label="请求数" width="105" align="right"><template #default="s">{{formatNumber(s.row.request_count)}}</template></el-table-column>
          <el-table-column prop="prompt_tokens" label="普通输入 Token" width="140" align="right"><template #default="s">{{formatNumber(s.row.prompt_tokens)}}</template></el-table-column>
          <el-table-column prop="cache_tokens" label="缓存读取 Token" width="140" align="right"><template #default="s">{{formatNumber(s.row.cache_tokens)}}</template></el-table-column>
          <el-table-column prop="cache_write_tokens" label="缓存写入 Token" width="140" align="right"><template #default="s">{{formatNumber(s.row.cache_write_tokens)}}</template></el-table-column>
          <el-table-column prop="completion_tokens" label="输出 Token" width="125" align="right"><template #default="s">{{formatNumber(s.row.completion_tokens)}}</template></el-table-column>
          <el-table-column prop="amount" label="金额" width="120" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column>
          <el-table-column prop="quota" label="Quota（参考）" width="130" align="right"><template #default="s">{{formatNumber(s.row.quota)}}</template></el-table-column>
          <el-table-column label="操作" width="170"><template #default="s"><template v-if="s.row.member_count!==undefined"><el-button link type="primary" @click.stop="openUpstream(s.row)">查看明细</el-button><el-button link type="primary" @click.stop="exportUpstream(s.row)">导出 CSV</el-button></template></template></el-table-column>
        </el-table></div>
      </AsyncPanel>
    </div>
    <el-drawer v-model="detailOpen" :title="`${selected?.channel_name || `渠道 ${selected?.channel_id || ''}`} · ${generationRange[0]} 至 ${generationRange[1]}`" size="82%">
      <div class="drawer-actions"><el-button v-if="selected" @click="exportDaily(selected)">导出日账单</el-button><el-button v-if="selected" @click="exportAnomalies(selected)">导出异常订单</el-button></div>
      <div class="drawer-summary"><span>折扣 <b>{{ Number(selected?.discount || 1).toFixed(2) }}</b></span><span>区间原价 <b>{{ money(selected?.amount || '0') }}</b></span><span>折扣总金额 <b>{{ money(selected?.discounted_amount || '0') }}</b></span></div>
      <el-alert v-if="detail.error.value" type="info" :title="detail.error.value" :closable="false" show-icon />
      <AsyncPanel :loading="detail.loading.value" :error="detail.error.value ? '' : detail.error.value" :empty="!detail.error.value && !detail.data.value?.details?.length" empty-text="该渠道在所选区间没有日账单数据" @retry="detail.reload">
        <el-table :data="detail.data.value?.details || []" class="channel-detail-table" table-layout="fixed">
          <el-table-column prop="day" label="日期" width="105" /><el-table-column label="模型信息" min-width="165"><template #default="s"><b class="cell-primary">{{ s.row.model_name }}</b><small class="cell-secondary">{{ s.row.group_name || "默认分组" }} · {{ s.row.tier_from > 0 ? `档位 ≥${formatNumber(s.row.tier_from)}` : "基础价" }}</small></template></el-table-column>
          <el-table-column label="订单" min-width="125"><template #default="s"><div class="metric-pairs"><span>总数</span><b>{{ formatNumber(s.row.request_count + s.row.abnormal_rows) }}</b><span>计费</span><b>{{ formatNumber(s.row.request_count) }}</b><span>异常</span><b :class="{ danger: s.row.abnormal_rows > 0 }">{{ formatNumber(s.row.abnormal_rows) }}</b></div></template></el-table-column><el-table-column label="Token 用量" min-width="230"><template #default="s"><div class="metric-grid"><span>普通</span><b>{{ formatNumber(s.row.prompt_tokens) }}</b><span>读取</span><b>{{ formatNumber(s.row.cache_tokens) }}</b><span>写入</span><b>{{ formatNumber(s.row.cache_write_tokens) }}</b><span>输出</span><b>{{ formatNumber(s.row.completion_tokens) }}</b></div></template></el-table-column>
          <el-table-column label="单价 / 1M" min-width="205"><template #default="s"><div class="metric-grid"><span>输入</span><b>{{ unitPrice(s.row.input_price) }}</b><span>读取</span><b>{{ unitPrice(s.row.cache_price) }}</b><span>写入</span><b>{{ unitPrice(s.row.cache_write_price) }}</b><span>输出</span><b>{{ unitPrice(s.row.output_price) }}</b></div></template></el-table-column>
          <el-table-column label="金额" min-width="135" align="right"><template #default="s"><b>{{ s.row.unpriced ? "无法取价" : discountedMoney(s.row.amount) }}</b><small v-if="!s.row.unpriced && Number(selected?.discount || 1) !== 1" class="cell-secondary">原价 {{ money(s.row.amount) }}</small><small class="cell-secondary">异常 {{ money(s.row.abnormal_amount) }}</small></template></el-table-column>
        </el-table>
      </AsyncPanel>
    </el-drawer>
    <el-drawer v-model="upstreamOpen" :title="`${selectedUpstream?.display_name||'上游渠道'} · ${generationRange[0]} 至 ${generationRange[1]}`" size="82%">
      <div class="drawer-actions"><el-button v-if="selectedUpstream" @click="exportUpstream(selectedUpstream)">导出 CSV</el-button></div>
      <AsyncPanel :loading="upstreamDetail.loading.value" :error="upstreamDetail.error.value" :empty="!upstreamDetail.data.value?.details?.length" empty-text="该组没有日账单数据" @retry="upstreamDetail.reload">
        <el-table :data="upstreamDetail.data.value?.details||[]"><el-table-column prop="day" label="日期" width="110"/><el-table-column prop="model_name" label="模型" min-width="180"/><el-table-column prop="request_count" label="请求数" width="100" align="right"/><el-table-column prop="prompt_tokens" label="普通输入" width="120" align="right"/><el-table-column prop="cache_tokens" label="缓存读取" width="120" align="right"/><el-table-column prop="cache_write_tokens" label="缓存写入" width="120" align="right"/><el-table-column prop="completion_tokens" label="输出" width="110" align="right"/><el-table-column label="金额" width="120" align="right"><template #default="s"><b>{{s.row.unpriced?'无法取价':money(s.row.amount)}}</b></template></el-table-column><el-table-column prop="quota" label="Quota（参考）" width="130" align="right"/></el-table>
        <h3>成员渠道小计</h3><el-table :data="upstreamDetail.data.value?.group.members||[]"><el-table-column prop="channel_name" label="渠道" min-width="180"/><el-table-column prop="model_name" label="模型" min-width="180"/><el-table-column prop="totals.request_count" label="请求数" width="100" align="right"/><el-table-column label="金额" width="120" align="right"><template #default="s"><b>{{money(s.row.totals.amount)}}</b></template></el-table-column><el-table-column prop="totals.quota" label="Quota（参考）" width="130" align="right"/></el-table>
      </AsyncPanel>
    </el-drawer>
  </AppShell>
</template>

<style scoped>
.page{display:flex;height:calc(100vh - 78px);min-height:0;flex-direction:column;gap:10px;overflow:hidden}.period-summary{display:flex;align-items:center;padding:8px 12px;border:1px solid var(--el-border-color-lighter);border-radius:6px;background:var(--el-fill-color-blank);color:var(--el-text-color-secondary)}.period-summary b{margin-left:8px;color:var(--el-text-color-primary)}.actual-period{margin-left:auto;font-size:12px}.actual-period strong{margin-left:6px;color:var(--el-text-color-primary)}.coverage-prompt{display:flex;align-items:center;gap:12px}.coverage-prompt .el-alert{flex:1}.coverage-prompt .el-button{flex:none}.content{min-height:0;flex:1}.table-wrap{height:calc(100% - 58px);min-height:240px}.table-wrap :deep(.cell),.channel-detail-table :deep(.cell){font-variant-numeric:tabular-nums}.channel-table :deep(.el-table__row){cursor:pointer}.sub,.cell-secondary{display:block;margin-top:3px;color:var(--el-text-color-secondary)}.cell-primary{color:var(--el-text-color-primary)}.metric-pairs,.metric-grid{display:grid;grid-template-columns:auto 1fr;column-gap:8px;row-gap:3px;align-items:center}.metric-grid{grid-template-columns:auto minmax(0,1fr) auto minmax(0,1fr)}.metric-pairs span,.metric-grid span{color:var(--el-text-color-secondary);font-size:12px}.metric-pairs b,.metric-grid b{text-align:right;white-space:nowrap}.danger{color:var(--el-color-danger)}.period-label{color:var(--el-text-color-secondary);font-size:13px}.drawer-actions{display:flex;justify-content:flex-end;gap:8px;margin-bottom:12px}.drawer-summary{display:flex;justify-content:flex-end;gap:28px;margin-bottom:12px;padding:10px 12px;background:var(--el-fill-color-light);border-radius:6px}.drawer-summary b{margin-left:6px;color:var(--el-color-primary);font-variant-numeric:tabular-nums}
</style>
