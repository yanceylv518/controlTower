<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue';
import type {BillingWorkspaceBill,BillingCatalogPage} from '@ct/shared';
import {dashboard} from '../api';
import {billingTaskErrorMessage} from '../utils/httpError';
import {coverageLabel,coverageStatus,coverageTooltip} from '../utils/billingCoverage';

const props=defineProps<{site:string;kind:'user'|'upstream';active:boolean}>();
const emit=defineEmits<{open:[bill:BillingWorkspaceBill];download:[bill:BillingWorkspaceBill]}>();
const period=ref('monthly'),month=ref(''),query=ref(''),appliedQuery=ref(''),page=ref(1),size=ref(20);
const items=ref<BillingCatalogPage['items']>([]),counts=ref<BillingCatalogPage['counts']>({monthly:0,daily:0,temporary:0});
const total=ref(0),subjects=ref(0),loading=ref(false),error=ref('');
const subjectLabel=computed(()=>props.kind==='user'?'用户':'上游');
const labels=[{value:'monthly',label:'月账单'},{value:'daily',label:'日账单'},{value:'temporary',label:'临时账单'}] as const;
let revision=0,controller:AbortController|undefined,debounce:ReturnType<typeof setTimeout>|undefined,disposed=false;
const name=(b:BillingWorkspaceBill)=>(props.kind==='user'?b.job.user_name:b.job.upstream_name)||`${subjectLabel.value} #${subjectID(b)}`;
const subjectID=(b:BillingWorkspaceBill)=>props.kind==='user'?b.job.user_id:b.job.upstream_id;
const day=(s?:string)=>s?new Date(s).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'}):'—';
const local=(s?:string)=>s?new Date(s).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'—';
const money=(v:string)=>Number(v).toLocaleString('zh-CN',{minimumFractionDigits:6,maximumFractionDigits:6});
const currency=(b:BillingWorkspaceBill)=>b.currency?.type==='CUSTOM'?b.currency.symbol||'自定义币种':b.currency?.type==='TOKENS'?'额度':b.currency?.type||'—';
async function load(quiet=false){
 if(!props.site||!props.active||disposed)return;
 const n=++revision;controller?.abort();controller=new AbortController();if(!quiet)loading.value=true;
 try{const r=await dashboard.billingCatalog({instance_id:props.site,kind:props.kind+'_statement',period:period.value,month:month.value||undefined,q:appliedQuery.value||undefined,page:page.value,page_size:size.value},controller.signal);
  if(n!==revision||disposed)return;
  if(page.value>1&&r.total<=(page.value-1)*size.value){page.value=Math.max(1,Math.ceil(r.total/size.value));return;}
  items.value=r.items;counts.value=r.counts;total.value=r.total;subjects.value=r.subjects;error.value='';
 }catch(e){if(n===revision&&!controller.signal.aborted)error.value=billingTaskErrorMessage(e,'账单列表加载失败');}
 finally{if(n===revision)loading.value=false;}
}
function search(){clearTimeout(debounce);const value=query.value.trim();if(value===appliedQuery.value){void load();return;}appliedQuery.value=value;}
function reset(){clearTimeout(debounce);query.value='';appliedQuery.value='';month.value='';page.value=1;void load();}
watch(query,()=>{clearTimeout(debounce);debounce=setTimeout(search,300);});
watch([period,month,appliedQuery,size],()=>{if(page.value!==1)page.value=1;else void load();});
watch(page,()=>void load());
watch(()=>[props.site,props.kind],()=>{revision++;controller?.abort();clearTimeout(debounce);query.value='';appliedQuery.value='';month.value='';period.value='monthly';page.value=1;items.value=[];counts.value={monthly:0,daily:0,temporary:0};total.value=subjects.value=0;error.value='';loading.value=false;void load();},{immediate:true});
watch(()=>props.active,active=>{if(active)void load();else{revision++;controller?.abort();loading.value=false;}});
const timer=setInterval(()=>{if(!loading.value&&props.active&&document.visibilityState!=='hidden')void load(true);},15000);
onBeforeUnmount(()=>{disposed=true;revision++;controller?.abort();clearTimeout(debounce);clearInterval(timer);});
defineExpose({refresh:load,month});
</script>

<template>
 <section class="catalog" aria-label="已生成账单">
  <div class="catalog-types" aria-label="账单分类"><button v-for="item in labels" :key="item.value" :class="{active:period===item.value}" :aria-pressed="period===item.value" @click="period=item.value">{{item.label}}<span>{{counts[item.value].toLocaleString()}}</span></button></div>
  <div class="catalog-filters">
   <label class="catalog-search">{{subjectLabel}} / 账单编号<el-input v-model="query" clearable :placeholder="`搜索${subjectLabel}名称、ID 或账单编号`" @keyup.enter="search"/></label>
   <label>账单月份<el-date-picker v-model="month" type="month" value-format="YYYY-MM" placeholder="全部月份" clearable/></label>
   <el-button @click="reset">重置</el-button><el-button :loading="loading" @click="load()">刷新</el-button>
  </div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" show-icon><el-button link @click="load()">重新加载</el-button></el-alert>
  <div v-if="!error" class="catalog-summary"><span>共 {{total.toLocaleString()}} 份{{labels.find(v=>v.value===period)?.label}} · {{subjects.toLocaleString()}} {{kind==='user'?'位用户':'个上游'}}</span><span>最近生成优先</span></div>
  <el-table v-loading="loading" :data="error?[]:items" row-key="job.id" :empty-text="error?'账单加载失败，请重试':loading?'正在加载':query||month?'没有符合条件的已生成账单':'暂无已生成账单'">
   <el-table-column :label="subjectLabel" min-width="160"><template #default="s"><div class="catalog-person"><span class="avatar">{{name(s.row).slice(0,1)}}</span><div><b>{{name(s.row)}}</b><small>#{{subjectID(s.row)}}</small></div></div></template></el-table-column>
   <el-table-column label="账期" :min-width="period==='temporary'?290:240"><template #default="s">
    <template v-if="period==='monthly'"><b>{{day(s.row.job.range_from).slice(0,7)}}</b><el-tooltip :content="coverageTooltip(s.row.job.monthly_coverage)"><div class="coverage" :class="{partial:!s.row.job.monthly_coverage?.complete}">{{coverageLabel(s.row.job.monthly_coverage)}}<small>{{coverageStatus(s.row.job.monthly_coverage)}}</small></div></el-tooltip></template>
    <b v-else>{{period==='daily'?day(s.row.job.range_from):local(s.row.job.range_from)+' — '+local(s.row.job.range_to)}}</b>
   </template></el-table-column>
   <el-table-column label="账单金额" min-width="165" align="right"><template #default="s"><b class="amount">{{money(s.row.amount)}}</b><small>{{currency(s.row)}}</small></template></el-table-column>
   <el-table-column label="生成时间" min-width="168"><template #default="s">{{local(s.row.generated_at)}}</template></el-table-column>
   <el-table-column label="操作" width="150" fixed="right" align="right"><template #default="s"><el-button link type="primary" @click="emit('open',s.row)">查看</el-button><el-button link @click="emit('download',s.row)">下载</el-button></template></el-table-column>
  </el-table>
  <footer><span>{{month||'全部月份'}} · {{labels.find(v=>v.value===period)?.label}}</span><el-pagination v-model:current-page="page" v-model:page-size="size" :page-sizes="[20,50,100]" :total="total" layout="sizes, prev, pager, next" :pager-count="5" small/></footer>
 </section>
</template>

<style scoped>
.catalog{background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:10px;min-width:0;overflow:hidden}.catalog-types{display:flex;gap:8px;padding:16px 18px;border-bottom:1px solid var(--el-border-color-lighter)}.catalog-types button{border:0;border-radius:6px;padding:8px 13px;font:inherit;font-size:13px;color:var(--el-text-color-regular);background:transparent;cursor:pointer}.catalog-types button.active{color:var(--el-color-primary);background:var(--el-color-primary-light-9)}.catalog-types span{font-size:11px;margin-left:8px;font-variant-numeric:tabular-nums}.catalog-filters{display:flex;align-items:flex-end;gap:12px;padding:18px;flex-wrap:wrap}.catalog-filters label{display:grid;gap:7px;font-size:12px;color:var(--el-text-color-secondary)}.catalog-search{flex:1;min-width:230px}.catalog-filters :deep(.el-date-editor){width:150px}.catalog-filters .el-button+.el-button{margin:0}.catalog-summary{display:flex;justify-content:space-between;gap:12px;padding:0 18px 16px;font-size:12px}.catalog-summary>span:last-child,small{color:var(--el-text-color-secondary)}.catalog :deep(.el-table th){background:var(--el-fill-color-light);font-weight:400}.catalog :deep(.el-table td){padding:13px 0}.catalog-person{display:flex;align-items:center;gap:10px}.avatar{background:var(--el-color-primary-light-9);color:var(--el-color-primary);border-radius:8px;width:30px;height:30px;display:grid;place-items:center;flex-shrink:0}.catalog b{font-weight:500}.catalog small{display:block;font-size:11px}.amount{font-variant-numeric:tabular-nums}.coverage{font-size:12px;color:var(--el-text-color-secondary);line-height:1.6;overflow-wrap:anywhere}.coverage.partial{color:var(--el-color-warning)}footer{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;padding:16px 18px;font-size:12px;color:var(--el-text-color-secondary)}@media(max-width:650px){.catalog-filters{padding:14px}.catalog-search{flex-basis:100%;min-width:0}.catalog-types{padding:12px}.catalog-types button{padding:8px 10px}footer :deep(.el-pagination){flex-wrap:wrap;gap:6px}}
</style>
