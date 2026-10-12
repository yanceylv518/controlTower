<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue';
import type { ProbeRecord, ProbeQuery } from '@ct/shared';
import { dashboard } from '../api';
import { useFiltersStore } from '../stores/filters';
import { formatTime } from '../utils/format';
import { probeKinds,probeOutcomes,probeReasons,outcomeTone,seconds } from '../utils/channelLifeline';
const props=defineProps<{channelId?:number;roundId?:string;from?:string;to?:string}>();
const emit=defineEmits<{round:[channel:number,id:string]}>();
const filters=useFiltersStore(), records=ref<ProbeRecord[]>([]),total=ref(0),loading=ref(false),error=ref('');
const hours=ref(1),range=ref<[Date,Date]>(),model=ref(''),kind=ref(''),outcome=ref(''),dispatch=ref(''),search=ref('');
const cursors=ref(['']),nextCursor=ref(''),applied=ref<ProbeQuery>();
let abort:AbortController|undefined,generation=0,disposed=false;
async function load(query=false){
 abort?.abort();const gen=++generation,controller=new AbortController();abort=controller;
 if(!filters.site_id){records.value=[];total.value=0;loading.value=false;return;}
 const site=filters.site_id;
 if(query||!applied.value){const now=new Date(),bounds=hours.value===0?range.value:[new Date(now.getTime()-hours.value*3600000),now];
  if(!props.from&&(!bounds?.[0]||!bounds[1]||bounds[0]>=bounds[1])){error.value='请选择有效时间范围';loading.value=false;return;}
  applied.value={site_id:site,channel_id:props.channelId,round_id:props.roundId,from:props.from||bounds![0].toISOString(),to:props.to||bounds![1].toISOString(),model:model.value,kind:kind.value,outcome:outcome.value,dispatch:dispatch.value,q:search.value.trim(),limit:50};cursors.value=[''];nextCursor.value='';records.value=[];total.value=0;
 }
 loading.value=true;error.value='';
 try{const v=await dashboard.probeRecords({...applied.value,cursor:cursors.value.at(-1)},controller.signal);if(gen!==generation||site!==filters.site_id||disposed)return;records.value=v.items;total.value=v.total;nextCursor.value=v.next_cursor||'';return true;}
 catch{if(gen===generation){error.value='探针记录读取失败，请重试。';return false;}}
 finally{if(gen===generation)loading.value=false;}
}
async function page(next:boolean){const previous=[...cursors.value];if(next&&nextCursor.value)cursors.value.push(nextCursor.value);else if(!next&&cursors.value.length>1)cursors.value.pop();if(await load()===false)cursors.value=previous;}
function reset(){abort?.abort();generation++;records.value=[];total.value=0;error.value='';applied.value=undefined;cursors.value=[''];nextCursor.value='';void load(true);}
watch(()=>[filters.site_id,props.channelId,props.roundId,props.from,props.to],reset,{immediate:true});
onBeforeUnmount(()=>{disposed=true;generation++;abort?.abort();});
defineExpose({refresh:()=>load(true)});
</script>
<template>
 <div class="probe-browser">
  <div class="controls">
   <template v-if="!roundId"><el-select v-model="hours" aria-label="记录时间范围"><el-option label="最近 1 小时" :value="1"/><el-option label="最近 6 小时" :value="6"/><el-option label="最近 24 小时" :value="24"/><el-option label="自定义" :value="0"/></el-select><el-date-picker v-if="hours===0" v-model="range" type="datetimerange" start-placeholder="开始时间" end-placeholder="结束时间"/>
   <el-input v-if="!channelId" v-model="search" placeholder="渠道名称 / ID" aria-label="记录渠道搜索" clearable/>
   <el-input v-model="model" placeholder="完整模型名称" aria-label="记录模型" clearable/><el-select v-model="kind" placeholder="全部探针类型" aria-label="记录类型" clearable><el-option v-for="(label,key) in probeKinds" :key="key" :label="label" :value="key"/></el-select></template>
   <el-select v-model="outcome" placeholder="全部结果" aria-label="记录结果" clearable><el-option v-for="key in ['success','error','slow','pending','skipped','unknown','canceled','unconfirmed']" :key="key" :label="probeOutcomes[key]" :value="key"/></el-select>
   <el-select v-model="dispatch" placeholder="全部调度记录" aria-label="是否发送" clearable><el-option label="仅已发送" value="sent"/><el-option label="仅未发送" value="unsent"/></el-select>
   <el-button type="primary" :loading="loading" @click="load(true)">查询</el-button><el-button :disabled="loading" @click="load()">刷新当前页</el-button>
  </div>
  <p class="muted" v-if="applied">{{formatTime(applied.from)}} — {{formatTime(applied.to)}} · {{total}} 条 · 最新在前</p>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <div v-loading="loading" class="records-table"><table><thead><tr><th>发送 / 应发时间</th><th>渠道 / 类型 / 模型</th><th>结果</th><th>耗时</th><th>说明 / 批次</th></tr></thead><tbody>
   <tr v-for="r in records" :key="r.id"><td data-label="发送 / 应发时间">{{formatTime(r.started_at)}}<small>{{r.outcome==='skipped'?'未发送 · 应发时刻':'已发起请求'}}</small></td><td data-label="渠道 / 类型 / 模型">#{{r.channel_id}} {{r.channel_name}}<small>{{probeKinds[r.kind]||r.kind}} · {{r.model_name}}</small></td><td data-label="结果" :class="outcomeTone(r.outcome)">{{probeOutcomes[r.outcome]||r.outcome}}</td><td data-label="耗时">{{r.outcome==='slow'?'≥ ':''}}{{seconds(r.duration_seconds)}}</td><td data-label="说明 / 批次">{{probeReasons[r.reason]||r.reason||'—'}}<div v-if="r.round_id"><el-button link type="primary" @click="emit('round',r.channel_id,r.round_id)">恢复批次 · {{r.round_id}}</el-button><small>第 {{r.round_index}} 次</small></div><details><summary>记录详情</summary><div>请求 ID：{{r.id}}</div><div>判慢截止：{{formatTime(r.deadline_at)}}</div><div>判定完成：{{r.finished_at?formatTime(r.finished_at):'未确认'}}</div></details></td></tr>
  </tbody></table><el-empty v-if="!records.length&&!loading&&!error" description="没有匹配记录；无记录不等于成功或停止"/></div>
  <div class="pager"><el-button :disabled="cursors.length===1||loading" @click="page(false)">上一页</el-button><span>第 {{cursors.length}} 页 · 每页 50 条</span><el-button :disabled="!nextCursor||loading" @click="page(true)">下一页</el-button></div>
  <p class="muted">未发送仅表示调度尝试，不计入实发；慢请求耗时为判慢阈值。批次关联从新版留痕开始，旧记录不补造。</p>
 </div>
</template>
<style scoped>
.controls{display:flex;gap:10px;flex-wrap:wrap}.controls :deep(.el-select),.controls :deep(.el-input){width:170px}.controls :deep(.el-date-editor){max-width:100%}.muted,small,details{font-size:12px;color:var(--ct-ink-3)}small{display:block;margin-top:4px}table{width:100%;border-collapse:collapse;text-align:left;table-layout:fixed}th{font-weight:500;color:var(--ct-ink-3)}td,th{padding:12px 8px;border-bottom:1px solid var(--ct-line);vertical-align:top;overflow-wrap:anywhere}th:last-child{width:30%}.records-table{min-height:120px}details{margin-top:6px}summary{cursor:pointer}.pager{display:flex;justify-content:flex-end;align-items:center;gap:10px;margin-top:16px}.good{color:var(--el-color-success)}.bad{color:var(--el-color-danger)}.warn{color:var(--el-color-warning)}
@media(max-width:650px){table,tbody,tr,td{display:block;width:100%}thead{display:none}tr{padding:10px 0;border-bottom:1px solid var(--ct-line)}td{border:0;padding:5px 0}td:before{content:attr(data-label);display:block;color:var(--ct-ink-3);font-size:12px}.controls :deep(.el-select),.controls :deep(.el-input){width:100%}.pager{flex-wrap:wrap}}
</style>
