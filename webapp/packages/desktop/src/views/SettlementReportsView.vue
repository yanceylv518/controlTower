<script setup lang="ts">
import{computed,onBeforeUnmount,ref,watch}from'vue';import ReportGenerationTasks from'../components/ReportGenerationTasks.vue';import AppShell from'../components/AppShell.vue';import{dashboard}from'../api';import{useFiltersStore}from'../stores/filters';
const filters=useFiltersStore(),period=ref('daily'),date=ref(new Date(Date.now()-86400000).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'})),dimension=ref('user'),loading=ref(false),error=ref(''),detail=ref(false);
type Raw=Awaited<ReturnType<typeof dashboard.settlementReport>>['items'][number];const items=ref<Raw[]>([]),failed=ref<number|null>(null),breakdown=ref<Raw[]>([]);let request:AbortController|undefined;
const money=(v:number|string)=>Number(v).toLocaleString('zh-CN',{minimumFractionDigits:2,maximumFractionDigits:6});
const total=computed(()=>items.value.reduce((a,v)=>({requests:a.requests+v.requests,input:a.input+v.input,output:a.output+v.output,cache:a.cache+v.cache,amount:a.amount+Number(v.amount),cost:a.cost+Number(v.cost),unknown:a.unknown+v.unknown_cost,empty:a.empty+v.empty}),{requests:0,input:0,output:0,cache:0,amount:0,cost:0,unknown:0,empty:0}));
const original=(raw:Raw[],field:'raw_amount'|'raw_cost')=>!raw.length&&generated.value?money(0):raw.length && raw.every(v=>v[field]!=='' && v[field]!=null)?money(raw.reduce((a,v)=>a+Number(v[field]),0)):'—';
const fallbackCount=(raw:Raw[])=>raw.reduce((n,v)=>n+(v.base_fallback||0),0);
const marginRate=(income:number,cost:number,unknown:number)=>unknown || income===0?'—':((income-cost)/income*100).toLocaleString('zh-CN',{minimumFractionDigits:2,maximumFractionDigits:2})+'%';
const rateLabel=(v:string)=>!v?'—':v==='mixed'?'多种折扣':Number(v)===1?'原价':`${Number((Number(v)*10).toFixed(6))} 折`;
const costRate=(raw:Raw[])=>raw.some(v=>!v.cost_discount)?'—':[...new Set(raw.map(v=>v.cost_discount))].length===1?rateLabel(raw[0].cost_discount):'多种折扣';
type Row={key:string;name:string;requests:number;input:number;output:number;cache:number;amount:number;cost:number;unknown:number;discount:string;raw:Raw[];children?:Row[]};
function summarize(raw:Raw[],key:string,name:string):Row{const rates=[...new Set(raw.map(v=>v.discount))];return{key,name,requests:raw.reduce((a,v)=>a+v.requests,0),input:raw.reduce((a,v)=>a+v.input,0),output:raw.reduce((a,v)=>a+v.output,0),cache:raw.reduce((a,v)=>a+v.cache,0),amount:raw.reduce((a,v)=>a+Number(v.amount),0),cost:raw.reduce((a,v)=>a+Number(v.cost),0),unknown:raw.reduce((a,v)=>a+v.unknown_cost,0),discount:rates.length===1?rateLabel(rates[0]):'多种折扣',raw};}
function channels(raw:Raw[],parent:string):Row[]{
 const groups=new Map<string,Raw[]>();for(const v of raw){const key=String(v.channel_id);groups.set(key,[...(groups.get(key)||[]),v]);}
 return [...groups].map(([key,values])=>summarize(values,`${parent}|channel:${key}`,`${values[0].upstream||'未关联上游'} / ${values[0].channel||'渠道'} #${key}`));
}
const rows=computed(()=>{
 const groups=new Map<string,Raw[]>();for(const v of items.value){const key=dimension.value==='user'?String(v.user_id):dimension.value==='model'?v.model:String(v.channel_id);groups.set(key,[...(groups.get(key)||[]),v]);}
 return [...groups].map(([key,raw])=>{
  const first=raw[0], root=`${dimension.value}:${key}`;
  const row=summarize(raw,root,dimension.value==='user'?`${first.user||'用户'} #${first.user_id}`:dimension.value==='model'?first.model:`${first.upstream||'未关联上游'} / ${first.channel||'渠道'} #${first.channel_id}`);
  if(dimension.value==='user'){
   const grouped=new Map<string,Raw[]>();for(const v of raw){const k=v.model+'|'+v.discount;grouped.set(k,[...(grouped.get(k)||[]),v]);}
   row.children=[...grouped].map(([k,v])=>{const child=summarize(v,root+'|model:'+k,v[0].model);child.children=channels(v,child.key);return child;});
  }else if(dimension.value==='model'){row.children=channels(raw,root);}
  return row;
 }).sort((a,b)=>b.amount-a.amount);
});
const currency=ref('—'),generated=ref(0),expected=ref(0),missing=ref<string[]>([]);
const range=computed(()=>{let from=date.value,to:string;if(period.value==='monthly'){from=from.slice(0,7)+'-01';const[y,m]=from.split('-').map(Number);to=m===12?`${y+1}-01-01`:`${y}-${String(m+1).padStart(2,'0')}-01`;}else{to=new Date(new Date(from+'T00:00:00+08:00').getTime()+86400000).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'});}return {from,to};});
const reportLabel=computed(()=>period.value==='monthly'?date.value.slice(0,7):date.value);
async function load(){await filters.loadInstances();if(!filters.site_id||!date.value)return;request?.abort();const controller=new AbortController();request=controller;loading.value=true;error.value='';try{const result=await dashboard.settlementReport({instance_id:filters.site_id,...range.value},controller.signal);if(request!==controller)return;items.value=result.items;generated.value=result.generated_days;expected.value=result.expected_days;missing.value=result.missing_days;currency.value=result.currency?.type==='CUSTOM'?result.currency.symbol:result.currency?.type==='TOKENS'?'额度':result.currency?.type||'—';failed.value=result.failed_requests;}catch(e){if(!controller.signal.aborted){items.value=[];generated.value=0;error.value=(e as {code?:string})?.code==='report_currency_mismatch'?'日报币种不一致，请分别查看日报，或统一币种后重新生成':'报表读取失败，请重试';}}finally{if(request===controller)loading.value=false;}}
function inspect(row:Row){breakdown.value=row.raw;detail.value=true;}
watch([()=>filters.site_id,period,date],()=>{items.value=[];generated.value=0;missing.value=[];void load();},{immediate:true});onBeforeUnmount(()=>request?.abort());
</script><template><AppShell title="报表中心"><template #tools><el-radio-group v-model="period"><el-radio-button value="daily">日报表</el-radio-button><el-radio-button value="monthly">月报表</el-radio-button></el-radio-group><el-date-picker v-model="date" :type="period==='monthly'?'month':'date'" :format="period==='monthly'?'YYYY-MM':'YYYY-MM-DD'" value-format="YYYY-MM-DD" :clearable="false" style="width:160px"/><el-button type="primary" :loading="loading" @click="load">查询报表</el-button></template>
<ReportGenerationTasks :site="filters.site_id" :from="range.from" :to="range.to" :label="reportLabel" @completed="load"/>
<el-alert v-if="error" type="error" :title="error" :closable="false"/>
<el-alert v-if="!loading&&!error&&!generated" type="info" :closable="false" :title="expected?'所选日期尚未生成报表':'所选日期尚未结束，暂不能生成正式报表'" style="margin-bottom:12px"/>
<el-alert v-else-if="!loading&&!error&&missing.length" type="warning" :closable="false" :title="`已生成 ${generated} / ${expected} 天，当前统计仅包含已生成日报`" style="margin-bottom:12px"><el-tooltip :content="missing.join('、')"><span>待生成 {{missing.length}} 天</span></el-tooltip></el-alert>
<div v-if="generated" class="report-kpis"><article><small>请求 / 失败</small><strong>{{total.requests.toLocaleString()}} <em>/ {{failed===null?'—':failed.toLocaleString()}}</em></strong></article><article><small>输入 / 输出 / 缓存</small><strong class="usage">{{total.input.toLocaleString()}} / {{total.output.toLocaleString()}} / {{total.cache.toLocaleString()}}</strong></article><article><small>原价金额 · {{currency}}</small><strong>{{original(items,'raw_amount')}}</strong><small v-if="fallbackCount(items)">{{fallbackCount(items)}} 笔按 quota 回退</small></article><article><small>结算收入 · {{currency}}</small><strong>{{money(total.amount)}}</strong></article><article><small>上游成本 · {{currency}}</small><strong>{{total.unknown?'待核定':money(total.cost)}}</strong></article><article><small>毛利 · {{currency}}</small><strong>{{total.unknown?'—':money(total.amount-total.cost)}}</strong><small>毛利率 {{marginRate(total.amount,total.cost,total.unknown)}}</small></article></div>
<section class="report-table" v-loading="loading"><header><el-radio-group v-model="dimension"><el-radio-button value="user">按用户</el-radio-button><el-radio-button value="model">按模型</el-radio-button><el-radio-button value="channel">按渠道</el-radio-button></el-radio-group><small v-if="total.unknown">{{total.unknown.toLocaleString()}} 笔请求成本待核定</small></header><el-table :data="rows" row-key="key" default-expand-all size="small" :empty-text="generated?'所选日期没有请求':'尚未生成报表'" class="grouped-report">
  <el-table-column prop="name" :label="dimension==='user'?'用户 / 模型 / 上游渠道':dimension==='model'?'模型 / 上游渠道':'上游 / 渠道'" min-width="300" fixed="left"/>
  <el-table-column label="调用与用量" header-align="center">
    <el-table-column prop="requests" label="调用次数" width="95" align="right"/>
    <el-table-column label="输入 / 输出 / 缓存" min-width="185" align="right"><template #default="s">{{s.row.input.toLocaleString()}} / {{s.row.output.toLocaleString()}} / {{s.row.cache.toLocaleString()}}</template></el-table-column>
  </el-table-column>
  <el-table-column :label="'原价金额 · '+currency" min-width="140" align="right"><template #default="s">{{original(s.row.raw,'raw_amount')}}<el-tooltip v-if="fallbackCount(s.row.raw)" :content="`${fallbackCount(s.row.raw)} 笔请求已打折但缺少折前额度，此处按 quota 回退作为计费基数`" placement="top"><small class="fallback-note">含 quota 回退</small></el-tooltip></template></el-table-column>
  <el-table-column :label="'用户收入 · '+currency" header-align="center" label-class-name="income-heading">
    <el-table-column prop="discount" label="模型折扣" width="100" align="center"/>
    <el-table-column label="折后金额" min-width="115" align="right"><template #default="s"><b>{{money(s.row.amount)}}</b></template></el-table-column>
  </el-table-column>
  <el-table-column :label="'上游成本 · '+currency" header-align="center" label-class-name="cost-heading">
    <el-table-column label="渠道折扣" width="100" align="center"><template #default="s">{{costRate(s.row.raw)}}</template></el-table-column>
    <el-table-column label="折后金额" min-width="115" align="right"><template #default="s"><el-button link type="primary" @click="inspect(s.row)">{{s.row.unknown?'待核定':money(s.row.cost)}}</el-button></template></el-table-column>
  </el-table-column>
  <el-table-column :label="'毛利 · '+currency" min-width="110" align="right"><template #default="s"><b>{{s.row.unknown?'—':money(s.row.amount-s.row.cost)}}</b><small class="margin-rate">{{marginRate(s.row.amount,s.row.cost,s.row.unknown)}}</small></template></el-table-column>
</el-table></section>
<el-dialog v-model="detail" title="渠道成本明细" width="min(1100px,95vw)">
<el-table :data="breakdown" size="small" empty-text="暂无成本明细">
  <el-table-column prop="upstream" label="上游" min-width="140"/>
  <el-table-column prop="channel" label="渠道" min-width="160"/>
  <el-table-column prop="model" label="模型" min-width="160"/>
  <el-table-column prop="requests" label="调用次数" width="95" align="right"/>
  <el-table-column :label="'上游成本 · '+currency" header-align="center">
    <el-table-column label="原价金额" min-width="110" align="right"><template #default="s">{{s.row.raw_amount===''?'—':money(s.row.raw_amount)}}<small v-if="s.row.base_fallback" class="fallback-note">含 quota 回退</small></template></el-table-column>
    <el-table-column label="渠道折扣" width="100" align="center"><template #default="s">{{rateLabel(s.row.cost_discount)}}</template></el-table-column>
    <el-table-column label="折后金额" min-width="115" align="right"><template #default="s">{{s.row.unknown_cost?'待核定':money(s.row.cost)}}</template></el-table-column>
  </el-table-column>
</el-table></el-dialog>
</AppShell></template><style scoped>.margin-rate{display:block;font-size:12px;color:var(--el-text-color-secondary);line-height:18px}.fallback-note{display:block;font-size:11px;color:var(--el-text-color-secondary)}.grouped-report :deep(.cell){font-variant-numeric:tabular-nums}.grouped-report :deep(.income-heading){color:var(--el-color-primary)}.grouped-report :deep(.cost-heading){color:var(--el-text-color-primary)}.report-kpis{display:grid;grid-template-columns:1.1fr 1.8fr 1fr 1fr 1fr 1fr;gap:10px;margin-bottom:12px}.report-kpis article,.report-table{background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:8px;padding:15px}.report-kpis small,header small{font-size:12px;color:var(--el-text-color-secondary)}.report-kpis strong{display:block;font-size:24px;margin-top:10px;font-variant-numeric:tabular-nums}.report-kpis .usage{font-size:15px;margin-top:18px}.report-kpis em{font-size:14px;font-style:normal;color:var(--el-text-color-secondary)}header{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px}@media(max-width:1100px){.report-kpis{grid-template-columns:repeat(3,1fr)}}@media(max-width:600px){.report-kpis{grid-template-columns:1fr}}</style>
