import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref, reactive, watch, effectScope } from 'vue'
const source = readFileSync(new URL('../src/views/BillingTasksView.vue', import.meta.url), 'utf8')
const script = source.split('<script setup lang="ts">')[1].split('</script>')[0]
  .replace(/^import .*;\r?\n/gm, '').replace(/void state\.reload\(\)\.then\(.*\);?/, '')
const compiled = ts.transpile(script, { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None })
const siteOf = item => item.site_id || item.instance_id
const deferred = () => { let resolve, reject; const promise = new Promise((yes,no) => { resolve=yes; reject=no }); return {promise,resolve,reject} }
function page(t) {
  const filters = reactive({site_id:'cn',instances:[
    {instance_id:'agent-a',site_id:'cn',name:'Pinducloud_cn',enabled:true,logs_readonly_configured:true},
    {instance_id:'agent-b',site_id:'cn',name:'Pinducloud_cn',enabled:true,logs_readonly_configured:true},
    {instance_id:'agent-c',site_id:'hk',name:'Pinducloud_cn',enabled:true,logs_readonly_configured:true},
    {instance_id:'legacy',enabled:true,logs_readonly_configured:true},
    {instance_id:'disabled',enabled:false,logs_readonly_configured:true},
    {instance_id:'unconfigured',enabled:true,logs_readonly_configured:false},
  ]})
  const users=[],upstreams=[],warnings=[]
  const passthrough={users:async args=>{users.push(args);return {items:[{id:1}]}}}
  const dashboard={billingUpstreams:async site=>{upstreams.push(site);return {items:[{id:2,enabled:true},{id:3,enabled:false}]}}}
  const scope=effectScope();t.after(()=>scope.stop())
  const names=['computed','ref','watch','onUnmounted','useRoute','useRouter','useFiltersStore','useAsyncData','ElMessage','dashboard','passthrough','siteOf','billingReadErrorMessage']
  const create = new Function(...names, `${compiled}\nreturn {createSites,createForm,createVisible,openCreate,changeCreateSite,loadUsers,userOptions,upstreamOptions,userLoading,upstreamLoading};`)
  const view=scope.run(()=>create(computed,ref,watch,()=>{},()=>({query:{}}),()=>({}),()=>filters,()=>({data:ref({items:[]}),reload:async()=>{}}),{warning:m=>warnings.push(m)},dashboard,passthrough,siteOf,(_,fallback)=>fallback))
  return {...view,filters,users,upstreams,warnings,passthrough,dashboard}
}
test('same-site collectors deduplicate by site ID, distinct sites with same name remain selectable',t=>{
  const p=page(t);assert.deepEqual(p.createSites.value,['cn','hk','legacy'])
  assert.match(source,/v-for="site in createSites" :key="site" :label="site" :value="site"/)
})
test('opening and switching user billing uses site IDs and never loads upstreams',async t=>{
  const p=page(t);await p.openCreate();assert.equal(p.users[0].site,'cn');assert.equal(p.upstreams.length,0)
  p.createForm.value.user_id=1;p.createForm.value.instance_id='hk';await p.changeCreateSite()
  assert.equal(p.users[1].site,'hk');assert.equal(p.createForm.value.user_id,undefined);assert.equal(p.upstreams.length,0)
})
test('upstream retry loads only enabled upstreams; switching type loads users',async t=>{
  const p=page(t);await p.openCreate({instance_id:'cn',job_type:'upstream_statement',upstream_id:2})
  assert.equal(p.users.length,0);assert.deepEqual(p.upstreams,['cn']);assert.deepEqual(p.upstreamOptions.value,[{id:2,enabled:true}])
  p.createForm.value.bill_type='user';await p.changeCreateSite();assert.equal(p.users.length,1);assert.equal(p.upstreamOptions.value.length,0)
  assert.match(source,/v-model="createForm.bill_type" @change="changeCreateSite"/)
})
test('late site response cannot overwrite current choices or loading state',async t=>{
  const p=page(t),old=deferred(),fresh=deferred();p.passthrough.users=({site})=>site==='cn'?old.promise:fresh.promise
  const first=p.openCreate();p.createForm.value.instance_id='hk';const second=p.changeCreateSite()
  old.resolve({items:[{id:10}]});await first;assert.equal(p.userLoading.value,true);assert.deepEqual(p.userOptions.value,[])
  fresh.resolve({items:[{id:20}]});await second;assert.deepEqual(p.userOptions.value,[{id:20}])
})
test('late upstream failure is silent after switching to users',async t=>{
  const p=page(t),old=deferred();p.dashboard.billingUpstreams=()=>old.promise
  const first=p.openCreate({instance_id:'cn',job_type:'upstream_statement'})
  p.createForm.value.bill_type='user';await p.changeCreateSite();old.reject(new Error('failed'));await first
  assert.deepEqual(p.warnings,[]);assert.deepEqual(p.userOptions.value,[{id:1}]);assert.equal(p.upstreamLoading.value,false)
})
test('latest search wins and closing suppresses pending errors',async t=>{
  const p=page(t);await p.openCreate();const old=deferred(),fresh=deferred()
  p.passthrough.users=({keyword})=>keyword==='old'?old.promise:fresh.promise
  const first=p.loadUsers('old'),second=p.loadUsers('new');fresh.resolve({items:[{id:22}]});await second
  old.resolve({items:[{id:11}]});await first;assert.deepEqual(p.userOptions.value,[{id:22}])
  const pending=deferred();p.passthrough.users=()=>pending.promise;const request=p.loadUsers();p.createVisible.value=false
  pending.reject(new Error('closed'));await request;assert.deepEqual(p.warnings,[]);assert.equal(p.userLoading.value,false)
})
test('current upstream failure still shows a relevant error',async t=>{
  const p=page(t);p.dashboard.billingUpstreams=async()=>{throw new Error('failed')}
  await p.openCreate({instance_id:'cn',job_type:'upstream_statement'});assert.deepEqual(p.warnings,['上游列表加载失败'])
})
