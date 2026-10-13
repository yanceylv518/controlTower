<script setup lang="ts">
import { computed,ref,watch,onBeforeUnmount,nextTick } from 'vue';
import type { LifelineChannel,LifelineCurrent } from '@ct/shared';
import { dashboard } from '../api';
import { useFiltersStore } from '../stores/filters';
import { formatTime } from '../utils/format';
import { probeOutcomes,seconds } from '../utils/channelLifeline';
import { currentScope,currentOrder,isCurrentSnapshotStale,lifelineStates,lifelineSwitches } from '../utils/lifelineCurrent';
import LifelineChannelDetail from './LifelineChannelDetail.vue';
const props=defineProps<{active:boolean}>();
const filters=useFiltersStore(),snapshot=ref<LifelineCurrent>(),loading=ref(false),error=ref(''),search=ref(''),model=ref(''),status=ref(''),state=ref('all'),sort=ref('attention'),page=ref(1),selected=ref(0),detailTab=ref('records'),focusedRound=ref('');
const auto=ref(false),interval=ref(30),clock=ref(Date.now()),order=ref<number[]>([]),panel=ref<HTMLElement>();
let abort:AbortController|undefined,generation=0,disposed=false,timer:ReturnType<typeof setTimeout>|undefined,restoreScroll=0;
const clockTimer=setInterval(()=>clock.value=Date.now(),1000);
const rows=computed(()=>snapshot.value?.channels||[]);
const snapshotStale=computed(()=>isCurrentSnapshotStale(snapshot.value?.queried_at,clock.value));
const emptyMessage=computed(()=>!filters.site_id?'请选择站点':!snapshot.value?'当前状态尚未读取成功，请点击「刷新当前状态」重试':'没有符合条件的渠道');
const models=computed(()=>[...new Set(rows.value.flatMap(r=>r.models))].sort());
const scope=computed(()=>currentScope(rows.value,search.value,model.value,status.value));
const matches=computed(()=>scope.value.filter(r=>state.value==='all'||r.state===state.value).sort((a,b)=>order.value.indexOf(a.channel_id)-order.value.indexOf(b.channel_id)));
const visible=computed(()=>matches.value.slice((page.value-1)*20,page.value*20));
const selection=computed(()=>rows.value.find(r=>r.channel_id===selected.value));
const pause=computed(()=>selected.value?'正在查看渠道详情':page.value>1?'正在浏览分页':!props.active?'正在查看其他视图':'');
function reorder(){order.value=currentOrder(rows.value,'all',sort.value).map(r=>r.channel_id);}
function schedule(){if(timer)clearTimeout(timer);timer=undefined;if(!disposed&&auto.value)timer=setTimeout(()=>{if(!document.hidden&&!pause.value&&!loading.value)void refresh(true);else schedule();},interval.value*1000);}
async function refresh(background=false){
 if(background&&(pause.value||!auto.value||loading.value))return;
 abort?.abort();const gen=++generation,controller=new AbortController();abort=controller;const site=filters.site_id;
 error.value='';if(!site){snapshot.value=undefined;loading.value=false;return;}
 loading.value=true;
 let timedOut=false;
 const deadline=setTimeout(()=>{timedOut=true;controller.abort();},20000);
 let onAbort=()=>{};
 const cancelled=new Promise<never>((_,reject)=>{onAbort=()=>reject(new Error('current request cancelled'));controller.signal.addEventListener('abort',onAbort,{once:true});});
 try{
  const v=await Promise.race([dashboard.lifelineCurrent(site,controller.signal),cancelled]);
  if(gen!==generation||site!==filters.site_id||disposed)return;
  if(background&&(!auto.value||pause.value))return;
  snapshot.value=v;clock.value=Date.now();
  if(!background)reorder();else{const ids=new Set(v.channels.map(r=>r.channel_id));order.value=order.value.filter(id=>ids.has(id));for(const r of v.channels)if(!order.value.includes(r.channel_id))order.value.push(r.channel_id);}
  page.value=Math.min(page.value,Math.max(1,Math.ceil(matches.value.length/20)));
 }catch(cause){
  if(gen===generation&&!disposed&&site===filters.site_id){
   const timeout=timedOut||(cause as {code?:string})?.code==='lifeline_current_timeout';
   error.value=`当前状态${timeout?'读取超时':'读取失败'}，${snapshot.value?'保留上次快照；请刷新重试。':'请刷新重试；尚未取得渠道数量和探测状态。'}`;
  }
 }finally{
  clearTimeout(deadline);controller.signal.removeEventListener('abort',onAbort);
  if(gen===generation&&!disposed){loading.value=false;schedule();}
 }
}
function open(row:LifelineChannel,tab='records'){restoreScroll=window.scrollY;selected.value=row.channel_id;detailTab.value=tab;focusedRound.value=tab==='rounds'?row.round_id||'':'';void nextTick(()=>panel.value?.scrollIntoView({block:'start'}));}
function back(){selected.value=0;focusedRound.value='';void nextTick(()=>window.scrollTo({top:restoreScroll}));schedule();}
watch([search,model,status,state,sort],()=>{page.value=1;reorder();});
watch([auto,interval],schedule);
watch(()=>filters.site_id,()=>{abort?.abort();generation++;snapshot.value=undefined;selected.value=0;page.value=1;search.value='';model.value='';status.value='';state.value='all';order.value=[];void refresh();},{immediate:true});
onBeforeUnmount(()=>{disposed=true;generation++;abort?.abort();clearInterval(clockTimer);if(timer)clearTimeout(timer);});
defineExpose({refresh});
</script>
<template>
 <section ref="panel" class="current-panel">
  <div class="toolbar"><span class="muted">当前快照 {{snapshot?formatTime(snapshot.queried_at):'尚未读取'}}</span><el-button :loading="loading" @click="refresh()">刷新当前状态</el-button><el-checkbox v-model="auto">自动刷新状态</el-checkbox><el-select v-if="auto" v-model="interval" aria-label="当前状态刷新间隔" style="width:115px"><el-option label="每 30 秒" :value="30"/><el-option label="每 60 秒" :value="60"/><el-option label="每 120 秒" :value="120"/></el-select><span class="muted">{{auto?(pause?`已暂停：${pause}`:'后台更新不重排列表'):'手动刷新'}}</span></div>
  <el-alert v-if="snapshotStale" title="页面数据已过期，以下保留上次快照；请刷新当前状态。" type="warning" :closable="false"/>
  <el-alert v-if="error" :title="error" type="error" :closable="false"/>
  <template v-if="selected"><el-button class="back" @click="back">← 返回渠道列表</el-button><LifelineChannelDetail v-if="selection" :key="`${filters.site_id}:${selected}`" :channel="selection" :initial-tab="detailTab" :round-id="focusedRound"/><el-empty v-else description="该渠道已不在当前目录，可到探针记录或历史统计查询"/></template>
  <template v-else>
   <div class="toolbar"><el-input v-model="search" aria-label="当前渠道搜索" placeholder="名称 / ID / 模型" clearable style="width:230px"/><el-select v-model="model" aria-label="当前模型筛选" placeholder="全部模型" filterable clearable><el-option v-for="m in models" :key="m" :label="m" :value="m"/></el-select><el-select v-model="status" aria-label="渠道开关筛选" placeholder="全部开关状态" clearable><el-option v-for="(label,key) in lifelineSwitches" :key="key" :label="label" :value="key"/></el-select><el-select v-model="sort" aria-label="当前渠道排序"><el-option label="需关注优先" value="attention"/><el-option label="渠道 ID" value="id"/></el-select></div>
   <div class="counts" aria-label="当前探测状态"><button v-for="(label,key) in lifelineStates" :key="key" type="button" :disabled="!snapshot" :aria-pressed="state===key" @click="state=key">{{label}} <strong>{{snapshot?scope.filter(r=>key==='all'||r.state===key).length:'—'}}</strong></button></div>
   <p v-if="snapshot" class="muted">显示 {{matches.length}} / {{scope.length}} 个渠道 · 站点共 {{rows.length}} 个 · 状态以当前快照为准；页面数据过期会单独提示。</p>
   <p v-else class="muted">{{loading?'正在读取当前状态…':'尚未取得渠道数量和探测状态。'}}</p>
   <div v-loading="loading" class="list"><table><thead><tr><th>渠道 / 模型</th><th>渠道开关</th><th>当前探测状态 / 原因</th><th>最近探针 / 恢复进度</th><th>详情</th></tr></thead><tbody><tr v-for="r in visible" :key="r.channel_id"><td data-label="渠道 / 模型"><button class="link" @click="open(r)">#{{r.channel_id}} {{r.channel_name||'未命名渠道'}}</button><small v-if="r.models.length<3">{{r.models.join(' / ')||'未配置模型'}}</small><details v-else><summary>{{r.models.length}} 个模型</summary>{{r.models.join(' / ')}}</details></td><td data-label="渠道开关"><el-tag size="small" type="info">{{lifelineSwitches[r.switch]||r.switch}}</el-tag><small>优先级 {{r.priority}} · 线上权重 {{r.weight}}</small></td><td data-label="当前探测状态 / 原因"><strong :class="r.state">{{lifelineStates[r.state]||r.state}}</strong><small>{{r.reason}}</small><small v-if="r.pending">{{r.pending}} 个请求待结果</small></td><td data-label="最近探针 / 恢复进度"><template v-if="r.current_round?.metrics">计划 {{r.current_round.planned}} 次 · 已发起 {{r.current_round.metrics.sent}} 次<small>已完成 {{r.current_round.metrics.sent-r.current_round.metrics.pending-r.current_round.metrics.unconfirmed}} · 执行中 {{r.current_round.metrics.pending}} · 待确认 {{r.current_round.metrics.unconfirmed}}</small></template><template v-else-if="r.current_round">本批计划 {{r.current_round.planned}} 次<small>逐条发送进度尚未确认</small></template><template v-else-if="r.last">{{formatTime(r.last.started_at)}}<small>{{probeOutcomes[r.last.outcome]||r.last.outcome}} · {{seconds(r.last.duration_seconds)}}</small></template><span v-else class="muted">无当前调度留痕，历史记录可单独查询</span><small>{{r.next}}<template v-if="r.next_at"> · {{formatTime(r.next_at)}}</template></small></td><td><div class="actions"><button class="link" @click="open(r,'records')">探针记录</button><button class="link" @click="open(r,r.current_round?'rounds':'events')">{{r.current_round?'恢复进度':'状态变更'}}</button></div></td></tr></tbody></table><el-empty v-if="!visible.length&&!loading" :description="emptyMessage"/></div>
   <el-pagination v-if="matches.length>20" v-model:current-page="page" :page-size="20" :total="matches.length" layout="prev, pager, next"/>
  </template>
 </section>
</template>
<style scoped>
.current-panel{display:grid;gap:14px;scroll-margin-top:80px}.toolbar,.counts{display:flex;align-items:center;flex-wrap:wrap;gap:10px}.toolbar :deep(.el-select){width:180px}.counts button{padding:8px 12px;background:var(--el-bg-color);border:1px solid var(--ct-line);border-radius:6px;color:var(--ct-ink);cursor:pointer}.counts button[aria-pressed=true]{border-color:var(--ct-accent);color:var(--ct-accent);background:var(--el-color-primary-light-9)}.counts strong{margin-left:8px}.muted,small,details{color:var(--ct-ink-3);font-size:12px}small{display:block;margin-top:5px}.list{border:1px solid var(--ct-line);border-radius:8px;background:var(--el-bg-color);min-height:120px}table{width:100%;table-layout:fixed;border-collapse:collapse;text-align:left}th,td{padding:13px 10px;vertical-align:top;border-bottom:1px solid var(--ct-line);overflow-wrap:anywhere}th{font-weight:500;font-size:12px;color:var(--ct-ink-3)}th:first-child{width:22%}th:nth-child(2){width:17%}th:nth-child(3){width:25%}th:nth-child(4){width:25%}.link{border:0;background:none;color:var(--ct-accent);padding:3px 0;text-align:left;cursor:pointer;font:inherit}.actions{display:grid;gap:7px}.routine{color:var(--el-color-success)}.recovery{color:var(--ct-accent)}.unknown,.waiting,.stopping{color:var(--el-color-warning)}.back{justify-self:start}.el-pagination{justify-content:flex-end}
@media(max-width:650px){table,tbody,tr,td{display:block;width:100%}thead{display:none}tr{padding:10px;border-bottom:1px solid var(--ct-line)}td{border:0;padding:5px 0}td:before{content:attr(data-label);display:block;color:var(--ct-ink-3);font-size:12px}.actions{display:flex;gap:16px}}
</style>
