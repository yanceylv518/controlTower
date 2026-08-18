<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { type BillingChannelSummary, type BillingJob, type BillingUpstreamGroup } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useFiltersStore } from "../stores/filters";
import { usePrefsStore } from "../stores/prefs";
import { formatNumber } from "../utils/format";
import { formatDateTime, savedGenerationRange, timeRangeShortcuts, validateGenerationRange } from "../utils/billingRange";
import { billingReadErrorMessage, downloadBillingFile, httpError } from "../utils/httpError";

type PageData = { items: BillingChannelSummary[]; job: BillingJob | null; channelError: string; currencySymbol: string; currencyRate: number };
const filters = useFiltersStore(), prefs = usePrefsStore();
const generating = ref(false), progress = ref(0), page = ref(1), pageSize = ref(20);
const exporting = ref<Record<number, boolean>>({});
const detailOpen = ref(false), selected = ref<BillingChannelSummary>();
const viewMode = ref<"channel"|"upstream">("channel"), upstreamSearch = ref("");
const upstreamOpen = ref(false), selectedUpstream = ref<BillingUpstreamGroup>();
const generationRange = ref<[string, string]>(savedGenerationRange("ct.billing.channel.range"));
void prefs.load();
const state = useAsyncData<PageData>(async () => {
  await filters.loadInstances();
  if (!filters.site_id) return { items: [], job: null, channelError: "", currencySymbol: "", currencyRate: 1 };
  const [from,to]=generationRange.value;
  const bill = await dashboard.billingChannels({ instance_id: filters.site_id, from, to });
  return {
    items: (bill.items || []).sort((a, b) => Number(b.amount) - Number(a.amount) || a.channel_id - b.channel_id),
    job: bill.generation_job || null,
    channelError: bill.warning || "",
    currencySymbol: bill.currency?.type ? bill.currency.symbol : (prefs.currencySymbol || "$"),
    currencyRate: (() => { const rate = Number(bill.currency?.exchange_rate); return Number.isFinite(rate) && rate > 0 ? rate : 1; })(),
  };
});
const upstreamState = useAsyncData(async()=>{if(viewMode.value!=="upstream"||!filters.site_id)return undefined;const[from,to]=generationRange.value;return dashboard.billingUpstreamChannels({instance_id:filters.site_id,from,to,job_id:job.value?.id})});

const items = computed(() => state.data.value?.items || []), job = computed(() => state.data.value?.job || null);
const channelSearch = ref("");
const filteredItems = computed(() => {
  const q = channelSearch.value.trim().toLowerCase();
  if (!q) return items.value;
  return items.value.filter(row => String(row.channel_id).includes(q) || (row.channel_name || "").toLowerCase().includes(q));
});
const upstreamItems = computed(()=>{const q=upstreamSearch.value.trim().toLowerCase();return (upstreamState.data.value?.items||[]).filter(g=>!q||g.base_url.toLowerCase().includes(q)||g.display_name.toLowerCase().includes(q)||g.members.some(m=>m.channel_name.toLowerCase().includes(q)||m.model_name.toLowerCase().includes(q))).map(g=>({...g,row_id:`g:${g.upstream_fp||'unmapped'}`,children:g.members.map(m=>({...m,row_id:`c:${m.channel_id}`,display_name:`${m.model_name||'未知模型'} · ${m.channel_name||`渠道 ${m.channel_id}`} · #${m.channel_id}`,request_count:m.totals.request_count,prompt_tokens:m.totals.prompt_tokens,completion_tokens:m.totals.completion_tokens,cache_tokens:m.totals.cache_tokens,cache_write_tokens:m.totals.cache_write_tokens,quota:m.totals.quota,amount:m.totals.amount})) ,request_count:g.totals.request_count,prompt_tokens:g.totals.prompt_tokens,completion_tokens:g.totals.completion_tokens,cache_tokens:g.totals.cache_tokens,cache_write_tokens:g.totals.cache_write_tokens,quota:g.totals.quota,amount:g.totals.amount}))});
const upstreamExpanded=computed(()=>upstreamItems.value.filter(g=>g.upstream_fp).map(g=>g.row_id));
const detail = useAsyncData(async () => {
  if (!selected.value || !filters.site_id) return undefined;
  const [from, to] = generationRange.value;
  return dashboard.billingChannels({ instance_id: filters.site_id, channel_id: selected.value.channel_id, from, to, job_id: job.value?.id });
});
const upstreamDetail=useAsyncData(async()=>{if(!selectedUpstream.value)return undefined;const[from,to]=generationRange.value;return dashboard.billingUpstreamDetail({instance_id:filters.site_id,fp:selectedUpstream.value.upstream_fp,from,to,job_id:job.value?.id})});
const actualPeriod = computed(() => job.value?.status === "complete" ? jobRange(job.value) : "尚未生成，下方仅显示渠道配置");
const pagedItems = computed(() => filteredItems.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
const currency = computed(() => state.data.value ? state.data.value.currencySymbol : (prefs.currencySymbol || "$"));
// USD-based amounts must be converted by the site exchange rate, not just relabeled.
const currencyRate = computed(() => state.data.value?.currencyRate || 1);
const money = (value: string) => `${currency.value}${(Number(value || 0) * currencyRate.value).toFixed(4)}`;
const unitPrice = (value: string | undefined) => value ? `${currency.value}${(Number(value) * currencyRate.value).toFixed(4)}` : "—";
const discountedMoney = (value: string | undefined) => money(String(Number(value || 0) * Number(selected.value?.discount || 1)));
function jobRange(value: BillingJob | null) { if(!value?.range_from||!value.range_to)return"";return`[${formatDateTime(new Date(value.range_from))}, ${formatDateTime(new Date(value.range_to))})`; }
const status = computed(() => {
  if (generating.value || ["pending", "running"].includes(job.value?.status || "")) return { type: "info" as const, title: `渠道账单生成中（${progress.value}%）`, text: `${jobRange(job.value)}；后台正在按小时分段扫描日志，页面会自动刷新进度。` };
  if (job.value?.status === "failed") return { type: "error" as const, title: "渠道账单生成失败", text: `${jobRange(job.value)}；${job.value.error_message || "可以重新创建任务。"}` };
  if (job.value?.status === "complete") return { type: "success" as const, title: "渠道账单生成完成", text: `${jobRange(job.value)}；数据截至 ${job.value.updated_at ? new Date(job.value.updated_at).toLocaleString() : "任务完成时间"}；共完成 ${job.value.total_steps} 个小时步骤，异常订单 ${job.value.abnormal_rows} 条。` };
  return { type: "warning" as const, title: "所选区间的渠道账单尚未生成", text: "下方仅显示当前渠道配置，所有数量和金额均为 0；生成后才会显示该区间的账单数据。" };
});

async function monitor(initial: BillingJob) {
  if (generating.value) return;
  generating.value = true;
  let current = initial;
  try {
    while (["pending", "running"].includes(current.status)) {
      progress.value = current.total_steps ? Math.round(current.completed_steps * 100 / current.total_steps) : 0;
      await new Promise((resolve) => setTimeout(resolve, 1500));
      current = await dashboard.billingJob(current.id);
    }
    progress.value = current.status === "complete" ? 100 : progress.value;
    await state.reload();
    current.status === "failed" ? ElMessage.error(current.error_message || "渠道账单生成失败") : ElMessage.success("渠道账单生成完成");
  } finally { generating.value = false; }
}
async function generate(force=false) {
  if (generating.value || !filters.site_id) return;
  const invalid=validateGenerationRange(generationRange.value);
  if(invalid){ElMessage.warning(invalid);return;}
  const [from,to]=generationRange.value;
  const result = await dashboard.generateBilling({ instance_id: filters.site_id, from, to, scope: "channel", force });
  if(result.job.status==="complete"){await state.reload();ElMessage.success(result.reused?"该区间已有渠道账单，已直接加载":"所选时间范围的渠道账单已经生成完成");return;}
  await monitor(result.job);
}
async function save(row: BillingChannelSummary, value: string) {
  const discount = Number(value);
  if (!Number.isFinite(discount) || discount < 0 || discount > 1) { ElMessage.warning("折扣必须在 0 到 1 之间"); return; }
  await dashboard.saveBillingChannelDiscount({ instance_id: filters.site_id, channel_id: row.channel_id, discount: String(discount) });
  row.discount = String(discount); row.discounted_amount = (Number(row.amount) * discount).toFixed(6); ElMessage.success("渠道折扣已保存");
}
function openDetail(row: BillingChannelSummary) { selected.value = row; detailOpen.value = true; void detail.reload(); }
function openUpstream(row: BillingUpstreamGroup){selectedUpstream.value=row;upstreamOpen.value=true;void upstreamDetail.reload()}
function upstreamURL(row:BillingUpstreamGroup){const[from,to]=generationRange.value;const p=new URLSearchParams({instance_id:filters.site_id,fp:row.upstream_fp,from,to,format:"csv"});if(job.value?.id)p.set("job_id",job.value.id);return`/api/dashboard/billing/upstream-channels/detail?${p}`}
async function exportUpstream(row:BillingUpstreamGroup){try{await downloadBillingFile(upstreamURL(row),"导出上游渠道 CSV 失败")}catch(error){ElMessage.error(billingReadErrorMessage(error,"导出上游渠道 CSV 失败"))}}
function channelReadURL(path: string, row: BillingChannelSummary, extra = "") {
  const [from, to] = generationRange.value;
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
watch(generationRange,(value)=>localStorage.setItem("ct.billing.channel.range",JSON.stringify(value)),{deep:true});
watch([generationRange, () => filters.site_id], () => { page.value = 1; void state.reload(); if(viewMode.value==="upstream")void upstreamState.reload(); if (detailOpen.value) void detail.reload(); });
watch(viewMode,mode=>{if(mode==="upstream")void upstreamState.reload()});
watch(channelSearch, () => { page.value = 1; });
watch(pageSize, () => { page.value = 1; });
watch(() => state.data.value?.job?.id, () => { const current = state.data.value?.job; if (current && ["pending", "running"].includes(current.status)) void monitor(current); if(viewMode.value==="upstream")void upstreamState.reload(); });
void state.reload();
</script>

<template>
  <AppShell title="渠道账单">
    <template #tools>
      <span class="period-label">账单周期</span><el-date-picker v-model="generationRange" type="datetimerange" value-format="YYYY-MM-DD HH:mm:ss" format="YYYY-MM-DD HH:mm:ss" range-separator="至" start-placeholder="开始时间" end-placeholder="结束时间" :shortcuts="timeRangeShortcuts" unlink-panels style="width:420px" />
      <el-button type="primary" :loading="generating" @click="generate()">生成渠道账单</el-button><el-popconfirm title="强制重新生成会创建新的渠道账单版本，确定继续？" @confirm="generate(true)"><template #reference><el-button :disabled="generating">强制重新生成</el-button></template></el-popconfirm>
    </template>
    <div class="page">
      <div class="period-summary"><span>选择区间</span><b>[{{ generationRange[0] }}, {{ generationRange[1] }})</b><span class="actual-period">下方数据区间 <strong>{{ actualPeriod }}</strong></span></div>
      <el-alert :type="status.type" :title="status.title" :description="status.text" :closable="false" show-icon />
      <el-alert v-if="state.data.value?.channelError" type="warning" title="部分渠道配置加载失败" :description="state.data.value.channelError" :closable="false" show-icon />
      <div class="view-switch"><el-radio-group v-model="viewMode" size="small"><el-radio-button value="channel">按渠道</el-radio-button><el-radio-button value="upstream">按上游 key</el-radio-button></el-radio-group><el-input v-if="viewMode==='upstream'" v-model="upstreamSearch" clearable placeholder="搜索渠道名、模型或 base_url" style="width:280px" /><el-input v-else v-model="channelSearch" clearable placeholder="搜索渠道名或 ID" style="width:240px" /></div>
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
          <el-table-column label="操作" width="130"><template #default="s"><template v-if="s.row.member_count!==undefined"><el-button link type="primary" @click.stop="openUpstream(s.row)">明细</el-button><el-button link type="primary" @click.stop="exportUpstream(s.row)">CSV</el-button></template></template></el-table-column>
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
.page{display:flex;height:calc(100vh - 78px);min-height:0;flex-direction:column;gap:10px;overflow:hidden}.period-summary{display:flex;align-items:center;padding:8px 12px;border:1px solid var(--el-border-color-lighter);border-radius:6px;background:var(--el-fill-color-blank);color:var(--el-text-color-secondary)}.period-summary b{margin-left:8px;color:var(--el-text-color-primary)}.actual-period{margin-left:auto;font-size:12px}.actual-period strong{margin-left:6px;color:var(--el-text-color-primary)}.content{min-height:0;flex:1}.table-wrap{height:calc(100% - 58px);min-height:240px}.table-wrap :deep(.cell),.channel-detail-table :deep(.cell){font-variant-numeric:tabular-nums}.channel-table :deep(.el-table__row){cursor:pointer}.sub,.cell-secondary{display:block;margin-top:3px;color:var(--el-text-color-secondary)}.cell-primary{color:var(--el-text-color-primary)}.metric-pairs,.metric-grid{display:grid;grid-template-columns:auto 1fr;column-gap:8px;row-gap:3px;align-items:center}.metric-grid{grid-template-columns:auto minmax(0,1fr) auto minmax(0,1fr)}.metric-pairs span,.metric-grid span{color:var(--el-text-color-secondary);font-size:12px}.metric-pairs b,.metric-grid b{text-align:right;white-space:nowrap}.danger{color:var(--el-color-danger)}.period-label{color:var(--el-text-color-secondary);font-size:13px}.drawer-actions{display:flex;justify-content:flex-end;gap:8px;margin-bottom:12px}.drawer-summary{display:flex;justify-content:flex-end;gap:28px;margin-bottom:12px;padding:10px 12px;background:var(--el-fill-color-light);border-radius:6px}.drawer-summary b{margin-left:6px;color:var(--el-color-primary);font-variant-numeric:tabular-nums}
</style>
