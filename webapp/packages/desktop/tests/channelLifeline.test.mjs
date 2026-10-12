import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed,ref,watch,nextTick } from 'vue'

const utilSource=readFileSync(new URL('../src/utils/channelLifeline.ts',import.meta.url),'utf8')
const utilCode=ts.transpileModule(utilSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const utils=new Function('exports',`${utilCode};return exports;`)({})
const source=readFileSync(new URL('../src/views/ChannelLifelineView.vue',import.meta.url),'utf8')
const script=source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,'')
const code=ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText
const snapshot=(channel=7)=>({retention_days:7,from:'2026-10-12T00:00:00Z',to:'2026-10-12T01:00:00Z',queried_at:'2026-10-12T01:00:00Z',channels:[{channel_id:channel,channel_name:'channel',model_name:'m'}],trend:[],bucket_seconds:60,total:{}})
function harness(liveWatch=false){
  const api={probeOverview:async()=>snapshot(),channelLifeline:async()=>({items:[],next_cursor:''}),probeRecords:async()=>({items:[],total:0,next_cursor:''})}
  const filters={site_id:'a'}, noop=()=>{}, timers=new Map(), document={hidden:false},stops=[];let id=0,unmount=noop
  const watchState=liveWatch ? (...args)=>{stops.push(watch(...args))} : noop
  const p=new Function('computed','ref','watch','onMounted','onBeforeUnmount','useFiltersStore','useRoute','dashboard','probeTrend','document','setTimeout','clearTimeout','filterProbeChannels','nextTick',`${code};return {refresh,selectChannel,closeChannel,loadDetail,pageDetail,changeDetail,openProbeRecords,filterChannels,resetChannelFilters,overview,applied,selected,events,records,cursors,nextCursor,error,detailError,loading,refreshing,detailLoading,kind,model,tab,source,outcome,recordModel,recordKind,recordModels,search,hours,channelPage,channelFilter,channelSort,channels,filteredChannels,visibleChannels,autoRefresh,refreshSeconds,autoRefreshPause,automaticRefresh,scheduleRefresh};`)(computed,ref,watchState,noop,fn=>{unmount=fn},()=>filters,()=>({query:{}}),api,utils.probeTrend,document,(fn,ms)=>{timers.set(++id,{fn,ms});return id},key=>timers.delete(key),utils.filterProbeChannels,nextTick)
  return {...p,api,filters,timers,document,unmount:()=>{unmount();stops.forEach(stop=>stop())}}
}
function deferred(){let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}

test('life labels distinguish decisions, disable and restore, and unknown has no success rate',()=>{
 assert.equal(utils.ratio(null),'—');assert.equal(utils.seconds(undefined),'—')
 assert.equal(utils.lifeTitle({source:'operation',operation:'channel.update',details:{target:{status:2,weight:0}}}),'禁用渠道')
 assert.equal(utils.lifeTitle({source:'operation',operation:'channel.update',details:{target:{status:1,weight:10}}}),'启用渠道')
 assert.equal(utils.lifeTitle({source:'decision',operation:'weight_write',details:{target:{weight:10}}}),'调权决策')
 assert.deepEqual(utils.lifeChanges({details:{before:{weight:100},target:{weight:0}}}),['权重：100 → 0'])
 const values=utils.probeTrend({...snapshot(),to:'2026-10-12T00:03:00Z',trend:[{at:'2026-10-12T00:01:00Z',success:2}]})
 assert.equal(values.length,3);assert.equal(values[0].value,undefined);assert.equal(values[1].value.success,2)
})
test('late site responses cannot mix channels; refresh failures preserve the same query only',async()=>{
 const p=harness(),old=deferred();p.api.probeOverview=()=>old.promise;const a=p.refresh()
 p.filters.site_id='b';p.api.probeOverview=async()=>snapshot(9);await p.refresh();old.resolve(snapshot(7));await a
 assert.equal(p.applied.value.site_id,'b');assert.equal(p.overview.value.channels[0].channel_id,9)
 p.api.probeOverview=async()=>{throw Error('network')};await p.refresh();assert.equal(p.overview.value.channels[0].channel_id,9);assert.match(p.error.value,/保留上次/)
 p.model.value='other';await p.refresh();assert.equal(p.overview.value,undefined);assert.match(p.error.value,/失败/)
})
test('stale detail ignored and keyset page stack resets on filter changes',async()=>{
 const p=harness();await p.refresh();p.selected.value=7;const old=deferred();p.api.channelLifeline=()=>old.promise;const pending=p.loadDetail()
 p.selected.value=9;p.api.channelLifeline=async q=>({items:[{id:`channel-${q.channel_id}`}],next_cursor:'next'});await p.loadDetail();old.resolve({items:[{id:'old'}]});await pending
 assert.equal(p.events.value[0].id,'channel-9');assert.equal(p.nextCursor.value,'next')
 p.pageDetail(true);assert.deepEqual([...p.cursors.value],['','next']);p.changeDetail();assert.deepEqual([...p.cursors.value],[''])
})
test('probe filters apply to records while channel operations remain visible',async()=>{
 const p=harness();p.model.value='m';p.kind.value='scheduled';await p.refresh();p.selected.value=7
 let query;p.api.channelLifeline=async q=>{query=q;return {items:[]}};await p.loadDetail();assert.equal(query.model,undefined);assert.equal(query.kind,undefined)
 p.tab.value='records';p.api.probeRecords=async q=>{query=q;return {items:[],total:0}};await p.loadDetail();assert.equal(query.model,'m');assert.equal(query.kind,'scheduled')
})

test('manual refresh is the default and record refresh does not reload the overview',async()=>{
 const p=harness();let summaries=0
 p.api.probeOverview=async()=>{summaries++;return snapshot()}
 await p.refresh();assert.equal(p.autoRefresh.value,false);assert.equal(p.timers.size,0)
 await p.automaticRefresh();assert.equal(summaries,1)
 p.selected.value=7;await p.loadDetail();assert.equal(summaries,1)
})

test('refresh preserves channel pagination and visible detail until the replacement is ready',async()=>{
 const p=harness(),data={...snapshot(),channels:Array.from({length:45},(_,i)=>({...snapshot().channels[0],channel_id:i+1}))}
 p.api.probeOverview=async()=>data;await p.refresh();p.channelPage.value=2;p.selected.value=7;p.cursors.value=['','older'];p.tab.value='records'
 p.records.value=[{id:'reading'}];const next=deferred();p.api.probeOverview=()=>next.promise
 let query;p.api.probeRecords=async q=>{query=q;return {items:[{id:'updated'}],total:1}}
 const refreshing=p.refresh();assert.equal(p.records.value[0].id,'reading');assert.equal(p.channelPage.value,2);assert.deepEqual([...p.cursors.value],['','older'])
 next.resolve(data);await refreshing;assert.equal(query.cursor,'older');assert.equal(p.records.value[0].id,'updated');assert.equal(p.channelPage.value,2)
})

test('automatic refresh waits after completion, stays in the background and never overlaps',async()=>{
 const p=harness();await p.refresh();p.autoRefresh.value=true;p.refreshSeconds.value=120
 const pending=deferred();let calls=0;p.api.probeOverview=()=>{calls++;return pending.promise}
 const task=p.automaticRefresh();assert.equal(p.refreshing.value,true);assert.equal(p.loading.value,false);assert.ok(p.overview.value)
 await p.automaticRefresh();assert.equal(calls,1)
 pending.resolve(snapshot());await task;assert.equal(p.refreshing.value,false);assert.equal(p.timers.size,1);assert.equal([...p.timers.values()][0].ms,120000)
 p.unmount();assert.equal(p.timers.size,0)
})

test('automatic refresh pauses for detail, either pagination, draft filters, custom time and hidden tabs',async()=>{
 const p=harness();await p.refresh();p.autoRefresh.value=true;let calls=0;p.api.probeOverview=async()=>{calls++;return snapshot()}
 p.selected.value=7;await p.automaticRefresh();assert.match(p.autoRefreshPause.value,/渠道记录/)
 p.cursors.value=['','next'];await p.automaticRefresh()
 p.closeChannel();p.channelPage.value=2;await p.automaticRefresh();assert.match(p.autoRefreshPause.value,/渠道分页/)
 p.channelPage.value=1;p.search.value='draft';await p.automaticRefresh();assert.match(p.autoRefreshPause.value,/尚未查询/)
 p.search.value='';p.hours.value=0;await p.automaticRefresh();assert.match(p.autoRefreshPause.value,/自定义/)
 p.hours.value=1;p.document.hidden=true;await p.automaticRefresh();assert.equal(calls,0)
 p.document.hidden=false;await p.automaticRefresh();assert.equal(calls,1)
})

test('entering detail during an automatic request freezes its query window and records',async()=>{
 const p=harness();await p.refresh();const original=p.applied.value.to, pending=deferred();p.autoRefresh.value=true;p.api.probeOverview=()=>pending.promise
 const task=p.automaticRefresh();p.api.channelLifeline=async()=>({items:[{id:'reading'}]});p.selectChannel({channel_id:7})
 pending.resolve({...snapshot(),to:'2026-10-12T02:00:00Z'});await task
 assert.equal(p.applied.value.to,original);assert.equal(p.events.value[0].id,'reading')
})

test('late automatic responses do not apply drafts and cannot overwrite a newer manual query',async()=>{
 const p=harness();await p.refresh();p.autoRefresh.value=true;const pending=deferred();p.api.probeOverview=()=>pending.promise
 const task=p.automaticRefresh();p.model.value='draft';pending.resolve(snapshot(9));await task
 assert.equal(p.overview.value.channels[0].channel_id,7);assert.equal(p.applied.value.model,'')
 p.model.value='';const old=deferred();p.api.probeOverview=()=>old.promise;const oldTask=p.automaticRefresh()
 p.model.value='new';p.api.probeOverview=async()=>snapshot(11);await p.refresh();old.resolve(snapshot(9));await oldTask
 assert.equal(p.overview.value.channels[0].channel_id,11);assert.equal(p.applied.value.model,'new')
})

test('a failed background update keeps charts and details, then unmount cancels the timer',async()=>{
 const p=harness();await p.refresh();p.autoRefresh.value=true;p.api.probeOverview=async()=>{throw Error('offline')}
 await p.automaticRefresh();assert.equal(p.overview.value.channels[0].channel_id,7);assert.match(p.error.value,/保留上次/);assert.equal(p.timers.size,1)
 p.unmount();await p.automaticRefresh();assert.equal(p.timers.size,0)
})

const channel=(id,values={})=>({channel_id:id,channel_name:`channel-${id}`,model_name:'m',due:1,sent:1,success:0,errors:0,slow:0,skipped:0,unknown:0,pending:0,unconfirmed:0,canceled:0,success_rate:null,mean_seconds:null,...values})

test('channel filters distinguish errors, missing results and channels without samples',()=>{
 const rows=[channel(1,{errors:2}),channel(2,{slow:1}),channel(3,{skipped:1}),channel(4,{unconfirmed:1}),channel(5,{unknown:1}),channel(6,{pending:1}),channel(7,{due:0,sent:0}),channel(8,{success:1,success_rate:1}),channel(9,{canceled:1})]
 const expected={all:[1,2,3,4,5,6,7,8,9],abnormal:[1,2,3,4,5],error:[1],slow:[2],skipped:[3],unconfirmed:[4],unknown:[5],pending:[6],empty:[7]}
 for(const [filter,ids] of Object.entries(expected))assert.deepEqual(utils.filterProbeChannels(rows,filter,'channel').map(r=>r.channel_id),ids,filter)
 assert.deepEqual(rows.map(r=>r.channel_id),expected.all)
})

test('sorts use the full list, put unknown measurements last and keep zero as a measured value',()=>{
 const rows=[channel(3,{errors:2,success_rate:null,mean_seconds:null}),channel(2,{errors:3,success_rate:0,mean_seconds:0,last_started_at:'2026-10-12T02:00:00Z'}),channel(1,{errors:3,success_rate:.8,mean_seconds:5,last_started_at:'2026-10-12T01:00:00Z'})]
 const order=sort=>utils.filterProbeChannels(rows,'all',sort).map(r=>r.channel_id)
 assert.deepEqual(order('errors'),[1,2,3]);assert.deepEqual(order('success_rate'),[2,1,3]);assert.deepEqual(order('mean_seconds'),[1,2,3]);assert.deepEqual(order('latest'),[2,1,3])
 const tied=[channel(7,{model_name:'z'}),channel(7,{model_name:'a'})];assert.deepEqual(utils.filterProbeChannels(tied,'all','channel').map(r=>r.model_name),['a','z'])
 assert.deepEqual(rows.map(r=>r.channel_id),[3,2,1])
})

test('list filters apply before pagination, reset its page and never requery or change overview totals',async t=>{
 const p=harness(true);t.after(p.unmount);let calls=0
 p.api.probeOverview=async()=>{calls++;return {...snapshot(),total:{errors:5},channels:Array.from({length:45},(_,i)=>channel(i+1,{errors:i>=40?1:0}))}}
 await p.refresh();const original=p.overview.value;p.channelPage.value=3;p.channelFilter.value='error';await nextTick()
 assert.equal(p.channelPage.value,1);assert.deepEqual(p.visibleChannels.value.map(r=>r.channel_id),[41,42,43,44,45]);assert.equal(p.filteredChannels.value.length,5)
 p.channelSort.value='errors';await nextTick();assert.equal(calls,1);assert.equal(p.overview.value,original);assert.equal(p.overview.value.total.errors,5)
 p.resetChannelFilters();await nextTick();assert.equal(p.filteredChannels.value.length,45);assert.equal(p.channelSort.value,'channel')
})

test('refresh clamps to the filtered page count while preserving the chosen filter and sort',async()=>{
 const p=harness();p.channelFilter.value='error';p.channelSort.value='errors'
 p.api.probeOverview=async()=>({...snapshot(),channels:Array.from({length:45},(_,i)=>channel(i+1,{errors:1}))});await p.refresh();p.channelPage.value=2
 p.api.probeOverview=async()=>({...snapshot(),channels:Array.from({length:45},(_,i)=>channel(i+1,{errors:i<10?1:0}))});await p.refresh()
 assert.equal(p.channelPage.value,1);assert.equal(p.channelFilter.value,'error');assert.equal(p.channelSort.value,'errors');assert.equal(p.visibleChannels.value.length,10)
})

test('opening an anomaly scopes records to its row model and outcome with exactly one request',async t=>{
 const p=harness(true);t.after(p.unmount);p.kind.value='scheduled';await p.refresh()
 const queries=[];p.api.probeRecords=async q=>{queries.push(q);return {items:[{id:`${q.channel_id}-${q.model}-${q.outcome}`}],total:1,next_cursor:'older'}}
 p.cursors.value=['','previous'];p.openProbeRecords(channel(7,{model_name:'model-a'}),'error');await nextTick()
 assert.equal(queries.length,1);assert.equal(queries[0].channel_id,7);assert.equal(queries[0].model,'model-a');assert.equal(queries[0].kind,'scheduled');assert.equal(queries[0].outcome,'error');assert.equal(queries[0].cursor,'');assert.equal(p.tab.value,'records')
 p.openProbeRecords(channel(8,{model_name:'model-b'}),'error');await nextTick();assert.equal(queries.length,2);assert.equal(queries[1].channel_id,8);assert.equal(queries[1].model,'model-b')
 p.openProbeRecords(channel(8,{model_name:'model-b'}),'slow');await nextTick();assert.equal(queries.length,3);assert.equal(queries[2].outcome,'slow')
 assert.deepEqual([...p.cursors.value],[''])
})

test('record filters reset the cursor, leave the overview alone and do not hide channel operations',async t=>{
 const p=harness(true);t.after(p.unmount);await p.refresh();const original=p.overview.value;const queries=[]
 p.api.probeRecords=async q=>{queries.push(q);return {items:[],total:0}}
 p.openProbeRecords(channel(7,{model_name:'a'}),'error');await nextTick()
 p.cursors.value=['','older'];p.recordKind.value='accelerated';p.recordModel.value='';p.changeDetail();await nextTick()
 assert.equal(queries.at(-1).cursor,'');assert.equal(queries.at(-1).model,'');assert.equal(queries.at(-1).kind,'accelerated');assert.equal(queries.at(-1).outcome,'error');assert.equal(p.overview.value,original)
 let life;p.api.channelLifeline=async q=>{life=q;return {items:[]}};p.tab.value='life';await nextTick()
 assert.equal(life.model,undefined);assert.equal(life.kind,undefined);assert.equal(life.outcome,undefined)
})
