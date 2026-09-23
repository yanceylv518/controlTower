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
function setup(t) {
  const source=readFileSync(new URL('../src/views/LogArchiveJobsView.vue',import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
  const requests=[],messages=[],cleanup=[],filters=vue.reactive({site_id:'A',loadInstances:async()=>{}})
  const dependencies={vue:{...vue,onMounted:()=>{},onUnmounted:fn=>cleanup.push(fn)},'element-plus':{ElMessage:{success:value=>messages.push(['success',value]),error:value=>messages.push(['error',value])}},'@ct/shared':{ApiError},'../api':{client:{request:(url,options)=>{const request={url,options,...pending()};requests.push(request);return request.promise}}},'../stores/filters':{useFiltersStore:()=>filters},'../utils/logArchive':helperExports}
  dependencies['../permissions']=permissionExports
  dependencies['../stores/auth']={useAuthStore:()=>({user:{role:'admin',permissions:['archive.manage']}})}
  const scope=vue.effectScope()
  const view=scope.run(()=>new Function('require','exports',transpile(source)+';return {get(name){return eval(name)}};')(name=>{if(!(name in dependencies))throw Error('Unexpected dependency '+name);return dependencies[name]},{}))
  t.after(()=>{for(const fn of cleanup)fn();scope.stop()})
  return {view,requests,messages,filters,cleanup}
}

function item(site){return {site_id:site,config:{version:1,instance_id:site,agent_id:'a',running:true,batch_size:1000,interval_seconds:2,delay_seconds:300,history_immutable:false,tasks:{collection:true,history:true}},targets:[],seen_at:new Date().toISOString(),status:{applied_version:1,state:'running',error:'',engine:{protocol:1,collection:{after_id:'9007199254740993',rows:'1',step:'collect'},history:{step:'verify_source',after_id:'0',rows:'0'},first_date:'2026-06-15',first_date_source:'archive'}}}}
const value=(ctx,name)=>ctx.view.get(name)
async function initial(ctx){value(ctx,'initialized').value=true;value(ctx,'month').value='2026-06';const p=value(ctx,'load')();ctx.requests.at(-1).resolve({protocol:1,items:[item('A')],days:[]});await p}
test('new control uses only two switches and never sends old pipeline configuration',async t=>{
 const ctx=setup(t);await initial(ctx);value(ctx,'toggle')('history');const req=ctx.requests.at(-1)
 assert.match(req.url,/log-archive-jobs\/A$/);const body=JSON.parse(req.options.body)
 assert.deepEqual(body.tasks,{collection:true,history:false});assert.equal(body.pipeline,undefined);assert.equal(body.full_history,undefined)
 req.reject(new Error('test failure'));await new Promise(r=>setImmediate(r))
})
test('new calendar excludes dates before the actual origin without inventing zero counts',async t=>{
 const ctx=setup(t);await initial(ctx);assert.equal(value(ctx,'days').value[0].date,'2026-06-15');assert.equal(value(ctx,'offset').value,0);assert.equal(value(ctx,'days').value[0].rows,'');assert.equal(value(ctx,'days').value[0].state,'unknown')
})
test('old site responses cannot replace the new site',async t=>{
 const ctx=setup(t);const first=value(ctx,'load')(),old=ctx.requests.at(-1);ctx.filters.site_id='B';await vue.nextTick();const current=ctx.requests.at(-1);current.resolve({protocol:1,items:[{...item('B'),status:{...item('B').status,engine:{protocol:1}}}],days:[]});await vue.nextTick();await vue.nextTick();old.resolve({protocol:1,items:[item('A')],days:[]});await first;assert.equal(value(ctx,'item').value.site_id,'B')
})

test('missing new API keeps an explicit unavailable state without inventing data or enabling writes',async t=>{
 const ctx=setup(t);const p=value(ctx,'load')();ctx.requests.at(-1).reject(new ApiError(404));await p
 assert.equal(value(ctx,'unavailable').value,true);assert.match(value(ctx,'emptyTitle').value,/接口尚未就绪/)
 assert.equal(value(ctx,'item').value,undefined);assert.deepEqual(value(ctx,'days').value,[]);assert.equal(value(ctx,'writable').value,false)
 await initial(ctx);assert.equal(value(ctx,'unavailable').value,false);assert.equal(value(ctx,'error').value,'');assert.equal(value(ctx,'writable').value,true)
})
test('refresh failure retains last report but disables actions',async t=>{
 const ctx=setup(t);await initial(ctx);const previous=value(ctx,'item').value;const p=value(ctx,'load')();ctx.requests.at(-1).reject(new ApiError(503));await p
 assert.equal(value(ctx,'item').value,previous);assert.equal(value(ctx,'writable').value,false);assert.equal(value(ctx,'unavailable').value,false)
})
