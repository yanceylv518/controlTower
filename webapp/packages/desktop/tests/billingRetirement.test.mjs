import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import {createRouter,createMemoryHistory} from 'vue-router'

class ApiError extends Error {}
const compile = source => ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function harness(user) {
 const permissions = {}
 vm.runInNewContext(compile(readFileSync(new URL('../src/permissions.ts',import.meta.url),'utf8')),{exports:permissions,require:()=>({ApiError})})
 const exports = {}, loaded = []
 const context = {exports, document:{title:''}, require:name=>{
  if(name==='vue-router') return {createRouter,createWebHistory:createMemoryHistory}
  if(name==='@ct/shared') return {ApiError}
  if(name==='./stores/auth') return {useAuthStore:()=>({user,load:async()=>{}})}
  if(name==='./api') return {setUnauthorizedHandler:()=>{}}
  if(name==='./permissions') return permissions
  loaded.push(name)
  return {default:{name,render:()=>null}}
 }}
 vm.runInNewContext(compile(readFileSync(new URL('../src/router.ts',import.meta.url),'utf8')),context)
 return {router:exports.router,loaded}
}

test('all retired user bill URLs reach the current workspace and preserve navigation context',async()=>{
 const {router,loaded}=harness({role:'admin',permissions:['billing.users']})
 for(const path of ['/billing','/billing/overview','/billing/generated','/billing/new']) {
  await router.push(path+'?instance_id=site-a&month=2026-09#daily')
  const route=router.currentRoute.value
  assert.equal(route.path,'/billing/new')
  assert.equal(route.query.instance_id,'site-a')
  assert.equal(route.query.month,'2026-09')
  assert.equal(route.hash,'#daily')
  assert.equal(route.meta.title,'用户账单')
  assert.equal(route.matched.at(-1).components.default.name,'./views/BillingWorkspaceView.vue')
 }
 assert.ok(!loaded.includes('./views/BillingView.vue'))
})

test('retired URL redirects keep permissions enforced for viewers and admins without billing access',async()=>{
 for(const [user,target] of [
  [{role:'viewer'},'/customers'],
  [{role:'admin',permissions:['overview.read']},'/'],
  [{role:'admin',permissions:[]},'/no-access'],
 ]) {
  const {router}=harness(user)
  await router.push('/billing/generated')
  assert.equal(router.currentRoute.value.path,target)
 }
})

test('upstream and report routes retain their existing workspaces',async()=>{
 const {router}=harness({role:'admin',permissions:['billing.channels']})
 await router.push('/billing/channels')
 assert.equal(router.currentRoute.value.path,'/billing/channels')
 assert.equal(router.currentRoute.value.matched.at(-1).props.default.kind,'upstream')
 await router.push('/billing/reports')
 assert.equal(router.currentRoute.value.matched.at(-1).components.default.name,'./views/SettlementReportsView.vue')
})