import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as vue from 'vue'

const transpile = source => ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const helperExports = {}
new Function('exports', transpile(readFileSync(new URL('../src/utils/logArchive.ts', import.meta.url), 'utf8')))(helperExports)
const permissionExports = {}
new Function('exports', transpile(readFileSync(new URL('../src/permissions.ts', import.meta.url), 'utf8')))(permissionExports)
class ApiError extends Error { constructor(status) { super('fixture failure'); this.status = status } }
function pending() { let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{promise,resolve,reject} }
function archiveItem(site) {
  return {site_id:site,name:site,enabled:true,days:[],targets:[{instance_id:site,agent_id:'agent',configured:true,seen_at:new Date().toISOString()}],seen_at:new Date().toISOString(),config:{version:1,instance_id:site,agent_id:'agent',running:false,batch_size:500,interval_seconds:30,delay_seconds:300},status:{supports_daily_check:true,agent_id:'agent',configured:true,applied_version:1,state:'paused',last_id:'1',verified_rows:0,last_batch_rows:0,error:''}}
}
function setup(t) {
  const source=readFileSync(new URL('../src/views/LogArchiveView.vue',import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const requests=[],messages=[],cleanup=[],filters=vue.reactive({site_id:'A',loadInstances:async()=>{}})
  const dependencies={vue:{...vue,onMounted:()=>{},onUnmounted:fn=>cleanup.push(fn)},'element-plus':{ElMessage:{success:value=>messages.push(['success',value]),error:value=>messages.push(['error',value])}},'@ct/shared':{ApiError},'../api':{client:{request:(url,options)=>{const request={url,options,...pending()};requests.push(request);return request.promise}}},'../stores/filters':{useFiltersStore:()=>filters},'../utils/logArchive':helperExports}
  dependencies['../permissions']=permissionExports
  dependencies['../stores/auth']={useAuthStore:()=>({user:{role:'admin',permissions:['archive.manage']}})}
  const scope=vue.effectScope()
  const view=scope.run(()=>new Function('require','exports',transpile(source)+';return {get(name){return eval(name)}};')(name=>{if(!(name in dependencies))throw Error('Unexpected dependency '+name);return dependencies[name]},{}))
  t.after(()=>{for(const fn of cleanup)fn();scope.stop()})
  return {view,requests,messages,filters,cleanup}
}
const value=(ctx,name)=>ctx.view.get(name)
const response=(request,site)=>({items:[archiveItem(site)],month:new URL(request.url,'http://test').searchParams.get('month')})
async function loadInitial(ctx) {
  const task=ctx.view.get('load')()
  const request=ctx.requests.at(-1)
  request.resolve(response(request,'A'))
  await task
}

test('viewing the processing month reads daily data without changing archive policy',async t=>{
  const ctx=setup(t);await loadInitial(ctx)
  value(ctx,'item').value.status.workflow={phase:'import_target',preparation:{table:'logs_202606'}}
  value(ctx,'item').value.workflow_days=[{date:'2026-09-01'}]
  const before=ctx.requests.length
  value(ctx,'showProcessingMonth')()
  assert.equal(value(ctx,'tab').value,'daily')
  assert.equal(value(ctx,'month').value,'2026-06')
  assert.deepEqual(value(ctx,'item').value.workflow_days,[])
  const request=ctx.requests.at(-1)
  assert.equal(ctx.requests.length,before+1)
  assert.equal(request.options?.method,undefined)
  assert.match(request.url,/month=2026-06/)
  request.resolve(response(request,'A'));await vue.nextTick()
})

test('full history starts without a date and preserves the operator declaration', async t => {
  const ctx=setup(t)
  const loading=value(ctx,'load')()
  const req=ctx.requests.at(-1), body=response(req,'A')
  body.capabilities={full_history:true}
  body.items[0].config.history_immutable=true
  req.resolve(body);await loading
  value(ctx,'toggle')()
  const put=ctx.requests.at(-1)
  const config=JSON.parse(put.options.body)
  assert.equal(config.full_history,true)
  assert.equal(config.running,true)
  assert.equal(config.history_immutable,true)
  assert.equal(config.reconcile_date,'')
  assert.equal(config.from,undefined)
  assert.equal(config.to,undefined)
  assert.equal(value(ctx,'fullHistory').value,true)
  put.reject(new ApiError(409));await new Promise(resolve=>setImmediate(resolve))
})
test('late archive responses cannot overwrite the newly selected site',async t=>{
  const ctx=setup(t),first=ctx.view.get('load')()
  const old=ctx.requests.at(-1)
  ctx.filters.site_id='B';await vue.nextTick()
  const current=ctx.requests.at(-1)
  assert.match(current.url,/site_id=B/)
  current.resolve(response(current,'B'))
  await vue.nextTick();await vue.nextTick()
  old.resolve({items:[archiveItem('A')],month:'2026-08'});await first
  assert.equal(value(ctx,'item').value.site_id,'B')
  assert.equal(value(ctx,'month').value,response(current,'B').month)
})
test('a reconciliation save conflict retains its dialog and reports the failure',async t=>{
  const ctx=setup(t);await loadInitial(ctx)
  const checkDate=helperExports.beijingDate(Date.now()-2*86_400_000)
  ctx.view.get('openCheck')(checkDate)
  value(ctx,'checkDate').value=checkDate
  const task=ctx.view.get('startCheck')()
  const request=ctx.requests.at(-1)
  assert.equal(request.options?.method,'PUT')
  request.reject(new ApiError(409));await task
  assert.equal(value(ctx,'checkDialog').value,true)
  assert.equal(ctx.messages.filter(([kind])=>kind==='success').length,0)
  assert.equal(ctx.messages.filter(([kind])=>kind==='error').length,1)
})
test('old-site save completion cannot close a newly opened site dialog or announce success',async t=>{
  const ctx=setup(t);await loadInitial(ctx)
  const task=ctx.view.get('save')({...value(ctx,'item').value.config,running:true},'A')
  const old=ctx.requests.at(-1)
  assert.equal(old.options?.method,'PUT')
  ctx.filters.site_id='B';await vue.nextTick()
  const current=ctx.requests.at(-1)
  current.resolve(response(current,'B'))
  await vue.nextTick();await vue.nextTick()
  value(ctx,'dialog').value=true
  old.resolve({saved:true});await task
  assert.equal(value(ctx,'dialog').value,true)
  assert.equal(ctx.messages.filter(([kind])=>kind==='success').length,0)
  assert.equal(value(ctx,'item').value.site_id,'B')
})
test('returning to the same site does not revive a save from its previous selection', {timeout:1500}, async t=>{
  const ctx=setup(t);await loadInitial(ctx)
  const task=ctx.view.get('save')({...value(ctx,'item').value.config,running:true},'A')
  const old=ctx.requests.at(-1)
  for(const site of ['B','A']) {
    ctx.filters.site_id=site;await vue.nextTick()
    const current=ctx.requests.at(-1)
    current.resolve(response(current,site))
    await vue.nextTick();await vue.nextTick()
  }
  value(ctx,'dialog').value=true
  old.resolve({saved:true});await task
  assert.equal(value(ctx,'dialog').value,true)
  assert.equal(ctx.messages.filter(([kind])=>kind==='success').length,0)
})
test('failed refresh retains visible data but prevents mutation using stale status',async t=>{
  const ctx=setup(t);await loadInitial(ctx)
  const refresh=ctx.view.get('load')()
  ctx.requests.at(-1).reject(new ApiError(503));await refresh
  assert.equal(value(ctx,'item').value.site_id,'A')
  assert.ok(value(ctx,'failure').value)
  const before=ctx.requests.length
  await ctx.view.get('save')({...value(ctx,'item').value.config,running:true},'A')
  assert.equal(ctx.requests.length,before)
})

test('independent controls preserve other tasks and selected collection range',async t=>{
 const ctx=setup(t);await loadInitial(ctx)
 const config=value(ctx,'item').value.config
 config.pipeline={migration:true,organization:true,verification:true,collection:true,collection_from:'2026-09-01',collection_through:'2026-09-20',collection_newest_first:true}
 config.history_immutable=true
 value(ctx,'savePipeline')({...config.pipeline,collection:false})
 const request=ctx.requests.at(-1), sent=JSON.parse(request.options.body)
 assert.equal(request.options.method,'PUT');assert.equal(sent.pipeline.collection,false)
 assert.equal(sent.pipeline.organization,true);assert.equal(sent.pipeline.verification,true)
 assert.equal(sent.pipeline.collection_from,'2026-09-01');assert.equal(sent.pipeline.collection_newest_first,true)
 assert.equal(sent.history_immutable,true);assert.equal(sent.full_history,true);assert.equal(sent.version,1)
 request.reject(new ApiError(409));await new Promise(resolve=>setImmediate(resolve))
})

test('group controls require confirmed online configuration and preserve collection when pausing history',async t=>{
 const ctx=setup(t);await loadInitial(ctx)
 const current=value(ctx,'item').value
 current.config.running=true
 current.config.pipeline={migration:true,organization:true,verification:true,collection:true}
 current.status.applied_version=0
 const count=ctx.requests.length
 value(ctx,'toggleGroup')('history')
 assert.equal(ctx.requests.length,count)
 current.status.applied_version=current.config.version
 value(ctx,'toggleGroup')('history')
 const request=ctx.requests.at(-1),sent=JSON.parse(request.options.body)
 assert.equal(sent.pipeline.organization,false)
 assert.equal(sent.pipeline.verification,false)
 assert.equal(sent.pipeline.collection,true)
 request.reject(new ApiError(409));await new Promise(resolve=>setImmediate(resolve))
})


test('calendar starts at observed first log and keeps user month on refresh',async t=>{
 const ctx=setup(t),loading=value(ctx,'load')(),req=ctx.requests.at(-1),body=response(req,'A')
 const origin={date:'2026-06-15',source:'archive',observed_at:new Date().toISOString()}
 body.items[0].status.calendar_origin=origin
 req.resolve(body);await loading
 assert.equal(value(ctx,'month').value,'2026-06')
 const first=ctx.requests.at(-1),firstBody=response(first,'A');firstBody.items[0].status.calendar_origin=origin
 first.resolve(firstBody);await new Promise(resolve=>setImmediate(resolve))
 assert.equal(value(ctx,'days').value[0].date,'2026-06-15')
 assert.equal(value(ctx,'calendarOffset').value,0)
 assert.equal(value(ctx,'disabledArchiveMonth')(new Date(2026,4,1)),true)
 value(ctx,'month').value='2026-08';value(ctx,'changeMonth')()
 const next=ctx.requests.at(-1),nextBody=response(next,'A');nextBody.items[0].status.calendar_origin=origin
 next.resolve(nextBody);await new Promise(resolve=>setImmediate(resolve))
 assert.equal(value(ctx,'month').value,'2026-08')
 assert.equal(value(ctx,'days').value[0].date,'2026-08-01')
})

test('empty source and archive show no artificial pending calendar days',async t=>{
 const ctx=setup(t);await loadInitial(ctx)
 value(ctx,'item').value.status.calendar_origin={date:'',source:'empty',observed_at:new Date().toISOString()}
 assert.deepEqual(value(ctx,'days').value,[])
 assert.equal(value(ctx,'missingDays').value.length,0)
})
