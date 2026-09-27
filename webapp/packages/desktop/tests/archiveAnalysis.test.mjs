import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,ref} from 'vue'
const utilSource=readFileSync(new URL('../src/utils/archiveAnalysis.ts',import.meta.url),'utf8')
const compiled=ts.transpileModule(utilSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const exports={};new Function('exports',compiled)(exports)
const {totals,usd,groupStats,csvCell}=exports
const currencyStub=()=>({money:ref(),moneyError:ref(''),moneyBusy:ref(false),refreshMoney:async()=>{}})
const moneyAPI={}
new Function('exports',ts.transpileModule(readFileSync(new URL('../src/utils/archiveMoney.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText)(moneyAPI)
const {quotaAmount,currencyUnit,moneyContext}=moneyAPI
test('exact quota, missing cache and consumption-only aggregation',()=>{
 const rows=[{dimensions:{type:'2',model_name:'m'},amounts:{quota:'9007199254740993',requests:'2',log_rows:'2'}},{dimensions:{type:'5'},amounts:{quota:'100',requests:'9'}}]
 assert.equal(totals(rows).quota,9007199254740993n)
 assert.equal(totals(rows).cache_tokens_missing,2n)
 assert.equal(usd(87767606n),'175.535212')
 assert.equal(groupStats(rows,'model_name')[0].requests,2n)
 assert.throws(()=>totals([{dimensions:{type:'2'},amounts:{quota:'1.2'}}]))
 assert.equal(csvCell('=SUM(A1)'),`"'=SUM(A1)"`)
})
function setup(){const source=readFileSync(new URL('../src/components/ArchiveUsageStatistics.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
 const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
 const props={siteId:'site-a'},requests=[];const client={request:url=>new Promise((resolve,reject)=>requests.push({url,resolve,reject}))}
 const c=new Function('computed','ref','watch','onUnmounted','defineProps','client','beijingDate','totals','usd','groupStats','csvCell','ApiError','useArchiveCurrency','quotaAmount','currencyUnit','moneyContext',js+';return {load,reset,rows,days,loaded,error,user,model,channel}')
 return {...c(computed,ref,()=>{},()=>{},()=>props,client,()=> '2026-07-04',totals,usd,groupStats,csvCell,class extends Error{},currencyStub,quotaAmount,currencyUnit,moneyContext),requests,props}}
const tick=()=>new Promise(resolve=>setImmediate(resolve))
test('sealed pages preserve version and filters and publish only complete result',async()=>{const c=setup();c.user.value='12';const pending=c.load();c.requests[0].resolve({items:[{date:'2026-07-04',state:'sealed',version_id:'v'},{date:'2026-07-05',state:'processing'}]});await tick()
 assert.match(c.requests[1].url,/user_id=12/);assert.match(c.requests[1].url,/version=v/)
 const row={group_hash:'a',dimensions:'{"type":"2"}',amounts:'{"quota":"4"}'};c.requests[1].resolve({items:[row],has_more:true});await tick();assert.equal(c.loaded.value,false);assert.equal(c.rows.value.length,0);assert.match(c.requests[2].url,/after_hash=a/)
 c.requests[2].resolve({items:[],has_more:false});await pending;assert.equal(c.rows.value.length,1);assert.deepEqual(c.days.value,['2026-07-04'])})
test('failed pagination discards partial results; reset rejects stale response',async()=>{const c=setup();let pending=c.load();c.requests[0].resolve({items:[{date:'2026-07-04',state:'sealed',version_id:'v'}]});await tick();c.requests[1].reject(new Error('failed'));await pending;assert.equal(c.loaded.value,false);assert.equal(c.rows.value.length,0)
 pending=c.load();c.reset();c.requests[2].resolve({items:[]});await pending;assert.equal(c.loaded.value,false)})

test('negative missing counters are rejected',()=>{
 assert.throws(()=>totals([{dimensions:{type:'2'},amounts:{quota:'0',quota_missing:'-1'}}]))
})
test('empty sealed month stays empty rather than manufacturing days',async()=>{
 const c=setup();const pending=c.load();c.requests[0].resolve({items:[]});await pending
 assert.equal(c.loaded.value,true);assert.equal(c.days.value.length,0);assert.equal(c.rows.value.length,0)
})
test('anomaly pagination keeps exact IDs and discards stale site response',async()=>{
 const source=readFileSync(new URL('../src/components/ArchiveAnomalyAnalysis.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
 const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
 const requests=[],props={siteId:'a'},client={request:url=>new Promise(resolve=>requests.push({url,resolve}))}
 const c=new Function('ref','computed','watch','onUnmounted','defineProps','client','beijingDate','usd','ApiError','useArchiveCurrency','quotaAmount','currencyUnit','moneyContext',js+';return {load,reset,rows,cursors,loaded}') (ref,computed,()=>{},()=>{},()=>props,client,()=> '2026-07-04',usd,class extends Error{},currencyStub,quotaAmount,currencyUnit,moneyContext)
 let pending=c.load();requests[0].resolve({items:[{id:'9007199254740993',created_at:'1783094400',quota:'0'}],has_more:true});await pending
 pending=c.load(2);assert.match(requests[1].url,/after_id=9007199254740993/);c.reset();requests[1].resolve({items:[{id:'old'}],has_more:false});await pending
 assert.equal(c.rows.value.length,0);assert.equal(c.loaded.value,false)
})
