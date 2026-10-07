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
 const init=new Function(...names,compiled+'\nreturn {open,options,selectedUsers,excludeAdmins,filterAdmins,searchUsers,submit,queryResults,roles,batch,items,waitingForReport,resultLabel,draftMonth,preview,previewLoading,previewError,selectionStatus,monthlyStatus,loadPreview,selectMissing,toggleSubject,selectionPage,visibleOptions,dialog};')
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
 assert.match(v.previewError.value,/offline/);assert.equal(v.selectionStatus(3),'状态未知');v.selectMissing();assert.deepEqual(v.selectedUsers.value,[])
 v.preview.value={3:{subject_id:3,total_days:30,complete:30,empty:0,monthly:{status:'complete'}},4:{subject_id:4,total_days:30,complete:10,empty:5,failed:1}}
 assert.equal(v.selectionStatus(4),'部分失败');v.selectMissing();assert.deepEqual(v.selectedUsers.value,[4])
 v.preview.value[4]={subject_id:4,total_days:30,complete:10,empty:5};assert.equal(v.selectionStatus(4),'部分已生成')
 v.preview.value[4]={subject_id:4,total_days:30,complete:0,empty:30};assert.equal(v.selectionStatus(4),'无消费，无需出账')
})

test('preview requests are paged, capped, and upstream uses its own scope',async t=>{
 const v=create(t);v.passthrough.users=async()=>({items:Array.from({length:65},(_,i)=>({id:i+10,username:'user'+i,role:1}))});v.open();await flush()
 assert.equal(v.previews.at(-1).q.subject_ids.length,20);v.selectionPage.value=4;await flush();assert.equal(v.previews.at(-1).q.subject_ids.length,5)
 for(let i=10;i<75;i++)v.toggleSubject(i,true);assert.equal(v.selectedUsers.value.length,50)
 const up=create(t,'upstream_statement');up.open();await flush();assert.equal(up.previews.at(-1).q.kind,'upstream_statement')
})
