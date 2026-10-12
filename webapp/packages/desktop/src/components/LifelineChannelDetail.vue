<script setup lang="ts">
import { ref,watch,onBeforeUnmount } from 'vue';
import type { LifelineChannel,ChannelLifeEvent,ProbeQuery } from '@ct/shared';
import { dashboard } from '../api';
import { useFiltersStore } from '../stores/filters';
import { formatTime } from '../utils/format';
import { lifeTitle,lifeChanges,probeOutcomes,outcomeTone } from '../utils/channelLifeline';
import { lifelineStates,lifelineSwitches } from '../utils/lifelineCurrent';
import LifelineProbeRecords from './LifelineProbeRecords.vue';
import LifelineRounds from './LifelineRounds.vue';
const props=defineProps<{channel:LifelineChannel;initialTab?:string;roundId?:string}>();
const filters=useFiltersStore(),tab=ref(props.initialTab||'records'),round=ref(props.roundId||''),events=ref<ChannelLifeEvent[]>([]),loading=ref(false),error=ref(''),cursors=ref(['']),nextCursor=ref(''),days=ref(1),applied=ref<ProbeQuery>();
let abort:AbortController|undefined,generation=0;
async function loadEvents(reset=false){abort?.abort();const gen=++generation,controller=new AbortController();abort=controller;if(!filters.site_id)return;if(reset||!applied.value){const now=new Date();applied.value={site_id:filters.site_id,channel_id:props.channel.channel_id,from:new Date(now.getTime()-days.value*86400000).toISOString(),to:now.toISOString(),limit:50};cursors.value=[''];nextCursor.value='';events.value=[];}
 loading.value=true;error.value='';try{const v=await dashboard.channelLifeline({...applied.value,cursor:cursors.value.at(-1),source:'changes'},controller.signal);if(gen!==generation)return;events.value=v.items;nextCursor.value=v.next_cursor||'';return true;}catch{if(gen===generation){error.value='状态变更读取失败，请重试。';return false;}}finally{if(gen===generation)loading.value=false;}
}
async function page(next:boolean){const previous=[...cursors.value];if(next&&nextCursor.value)cursors.value.push(nextCursor.value);else if(!next&&cursors.value.length>1)cursors.value.pop();else return;if(await loadEvents()===false)cursors.value=previous;}
function openRound(_channel:number,id:string){round.value=id;tab.value='rounds';}
watch(tab,()=>{if(tab.value==='events')void loadEvents(true);});
watch(()=>[filters.site_id,props.channel.channel_id,props.initialTab,props.roundId],()=>{abort?.abort();generation++;events.value=[];applied.value=undefined;tab.value=props.initialTab||'records';round.value=props.roundId||'';if(tab.value==='events')void loadEvents(true);},{immediate:true});
onBeforeUnmount(()=>{generation++;abort?.abort();});
</script>
<template>
 <section class="detail"><header><div><h3>#{{channel.channel_id}} {{channel.channel_name}}</h3><p class="muted">{{channel.models.join(' / ')}} · 优先级 {{channel.priority}} · 线上权重 {{channel.weight}}</p></div><el-tag>{{lifelineSwitches[channel.switch]||channel.switch}} / {{lifelineStates[channel.state]||channel.state}}</el-tag></header>
 <div class="facts"><div><small>当前状态 / 原因</small>{{channel.reason}}</div><div><small>接下来会发生什么</small>{{channel.next}}<p v-if="channel.next_at" class="muted">预计 {{formatTime(channel.next_at)}}</p></div><div><small>最后调度确认</small>{{channel.confirmed_at?formatTime(channel.confirmed_at):'尚无确认'}}<p class="muted">下方为独立历史查询</p></div></div>
 <el-tabs v-model="tab"><el-tab-pane name="records" label="探针发送记录"/><el-tab-pane name="rounds" label="恢复检测批次"/><el-tab-pane name="events" label="状态变更"/></el-tabs>
 <LifelineProbeRecords v-if="tab==='records'" :channel-id="channel.channel_id" @round="openRound"/>
 <template v-else-if="tab==='rounds'"><el-button v-if="round" class="all-rounds" @click="round=''">查看此渠道全部批次</el-button><LifelineRounds :channel-id="channel.channel_id" :focused-round="round"/></template>
 <template v-else><div class="tools"><el-select v-model="days" aria-label="变更记录范围"><el-option label="最近 24 小时" :value="1"/><el-option label="最近 7 天" :value="7"/><el-option label="最近 30 天" :value="30"/></el-select><el-button :loading="loading" @click="loadEvents(true)">查询变更</el-button><el-button :disabled="loading" @click="loadEvents()">刷新当前页</el-button></div><p class="muted" v-if="applied">{{formatTime(applied.from)}} — {{formatTime(applied.to)}} · 受站点保留期限制</p><el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <ol class="timeline" v-loading="loading"><li v-for="e in events" :key="e.id"><small>{{formatTime(e.at)}}</small><strong>{{lifeTitle(e)}}</strong> <span :class="outcomeTone(e.outcome)">{{probeOutcomes[e.outcome]||e.outcome}}</span><p>{{lifeChanges(e).join(' · ')}}</p><small>{{e.actor||'系统'}} · {{e.source==='decision'?'调权决策':'操作记录'}}</small><details><summary>关联标识</summary>{{e.id}}<div v-if="e.command_id">命令：{{e.command_id}}</div></details></li></ol><el-empty v-if="!events.length&&!loading&&!error" description="此范围没有状态变更"/><div class="tools"><el-button :disabled="cursors.length===1||loading" @click="page(false)">上一页</el-button><span>第 {{cursors.length}} 页</span><el-button :disabled="!nextCursor||loading" @click="page(true)">下一页</el-button></div></template>
 </section>
</template>
<style scoped>
.detail{background:var(--ct-surface,var(--el-bg-color));border:1px solid var(--ct-line);border-radius:8px;padding:18px;min-width:0}header,.tools{display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap}h3{margin:0}.facts{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:18px;margin:18px 0}.muted,small,details{color:var(--ct-ink-3);font-size:12px;overflow-wrap:anywhere}small{display:block;margin-bottom:6px}.tools{justify-content:flex-start}.tools .el-select{width:150px}.timeline{list-style:none;padding:0 0 0 16px;border-left:2px solid var(--ct-line);min-height:80px}.timeline li{margin:0 0 20px;overflow-wrap:anywhere}.good{color:var(--el-color-success)}.bad{color:var(--el-color-danger)}.warn{color:var(--el-color-warning)}.all-rounds{margin-bottom:12px}@media(max-width:650px){.facts{grid-template-columns:1fr}.detail{padding:12px}}
</style>
