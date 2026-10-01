<script setup lang="ts">
import {computed,onBeforeUnmount,ref} from 'vue';
import {ElMessage} from 'element-plus';
import type {BillingJob,BillingDetailRow,BillingDetailFilter,BillingDetailTask} from '@ct/shared';
import {dashboard} from '../api';
import {billingTaskErrorMessage,startBillingFileDownload} from '../utils/httpError';
import {formatBillingDiscount} from '../utils/billingDiscount';
const visible=ref(false),job=ref<BillingJob>(),intent=ref(false),rows=ref<BillingDetailRow[]>([]),loading=ref(false),error=ref('');
const models=ref<string[]>([]),tokens=ref<string[]>([]),model=ref(''),token=ref(''),range=ref<[string,string]|null>(null),currency=ref(''),total=ref(0);
const preparation=ref<BillingDetailTask>(),ready=ref(false),next=ref(0),cursors=ref<number[]>([0]),page=ref(0),applied=ref<BillingDetailFilter>({});
const exporting=ref(false),exports=ref<(BillingDetailTask&{jobId:string;label:string;job:BillingJob})[]>([]);
let revision=0,polling=false,disposed=false;
const label=computed(()=>job.value?new Date(job.value.range_from!).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'}):'');
const filtered=computed(()=>Boolean(applied.value.from||applied.value.to||applied.value.model||applied.value.token));
const percent=(t:BillingDetailTask)=>t.status==='complete'?100:Math.min(99,t.total?Math.floor(t.processed/t.total*100):0);
function currentFilter():BillingDetailFilter{return {from:range.value?.[0]||'',to:range.value?.[1]||'',model:model.value,token:token.value};}
async function load(retry=false){if(!job.value)return;const n=++revision;loading.value=true;error.value='';try{
 const r=await dashboard.billingDetailPage(job.value.id,{...applied.value,cursor:cursors.value[page.value],...(retry?{retry:1}:{})});
 if(n!==revision||disposed)return;
 preparation.value=r.task;ready.value=!r.preparing;
 if(r.preparing){rows.value=[];return;}
 rows.value=r.items||[];next.value=r.next_cursor||0;total.value=r.total||0;currency.value=r.currency||'';models.value=r.models||[];tokens.value=r.tokens||[];
 }catch(e){if(n===revision)error.value=billingTaskErrorMessage(e,'明细加载失败');}finally{if(n===revision)loading.value=false;}}
function open(v:BillingJob,forExport=false,initialModel=''){job.value=v;intent.value=forExport;model.value=initialModel;token.value='';range.value=null;rows.value=[];models.value=[];tokens.value=[];ready.value=false;preparation.value=undefined;applied.value=initialModel?{model:initialModel}:{};cursors.value=[0];page.value=0;next.value=0;visible.value=true;void load();}
function search(){applied.value=currentFilter();cursors.value=[0];page.value=0;void load();}
function changePage(forward:boolean){if(forward){cursors.value=cursors.value.slice(0,page.value+1);cursors.value.push(next.value);page.value++;}else{page.value--;}void load();}
async function startExport(all:boolean){if(!job.value)return;exporting.value=true;try{
 const v=job.value,dayLabel=label.value,r=await dashboard.exportBillingDetails(v.id,all?{}:currentFilter());
 if(disposed)return;
 const existing=exports.value.findIndex(t=>t.key===r.key&&t.jobId===v.id);const task={...r,jobId:v.id,label:`${v.user_name||'用户 #'+v.user_id} · ${dayLabel} · ${all?'全部明细':'筛选明细'}`,job:v};
 if(existing>=0)exports.value[existing]=task;else exports.value.push(task);
 if(r.status==='complete'){download(task);ElMessage.success('已发起下载');}else if(r.status==='failed'){ElMessage.error(r.error||'导出失败，请重试');}else{ElMessage.success('正在导出，完成后自动下载');}
 }catch(e){ElMessage.error(billingTaskErrorMessage(e));}finally{exporting.value=false;}}
function download(t:BillingDetailTask&{jobId:string}){startBillingFileDownload(`/api/dashboard/billing/statements/details?id=${encodeURIComponent(t.jobId)}&action=download&key=${encodeURIComponent(t.key)}`);}
const timer=setInterval(async()=>{if(polling||disposed)return;polling=true;try{
 if(visible.value&&preparation.value?.status==='running'&&!loading.value)await load();
 for(const t of exports.value.filter(t=>t.status==='running')){try{const r=await dashboard.billingDetailTask(t.jobId,t.key);if(!disposed){Object.assign(t,r);if(r.status==='complete'){download(t);ElMessage.success('导出完成，已发起下载');}}}catch{t.status='failed';t.error='任务不可用，请重新导出';}}
 }finally{polling=false;}},1500);
onBeforeUnmount(()=>{disposed=true;revision++;clearInterval(timer);});
defineExpose({open});
</script>
<template>
<div v-if="exports.length&&!visible" class="detail-export-tasks">
 <div v-for="task in exports" :key="task.key" class="detail-export-task">
  <span>{{task.label}}</span><el-progress v-if="task.status==='running'" :percentage="percent(task)" :stroke-width="5"/>
  <span v-if="task.status==='running'">已处理 {{task.processed.toLocaleString()}} / {{task.total.toLocaleString()}}</span>
  <span v-else-if="task.status==='complete'">{{task.matched.toLocaleString()}} 条 · 已就绪</span><span v-else class="failed">{{task.error}}</span>
  <el-button v-if="task.status==='complete'" link type="primary" @click="download(task)">下载文件</el-button>
  <el-button v-if="task.status==='failed'" link @click="open(task.job,true)">重新导出</el-button>
  <el-button v-if="task.status!=='running'" link @click="exports=exports.filter(t=>t.key!==task.key)">关闭</el-button>
 </div>
</div>
<el-dialog v-model="visible" :title="label+(intent?' · 下载日明细':' · 日账单明细')" width="min(1280px,96vw)" class="daily-detail-dialog">
 <div v-if="preparation" class="detail-preparing"><span>{{preparation.status==='failed'?'明细准备失败':'正在整理已有账单明细'}}</span><el-progress v-if="preparation.status!=='failed'" :percentage="percent(preparation)"/><el-button v-else @click="load(true)">重试</el-button></div>
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <template v-if="ready">
 <div class="detail-filters">
  <el-date-picker v-model="range" type="datetimerange" format="MM-DD HH:mm" value-format="YYYY-MM-DD HH:mm:ss" start-placeholder="开始时间" end-placeholder="结束时间"/>
  <el-select v-model="model" filterable clearable placeholder="全部模型"><el-option v-for="v in models" :key="v" :label="v" :value="v"/></el-select>
  <el-select v-model="token" filterable clearable placeholder="全部令牌"><el-option v-for="v in tokens" :key="v" :label="v" :value="v"/></el-select>
  <el-button type="primary" :loading="loading" @click="search">查询</el-button>
 </div>
 <p class="download-hint">单个文件直接下载 Excel，多个分片文件自动打包 ZIP。</p><div class="detail-toolbar"><span>当日共 {{total.toLocaleString()}} 条 · {{currency}}</span><div><el-button :loading="exporting" @click="startExport(false)">按条件导出</el-button><el-button type="primary" :loading="exporting" @click="startExport(true)">下载全部明细</el-button></div></div>
 <div v-if="exports.some(t=>t.jobId===job?.id)" class="detail-export-tasks" aria-label="明细导出任务">
  <div v-for="task in exports.filter(t=>t.jobId===job?.id)" :key="task.key" class="detail-export-task">
   <span>{{task.label}}</span>
   <template v-if="task.status==='running'"><el-progress :percentage="percent(task)" :stroke-width="5"/><span>已处理 {{task.processed.toLocaleString()}} / {{task.total.toLocaleString()}}</span></template>
   <template v-else-if="task.status==='complete'"><span>{{task.matched.toLocaleString()}} 条 · 文件已就绪</span><el-button link type="primary" @click="download(task)">下载文件</el-button></template>
   <template v-else><span class="failed">{{task.error}}</span><span>请重新导出</span></template>
  </div>
 </div>
 <el-table v-loading="loading" :data="rows" size="small" max-height="480">
  <el-table-column prop="time" label="时间" width="164"/><el-table-column prop="request_id" label="请求 ID" min-width="180" show-overflow-tooltip/>
  <el-table-column prop="model" label="模型" min-width="165" show-overflow-tooltip/><el-table-column label="令牌" min-width="150" show-overflow-tooltip><template #default="s">{{s.row.token}}<small v-if="s.row.token_id&&s.row.token_id!=='0'"> #{{s.row.token_id}}</small></template></el-table-column>
  <el-table-column prop="input" label="普通输入" width="85" align="right"/><el-table-column prop="output" label="普通输出" width="85" align="right"/>

  <el-table-column prop="cache_read" label="缓存读" width="85" align="right"/><el-table-column prop="cache_write" label="缓存写" width="85" align="right"/>
  <el-table-column v-for="col in [{key:'image_input_tokens',label:'图像输入'},{key:'image_output_tokens',label:'图像输出'},{key:'audio_input_tokens',label:'音频输入'},{key:'audio_output_tokens',label:'音频输出'}]" :key="col.key" :label="col.label" width="95" align="right"><template #default="s">{{s.row[col.key]!==undefined&&s.row[col.key]!==''?Number(s.row[col.key]).toLocaleString('zh-CN'):'0'}}</template></el-table-column>
  <el-table-column :label="`模型单价（${currency}/百万 Token；按次另标）`" min-width="320"><template #default="s">{{s.row.unit_price||'未记录'}}</template></el-table-column>
  <el-table-column label="原价金额" min-width="115" align="right"><template #default="s">{{s.row.before_amount?Number(s.row.before_amount).toLocaleString('zh-CN',{maximumFractionDigits:6}):'—'}}</template></el-table-column><el-table-column label="折扣" min-width="90"><template #default="s">{{formatBillingDiscount(s.row.discount)}}</template></el-table-column><el-table-column label="折后金额" min-width="115" align="right"><template #default="s">{{Number(s.row.amount).toLocaleString('zh-CN',{minimumFractionDigits:2,maximumFractionDigits:6})}}</template></el-table-column>
 </el-table>
 <div class="detail-pagination"><span>{{filtered?'筛选结果 · ':''}}第 {{page+1}} 页 · 本页 {{rows.length}} 条</span><el-button :disabled="page===0||loading" @click="changePage(false)">上一页</el-button><el-button :disabled="!next||loading" @click="changePage(true)">下一页</el-button></div>
 </template>
 <el-skeleton v-else-if="!preparation&&!error" :rows="4" animated/>
</el-dialog>
</template>
<style scoped>
.download-hint{font-size:12px;color:var(--el-text-color-secondary);margin:0 0 12px}
.detail-export-tasks{display:grid;gap:8px;margin:10px 0}.detail-export-task{display:flex;align-items:center;gap:12px;padding:10px 14px;border:1px solid var(--el-border-color-light);border-radius:8px;font-size:12px;background:var(--el-fill-color-lighter)}.detail-export-task .el-progress{width:160px}.failed{color:var(--el-color-danger)}.detail-filters{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:12px}.detail-filters :deep(.el-date-editor){max-width:360px;flex:1;min-width:280px}.detail-filters .el-select{width:190px}.detail-toolbar{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:12px;color:var(--el-text-color-secondary);font-size:12px}.detail-pagination{display:flex;gap:8px;align-items:center;justify-content:flex-end;margin-top:12px}.detail-pagination>span{margin-right:auto;color:var(--el-text-color-secondary);font-size:12px}.detail-preparing{padding:20px 0;display:grid;gap:12px}small{color:var(--el-text-color-secondary)}@media(max-width:700px){.detail-toolbar,.detail-export-task{flex-wrap:wrap}.detail-filters .el-select{flex:1;min-width:140px}}
</style>
