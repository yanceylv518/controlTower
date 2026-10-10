import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {ref,computed,reactive,watch,effectScope,nextTick} from 'vue';
const source=readFileSync(new URL('../src/views/BillingWorkspaceView.vue',import.meta.url),'utf8');
const sf=ts.createSourceFile('page.ts',source.split('<script setup lang="ts">')[1].split('</script>')[0],ts.ScriptTarget.Latest,true,ts.ScriptKind.TS);
const printer=ts.createPrinter();
const bare=sf.statements.filter(s=>!ts.isImportDeclaration(s)).map(s=>printer.printNode(ts.EmitHint.Unspecified,s,sf)).join('\n');
const compiled=ts.transpileModule(bare,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
const flush=async()=>{await new Promise(setImmediate);await nextTick();};
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
function create(t,kind='user'){
 const filters=reactive({site_id:'a',loadInstances:async()=>{}}),props=reactive({kind});
 const writes=[],loads=[],messages=[],dispose=[],queries=[];
 const dashboard={billingGenerationStatus:async()=>({busy:false,jobs:[],targets:[]}),siteCurrency:async()=>({type:'CNY'}),billingUpstreams:async()=>({items:[{id:9,name:'Upstream'}]}),createNewStatement:async q=>{writes.push(q);return {accepted:true};},generateMissingBills:async q=>{writes.push(q);return {outcome:'registered'};},billingWorkspace:async q=>{loads.push(q);return {items:[],remaining_days:0};},billingJobs:async()=>({items:[]})};
 const passthrough={users:async q=>{queries.push(q);return {items:[{id:7,username:'Alice'},{id:8,username:'Bob'}]};}};
 const ElMessage=Object.assign(value=>messages.push(value),{warning:value=>messages.push(value),success:value=>messages.push(value),error:value=>messages.push(value)});
 const names=['ref','computed','watch','onBeforeUnmount','withDefaults','defineProps','useFiltersStore','useBillingDownload','setInterval','clearInterval','ElMessage','dashboard','passthrough','billingWorkspaceRows','coverageLabel','coverageStatus','coverageTooltip','formatBillingDiscount','billingTaskErrorMessage'];
 const args=[ref,computed,watch,f=>dispose.push(f),p=>p,()=>props,()=>filters,()=>({pending:ref([]),download:()=>{}}),()=>1,()=>{},ElMessage,dashboard,passthrough,()=>[],()=>'',()=>'',()=>'',String,String];
 const scope=effectScope();const destroy=()=>{dispose.forEach(f=>f());scope.stop();};t.after(destroy);
 const page=scope.run(()=>new Function(...names,compiled+';return {openGenerate,submit,searchSubjects,generationBusy,temporarySubject,range,selected,month,tab,view,generate,error,saving,subjects,excludeZeroOutput,subjectError:typeof subjectError=== "undefined"?undefined:subjectError,subjectLoading:typeof subjectLoading=== "undefined"?undefined:subjectLoading};')(...args));
 return {...page,filters,props,dashboard,passthrough,writes,loads,messages,queries,destroy};
}
async function open(c,subject=7){await flush();c.openGenerate('temporary');await flush();c.temporarySubject.value=subject;c.range.value=['2026-10-01T00:00:00+08:00','2026-10-02T00:00:00+08:00'];}

test('late submission cannot restore an old object after switching site',async t=>{
 const c=create(t);await open(c);const request=deferred();c.dashboard.createNewStatement=()=>request.promise;
 const old=c.submit();c.filters.site_id='b';await flush();request.resolve({accepted:true});await old;await flush();
 assert.equal(c.view.value,'catalog');assert.equal(c.selected.value,undefined);assert.equal(c.loads.length,0);assert.deepEqual(c.messages,[]);
});
test('catalog lookup failure has its own visible error state and retry recovers',async t=>{
 const c=create(t);await flush();c.passthrough.users=async()=>{throw Error('offline');};c.openGenerate('temporary');await flush();
 assert.equal(c.view.value,'catalog');assert.ok(c.subjectError?.value);assert.equal(c.subjectLoading.value,false);
 c.passthrough.users=async()=>({items:[{id:7,username:'Alice'}]});await c.searchSubjects();assert.equal(c.subjectError.value,'');assert.equal(c.subjects.value[0].name,'Alice');
});

for(const kind of ['user','upstream'])test(`${kind} draft stays separate and successful submission opens its captured period`,async t=>{
 const c=create(t,kind);await flush();c.selected.value=7;await open(c,kind==='user'?8:9);
 assert.equal(c.selected.value,7);assert.equal(c.view.value,'catalog');
 const req=deferred();c.dashboard.createNewStatement=q=>{c.writes.push(q);return req.promise;};
 const submit=c.submit();assert.equal(c.saving.value,true);await c.submit();assert.equal(c.writes.length,1);
 c.range.value=['2026-08-01T00:00:00+08:00','2026-08-02T00:00:00+08:00'];
 req.resolve({accepted:true});await submit;await flush();
 const q=c.writes[0];assert.equal(q.instance_id,'a');assert.equal(q.statement_type,kind);assert.equal(q.period,'temporary');assert.equal(q.from,'2026-10-01T00:00:00+08:00');assert.equal(q.exclude_zero_output,kind==='upstream');
 assert.equal(q[kind==='user'?'user_id':'upstream_id'],kind==='user'?8:9);assert.equal(q[kind==='user'?'upstream_id':'user_id'],undefined);
 assert.equal(c.selected.value,kind==='user'?8:9);assert.equal(c.month.value,'2026-10');assert.equal(c.tab.value,'temporary');assert.equal(c.view.value,'workspace');assert.equal(c.generate.value,false);assert.equal(c.saving.value,false);
});
test('A to B to A does not let an old failure clear a newer submission',async t=>{
 const c=create(t);await open(c);const old=deferred(),fresh=deferred();let count=0;c.dashboard.createNewStatement=()=>++count===1?old.promise:fresh.promise;
 const first=c.submit();c.filters.site_id='b';await flush();c.filters.site_id='a';await open(c,8);const second=c.submit();
 old.reject(Error('old failure'));await first;assert.equal(c.saving.value,true);assert.equal(c.generate.value,true);assert.deepEqual(c.messages,[]);
 fresh.resolve({accepted:true});await second;assert.equal(c.selected.value,8);assert.equal(c.saving.value,false);
});
test('kind changes and unmount suppress pending submission results',async t=>{
 for(const change of ['kind','unmount']){
  const c=create(t);await open(c);const req=deferred();c.dashboard.createNewStatement=()=>req.promise;const pending=c.submit();
  if(change==='kind'){c.props.kind='upstream';await flush();}else c.destroy();
  req.resolve({accepted:true});await pending;await flush();assert.equal(c.loads.length,0);assert.deepEqual(c.messages,[]);assert.equal(c.selected.value,undefined);
 }
});
test('old lookup errors cannot overwrite a later search or a closed dialog',async t=>{
 const c=create(t);await open(c);const old=deferred();c.passthrough.users=()=>old.promise;const first=c.searchSubjects('old');await flush();
 c.passthrough.users=async()=>({items:[{id:8,username:'Bob'}]});await c.searchSubjects('new');old.reject(Error('old search'));await first;
 assert.equal(c.subjectError.value,'');assert.equal(c.subjectLoading.value,false);assert.ok(c.subjects.value.some(u=>u.id===7));
 const closed=deferred();c.passthrough.users=()=>closed.promise;const query=c.searchSubjects();await flush();c.generate.value=false;await flush();closed.reject(Error('closed'));await query;assert.equal(c.subjectError.value,'');assert.equal(c.subjectLoading.value,false);
});
test('lookup errors reset on site/kind changes and upstream errors can retry',async t=>{
 const c=create(t);await flush();c.passthrough.users=async()=>{throw Error('a offline');};c.openGenerate('temporary');await flush();assert.ok(c.subjectError.value);
 c.filters.site_id='b';c.props.kind='upstream';await flush();assert.equal(c.subjectError.value,'');assert.equal(c.generate.value,false);assert.equal(c.temporarySubject.value,undefined);
 c.dashboard.billingUpstreams=async()=>{throw Error('upstream offline');};c.openGenerate('temporary');await flush();assert.ok(c.subjectError.value);
 c.dashboard.billingUpstreams=async()=>({items:[{id:9,name:'Recovered'}]});await c.searchSubjects();assert.equal(c.subjectError.value,'');assert.equal(c.subjects.value[0].id,9);
});
test('invalid ranges, unresolved choices and busy generation cannot submit',async t=>{
 const c=create(t);await open(c);
 for(const range of [null,['',''],['invalid','invalid'],['2026-10-02','2026-10-01']]){c.range.value=range;await c.submit();}
 c.range.value=['2026-10-01T00:00:00+08:00','2026-10-02T00:00:00+08:00'];
 c.subjectLoading.value=true;await c.submit();c.subjectLoading.value=false;c.subjectError.value='offline';await c.submit();c.subjectError.value='';c.generationBusy.value=true;await c.submit();
 assert.equal(c.writes.length,0);assert.equal(c.saving.value,false);
});
test('current submission failure preserves the draft and permits a retry',async t=>{
 const c=create(t);await open(c);c.dashboard.createNewStatement=async()=>{throw Error('current failure');};await c.submit();
 assert.equal(c.generate.value,true);assert.equal(c.temporarySubject.value,7);assert.equal(c.saving.value,false);assert.match(c.messages.at(-1),/current failure/);
 c.dashboard.createNewStatement=async q=>{c.writes.push(q);return {accepted:true};};await c.submit();assert.equal(c.writes.length,1);assert.equal(c.tab.value,'temporary');
});
