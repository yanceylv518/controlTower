<script setup lang="ts">
import {computed,onBeforeUnmount,ref,watch} from 'vue';
import {ElMessage,ElMessageBox} from 'element-plus';
import type {BillingGenerationBatch,BillingGenerationProgress} from '@ct/shared';
import {dashboard,passthrough} from '../api';
import {billingTaskErrorMessage} from '../utils/httpError';
type Subject={id:number;name:string;role?:number};
const props=defineProps<{site:string;kind?:string;month:string;selected?:number;subjects:Subject[];busy?:boolean}>();
const statementKind=computed(()=>props.kind||'user_statement');
const subjectLabel=computed(()=>statementKind.value==='upstream_statement'?'上游':'用户');
const unit=computed(()=>statementKind.value==='upstream_statement'?'个':'位');
const emit=defineEmits<{submitted:[month:string]}>();
const dialog=ref(false),progress=ref(false),draftMonth=ref(''),selectedUsers=ref<number[]>([]),saving=ref(false),searching=ref(false),queryResults=ref<Subject[]>([]),names=ref<Record<number,string>>({});
const excludeAdmins=ref(true),searchKeyword=ref(''),roles=ref<Record<number,number|undefined>>({});
const canSelect=(id:number)=>statementKind.value==='upstream_statement'||!excludeAdmins.value||(roles.value[id] ?? 10) < 10;
function filterAdmins(){selectedUsers.value=selectedUsers.value.filter(canSelect);void searchUsers(searchKeyword.value);}
const recovered=ref<{instance_id:string;kind:string;subject_id:number;from:string;to:string}[]>([]);
const otherBatches=computed(()=>{const groups=new Map<string,{month:string;from:string;count:number}>();for(const t of recovered.value){if(t.kind!==statementKind.value||t.from===batch.value?.from)continue;const g=groups.get(t.from)||{from:t.from,month:new Date(t.from).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'}).slice(0,7),count:0};g.count++;groups.set(t.from,g);}return [...groups.values()];});
function viewOther(from:string){batch.value=undefined;items.value=[];restore([...recovered.value.filter(t=>t.from===from),...recovered.value.filter(t=>t.from!==from)]);progress.value=true;}
const excludeZeroOutput=ref(false);
const overwrite=ref(false),cancelling=ref(false);
const batch=ref<BillingGenerationBatch>(),batchMonth=ref(''),items=ref<BillingGenerationProgress[]>([]),refreshing=ref(false),error=ref('');
let searchRevision=0,progressRevision=0,disposed=false;
const options=computed(()=>{const found=new Map(queryResults.value.map(u=>[u.id,u]));for(const id of selectedUsers.value){if(!found.has(id))found.set(id,{id,name:names.value[id]||`${subjectLabel.value} #${id}`});}return [...found.values()].filter(u=>canSelect(u.id));});
const selectionPage=ref(1),previewLoading=ref(false),previewError=ref(''),preview=ref<Record<number,BillingGenerationProgress>>({});
let previewRevision=0,previewController:AbortController|undefined;
const matchingOptions=computed(()=>queryResults.value.filter(u=>canSelect(u.id)));
const visibleOptions=computed(()=>matchingOptions.value.slice((selectionPage.value-1)*20,selectionPage.value*20));
const businessToday=()=>new Date().toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'});
const validDraftMonth=computed(()=>/^\d{4}-(0[1-9]|1[0-2])$/.test(draftMonth.value)&&`${draftMonth.value}-01`<businessToday());
const disabledMonth=(date:Date)=>`${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-01`>=businessToday();
function selectionStatus(id:number){
 const v=preview.value[id];
 if(!v)return previewLoading.value?'查询中':'状态未知';
 if(!v.total_days)return '暂无完整日账期';
 if(v.failed||v.error||v.monthly?.status==='failed')return v.complete?'部分失败':'生成失败';
 if(v.running||['running','publishing'].includes(v.monthly?.status||''))return '生成中';
 if(v.days?.some(d=>d.status==='pending'&&d.job_id)||v.monthly?.status==='pending')return '排队中';
 if(v.pending)return '待补齐';
 if(v.outcome==='cancelled')return '已取消';
 if(v.complete+v.empty>=v.total_days){
  if(v.empty===v.total_days)return '无消费，无需出账';
  return v.monthly?.status==='complete'?'已生成':'日账单已齐，待汇总';
 }
 return v.complete?'部分已生成':v.empty?'部分无消费':'未生成 / 未检查';
}
function monthlyStatus(id:number){const v=preview.value[id];if(!v)return '—';if(v.total_days>0&&v.empty===v.total_days)return '无需出账';return v.monthly?.status==='complete'?'已生成':v.monthly?labels[v.monthly.status]||v.monthly.status:'未生成';}
function toggleSubject(id:number,checked:boolean){if(checked){if(selectedUsers.value.length<50&&!selectedUsers.value.includes(id))selectedUsers.value.push(id);}else selectedUsers.value=selectedUsers.value.filter(v=>v!==id);}
function selectMissing(){for(const u of visibleOptions.value){const v=preview.value[u.id];if(v&&v.total_days>0&&!['已生成','无消费，无需出账','生成中','排队中'].includes(selectionStatus(u.id)))toggleSubject(u.id,true);}}
async function loadPreview(){
 const n=++previewRevision;previewController?.abort();preview.value={};previewError.value='';previewLoading.value=false;
 if(!dialog.value||!validDraftMonth.value||!props.site||searching.value||!visibleOptions.value.length)return;
 const controller=new AbortController();previewController=controller;previewLoading.value=true;
 const request={instance_id:props.site,kind:statementKind.value,subject_ids:visibleOptions.value.map(u=>u.id),...monthRange(draftMonth.value)};
 try{const r=await dashboard.billingGenerationPreview(request,controller.signal);if(n===previewRevision&&!disposed)preview.value=Object.fromEntries(r.items.map(v=>[v.subject_id,v]));}
 catch(e){if(n===previewRevision&&!controller.signal.aborted)previewError.value=billingTaskErrorMessage(e,'账单状态读取失败，请重试');}
 finally{if(n===previewRevision)previewLoading.value=false;}
}
watch([dialog,draftMonth,()=>props.site,statementKind,searching,()=>visibleOptions.value.map(u=>u.id).join(',')],()=>void loadPreview());
watch(draftMonth,(value,old)=>{if(dialog.value&&old&&value!==old)selectedUsers.value=[];selectionPage.value=1;});
const terminal=(s:string)=>['complete','no_consumption','failed','cancelled'].includes(s);
const hasBatch=computed(()=>!!batch.value);
const active=computed(()=>!!batch.value&&(items.value.length===0||items.value.some(v=>!terminal(v.outcome))));
const totals=computed(()=>items.value.reduce((a,v)=>({users:a.users+(['complete','no_consumption'].includes(v.outcome)?1:0),errors:a.errors+(v.outcome==='failed'?1:0),days:a.days+v.total_days,done:a.done+v.complete+v.empty,empty:a.empty+v.empty,pending:a.pending+v.pending,running:a.running+v.running,requests:a.requests+v.processed}),{users:0,errors:0,days:0,done:0,empty:0,pending:0,running:0,requests:0}));
const succeeded=computed(()=>!!batch.value&&items.value.length===batch.value.subject_ids.length&&items.value.every(v=>['complete','no_consumption'].includes(v.outcome)));
const cancelled=computed(()=>items.value.some(v=>v.outcome==='cancelled')&&!active.value);
const waitingForReport=computed(()=>active.value&&items.value.some(v=>v.waiting_for==='report'));
const resultLabel=computed(()=>waitingForReport.value?'等待报表完成':cancelled.value?'已取消':succeeded.value?(items.value.every(v=>v.outcome==='no_consumption')?'无消费，无需生成账单':'生成成功'):totals.value.errors?(active.value?'生成中，部分失败':'生成失败'):'正在生成');
const progressStatus=computed(()=>totals.value.errors?'exception':succeeded.value?'success':undefined);
const percentage=computed(()=>{if(succeeded.value)return 100;const months=items.value.filter(v=>v.monthly);const total=totals.value.days+months.length;const done=totals.value.done+months.filter(v=>v.monthly?.status==='complete').length;return total?Math.floor(done/total*100):0;});
const labels:Record<string,string>={cancelled:'已取消',superseded:'已被覆盖',registered:'等待检查',unchecked:'待检查',queued:'等待生成',waiting:'等待生成',pending:'排队中',generating:'生成中',running:'生成中',publishing:'写入账单文件',complete:'已完成',no_data:'无消费，已跳过',no_consumption:'无消费，无需出账',failed:'失败',awaiting_monthly:'等待汇总月账单'};
const local=(v?:string)=>v?new Date(v).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'—';
function monthRange(value:string){const [y,m]=value.split('-').map(Number);const next=m===12?`${y+1}-01`:`${y}-${String(m+1).padStart(2,'0')}`;return {from:`${value}-01T00:00:00+08:00`,to:`${next}-01T00:00:00+08:00`};}
async function searchUsers(keyword='') {searchKeyword.value=keyword;selectionPage.value=1;const n=++searchRevision;queryResults.value=[];searching.value=true;try{let values:Subject[];if(statementKind.value==='upstream_statement'){const r=await dashboard.billingUpstreams(props.site);values=r.items.map(u=>({id:u.id,name:u.name})).filter(u=>`${u.name} ${u.id}`.toLowerCase().includes(keyword.toLowerCase()));}else{const r=await passthrough.users({site:props.site,keyword,limit:200,exclude_admin:excludeAdmins.value?1:0});values=r.items.map(u=>({id:u.id,name:u.display_name||u.username,role:u.role}));}if(n!==searchRevision||disposed)return;queryResults.value=values;for(const u of queryResults.value){names.value[u.id]=u.name;roles.value[u.id]=u.role;}selectedUsers.value=selectedUsers.value.filter(canSelect);}catch(e){ElMessage.error(billingTaskErrorMessage(e,subjectLabel.value+'加载失败'));}finally{if(n===searchRevision)searching.value=false;}}
function open(){if(active.value||saving.value||props.busy)return;overwrite.value=false;excludeZeroOutput.value=statementKind.value==='upstream_statement';selectionPage.value=1;searchKeyword.value='';draftMonth.value=props.month;for(const u of props.subjects){names.value[u.id]=u.name;roles.value[u.id]=u.role;}selectedUsers.value=props.selected&&canSelect(props.selected)?[props.selected]:[];queryResults.value=[...props.subjects];dialog.value=true;void searchUsers();}
async function submit(){if(active.value||props.busy)return;selectedUsers.value=selectedUsers.value.filter(canSelect);if(!validDraftMonth.value||selectedUsers.value.length===0)return;saving.value=true;try{
 const request={kind:statementKind.value,instance_id:props.site,subject_ids:[...selectedUsers.value],overwrite:overwrite.value,exclude_zero_output:excludeZeroOutput.value,...monthRange(draftMonth.value)};
 const r=await dashboard.generateBillingBatch(request);if(disposed||props.site!==request.instance_id||statementKind.value!==request.kind)return;batch.value=request;batchMonth.value=draftMonth.value;items.value=r.items;error.value='';dialog.value=false;progress.value=true;emit('submitted',draftMonth.value);
 if(r.items.every(v=>v.outcome==='no_consumption'))ElMessage.info(`所选${subjectLabel.value}在该范围内均无消费记录，无需生成账单`);else if(r.items.every(v=>['complete','no_consumption'].includes(v.outcome)))ElMessage.info('所选范围账单已齐全');else ElMessage.success(`已提交 ${request.subject_ids.length} ${unit.value}${subjectLabel.value}的账单生成任务`);
 }catch(e){ElMessage.error(billingTaskErrorMessage(e));}finally{saving.value=false;}}
async function cancelTask(){if(!batch.value||cancelling.value)return;
 try{await ElMessageBox.confirm('取消未完成的任务，已完成的日账单将保留。','取消生成',{confirmButtonText:'取消任务',cancelButtonText:'继续生成',type:'warning'});}catch{return;}
 cancelling.value=true;try{await dashboard.cancelBillingBatch(batch.value);await refresh();ElMessage.success('任务已取消，已完成的日账单已保留');}catch(e){ElMessage.error(billingTaskErrorMessage(e,'取消失败'));}finally{cancelling.value=false;}
}
async function refresh(){if(!batch.value||refreshing.value)return;const n=++progressRevision;refreshing.value=true;try{const r=await dashboard.billingBatchProgress(batch.value);if(n===progressRevision&&!disposed){items.value=r.items;if(batch.value&&r.subject_ids)batch.value.subject_ids=r.subject_ids;error.value='';}}catch(e){if(n===progressRevision)error.value=billingTaskErrorMessage(e,'进度读取失败');}finally{if(n===progressRevision)refreshing.value=false;}}
watch(()=>[props.site,props.kind],()=>{searchRevision++;progressRevision++;refreshing.value=false;dialog.value=false;progress.value=false;batch.value=undefined;items.value=[];names.value={};roles.value={};selectedUsers.value=[];queryResults.value=[];searchKeyword.value='';searching.value=false;recovered.value=[];});
const timer=setInterval(()=>{if(active.value||progress.value)void refresh();},4000);
onBeforeUnmount(()=>{disposed=true;searchRevision++;progressRevision++;previewRevision++;previewController?.abort();clearInterval(timer);});
function restore(targets:{instance_id:string;kind:string;subject_id:number;from:string;to:string}[]){
 recovered.value=targets;
 if(active.value||saving.value)return;
 const first=targets.find(t=>t.kind===statementKind.value);if(!first)return;
 batch.value={kind:first.kind,instance_id:first.instance_id,from:first.from,to:first.to,subject_ids:targets.filter(t=>t.kind===first.kind&&t.from===first.from&&t.to===first.to).map(t=>t.subject_id).slice(0,50)};
 batchMonth.value=new Date(first.from).toLocaleDateString('sv-SE',{timeZone:'Asia/Shanghai'}).slice(0,7);
 for(const u of props.subjects)names.value[u.id]=u.name;
 void refresh();
}
watch(()=>props.subjects,users=>{for(const u of users)names.value[u.id]=u.name;});
defineExpose({open,active,hasBatch,saving,restore});
</script>
<template>
<div v-if="batch" class="batch-status" :class="{'is-success':succeeded,'is-failed':totals.errors}"><button @click="progress=true;refresh()"><span>{{batchMonth}} · {{batch.subject_ids.length}} {{unit}}{{subjectLabel}}</span><span><strong>{{resultLabel}}</strong> · 已完成 {{totals.users}} {{unit}}<span v-if="totals.errors"> · 失败 {{totals.errors}} {{unit}}</span></span><el-progress :percentage="percentage" :stroke-width="5" :status="progressStatus"/><b>{{active?'查看生成进度':'查看生成结果'}} →</b></button><el-button v-if="active" link type="danger" :loading="cancelling" @click="cancelTask">取消任务</el-button><el-button v-if="!active" link @click="batch=undefined;progress=false">关闭</el-button></div>
<div v-if="otherBatches.length" class="other-batches"><el-button v-for="b in otherBatches" :key="b.from" link type="primary" @click="viewOther(b.from)">{{b.month}} · {{b.count}} {{unit}}{{subjectLabel}} · 查看生成进度</el-button></div>
<el-dialog v-model="dialog" class="billing-generation-dialog" top="5vh" title="生成账单" width="min(920px,95vw)" :close-on-click-modal="!saving" :close-on-press-escape="!saving" :show-close="!saving">
 <el-form label-position="top">
  <div class="generation-controls">
   <el-form-item label="账单月份"><el-date-picker v-model="draftMonth" type="month" value-format="YYYY-MM" :clearable="false" :disabled="saving" :disabled-date="disabledMonth" style="width:170px"/></el-form-item>
   <el-form-item :label="`搜索${subjectLabel}`"><el-input v-model="searchKeyword" :disabled="saving" clearable :placeholder="`${subjectLabel}名称或 ID，回车搜索`" @keyup.enter="searchUsers(searchKeyword)" @clear="searchUsers('')"><template #append><el-button :disabled="saving" @click="searchUsers(searchKeyword)">搜索</el-button></template></el-input></el-form-item>
  </div>
  <div class="generation-selection-tools"><el-checkbox v-if="statementKind==='user_statement'" v-model="excludeAdmins" :disabled="saving" @change="filterAdmins">过滤管理员及超级管理员</el-checkbox><span>已选 {{selectedUsers.length}} / 50 {{unit}}</span><el-button link :disabled="saving||previewLoading||searching||!!previewError" @click="selectMissing">选择本页待生成</el-button><el-button link :disabled="saving||!selectedUsers.length" @click="selectedUsers=[]">清空选择</el-button><el-button link :disabled="saving||searching" :loading="previewLoading" @click="loadPreview">刷新状态</el-button></div>
  <p class="generation-note">按所选月份查看；当月只统计截至昨日的完整日期。默认补齐缺失账单，临时账单不计入日 / 月账单覆盖。</p>
  <el-alert v-if="previewError" :title="previewError" type="error" :closable="false"/>
  <el-table v-loading="searching||previewLoading" :data="visibleOptions" row-key="id" max-height="min(340px,35vh)" :empty-text="searching?'正在搜索':`没有匹配的${subjectLabel}`">
   <el-table-column width="48"><template #default="s"><el-checkbox :model-value="selectedUsers.includes(s.row.id)" :aria-label="`选择 ${s.row.name}`" :disabled="saving||(!selectedUsers.includes(s.row.id)&&selectedUsers.length>=50)" @change="toggleSubject(s.row.id,!!$event)"/></template></el-table-column>
   <el-table-column :label="subjectLabel" min-width="165"><template #default="s"><b>{{s.row.name}}</b><small class="id">#{{s.row.id}}</small></template></el-table-column>
   <el-table-column label="生成情况" min-width="165"><template #default="s"><el-tag :type="selectionStatus(s.row.id).includes('失败')?'danger':selectionStatus(s.row.id)==='已生成'?'success':selectionStatus(s.row.id)==='部分已生成'?'warning':'info'" size="small">{{selectionStatus(s.row.id)}}</el-tag></template></el-table-column>
   <el-table-column label="日账单" min-width="160"><template #default="s"><span v-if="preview[s.row.id]">已生成 {{preview[s.row.id].complete}} / {{preview[s.row.id].total_days}} 天<small class="id">无消费 {{preview[s.row.id].empty}} 天 · 失败 {{preview[s.row.id].failed}} 天</small></span><span v-else>—</span></template></el-table-column>
   <el-table-column label="月账单" min-width="140"><template #default="s">{{monthlyStatus(s.row.id)}}</template></el-table-column>
  </el-table>
  <div class="generation-paging"><small>当前搜索 {{matchingOptions.length}} {{unit}}{{subjectLabel}}<span v-if="statementKind==='user_statement'&&queryResults.length>=200">（最多展示 200 条，请搜索缩小范围）</span></small><el-pagination v-model:current-page="selectionPage" :disabled="saving" :page-size="20" :total="matchingOptions.length" layout="prev,pager,next"/></div>
  <div v-if="selectedUsers.length" class="generation-picked"><el-tag v-for="id in selectedUsers" :key="id" :closable="!saving" @close="toggleSubject(id,false)">{{names[id]||subjectLabel}} · #{{id}}</el-tag></div>
  <el-checkbox v-model="excludeZeroOutput" :disabled="saving">排除输出为 0 的请求</el-checkbox>
  <el-checkbox v-model="overwrite" :disabled="saving">覆盖已有账单</el-checkbox><p class="generation-note">{{overwrite?(statementKind==='upstream_statement'?'按当前渠道归属、已配置的账期折扣和金额配置重算所选月份。漏渠道可用此方式补齐，原有渠道金额也可能变化；新月账完整生成后替代旧月账。':'重新生成所选月份的账单，新月账完整生成后替代旧月账。'):'已完成的日账单会复用，只补齐缺失日期；修复已生成账单漏渠道时，请勾选覆盖已有账单。'}}</p>
 </el-form>
 <template #footer><el-button :disabled="saving" @click="dialog=false">取消</el-button><el-button type="primary" :loading="saving" :disabled="active||busy||!selectedUsers.length||!validDraftMonth" @click="submit">{{overwrite?'覆盖生成':'补齐并生成'}}{{selectedUsers.length?'（'+selectedUsers.length+' '+unit+subjectLabel+'）':''}}</el-button></template>
</el-dialog>
<el-dialog v-model="progress" :title="batchMonth+(active?' · 账单生成进度':' · 账单生成结果')" width="min(1180px,96vw)">
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <el-alert v-if="waitingForReport" title="等待本站点报表完成后生成账单，其他站点独立排队。" type="info" :closable="false"/><div class="batch-metrics"><div><small>已完成{{subjectLabel}}</small><b>{{totals.users}} / {{batch?.subject_ids.length||0}}</b><span v-if="totals.errors">失败 {{totals.errors}} {{unit}}</span></div><div><small>已处理日账期</small><b>{{totals.done}} / {{totals.days}}</b><span>无消费跳过 {{totals.empty}} 个</span></div><div><small>排队 / 执行中</small><b>{{totals.pending}} / {{totals.running}}</b></div><div><small>已处理请求</small><b>{{totals.requests.toLocaleString()}}</b></div></div>
 <div class="batch-overall"><el-progress :percentage="percentage" :status="progressStatus"/><el-button v-if="active" type="danger" plain :loading="cancelling" @click="cancelTask">取消任务</el-button><el-button :loading="refreshing" @click="refresh">刷新进度</el-button></div>
 <el-table :data="items" row-key="subject_id" size="small" max-height="530" empty-text="正在读取进度">
  <el-table-column type="expand"><template #default="s"><div class="batch-days"><el-alert v-if="s.row.error" :title="s.row.error" type="error" :closable="false"/><div v-if="s.row.monthly" class="monthly-status">月账单：{{labels[s.row.monthly.status]||s.row.monthly.status}}<span v-if="s.row.monthly.error"> · {{s.row.monthly.error}}</span></div>
   <el-table :data="s.row.days" size="small" max-height="380"><el-table-column prop="day" label="日期" width="120"/><el-table-column label="阶段" width="150"><template #default="d">{{labels[d.row.status]||d.row.status}}</template></el-table-column><el-table-column label="已处理请求" width="130" align="right"><template #default="d">{{d.row.processed.toLocaleString()}}</template></el-table-column><el-table-column label="更新时间" width="190"><template #default="d">{{local(d.row.updated_at)}}</template></el-table-column><el-table-column prop="error" label="失败原因" min-width="220" show-overflow-tooltip/></el-table>
  </div></template></el-table-column>
  <el-table-column :label="subjectLabel" min-width="180"><template #default="s"><b>{{names[s.row.subject_id]||subjectLabel}}</b><small class="id">#{{s.row.subject_id}}</small></template></el-table-column>
  <el-table-column label="当前状态" min-width="155"><template #default="s"><el-tag :type="s.row.outcome==='failed'?'danger':['complete','no_consumption'].includes(s.row.outcome)?'success':'primary'" size="small">{{s.row.waiting_for==='report'?'等待报表完成':labels[s.row.outcome]||s.row.outcome}}</el-tag></template></el-table-column>
  <el-table-column label="已检查日期" width="115"><template #default="s">{{s.row.checked}} / {{s.row.total_days}}</template></el-table-column>
  <el-table-column label="已生成 / 无消费" width="135"><template #default="s">{{s.row.complete}} / {{s.row.empty}}</template></el-table-column>
  <el-table-column label="排队 / 执行 / 失败" width="155"><template #default="s">{{s.row.pending}} / {{s.row.running}} / {{s.row.failed}}</template></el-table-column>
  <el-table-column label="已处理请求" width="130" align="right"><template #default="s">{{s.row.processed.toLocaleString()}}</template></el-table-column>
  <el-table-column label="最近检查" width="180"><template #default="s">{{local(s.row.last_attempt)}}</template></el-table-column>
 </el-table>
</el-dialog>
</template>
<style scoped>
:global(.billing-generation-dialog .el-dialog__body){max-height:calc(90vh - 125px);overflow:auto}.generation-controls{display:flex;gap:20px;flex-wrap:wrap}.generation-controls .el-form-item:last-child{flex:1;min-width:220px}.generation-selection-tools{display:flex;align-items:center;gap:12px;flex-wrap:wrap}.generation-selection-tools>span{margin-left:auto}.generation-note,.generation-paging{font-size:12px;color:var(--el-text-color-secondary)}.generation-note{line-height:1.6;margin:10px 0}.generation-paging{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-top:10px;flex-wrap:wrap}.generation-picked{display:flex;flex-wrap:wrap;gap:6px;max-height:80px;overflow:auto;margin:12px 0}

.admin-filter-note{margin-left:12px;font-size:12px;color:var(--el-text-color-secondary)}
.batch-status.is-success>button:first-child{border-color:var(--el-color-success-light-7);background:var(--el-color-success-light-9);color:var(--el-color-success)}.batch-status.is-failed>button:first-child{border-color:var(--el-color-danger-light-7);background:var(--el-color-danger-light-9);color:var(--el-color-danger)}
.batch-status{display:flex;align-items:center;gap:8px;margin:10px 0}.batch-status>button:first-child{display:flex;flex:1;min-width:0;align-items:center;gap:14px;border:1px solid var(--el-color-primary-light-7);background:var(--el-color-primary-light-9);color:var(--el-color-primary);border-radius:8px;padding:11px 14px;cursor:pointer;text-align:left}.batch-status .el-progress{width:180px}.batch-status b{margin-left:auto;font-size:12px;font-weight:500}.batch-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin-bottom:16px}.batch-metrics>div{padding:12px 14px;border:1px solid var(--el-border-color-light);border-radius:8px;display:grid;gap:7px}.batch-metrics small,.batch-metrics span,.id{font-size:12px;color:var(--el-text-color-secondary)}.batch-metrics b{font-size:22px;font-variant-numeric:tabular-nums}.batch-overall{display:flex;gap:18px;align-items:center;margin-bottom:14px}.batch-overall .el-progress{flex:1}.batch-days{padding:12px 18px;background:var(--el-fill-color-lighter)}.monthly-status{padding:8px 0}.id{display:block;margin-top:3px}@media(max-width:700px){.batch-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.batch-status>button:first-child{flex-wrap:wrap}.batch-status .el-progress{flex:1}.batch-days{padding:8px}}
</style>
