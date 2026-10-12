import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed,ref } from 'vue'

const utilSource=readFileSync(new URL('../src/utils/channelLifeline.ts',import.meta.url),'utf8')
const utilCode=ts.transpileModule(utilSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const utils=new Function('exports',`${utilCode};return exports;`)({})
const source=readFileSync(new URL('../src/views/ChannelLifelineView.vue',import.meta.url),'utf8')
const script=source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,'')
const code=ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText
const snapshot=(channel=7)=>({retention_days:7,from:'2026-10-12T00:00:00Z',to:'2026-10-12T01:00:00Z',queried_at:'2026-10-12T01:00:00Z',channels:[{channel_id:channel,channel_name:'channel',model_name:'m'}],trend:[],bucket_seconds:60,total:{}})
function harness(){
  const api={probeOverview:async()=>snapshot(),channelLifeline:async()=>({items:[],next_cursor:''}),probeRecords:async()=>({items:[],total:0,next_cursor:''})}
  const filters={site_id:'a'}, noop=()=>{}
  const p=new Function('computed','ref','watch','onMounted','onBeforeUnmount','useFiltersStore','useRoute','dashboard','probeTrend',`${code};return {refresh,selectChannel,loadDetail,pageDetail,changeDetail,overview,applied,selected,events,records,cursors,nextCursor,error,detailError,loading,kind,model,tab,source};`)(computed,ref,noop,noop,noop,()=>filters,()=>({query:{}}),api,utils.probeTrend)
  return {...p,api,filters}
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
test('late site responses cannot mix channels or report healthy empty data on failure',async()=>{
 const p=harness(),old=deferred();p.api.probeOverview=()=>old.promise;const a=p.refresh()
 p.filters.site_id='b';p.api.probeOverview=async()=>snapshot(9);await p.refresh();old.resolve(snapshot(7));await a
 assert.equal(p.applied.value.site_id,'b');assert.equal(p.overview.value.channels[0].channel_id,9)
 p.api.probeOverview=async()=>{throw Error('network')};await p.refresh();assert.equal(p.overview.value,undefined);assert.match(p.error.value,/失败/)
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
