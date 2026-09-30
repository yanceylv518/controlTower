import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,ref} from 'vue'
const compile=source=>ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const analysis={},session={}
new Function('exports',compile(readFileSync(new URL('../src/utils/archiveAnalysis.ts',import.meta.url),'utf8')))(analysis)
new Function('exports',compile(readFileSync(new URL('../src/utils/archiveOverviewCache.ts',import.meta.url),'utf8')))(session)
function setup(identity={}){
 const props={siteId:'a'},requests=[],watchers=[],cleanup=[]
 const source=readFileSync(new URL('../src/components/ArchiveDataView.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
 const args={computed,ref,watch:(...args)=>watchers.push(args),onUnmounted:f=>cleanup.push(f),defineProps:()=>props,client:{request:url=>new Promise((resolve,reject)=>requests.push({url,resolve,reject}))},useAuthStore:()=>({user:identity}),overviewCache:session.overviewCache,beijingDate:()=> '2026-09-27',archiveReadError:()=> '读取失败',...analysis,useArchiveCurrency:()=>({money:ref(),moneyError:ref(''),refreshMoney:async()=>{}}),quotaAmount:()=> '—',currencyUnit:()=> '额度',setInterval:()=>0,clearInterval:()=>{}}
 const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
 const c=new Function(...Object.keys(args),js+';return {load,data,query,total,hasStats,month,selected,dimension,anomalies,cache,quick,range,user,model,channel,options}')(...Object.values(args))
 return {...c,props,requests,watchers,dispose:()=>cleanup.forEach(f=>f())}
}
const fixture={days:[{date:'2026-09-27',version:'live',ready:false,state:'collecting'}],rows:[{date:'2026-09-27',dimensions:{type:'2',model_name:'m'},amounts:{requests:'1',quota:'9007199254740993',log_rows:'1',anomaly_rows:'1',empty_output:'1'}}],observed_at:'2026-09-27T00:00:00Z'}
test('one request loads unsealed statistics, revisits use session cache',async()=>{
 const identity={},c=setup(identity);let pending=c.load();assert.equal(c.requests.length,1);assert.match(c.requests[0].url,/overview/);c.requests[0].resolve({items:[fixture]});await pending
 assert.equal(c.total.value.quota,9007199254740993n);assert.equal(c.hasStats.value,true);assert.equal(c.anomalies(fixture.rows,'empty_output'),'1')
 c.dispose();const next=setup(identity);await next.load();assert.equal(next.requests.length,0);assert.equal(next.total.value.quota,9007199254740993n)
 const other=setup({});pending=other.load();assert.equal(other.data.value,undefined);other.requests[0].resolve({items:[{...fixture,rows:[],days:[]}]});await pending
 next.dispose();other.dispose()
})
test('site changes reject old responses; unknown baseline is not a measured zero',async()=>{
 const c=setup();const first=c.load();c.props.siteId='b';const second=c.load();c.requests[1].resolve({items:[{...fixture,rows:[]}]});await second;c.requests[0].resolve({items:[fixture]});await first
 assert.equal(c.data.value.rows.length,0);assert.equal(c.hasStats.value,false);assert.equal(c.anomalies([],'empty_output'),'—');c.dispose()
})
test('refresh failures preserve cached data and never replace it with empty totals',async()=>{
 const c=setup();let pending=c.load();c.requests[0].resolve({items:[fixture]});await pending;pending=c.load(true);assert.equal(c.total.value.quota,9007199254740993n);c.requests[1].reject(new Error('timeout'));await pending;assert.equal(c.total.value.quota,9007199254740993n);c.dispose()
})

test('recent thirty days includes the previous month and remains inclusive',()=>{const c=setup();c.quick('recent');assert.deepEqual(c.range.value,['2026-08-29','2026-09-27']);assert.match(c.query.value,/from=2026-08-29/);assert.match(c.query.value,/through=2026-09-27/);c.dispose()})

for (const [field,param,selected] of [['model','model','gpt-5'],['user','user_id','4'],['channel','channel_id','252']]) {
 for (const cleared of [undefined,null,'']) {
  test(`${field} clear (${String(cleared)}) restores all-data query, cache and refreshed options`,async()=>{
   const c=setup(),all={...fixture,options:{model_name:{'gpt-5':true,'other':true}}}
   const initial=c.query.value
   let pending=c.load();c.requests.at(-1).resolve({items:[all]});await pending
   c[field].value=selected
   pending=c.load()
   assert.equal(new URL(c.requests.at(-1).url,'http://test').searchParams.get(param),selected)
   c.requests.at(-1).resolve({items:[{...fixture,rows:[],options:{}}]});await pending
   c[field].value=cleared
   assert.equal(c.query.value,initial)
   await c.load()
   assert.equal(c.requests.length,2)
   assert.equal(c.total.value.requests,1n)
   assert.deepEqual(c.options('model_name'),['gpt-5','other'])
   pending=c.load(true)
   assert.equal(new URL(c.requests.at(-1).url,'http://test').searchParams.get(param),'')
   c.requests.at(-1).resolve({items:[all]});await pending
   assert.equal(c.total.value.requests,1n)
   c.dispose()
  })
 }
}
