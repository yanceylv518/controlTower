import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {computed,ref,watch,nextTick,reactive} from 'vue';
const transpile=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
const u=new Function('exports',`${transpile(readFileSync(new URL('../src/utils/lifelineCurrent.ts',import.meta.url),'utf8'))};return exports;`)({});
function harness(name,expose,props,api,overrides={}){
 const filters={site_id:'a'},stops=[],cleanup=[];
 const s=readFileSync(new URL(`../src/components/${name}.vue`,import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,'');
 const vars={...u,computed,ref,nextTick,watch:(...args)=>stops.push(watch(...args)),onBeforeUnmount:fn=>cleanup.push(fn),defineProps:()=>props,defineEmits:()=>()=>{},defineExpose:()=>{},dashboard:api,useFiltersStore:()=>filters,setInterval:()=>1,clearInterval:()=>{},setTimeout:()=>1,clearTimeout:()=>{},document:{hidden:false},window:{scrollY:0,scrollTo:()=>{}}};
 Object.assign(vars,overrides);
 const p=new Function(...Object.keys(vars),`${transpile(s)};return {${expose}};`)(...Object.values(vars));
 return {...p,filters,stop:()=>{cleanup.forEach(fn=>fn());stops.forEach(fn=>fn());}};
}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}};
test('state change timeout is explicit and retains the current page',async t=>{
 const api={channelLifeline:async()=>({items:[{id:'kept'}],next_cursor:'next'})};
 const p=harness('LifelineChannelDetail','loadEvents,events,error,loading', {channel:{channel_id:7},initialTab:'events'},api);t.after(p.stop);
 await p.loadEvents(true);
 api.channelLifeline=async()=>{throw {code:'channel_lifeline_timeout'}};
 await p.loadEvents();assert.match(p.error.value,/超时/);assert.equal(p.events.value[0].id,'kept');assert.equal(p.loading.value,false);
});
test('same-channel status ticks preserve the chosen detail tab, batch and event query',async t=>{
 const props=reactive({channel:{channel_id:7,state:'unknown'},initialTab:'records'});
 let requests=0;const api={channelLifeline:async()=>{requests++;return {items:[{id:'change'}],next_cursor:'older'}}};
 const p=harness('LifelineChannelDetail','tab,round,events,applied,cursors,openRound',props,api);t.after(p.stop);
 p.tab.value='events';await nextTick();await nextTick();
 const query=p.applied.value,count=requests;p.cursors.value.push('older');
 for(let i=0;i<3;i++){props.channel={...props.channel,reason:`状态过期 ${i}`};await nextTick();}
 assert.equal(p.tab.value,'events');assert.equal(requests,count);assert.equal(p.applied.value,query);assert.equal(p.events.value[0].id,'change');assert.equal(p.cursors.value.length,2);
 p.openRound(7,'batch-7');props.channel={...props.channel,state:'recovery'};await nextTick();
 assert.equal(p.tab.value,'rounds');assert.equal(p.round.value,'batch-7');
 props.channel={channel_id:8,state:'unknown'};await nextTick();
 assert.equal(p.tab.value,'records');assert.equal(p.round.value,'');assert.deepEqual([...p.events.value],[]);
});
test('detail opened at events still allows a persistent switch to records',async t=>{
 const props=reactive({channel:{channel_id:7},initialTab:'events'});
 const p=harness('LifelineChannelDetail','tab',props,{channelLifeline:async()=>({items:[]})});t.after(p.stop);
 await nextTick();p.tab.value='records';await nextTick();props.channel={channel_id:7,reason:'new snapshot'};await nextTick();
 assert.equal(p.tab.value,'records');
 props.initialTab='rounds';await nextTick();assert.equal(p.tab.value,'rounds');
});
test('failed pagination retains the previous page across records, batches and changes',async t=>{
 for(const [name,method,expose,props] of [
  ['LifelineProbeRecords','probeRecords','load,page,cursors',{channelId:7}],
  ['LifelineRounds','probeRounds','load,page,cursors',{channelId:7}],
  ['LifelineChannelDetail','channelLifeline','loadEvents,page,cursors',{channel:{channel_id:7},initialTab:'events'}]
 ]){
  const api={[method]:async()=>({items:[],total:0,next_cursor:'next'})};
  const p=harness(name,expose,props,api);t.after(p.stop);await (p.load||p.loadEvents)(true);
  api[method]=async()=>{throw Error('offline')};await p.page(true);assert.deepEqual([...p.cursors.value],['']);
 }
});
const snapshot=(id=1)=>{const at=new Date().toISOString();return {queried_at:at,channels:[{channel_id:id,channel_name:'c',models:['m'],switch:'enabled',state:'routine',confirmed_at:at,pending:0,accelerated:0}]}};
test('current read timeout ends loading, keeps unknown counts and ignores late responses',async t=>{
 const tasks=new Map();let id=0,signal;const late=deferred();
 const api={lifelineCurrent:(_,s)=>{signal=s;return late.promise}};
 const p=harness('LifelineCurrentPanel','refresh,snapshot,loading,error,emptyMessage', {active:true},api,{
  setTimeout:(fn,ms)=>{const key=++id;tasks.set(key,{fn,ms});return key},clearTimeout:key=>tasks.delete(key)
 });t.after(p.stop);
 assert.equal(p.loading.value,true);assert.equal(p.snapshot.value,undefined);
 const timeout=[...tasks.values()].find(v=>v.ms===20000);assert.ok(timeout);timeout.fn();
 await nextTick();await nextTick();await nextTick();
 assert.equal(signal.aborted,true);assert.equal(p.loading.value,false);assert.match(p.error.value,/超时/);assert.match(p.emptyMessage.value,/尚未读取成功/);
 late.resolve(snapshot(1));await nextTick();assert.equal(p.snapshot.value,undefined);
 api.lifelineCurrent=async()=>snapshot(2);await p.refresh();assert.equal(p.snapshot.value.channels[0].channel_id,2);assert.equal(p.error.value,'');assert.equal(tasks.size,0);
 api.lifelineCurrent=async()=>{throw {code:'lifeline_current_timeout'}};await p.refresh();assert.match(p.error.value,/超时.*保留/);assert.equal(p.snapshot.value.channels[0].channel_id,2);
});
test('superseded current request cannot clear the newer loading state',async t=>{
 const old=deferred(),fresh=deferred(),api={lifelineCurrent:()=>old.promise};
 const p=harness('LifelineCurrentPanel','refresh,snapshot,loading,error', {active:true},api);t.after(p.stop);
 api.lifelineCurrent=()=>fresh.promise;const run=p.refresh();await nextTick();await nextTick();assert.equal(p.loading.value,true);assert.equal(p.error.value,'');
 old.resolve(snapshot(1));await nextTick();assert.equal(p.snapshot.value,undefined);
 fresh.resolve(snapshot(2));await run;assert.equal(p.loading.value,false);assert.equal(p.snapshot.value.channels[0].channel_id,2);
});
test('current panel ignores old-site replies and retains same-site snapshot on failure',async t=>{
 const old=deferred(),api={lifelineCurrent:()=>old.promise};
 const p=harness('LifelineCurrentPanel','refresh,snapshot,rows,clock,snapshotStale,loading,error,auto,selected,order', {active:true},api);t.after(p.stop);
 p.filters.site_id='b';api.lifelineCurrent=async()=>snapshot(2);await p.refresh();old.resolve(snapshot(1));await nextTick();assert.equal(p.snapshot.value.channels[0].channel_id,2);
 api.lifelineCurrent=async()=>{throw Error('offline')};await p.refresh();assert.equal(p.snapshot.value.channels[0].channel_id,2);assert.match(p.error.value,/保留/);
 p.clock.value=Date.now()+61000;assert.equal(p.rows.value[0].state,'routine');assert.equal(p.snapshotStale.value,true);
});
test('background state updates do not reorder channels or overwrite a detail opened mid-request',async t=>{
 const api={lifelineCurrent:async()=>{const s=snapshot();s.channels.push({...s.channels[0],channel_id:2});return s}};
 const p=harness('LifelineCurrentPanel','refresh,snapshot,auto,selected,order', {active:true},api);t.after(p.stop);await p.refresh();p.auto.value=true;
 api.lifelineCurrent=async()=>{const s=snapshot();s.channels.unshift({...s.channels[0],channel_id:2,state:'unknown'});return s};await p.refresh(true);assert.deepEqual([...p.order.value],[1,2]);
 const late=deferred();api.lifelineCurrent=()=>late.promise;const run=p.refresh(true);p.selected.value=1;late.resolve(snapshot(9));await run;assert.equal(p.snapshot.value.channels[0].channel_id,2);
});
test('record queries default to all results, keep applied filters across pages, and reject old-site data',async t=>{
 const old=deferred(),queries=[],api={probeRecords:q=>{queries.push(q);return old.promise}};
 const p=harness('LifelineProbeRecords','load,records,total,outcome,dispatch,applied,cursors,nextCursor,reset', {channelId:7},api);t.after(p.stop);
 assert.equal(queries[0].outcome,'');assert.equal(queries[0].channel_id,7);
 p.filters.site_id='b';api.probeRecords=async q=>{queries.push(q);return {items:[{id:'b'}],total:1,next_cursor:'older'}};p.reset();await nextTick();old.resolve({items:[{id:'a'}],total:1});await nextTick();assert.equal(p.records.value[0].id,'b');
 p.outcome.value='error';p.cursors.value.push('older');await p.load();assert.equal(queries.at(-1).outcome,'');assert.equal(queries.at(-1).cursor,'older');
 await p.load(true);assert.equal(queries.at(-1).outcome,'error');assert.equal(queries.at(-1).cursor,'');
 p.dispatch.value='unsent';await p.load(true);assert.equal(queries.at(-1).dispatch,'unsent');
});
test('round-linked request lookup preserves exact batch identity and channel scope',async t=>{
 let query;const api={probeRecords:async q=>{query=q;return {items:[],total:0}}};
 const p=harness('LifelineProbeRecords','load', {channelId:7,roundId:'batch-1',from:'2026-10-13T00:00:00Z',to:'2026-10-13T01:00:00Z'},api);t.after(p.stop);await p.load(true);
 assert.equal(query.round_id,'batch-1');assert.equal(query.channel_id,7);assert.equal(query.from,'2026-10-13T00:00:00Z');
});
