import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed, ref, reactive, watch, effectScope} from 'vue'

const source=readFileSync(new URL('../src/components/BillingGenerationBatch.vue',import.meta.url),'utf8')
const script=source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?\n/gm,'')
const compiled=ts.transpile(script,{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None})
const users=[{id:1,username:'admin',role:10},{id:2,username:'root',role:100},{id:3,username:'alice',role:1},{id:4,username:'bob',role:1}]
function create(t,kind='user_statement') {
 const props=reactive({site:'a',kind,month:'2026-09',selected:1,subjects:users.map(u=>({id:u.id,name:u.username,role:u.role}))})
 const queries=[],requests=[]
 const passthrough={users:async q=>{queries.push(q);return {items:users}}}
 const previews=[];const dashboard={billingGenerationPreview:async(q,signal)=>{previews.push({q,signal});return {items:q.subject_ids.map(id=>({subject_id:id,total_days:30,complete:0,empty:0,failed:0,pending:0,running:0,outcome:'registered'}))}},billingUpstreams:async()=>({items:[{id:1,name:'上游'}]}),generateBillingBatch:async q=>{requests.push(q);return {items:[{subject_id:3,outcome:'complete'}]}}}
 const scope=effectScope();t.after(()=>scope.stop())
 const names=['computed','ref','watch','onBeforeUnmount','defineProps','defineEmits','defineExpose','setInterval','clearInterval','ElMessage','ElMessageBox','dashboard','passthrough','billingTaskErrorMessage']
 const init=new Function(...names,compiled+'\nreturn {statusFilter,statusCounts,filteredOptions,pageSelected,pageIndeterminate,selectPage,selectFiltered,statusGroup,restore,refresh,progressName,names,excludeZeroOutput,open,options,selectedUsers,excludeAdmins,filterAdmins,searchUsers,submit,queryResults,roles,batch,items,waitingForReport,resultLabel,draftMonth,preview,previewLoading,previewError,selectionStatus,monthlyStatus,loadPreview,toggleSubject,selectionPage,visibleOptions,dialog};')
 const view=scope.run(()=>init(computed,ref,watch,()=>{},()=>props,()=>()=>{},()=>{},()=>1,()=>{}, {error:()=>{},info:()=>{},success:()=>{}},{},dashboard,passthrough,String))
 return {...view,props,passthrough,dashboard,previews,queries,requests}
}
const flush=()=>new Promise(resolve=>setImmediate(resolve))

test('billing explains report wait and resets that state when switching sites',async t=>{
 const v=create(t)
 v.batch.value={subject_ids:[3]};v.items.value=[{subject_id:3,outcome:'queued',waiting_for:'report'}]
 assert.equal(v.waitingForReport.value,true);assert.equal(v.resultLabel.value,'等待报表完成')
 v.props.site='b';await flush();assert.equal(v.waitingForReport.value,false)
})

test('default excludes admins/root and an admin preselection; normal selections survive searches',async t=>{
 const v=create(t);v.open();await flush()
 assert.equal(v.queries[0].exclude_admin,1)
 assert.deepEqual(v.options.value.map(u=>u.id),[3,4]);assert.deepEqual(v.selectedUsers.value,[])
 v.selectedUsers.value=[3];v.passthrough.users=async()=>({items:[users[3]]});await v.searchUsers('bob')
 assert.deepEqual(v.options.value.map(u=>u.id),[4,3]);assert.deepEqual(v.selectedUsers.value,[3])
})
test('switching filter clears selected admins and only eligible IDs are submitted',async t=>{
 const v=create(t);v.open();await flush();v.excludeAdmins.value=false;v.filterAdmins();await flush()
 assert.deepEqual(v.options.value.map(u=>u.id),[1,2,3,4]);assert.equal(v.queries.at(-1).exclude_admin,0)
 v.selectedUsers.value=[1,2,3];v.excludeAdmins.value=true;v.filterAdmins()
 assert.deepEqual(v.selectedUsers.value,[3]);await flush();await v.submit()
 assert.deepEqual(v.requests[0].subject_ids,[3])
})
test('outdated search cannot restore admins after filter changes; site changes clear cached roles',async t=>{
 const v=create(t);v.open();await flush();let finish
 v.excludeAdmins.value=false;v.passthrough.users=()=>new Promise(resolve=>{finish=resolve});const old=v.searchUsers('old')
 v.excludeAdmins.value=true;v.passthrough.users=async()=>({items:[users[2]]});v.filterAdmins();await flush()
 finish({items:users});await old;assert.deepEqual(v.options.value.map(u=>u.id),[3])
 v.props.site='b';await flush();assert.deepEqual(v.options.value,[]);assert.deepEqual(v.roles.value,{})
})
test('upstream selection is unaffected and unknown user roles are not treated as normal users',async t=>{
 const v=create(t,'upstream_statement');v.open();await flush();assert.deepEqual(v.options.value.map(u=>u.id),[1]);assert.deepEqual(v.selectedUsers.value,[1]);assert.equal(v.queries.length,0)
 const u=create(t);u.passthrough.users=async()=>({items:[{id:9,username:'unknown'}]});u.open();await flush();assert.deepEqual(u.options.value,[])
})


test('generation month is editable and preview does not create a task',async t=>{
 const v=create(t);v.open();await flush();assert.equal(v.previews[0].q.from,'2026-09-01T00:00:00+08:00');assert.equal(v.requests.length,0)
 v.selectedUsers.value=[3];v.draftMonth.value='2026-08';await flush();assert.deepEqual(v.selectedUsers.value,[]);assert.equal(v.previews.at(-1).q.from,'2026-08-01T00:00:00+08:00')
 v.selectedUsers.value=[4];await v.submit();assert.equal(v.requests[0].from,'2026-08-01T00:00:00+08:00');assert.equal(v.requests[0].to,'2026-09-01T00:00:00+08:00')
})

test('late preview cannot overwrite a different month or a closed dialog',async t=>{
 const v=create(t);v.open();await flush();let finish
 v.dashboard.billingGenerationPreview=()=>new Promise(r=>finish=r);const old=v.loadPreview()
 v.dashboard.billingGenerationPreview=async()=>({items:[{subject_id:3,total_days:31,complete:31,empty:0,monthly:{status:'complete'}}]})
 v.draftMonth.value='2026-08';await flush();finish({items:[{subject_id:3,total_days:30,complete:0}]});await old;assert.equal(v.selectionStatus(3),'已生成')
 v.dialog.value=false;await flush();assert.deepEqual(v.preview.value,{})
})

test('preview failure stays unknown; partial and failed subjects can be selected without including completed bills',async t=>{
 const v=create(t);v.open();await flush();v.dashboard.billingGenerationPreview=async()=>{throw Error('offline')};await v.loadPreview()
 assert.match(v.previewError.value,/offline/);assert.equal(v.selectionStatus(3),'状态未知');v.statusFilter.value='missing';await flush();v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[])
 v.preview.value={3:{subject_id:3,total_days:30,complete:30,empty:0,monthly:{status:'complete'}},4:{subject_id:4,total_days:30,complete:10,empty:5,failed:1}}
 assert.equal(v.selectionStatus(4),'部分失败');v.statusFilter.value='partial';await flush();v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[4])
 v.preview.value[4]={subject_id:4,total_days:30,complete:10,empty:5};assert.equal(v.selectionStatus(4),'部分已生成')
 v.preview.value[4]={subject_id:4,total_days:30,complete:0,empty:30};assert.equal(v.selectionStatus(4),'无消费，无需出账')
})

test('preview requests are paged, capped, and upstream uses its own scope',async t=>{
 const v=create(t);v.passthrough.users=async()=>({items:Array.from({length:65},(_,i)=>({id:i+10,username:'user'+i,role:1}))});v.open();await flush()
 assert.deepEqual(v.previews.map(p=>p.q.subject_ids.length),[20,20,20,5]);const count=v.previews.length;v.selectionPage.value=4;await flush();assert.equal(v.previews.length,count);assert.equal(Object.keys(v.preview.value).length,65)
 for(let i=10;i<75;i++)v.toggleSubject(i,true);assert.equal(v.selectedUsers.value.length,50)
 const up=create(t,'upstream_statement');up.open();await flush();assert.equal(up.previews.at(-1).q.kind,'upstream_statement')
})

test('user includes zero output by default; upstream excludes, with explicit override',async t=>{
 for(const kind of ['user_statement','upstream_statement']){
  const v=create(t,kind);v.open();await flush();assert.equal(v.excludeZeroOutput.value,kind==='upstream_statement');
  v.selectedUsers.value=[kind==='user_statement'?3:1];await v.submit();assert.equal(v.requests[0].exclude_zero_output,kind==='upstream_statement');
  v.excludeZeroOutput.value=kind!=='upstream_statement';await v.submit();assert.equal(v.requests[1].exclude_zero_output,kind!=='upstream_statement');
 }
})


test('restored upstream progress shows API names without opening the subject selector',async t=>{
 const v=create(t,'upstream_statement');v.props.subjects=[];await flush()
 v.dashboard.billingBatchProgress=async()=>({items:[{subject_id:28,subject_name:'趋境',outcome:'registered'}]})
 v.restore([{instance_id:'a',kind:'upstream_statement',subject_id:28,from:'2026-09-01T00:00:00+08:00',to:'2026-10-01T00:00:00+08:00'}]);await flush()
 assert.equal(v.progressName(v.items.value[0]),'趋境');assert.equal(v.queries.length,0)
 v.names.value[28]='旧名称';assert.equal(v.progressName(v.items.value[0]),'趋境')
 assert.equal(v.progressName({subject_id:28}),'旧名称');assert.equal(v.progressName({subject_id:999}),'上游')
})

test('late upstream progress cannot restore previous site names after switching sites',async t=>{
 const v=create(t,'upstream_statement');let finish
 v.dashboard.billingBatchProgress=()=>new Promise(resolve=>finish=resolve)
 v.restore([{instance_id:'a',kind:'upstream_statement',subject_id:28,from:'2026-09-01T00:00:00+08:00',to:'2026-10-01T00:00:00+08:00'}])
 v.props.site='b';await flush();finish({items:[{subject_id:28,subject_name:'旧站点上游',outcome:'registered'}]});await flush()
 assert.deepEqual(v.items.value,[]);assert.deepEqual(v.names.value,{})
})


test('status filters distinguish complete, partial, missing, running, empty and unknown',async t=>{
 const v=create(t);v.passthrough.users=async()=>({items:Array.from({length:6},(_,i)=>({id:i+10,username:'u'+i,role:1}))});v.open();await flush()
 v.preview.value={10:{subject_id:10,total_days:30,complete:30,empty:0,monthly:{status:'complete'}},11:{subject_id:11,total_days:30,complete:10,empty:0},12:{subject_id:12,total_days:30,complete:0,empty:0},13:{subject_id:13,total_days:30,complete:0,empty:0,running:1},14:{subject_id:14,total_days:30,complete:0,empty:30}}
 assert.deepEqual(v.statusCounts.value,{all:6,complete:1,partial:1,missing:1,active:1,empty:1,unknown:1})
 v.statusFilter.value='partial';await flush();assert.deepEqual(v.visibleOptions.value.map(u=>u.id),[11])
 v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[11]);v.statusFilter.value='missing';await flush();v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[11,12])
 v.statusFilter.value='complete';await flush();v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[11,12,10])
})

test('page selection preserves other pages, filtered selection spans pages and respects batch limit',async t=>{
 const v=create(t);v.passthrough.users=async()=>({items:Array.from({length:65},(_,i)=>({id:i+10,username:'u'+i,role:1}))});v.open();await flush()
 v.selectPage(true);assert.equal(v.selectedUsers.value.length,20);assert.equal(v.pageSelected.value,true)
 v.selectionPage.value=2;await flush();v.toggleSubject(30,true);assert.equal(v.pageIndeterminate.value,true)
 v.selectionPage.value=1;await flush();v.selectPage(false);assert.deepEqual(v.selectedUsers.value,[30])
 v.statusFilter.value='missing';await flush();v.selectFiltered();assert.equal(v.selectedUsers.value.length,50);assert.equal(new Set(v.selectedUsers.value).size,50);assert.ok(v.selectedUsers.value.includes(59));assert.ok(!v.selectedUsers.value.includes(74))
 v.props.site='b';await flush();assert.deepEqual(v.selectedUsers.value,[])
})

test('incomplete status scan cannot silently select unknown users and stale scans stop',async t=>{
 const v=create(t);v.passthrough.users=async()=>({items:Array.from({length:25},(_,i)=>({id:i+10,username:'u'+i,role:1}))});let calls=0
 v.dashboard.billingGenerationPreview=async q=>{calls++;if(calls===2)throw Error('failed page');return {items:q.subject_ids.map(subject_id=>({subject_id,total_days:30,complete:0,empty:0}))}}
 v.open();await flush();assert.match(v.previewError.value,/failed page/);assert.deepEqual(v.preview.value,{});v.statusFilter.value='missing';await flush();v.selectFiltered();assert.deepEqual(v.selectedUsers.value,[])
 let finish;calls=0;v.dashboard.billingGenerationPreview=()=>{calls++;return new Promise(r=>finish=r)};const old=v.loadPreview();v.dialog.value=false;await flush();finish({items:[]});await old;assert.equal(calls,1);assert.deepEqual(v.preview.value,{})
})


test('restored user progress resolves names by IDs without opening selection and caches them',async t=>{
 const v=create(t);v.props.subjects=[];
 v.dashboard.billingBatchProgress=async()=>({items:[{subject_id:9,outcome:'registered'},{subject_id:71,outcome:'registered'}]});
 v.passthrough.users=async q=>{v.queries.push(q);return {items:[{id:9,display_name:'中博',username:'user9'},{id:71,username:'user71'}]}};
 v.restore([{instance_id:'a',kind:'user_statement',subject_id:9,from:'2026-09-01',to:'2026-10-01'}]);await flush();
 assert.equal(v.progressName(v.items.value[0]),'中博');assert.equal(v.progressName(v.items.value[1]),'user71');
 assert.equal(v.queries[0].user_ids,'9,71');assert.equal(v.queries[0].exclude_admin,undefined);
 await v.refresh();assert.equal(v.queries.length,1);
});

test('late user names cannot cross instance changes',async t=>{
 const v=create(t);v.props.subjects=[];let resolve;
 v.dashboard.billingBatchProgress=async()=>({items:[{subject_id:9,outcome:'registered'}]});
 v.passthrough.users=()=>new Promise(r=>{resolve=r});
 v.restore([{instance_id:'a',kind:'user_statement',subject_id:9,from:'2026-09-01',to:'2026-10-01'}]);await flush();
 v.props.site='b';await flush();resolve({items:[{id:9,username:'other-site'}]});await flush();
 assert.equal(v.names.value[9],undefined);
});
