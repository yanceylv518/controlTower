<script setup lang="ts">
import { ref,watch,onBeforeUnmount } from 'vue';
import type { ProbeRound,ProbeQuery } from '@ct/shared';
import { dashboard } from '../api';
import { useFiltersStore } from '../stores/filters';
import { formatTime } from '../utils/format';
import LifelineProbeRecords from './LifelineProbeRecords.vue';
const props=defineProps<{channelId:number;focusedRound?:string}>();
const filters=useFiltersStore(),items=ref<ProbeRound[]>([]),loading=ref(false),error=ref(''),expanded=ref(''),nextCursor=ref(''),cursors=ref(['']),queryWindow=ref<ProbeQuery>();
const statuses:Record<string,string>={pending:'等待执行',delivered:'已下发，等待回执',executing:'任务处理中',succeeded:'任务已完成',failed:'任务失败',expired:'任务已过期'};
let generation=0,abort:AbortController|undefined;
function terminal(r:ProbeRound){return ['succeeded','failed','expired'].includes(r.status);}
async function load(reset=false){abort?.abort();const gen=++generation,controller=new AbortController();abort=controller;if(!filters.site_id)return;
 if(reset||!queryWindow.value){const now=new Date();queryWindow.value={site_id:filters.site_id,channel_id:props.channelId,round_id:props.focusedRound,from:new Date(now.getTime()-(props.focusedRound?365:7)*86400000).toISOString(),to:now.toISOString(),limit:20};cursors.value=[''];items.value=[];nextCursor.value='';expanded.value=props.focusedRound||'';}
 loading.value=true;error.value='';try{const v=await dashboard.probeRounds({...queryWindow.value,cursor:cursors.value.at(-1)},controller.signal);if(gen!==generation)return;items.value=v.items;nextCursor.value=v.next_cursor||'';return true;}catch{if(gen===generation){error.value='恢复批次读取失败，请重试。';return false;}}finally{if(gen===generation)loading.value=false;}
}
async function page(next:boolean){const previous=[...cursors.value];if(next&&nextCursor.value)cursors.value.push(nextCursor.value);else if(!next&&cursors.value.length>1)cursors.value.pop();else return;expanded.value='';if(await load()===false)cursors.value=previous;}
watch(()=>[filters.site_id,props.channelId,props.focusedRound],()=>void load(true),{immediate:true});onBeforeUnmount(()=>{generation++;abort?.abort();});
</script>
<template>
 <div class="rounds"><header><span>{{focusedRound?'指定恢复检测批次':'最近 7 天的恢复检测批次（受站点保留期限制）'}}</span><el-button :loading="loading" @click="load(true)">刷新批次</el-button></header>
 <el-alert title="单次探针成功、批次任务完成、渠道实际恢复是不同结果。检测完成后按规则评估，通过后才尝试开启，以渠道状态确认结果为准。" type="info" :closable="false"/>
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <section v-for="r in items" :key="r.id" class="round"><header><div><strong>{{statuses[r.status]||r.status}}</strong><small>{{formatTime(r.created_at)}} · {{r.model}}</small></div><el-button :aria-expanded="expanded===r.id" @click="expanded=expanded===r.id?'':r.id">{{expanded===r.id?'收起请求':'查看请求'}}</el-button></header>
  <p class="muted">批次 {{r.id}} · 状态更新 {{formatTime(r.updated_at)}}</p>
  <template v-if="r.metrics"><p>计划 {{r.planned}} 次 · 已发起 {{r.metrics.sent}} 次 · 已完成 {{r.metrics.sent-r.metrics.pending-r.metrics.unconfirmed}} 次</p><p>成功 {{r.metrics.success}} · 错误 {{r.metrics.errors}} · 慢 {{r.metrics.slow}} · 执行中 {{r.metrics.pending}} · 结果待确认 {{r.metrics.unconfirmed}}</p><el-progress :percentage="Math.min(100,Math.round((r.metrics.sent-r.metrics.pending-r.metrics.unconfirmed)/Math.max(1,r.planned)*100))" :show-text="false"/><p class="muted">{{Math.max(0,r.planned-r.metrics.sent)}} 次{{terminal(r)?'未执行（批次已结束）':'尚未发起'}}，不计为漏测。控制异常 {{r.metrics.unknown}} · 取消 {{r.metrics.canceled}}。</p></template>
  <p v-else>计划 {{r.planned}} 次 · 尚无可关联的逐条记录<span v-if="r.reported_attempts!=null">；任务报告完成 {{r.reported_attempts}} 次、成功 {{r.reported_successes??'未知'}} 次</span>。</p>
  <p v-if="!r.metrics" class="muted">可能是尚未开始、Agent 任务或旧版记录；不据此判断没有发送，也不补造进度。</p>
  <LifelineProbeRecords v-if="expanded===r.id&&r.metrics" :channel-id="channelId" :round-id="r.id" :from="r.created_at" :to="new Date().toISOString()"/>
 </section>
 <el-empty v-if="!items.length&&!loading&&!error" description="此范围内没有恢复检测批次"/>
 <div class="pager"><el-button :disabled="cursors.length===1||loading" @click="page(false)">上一页</el-button><span>第 {{cursors.length}} 页</span><el-button :disabled="!nextCursor||loading" @click="page(true)">下一页</el-button></div>
 </div>
</template>
<style scoped>
.rounds{display:grid;gap:14px}header{display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap}.round{border:1px solid var(--ct-line);border-radius:8px;padding:16px;min-width:0}.muted,small{color:var(--ct-ink-3);font-size:12px;overflow-wrap:anywhere}small{display:block;margin-top:5px}.pager{display:flex;justify-content:flex-end;align-items:center;gap:10px}
</style>
