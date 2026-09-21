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
