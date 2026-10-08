<script setup lang="ts">
import {useBillingDownload} from '../utils/billingDownload';
const {pending:downloading,download:downloadFile}=useBillingDownload();
import {ref,watch,onBeforeUnmount} from 'vue';
import {coverageLabel,coverageStatus,coverageTooltip} from '../utils/billingCoverage';
import type {BillingJob,BillingMonthlyCoverage} from '@ct/shared';
import {client} from '../api';
import {billingTaskErrorMessage} from '../utils/httpError';
type Preview={coverage?:BillingMonthlyCoverage;legacy_mixed_discounts?:boolean;covered_days:number;headers:string[];rows:string[][];totals:string[];total:number;page_size:number;currency:string;numeric_start:number};
const visible=ref(false),loading=ref(false),error=ref(''),job=ref<BillingJob>(),name=ref(''),dimension=ref('month'),page=ref(1),data=ref<Preview>();
let revision=0;
const date=(value?:string)=>value?new Date(value).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'}):'';
async function load(){const id=job.value?.id;if(!id||!visible.value)return;const n=++revision;loading.value=true;error.value='';data.value=undefined;try{const result=await client.request<Preview>(`/api/dashboard/billing/statements/result?id=${encodeURIComponent(id)}&section=monthly&dimension=${dimension.value}&page=${page.value}`);if(n===revision)data.value=result;}catch(e){if(n===revision)error.value=billingTaskErrorMessage(e);}finally{if(n===revision)loading.value=false;}}
function open(value:BillingJob,subject:string){revision++;job.value=value;name.value=subject;dimension.value='month';page.value=1;data.value=undefined;visible.value=true;void load();}
function changeDimension(){page.value=1;void load();}
function download(){if(job.value)void downloadFile(job.value.id,`/api/dashboard/billing/statements/result?id=${encodeURIComponent(job.value.id)}&download=1&export=summary`);}
function format(value:string,index:number){if(!data.value||index<data.value.numeric_start||data.value.headers[index]?.startsWith('模型单价')||index===data.value.headers.length-2||value===''||value==='—')return value;const n=Number(value);return Number.isFinite(n)?n.toLocaleString('zh-CN',{minimumFractionDigits:index>=data.value.headers.length-3?2:0,maximumFractionDigits:6}):value;}
watch(visible,v=>{if(!v)revision++});onBeforeUnmount(()=>revision++);defineExpose({open});
</script>
<template>
<el-drawer v-model="visible" size="96%" class="monthly-bill" destroy-on-close>
 <template #header><div class="heading"><div><h3>{{date(job?.range_from).slice(0,7)}} 月账单 · {{name}}</h3><span>{{job?.instance_id}}</span></div><el-button :loading="!!job&&downloading.includes(job.id)" :disabled="!!job&&downloading.includes(job.id)" @click="download">{{job&&downloading.includes(job.id)?'正在准备':'下载月账单'}}</el-button></div></template>
 <div class="toolbar"><el-radio-group v-model="dimension" @change="changeDimension"><el-radio-button value="month">月统计</el-radio-button><el-radio-button value="daily">每日模型统计</el-radio-button><el-radio-button v-if="job?.job_type==='user_statement'" value="token">每日令牌统计</el-radio-button></el-radio-group><span v-if="data">已汇总 {{data.covered_days}} 天 · 金额 {{data.currency}}</span></div>
 <el-alert v-if="error" :title="error" type="error" :closable="false"><el-button link @click="load">重试</el-button></el-alert>
 <el-tooltip v-if="data" :content="coverageTooltip(data.coverage)" placement="top"><p class="coverage-note">{{coverageLabel(data.coverage)}} · {{coverageStatus(data.coverage)}}</p></el-tooltip>
 <p class="discount-note">同一模型按折扣分行，原价与折扣用量分别统计。</p>
 <el-alert v-if="data?.legacy_mixed_discounts" title="部分旧账单未保存折扣分项。请重新生成对应日账单，再汇总月账单。" type="warning" :closable="false"/>
 <div v-loading="loading" class="table-wrap"><el-table v-if="data" :data="data.rows" size="small" border max-height="calc(100vh - 240px)" show-summary :summary-method="()=>data!.totals.map(format)" empty-text="暂无统计">
 <el-table-column v-for="(header,index) in data.headers" :key="header" :label="header" :min-width="header.startsWith('模型单价')?320:header==='模型'?230:header==='令牌'?180:index>=data.numeric_start?125:115" :fixed="index<2?'left':index>=data.headers.length-3?'right':undefined" :align="index>=data.numeric_start&&!header.startsWith('模型单价')?'right':'left'"><template #default="scope">{{format(scope.row[index],index)}}</template></el-table-column>
 </el-table></div>
 <el-pagination v-if="data" v-model:current-page="page" :page-size="data.page_size" :total="data.total" layout="total, prev, pager, next" @current-change="load"/>
</el-drawer>
</template>
<style scoped>
.discount-note{margin:0 0 12px;font-size:12px;color:var(--el-text-color-secondary)}
.heading{display:flex;align-items:center;justify-content:space-between;gap:20px;width:100%;padding-right:24px}.heading h3{margin:0 0 6px;font-size:19px;color:var(--el-text-color-primary)}.heading span,.toolbar>span{font-size:12px;color:var(--el-text-color-secondary)}.toolbar{display:flex;align-items:center;justify-content:space-between;margin-bottom:14px;gap:10px}.table-wrap{min-height:220px}.el-pagination{justify-content:flex-end;margin-top:14px}:deep(.el-table__footer-wrapper){font-weight:600}.el-alert{margin-bottom:12px}
</style>
