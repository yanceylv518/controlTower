import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,ref,reactive,watch,effectScope} from 'vue'
const source=readFileSync(new URL('../src/components/BillingCatalog.vue',import.meta.url),'utf8')
const script=source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?\n/gm,'')
const code=ts.transpile(script,{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None})
const flush=()=>new Promise(resolve=>setImmediate(resolve))
const result=(id='bill')=>({items:[{job:{id}}],total:521,subjects:32,counts:{monthly:21,daily:521,temporary:2}})
function create(t){
 const props=reactive({site:'one',kind:'user',active:true}),calls=[]
 const dashboard={billingCatalog:async (params,signal)=>{calls.push({params,signal});return result()}}
 const scope=effectScope(),dispose=[];t.after(()=>{dispose.forEach(f=>f());scope.stop()})
 const init=new Function('computed','ref','watch','onBeforeUnmount','defineProps','defineEmits','defineExpose','setInterval','clearInterval','dashboard','billingTaskErrorMessage','coverageLabel','coverageStatus','coverageTooltip',code+'\nreturn {period,month,query,appliedQuery,page,size,items,total,counts,error,load,search,reset};')
 const state=scope.run(()=>init(computed,ref,watch,f=>dispose.push(f),()=>props,()=>()=>{},()=>{},()=>1,()=>{},dashboard,String,()=>'',()=>'',()=>''))
 return {...state,props,calls,dashboard}
}
test('default discovers all monthly bills without user or month and paginates on server',async t=>{
 const v=create(t);await flush();assert.equal(v.calls[0].params.period,'monthly');assert.equal(v.calls[0].params.month,undefined);assert.equal(v.total.value,521)
 v.page.value=27;await flush();assert.equal(v.calls.at(-1).params.page,27)
 v.period.value='daily';await flush();assert.equal(v.page.value,1);assert.equal(v.calls.at(-1).params.period,'daily')
 v.month.value='2026-09';v.query.value='old';v.search();await flush();assert.equal(v.calls.at(-1).params.q,'old')
 v.reset();await flush();assert.equal(v.period.value,'daily');assert.equal(v.month.value,'');assert.equal(v.appliedQuery.value,'')
})
test('return retains filters; inactive and old-site requests cannot overwrite current results',async t=>{
 const v=create(t);await flush();v.month.value='2026-08';await flush();v.props.active=false;await flush();const n=v.calls.length;await v.load();assert.equal(v.calls.length,n)
 v.props.active=true;await flush();assert.equal(v.month.value,'2026-08')
 let finish;v.dashboard.billingCatalog=()=>new Promise(r=>finish=r);const old=v.load()
 v.dashboard.billingCatalog=async()=>result('current');v.props.site='two';v.props.kind='upstream';await flush();finish(result('old'));await old
 assert.equal(v.items.value[0].job.id,'current');assert.equal(v.period.value,'monthly');assert.equal(v.month.value,'')
})
test('failed queries expose an error and retry recovers',async t=>{
 const v=create(t);await flush();v.dashboard.billingCatalog=async()=>{throw Error('network')};await v.load();assert.match(v.error.value,/network/)
 v.dashboard.billingCatalog=async()=>result();await v.load();assert.equal(v.error.value,'')
})
