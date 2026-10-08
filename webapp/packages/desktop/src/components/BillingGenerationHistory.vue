<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue';
import type {BillingGenerationTask} from '@ct/shared';
import {dashboard} from '../api';
import {billingTaskErrorMessage} from '../utils/httpError';
const props=defineProps<{site:string;kind?:string;subjects:{id:number;name:string}[]}>();
const subjectLabel=computed(()=>props.kind==='upstream_statement'?'上游':'用户');
const unit=computed(()=>props.kind==='upstream_statement'?'个':'位');
const visible=ref(false),details=ref(false),loading=ref(false),detailLoading=ref(false),error=ref(''),detailError=ref(''),page=ref(1),total=ref(0),items=ref<BillingGenerationTask[]>([]),selected=ref<BillingGenerationTask>();
let revision=0,detailRevision=0;
const names=computed(()=>new Map(props.subjects.map(u=>[u.id,u.name])));
const label:Record<string,string>={complete:'生成成功',no_consumption:'无消费，已跳过',generating:'生成中',registered:'等待检查',queued:'排队中',pending:'排队中',waiting:'等待生成',running:'生成中',publishing:'写入文件',failed:'失败',cancelled:'已取消',unchecked:'待检查',no_data:'无消费，已跳过',superseded:'已被覆盖',awaiting_monthly:'汇总月账单'};
const local=(v?:string)=>v?new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'—';
const date=(v:string)=>new Date(v).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'});
const range=(v:BillingGenerationTask)=>v.source==='manual'||v.source==='legacy'?date(v.from).slice(0,7):v.source==='temporary'?`${local(v.from)} — ${local(v.to)}`:`${date(v.from)} — ${date(new Date(new Date(v.to).getTime()-1).toISOString())}`;
const kind=(v:BillingGenerationTask)=>v.source==='temporary'?'临时账单':v.source==='automatic'?'自动生成':v.overwrite?'覆盖生成':'生成账单';
const type=(s:string)=>s==='failed'?'danger':s==='generating'?'primary':s==='cancelled'?'info':'success';
async function load(){const n=++revision;loading.value=true;try{const r=await dashboard.billingGenerationHistory(props.site,page.value,props.kind);if(n===revision){items.value=r.items;total.value=r.total;error.value='';}}catch(e){if(n===revision)error.value=billingTaskErrorMessage(e,'生成记录加载失败');}finally{if(n===revision)loading.value=false;}}
function open(){visible.value=true;page.value=1;void load();}
async function view(task:BillingGenerationTask){selected.value=task;details.value=true;detailError.value='';const n=++detailRevision;detailLoading.value=true;try{const r=await dashboard.billingGenerationHistoryDetail(props.site,task.id,props.kind);if(n===detailRevision)selected.value=r;}catch(e){if(n===detailRevision)detailError.value=billingTaskErrorMessage(e,'任务详情加载失败');}finally{if(n===detailRevision)detailLoading.value=false;}}
watch(()=>[props.site,props.kind],()=>{revision++;detailRevision++;visible.value=false;details.value=false;items.value=[];selected.value=undefined;});
const timer=setInterval(()=>{if(visible.value&&!loading.value&&items.value.some(t=>t.outcome==='generating'))void load();if(details.value&&selected.value?.outcome==='generating'&&!detailLoading.value)void view(selected.value);},5000);
onBeforeUnmount(()=>{revision++;detailRevision++;clearInterval(timer);});
defineExpose({open});
</script>
<template>
 <el-dialog v-model="visible" title="生成记录" width="min(1120px,96vw)">
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <el-table v-loading="loading" :data="items" row-key="id" size="small" max-height="570" empty-text="暂无生成任务">
   <el-table-column label="提交时间" width="178"><template #default="s">{{local(s.row.created_at)}}</template></el-table-column>
   <el-table-column label="任务 / 账期" min-width="210"><template #default="s"><b>{{range(s.row)}}</b><small class="task-sub">{{kind(s.row)}} · {{s.row.exclude_zero_output?'排除空输出':'包含空输出'}}</small></template></el-table-column>
   <el-table-column :label="subjectLabel" width="110"><template #default="s">{{s.row.subject_ids.length}} {{unit}}<small class="task-sub">完成 {{s.row.completed_users}} {{unit}}</small></template></el-table-column>
   <el-table-column label="已生成 / 无消费" width="140"><template #default="s">{{s.row.complete_days}} / {{s.row.empty_days}}</template></el-table-column>
   <el-table-column label="进度" width="85"><template #default="s">{{s.row.percentage}}%</template></el-table-column>
   <el-table-column label="状态" width="150"><template #default="s"><el-tag :type="type(s.row.outcome)" size="small">{{label[s.row.outcome]||s.row.outcome}}</el-tag></template></el-table-column>
   <el-table-column label="操作" width="95"><template #default="s"><el-button link type="primary" @click="view(s.row)">查看详情</el-button></template></el-table-column>
  </el-table>
  <div class="history-footer"><el-button :loading="loading" @click="load">刷新</el-button><span>共 {{total}} 个任务</span><el-pagination v-model:current-page="page" :page-size="20" :total="total" layout="prev, pager, next" small @current-change="load"/></div>
 </el-dialog>
 <el-dialog v-model="details" title="任务详情" width="min(1080px,96vw)">
  <el-alert v-if="detailError" :title="detailError" type="error" :closable="false"/>
  <template v-if="selected"><div class="task-summary"><b>{{range(selected)}} · {{kind(selected)}}</b><span>{{selected.subject_ids.length}} {{unit}}{{subjectLabel}}</span><el-tag :type="type(selected.outcome)">{{label[selected.outcome]||selected.outcome}}</el-tag><span>{{selected.percentage}}%</span></div>
   <el-table v-loading="detailLoading" :data="selected.items||[]" row-key="subject_id" size="small" max-height="560" empty-text="暂无任务详情">
    <el-table-column type="expand"><template #default="s"><div class="task-days"><el-alert v-if="s.row.error" :title="s.row.error" type="error" :closable="false"/><p v-if="s.row.monthly">月账单：{{label[s.row.monthly.status]||s.row.monthly.status}} {{s.row.monthly.error||''}}</p><el-table :data="s.row.days" size="small" max-height="340"><el-table-column prop="day" label="日期" width="130"/><el-table-column label="状态" width="160"><template #default="d">{{label[d.row.status]||d.row.status}}</template></el-table-column><el-table-column label="已处理请求" width="130" align="right"><template #default="d">{{d.row.processed.toLocaleString()}}</template></el-table-column><el-table-column label="更新时间" width="185"><template #default="d">{{local(d.row.updated_at)}}</template></el-table-column><el-table-column prop="error" label="失败原因" min-width="180" show-overflow-tooltip/></el-table></div></template></el-table-column>
    <el-table-column :label="subjectLabel" min-width="190"><template #default="s">{{names.get(s.row.subject_id)||subjectLabel}} · #{{s.row.subject_id}}</template></el-table-column>
    <el-table-column label="状态" min-width="160"><template #default="s">{{label[s.row.outcome]||s.row.outcome}}</template></el-table-column>
    <el-table-column label="已生成 / 无消费" width="150"><template #default="s">{{s.row.complete}} / {{s.row.empty}}</template></el-table-column>
    <el-table-column label="排队 / 执行 / 失败" width="160"><template #default="s">{{s.row.pending}} / {{s.row.running}} / {{s.row.failed}}</template></el-table-column>
    <el-table-column label="已处理请求" width="135" align="right"><template #default="s">{{s.row.processed.toLocaleString()}}</template></el-table-column>
   </el-table>
  </template>
 </el-dialog>
</template>
<style scoped>
.task-sub{display:block;margin-top:4px;font-size:12px;color:var(--el-text-color-secondary)}.history-footer{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-top:16px;flex-wrap:wrap}.task-summary{display:flex;align-items:center;gap:16px;flex-wrap:wrap;margin-bottom:16px}.task-days{padding:10px 16px;background:var(--el-fill-color-lighter)}.task-days p{margin:5px 0 12px}.history-footer :deep(.el-pagination){flex-wrap:wrap}
</style>
